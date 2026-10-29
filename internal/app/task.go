package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// TaskLease is the live lease of an in-progress task. It is only rendered
// while status=in_progress and the deadline has not passed (tech design §4.2).
type TaskLease struct {
	LeaseID string
	Until   time.Time
}

// TaskView is the read model the task endpoints render. DependsOn holds
// business ids ordered by the dependency's seq_no; Lease is nil unless the task
// is in progress and the lease is still valid. WorkerID is the executor of an
// in-progress card and nil for every other status.
type TaskView struct {
	ID           string
	TaskID       string
	ProjectID    string
	SeqNo        int
	Title        string
	BodyMD       string
	Priority     int
	Status       int
	WorkerID     *string
	AttemptCount int
	MaxAttempts  int
	Reason       string
	LastActor    string
	Version      int
	DependsOn    []string
	Lease        *TaskLease
	CreateTime   time.Time
	UpdateTime   time.Time
}

// TaskDetail adds the current report to a task. Report is nil until Task-11
// uploads one.
type TaskDetail struct {
	Task   TaskView
	Report *domain.Material
}

// TaskCursor is the decoded keyset position of the last row of a page. It is
// identical in shape to store.TaskCursor; the app keeps its own type so the API
// does not depend on the store package.
type TaskCursor struct {
	Priority int
	SeqNo    int
	ID       int64
}

// ListTasksInput is the use-case input of GET /v1/tasks.
type ListTasksInput struct {
	ProjectID string
	Statuses  []int
	After     *TaskCursor
	Limit     int
}

// TaskPage is one page of tasks plus the position of the last returned row
// when more rows exist.
type TaskPage struct {
	Items []TaskView
	Next  *TaskCursor
}

// CreateTaskInput is the use-case input of POST /v1/tasks: one task added to a
// project that is still in planning.
type CreateTaskInput struct {
	ProjectID string
	Title     string
	BodyMD    string
	Priority  int
	DependsOn []string
}

// UpdateTaskInput is the use-case input of PATCH /v1/tasks. Nil means "field
// not supplied"; only these three fields are business-writable (tech design
// §4.3).
type UpdateTaskInput struct {
	Title    *string
	BodyMD   *string
	Priority *int
}

// TaskService implements the task use cases. It owns the business rules and the
// transaction boundaries; SQL stays in the store package.
type TaskService struct {
	store       *store.Store
	materials   *material.Writer
	now         func() time.Time
	maxAttempts int
}

// NewTaskService builds the task service on top of an open store.
func NewTaskService(st *store.Store) *TaskService {
	return &TaskService{
		store:       st,
		now:         func() time.Time { return time.Now().UTC() },
		maxAttempts: defaultTaskMaxAttempts,
	}
}

// SetMaxAttempts overrides the attempt budget written to newly created tasks.
// Values <= 0 are ignored so a misconfigured value never produces an
// unclaimable task.
func (s *TaskService) SetMaxAttempts(n int) {
	if n > 0 {
		s.maxAttempts = n
	}
}

// CreateTask inserts one task card into a project that is still being built
// (tasks_draft, tasks_review) or has already been approved (pending_done).
//
// The project status, the next sequence number and the dependency edges are all
// read and written in one write transaction, so a concurrent submit or create
// cannot hand out the same seq_no. When the project is still in tasks_draft the
// insert opens the batch review gate in the same transaction (4 → 5), so the
// first card always makes the batch reviewable. Validation of the payload runs
// first: a bad request leaves no row behind.
func (s *TaskService) CreateTask(ctx context.Context, in CreateTaskInput, actor string) (TaskDetail, error) {
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	if in.ProjectID == "" {
		return TaskDetail{}, invalidField("project_id", "is required")
	}

	normalized, fields := validateCreateTask(in)
	if len(fields) > 0 {
		return TaskDetail{}, &domain.ValidationError{Fields: fields}
	}
	actor = actorOrDefault(actor)

	var out TaskDetail
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		project, err := store.GetProjectByProjectID(ctx, tx, normalized.ProjectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: normalized.ProjectID}
			}
			return err
		}
		// Cards may be added while the batch is being written, while it is
		// under review, and after it was approved (pending_done). Terminal
		// projects reject the insert.
		if project.Status != domain.StatusTasksDraft &&
			project.Status != domain.StatusTasksReview &&
			project.Status != domain.StatusPendingDone {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q is in status %s; tasks can only be created in tasks_draft, tasks_review or pending_done",
				normalized.ProjectID, domain.StatusName(project.Status))}
		}

		seq, err := store.NextSeqNo(ctx, tx, "tasks", "project_id", project.ID)
		if err != nil {
			return err
		}
		taskID := fmt.Sprintf("%s-T%04d", project.ProjectID, seq)

		for _, dependsOn := range normalized.DependsOn {
			if dependsOn == taskID {
				return &domain.ValidationError{
					Fields: []domain.FieldError{{Field: "depends_on", Reason: "must not depend on itself"}},
				}
			}
		}

		if verr := s.validateDependencyGraph(ctx, tx, project, taskID, normalized.DependsOn); verr != nil {
			return verr
		}

		ts := formatTimestamp(s.timestamp())
		id, err := store.CreateTask(ctx, tx, store.NewTask{
			TaskID:      taskID,
			ProjectID:   project.ID,
			SeqNo:       seq,
			Title:       normalized.Title,
			BodyMD:      normalized.BodyMD,
			Priority:    normalized.Priority,
			Status:      domain.TaskBacklog,
			MaxAttempts: s.maxAttempts,
			LastActor:   actor,
			CreateTime:  ts,
			UpdateTime:  ts,
		})
		if err != nil {
			if errors.Is(err, store.ErrUniqueViolation) {
				return &domain.ConflictError{Reason: fmt.Sprintf(
					"task %q already exists in project %q", taskID, normalized.ProjectID)}
			}
			return err
		}

		for _, dependsOn := range normalized.DependsOn {
			if err := store.CreateTaskDependencyByTaskIDs(ctx, tx, taskID, dependsOn, ts); err != nil {
				return err
			}
		}

		if project.Status == domain.StatusTasksDraft {
			next, err := domain.ProjectNextStatus(project.Status, domain.ProjectActionAddFirstTask, "")
			if err != nil {
				return err
			}
			affected, err := store.TransitionProject(ctx, tx, project.ID,
				project.Status, project.Version, next, project.Reason, actor, ts)
			if err != nil {
				return err
			}
			if affected == 0 {
				fresh, readErr := store.GetProjectByProjectID(ctx, tx, project.ProjectID)
				if readErr != nil {
					return readErr
				}
				return &domain.InvalidVersionError{Current: fresh.Version, Expected: project.Version}
			}
		}

		out, err = s.detail(ctx, tx, id)
		return err
	})
	if err != nil {
		return TaskDetail{}, err
	}
	return out, nil
}

// validateDependencyGraph checks that every dependency exists in the same
// project and that adding the new edges keeps the project graph acyclic.
//
// A cycle through a not-yet-inserted task is normally impossible (nothing can
// depend on a task that does not exist yet), but the guard is cheap and keeps
// the invariant true even if rows are ever imported or restored from a backup.
func (s *TaskService) validateDependencyGraph(ctx context.Context, tx *sql.Tx, project store.Project, taskID string, dependsOn []string) error {
	if len(dependsOn) == 0 {
		return nil
	}

	refs, err := store.TaskIDsByProject(ctx, tx, project.ID)
	if err != nil {
		return err
	}
	codeByID := make(map[int64]string, len(refs))
	exists := make(map[string]struct{}, len(refs))
	graph := make(map[string][]string, len(refs)+1)
	for _, ref := range refs {
		codeByID[ref.ID] = ref.TaskID
		exists[ref.TaskID] = struct{}{}
		graph[ref.TaskID] = nil
	}

	edges, err := store.ListTaskDependencies(ctx, tx, project.ID)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		owner, ok := codeByID[edge.TaskID]
		if !ok {
			continue
		}
		dep, ok := codeByID[edge.DependsOn]
		if !ok {
			continue
		}
		graph[owner] = append(graph[owner], dep)
	}

	fields := make([]domain.FieldError, 0, len(dependsOn))
	for _, dep := range dependsOn {
		if _, ok := exists[dep]; !ok {
			fields = append(fields, domain.FieldError{
				Field:  "depends_on",
				Reason: fmt.Sprintf("%q does not exist in project %q", dep, project.ProjectID),
			})
		}
	}
	if len(fields) > 0 {
		return &domain.ValidationError{Fields: fields}
	}

	graph[taskID] = dependsOn
	if cycle := findTaskCycle(graph, taskID); len(cycle) > 0 {
		message := "dependency cycle: " + strings.Join(cycle, " → ")
		return &domain.ValidationError{
			Message: message,
			Fields:  []domain.FieldError{{Field: "depends_on", Reason: message}},
		}
	}
	return nil
}

// findTaskCycle runs a DFS over the project graph (node = business task id,
// edge = "depends on") and returns the cycle reachable from start as a closed
// path such as [A B C A], or nil when the graph is acyclic.
func findTaskCycle(graph map[string][]string, start string) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)

	color := make(map[string]int, len(graph))
	stack := make([]string, 0, len(graph))

	var visit func(node string) []string
	visit = func(node string) []string {
		color[node] = grey
		stack = append(stack, node)

		for _, dep := range graph[node] {
			switch color[dep] {
			case grey:
				from := 0
				for i, stacked := range stack {
					if stacked == dep {
						from = i
						break
					}
				}
				cycle := append([]string(nil), stack[from:]...)
				return append(cycle, dep)
			case white:
				if cycle := visit(dep); len(cycle) > 0 {
					return cycle
				}
			}
		}

		stack = stack[:len(stack)-1]
		color[node] = black
		return nil
	}

	return visit(start)
}

// GetTask returns one task with its dependencies and, when present, the current
// report. Lease is only rendered for an unexpired in_progress lease.
func (s *TaskService) GetTask(ctx context.Context, taskID string) (TaskDetail, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskDetail{}, invalidField("id", "is required")
	}

	row, err := store.GetTaskByTaskID(ctx, s.store.Read(), taskID)
	if err != nil {
		if store.IsNotFound(err) {
			return TaskDetail{}, &domain.NotFoundError{Resource: "task", Key: taskID}
		}
		return TaskDetail{}, err
	}
	return s.detail(ctx, s.store.Read(), row.ID)
}

// ListTasks returns one page ordered by priority DESC, seq_no ASC, id ASC.
//
// A project_id that does not exist is a 404 rather than an empty page: a
// mistyped project number must not look like a project without tasks.
func (s *TaskService) ListTasks(ctx context.Context, in ListTasksInput) (TaskPage, error) {
	if in.Limit < 1 || in.Limit > maxListLimit {
		return TaskPage{}, invalidField("limit", fmt.Sprintf("must be between 1 and %d", maxListLimit))
	}
	if in.After != nil {
		if in.After.SeqNo < 0 || in.After.ID < 0 {
			return TaskPage{}, invalidField("cursor", "is invalid")
		}
	}

	fields := make([]domain.FieldError, 0, len(in.Statuses))
	statuses := make([]int, 0, len(in.Statuses))
	seen := make(map[int]struct{}, len(in.Statuses))
	for _, status := range in.Statuses {
		if status < domain.TaskBacklog || status > domain.TaskCancelled {
			fields = append(fields, domain.FieldError{
				Field:  "status",
				Reason: fmt.Sprintf("must be between %d and %d", domain.TaskBacklog, domain.TaskCancelled),
			})
			continue
		}
		if _, dup := seen[status]; dup {
			continue
		}
		seen[status] = struct{}{}
		statuses = append(statuses, status)
	}
	if len(fields) > 0 {
		return TaskPage{}, &domain.ValidationError{Fields: fields}
	}

	filter := store.TaskFilter{Statuses: statuses, Limit: in.Limit + 1}
	if strings.TrimSpace(in.ProjectID) != "" {
		project, err := store.GetProjectByProjectID(ctx, s.store.Read(), strings.TrimSpace(in.ProjectID))
		if err != nil {
			if store.IsNotFound(err) {
				return TaskPage{}, &domain.NotFoundError{Resource: "project", Key: strings.TrimSpace(in.ProjectID)}
			}
			return TaskPage{}, err
		}
		filter.ProjectID = project.ID
	}
	if in.After != nil {
		filter.After = &store.TaskCursor{
			Priority: in.After.Priority,
			SeqNo:    in.After.SeqNo,
			ID:       in.After.ID,
		}
	}

	rows, err := store.ListTasks(ctx, s.store.Read(), filter)
	if err != nil {
		return TaskPage{}, err
	}

	page := TaskPage{Items: make([]TaskView, 0, min(len(rows), in.Limit))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		last := rows[len(rows)-1]
		page.Next = &TaskCursor{Priority: last.Priority, SeqNo: last.SeqNo, ID: last.ID}
	}

	deps, err := s.dependencies(ctx, s.store.Read(), rows)
	if err != nil {
		return TaskPage{}, err
	}
	for _, row := range rows {
		view, err := s.viewFromRow(row, deps[row.ID])
		if err != nil {
			return TaskPage{}, err
		}
		page.Items = append(page.Items, view)
	}
	return page, nil
}

// UpdateTask patches the three business-writable fields of one task.
//
// Platform-maintained columns never reach this method: the HTTP layer rejects
// them while decoding the body. The write bumps version and update_time and
// records the actor, but never touches status or reason.
func (s *TaskService) UpdateTask(ctx context.Context, taskID string, in UpdateTaskInput, actor string) (TaskDetail, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskDetail{}, invalidField("id", "is required")
	}
	normalized, fields := validateUpdateTask(in)
	if len(fields) > 0 {
		return TaskDetail{}, &domain.ValidationError{Fields: fields}
	}
	actor = actorOrDefault(actor)

	var out TaskDetail
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "task", Key: taskID}
			}
			return err
		}

		fieldsToWrite := store.TaskFields{
			Title:      row.Title,
			BodyMD:     row.BodyMD,
			Priority:   row.Priority,
			LastActor:  actor,
			UpdateTime: formatTimestamp(s.nextTimestamp(row.UpdateTime)),
		}
		if normalized.Title != nil {
			fieldsToWrite.Title = *normalized.Title
		}
		if normalized.BodyMD != nil {
			fieldsToWrite.BodyMD = *normalized.BodyMD
		}
		if normalized.Priority != nil {
			fieldsToWrite.Priority = *normalized.Priority
		}

		if err := store.UpdateTaskFields(ctx, tx, row.ID, fieldsToWrite); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, row.ID)
		return err
	})
	if err != nil {
		return TaskDetail{}, err
	}
	return out, nil
}

// DeleteTask removes a card that was never picked up: status must still be
// backlog and attempt_count must be 0. The guards, the dependency cleanup and
// the row removal share one write transaction.
func (s *TaskService) DeleteTask(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return invalidField("id", "is required")
	}

	return s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "task", Key: taskID}
			}
			return err
		}

		// Both guards name the reason the card is not removable; when both
		// apply the message carries both facts so the client needs no extra
		// round trip to learn why.
		switch {
		case row.Status != domain.TaskBacklog && row.AttemptCount != 0:
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"task %q has been claimed %d time(s) and is in status %s; only backlog tasks that were never claimed can be deleted",
				taskID, row.AttemptCount, domain.TaskStatusName(row.Status))}
		case row.Status != domain.TaskBacklog:
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"task %q is in status %s; only backlog tasks can be deleted",
				taskID, domain.TaskStatusName(row.Status))}
		case row.AttemptCount != 0:
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"task %q has been claimed %d time(s); only tasks that were never claimed can be deleted",
				taskID, row.AttemptCount)}
		}

		if _, err := store.DeleteDependenciesTouching(ctx, tx, row.ID); err != nil {
			return err
		}
		return store.DeleteTask(ctx, tx, row.ID)
	})
}

// detail loads one task plus its dependencies and report. q may be a
// transaction (right after a write) or the read pool.
func (s *TaskService) detail(ctx context.Context, q store.Querier, physicalID int64) (TaskDetail, error) {
	row, err := store.GetTaskByID(ctx, q, physicalID)
	if err != nil {
		return TaskDetail{}, err
	}
	dependsOn, err := store.ListDependenciesByTask(ctx, q, row.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	view, err := s.viewFromRow(row, dependsOn)
	if err != nil {
		return TaskDetail{}, err
	}

	detail := TaskDetail{Task: view}
	if row.ReportMaterialID != nil {
		report, err := store.GetMaterialByID(ctx, q, *row.ReportMaterialID)
		if err != nil {
			return TaskDetail{}, err
		}
		mapped, err := materialFromRow(report)
		if err != nil {
			return TaskDetail{}, err
		}
		detail.Report = &mapped
	}
	return detail, nil
}

// dependencies resolves the dependencies of a whole page in one query.
func (s *TaskService) dependencies(ctx context.Context, q store.Querier, rows []store.Task) (map[int64][]string, error) {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return store.TaskDependenciesByTaskIDs(ctx, q, ids)
}

// viewFromRow converts the store read model into the use-case view. The lease
// is rendered only while the task is in progress and the deadline has not
// passed; reclaiming an expired lease is Task-10's job, not a read side effect.
func (s *TaskService) viewFromRow(row store.Task, dependsOn []string) (TaskView, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return TaskView{}, err
	}
	updateTime, err := parseTimestamp(row.UpdateTime)
	if err != nil {
		return TaskView{}, err
	}
	if dependsOn == nil {
		dependsOn = []string{}
	}

	view := TaskView{
		ID:           row.TaskID,
		TaskID:       row.TaskID,
		ProjectID:    row.ProjectCode,
		SeqNo:        row.SeqNo,
		Title:        row.Title,
		BodyMD:       row.BodyMD,
		Priority:     row.Priority,
		Status:       row.Status,
		WorkerID:     row.WorkerID,
		AttemptCount: row.AttemptCount,
		MaxAttempts:  row.MaxAttempts,
		Reason:       stringValue(row.Reason),
		LastActor:    stringValue(row.LastActor),
		Version:      row.Version,
		DependsOn:    dependsOn,
		CreateTime:   createTime,
		UpdateTime:   updateTime,
	}

	if row.Status == domain.TaskInProgress && row.LeaseID != nil && row.LeaseUntil != nil {
		until, err := parseTimestamp(*row.LeaseUntil)
		if err != nil {
			return TaskView{}, err
		}
		if !s.now().UTC().After(until) {
			view.Lease = &TaskLease{LeaseID: *row.LeaseID, Until: until}
		}
	}
	return view, nil
}

// validateCreateTask normalises one create request. Field names carry no
// tasks[i] prefix: this endpoint creates a single card.
func validateCreateTask(in CreateTaskInput) (CreateTaskInput, []domain.FieldError) {
	var fields []domain.FieldError

	title, titleFields := validateTaskTitle("title", in.Title)
	fields = append(fields, titleFields...)
	in.Title = title

	body, bodyFields := validateTaskBody(in.BodyMD)
	fields = append(fields, bodyFields...)
	in.BodyMD = body

	fields = append(fields, validateTaskPriority(in.Priority)...)

	dependsOn := make([]string, 0, len(in.DependsOn))
	seen := make(map[string]struct{}, len(in.DependsOn))
	for _, dep := range in.DependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			fields = append(fields, domain.FieldError{Field: "depends_on", Reason: "must contain non-empty task ids"})
			continue
		}
		if _, dup := seen[dep]; dup {
			fields = append(fields, domain.FieldError{
				Field:  "depends_on",
				Reason: fmt.Sprintf("must not contain duplicate task id %q", dep),
			})
			continue
		}
		seen[dep] = struct{}{}
		dependsOn = append(dependsOn, dep)
	}
	in.DependsOn = dependsOn

	return in, fields
}

// validateUpdateTask normalises a patch and requires at least one writable
// field. The messages match the create path, so a client sees the same reason
// whichever endpoint rejected the value.
func validateUpdateTask(in UpdateTaskInput) (UpdateTaskInput, []domain.FieldError) {
	var fields []domain.FieldError

	if in.Title == nil && in.BodyMD == nil && in.Priority == nil {
		fields = append(fields, domain.FieldError{
			Field:  "body",
			Reason: "at least one of title, body_md, priority is required",
		})
	}

	if in.Title != nil {
		title, titleFields := validateTaskTitle("title", *in.Title)
		fields = append(fields, titleFields...)
		in.Title = &title
	}
	if in.BodyMD != nil {
		body, bodyFields := validateTaskBody(*in.BodyMD)
		fields = append(fields, bodyFields...)
		in.BodyMD = &body
	}
	if in.Priority != nil {
		fields = append(fields, validateTaskPriority(*in.Priority)...)
	}
	return in, fields
}

// validateTaskTitle trims a title and enforces the character bound.
func validateTaskTitle(field, title string) (string, []domain.FieldError) {
	trimmed := strings.TrimSpace(title)
	switch {
	case trimmed == "":
		return trimmed, []domain.FieldError{{Field: field, Reason: "is required"}}
	case utf8.RuneCountInString(trimmed) > MaxTaskTitleLen:
		return trimmed, []domain.FieldError{{
			Field:  field,
			Reason: fmt.Sprintf("must be at most %d characters", MaxTaskTitleLen),
		}}
	}
	return trimmed, nil
}

// validateTaskBody rejects an empty body and enforces the byte cap. The body is
// stored byte-for-byte (no trimming): markdown whitespace is content.
func validateTaskBody(body string) (string, []domain.FieldError) {
	switch {
	case strings.TrimSpace(body) == "":
		return body, []domain.FieldError{{Field: "body_md", Reason: "is required"}}
	case len(body) > MaxTaskBodyBytes:
		return body, []domain.FieldError{{
			Field:  "body_md",
			Reason: fmt.Sprintf("must be at most %d bytes", MaxTaskBodyBytes),
		}}
	}
	return body, nil
}

// validateTaskPriority enforces the optional scheduling hint range.
func validateTaskPriority(priority int) []domain.FieldError {
	if priority < MinTaskPriority || priority > MaxTaskPriority {
		return []domain.FieldError{{
			Field:  "priority",
			Reason: fmt.Sprintf("must be between %d and %d", MinTaskPriority, MaxTaskPriority),
		}}
	}
	return nil
}

// invalidField builds a one-field validation error.
func invalidField(field, reason string) *domain.ValidationError {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Reason: reason}}}
}

// timestamp returns the current time truncated to milliseconds, the precision
// persisted in create_time/update_time.
func (s *TaskService) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// nextTimestamp returns a timestamp strictly after the persisted update_time,
// so two patches in the same millisecond still order deterministically.
func (s *TaskService) nextTimestamp(previous string) time.Time {
	next := s.timestamp()
	old, err := parseTimestamp(previous)
	if err != nil {
		return next
	}
	if !next.After(old) {
		return old.Add(time.Millisecond)
	}
	return next
}
