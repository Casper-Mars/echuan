package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

const (
	// MaxPlanTasks bounds one POST /v1/projects/plans batch. The upper limit is
	// a request-size guard, not a business rule: a plan larger than this must be
	// split by the CTO.
	MaxPlanTasks = 200

	// MaxTaskTitleLen bounds a task title in characters (same bound as a
	// project title, so the two read consistently in the UI).
	MaxTaskTitleLen = 200

	// MaxTaskBodyBytes bounds one task body_md. Task bodies are working notes,
	// not material uploads, so they do not share material_max_bytes.
	MaxTaskBodyBytes = 64 * 1024

	// MinTaskPriority / MaxTaskPriority bound the optional scheduling hint.
	MinTaskPriority = -1000
	MaxTaskPriority = 1000

	// defaultTaskMaxAttempts mirrors the tasks.max_attempts column default and
	// is used when the service is built without an explicit config value.
	defaultTaskMaxAttempts = 3
)

// PlanTask is one task of a submitted breakdown. DependsOn holds 1-based
// sequence numbers of the same batch (not task ids); SubmitPlan translates them
// into the real ids after the rows exist.
type PlanTask struct {
	Title     string
	BodyMD    string
	Priority  int
	DependsOn []int
}

// PlannedTask is one persisted task as reported by SubmitPlan. DependsOn is
// the list of business task ids ("APP-1-T0001") the task depends on.
type PlannedTask struct {
	ID          string
	SeqNo       int
	Title       string
	Priority    int
	Status      int
	DependsOn   []string
	BodyMD      string
	LastActor   string
	MaxAttempts int
	Version     int
}

// PlanResult is the outcome of one POST /v1/projects/plans: the project as it
// looks after the transition plus every task that was inserted, in seq order.
type PlanResult struct {
	Project domain.Project
	Tasks   []PlannedTask
}

// SetMaxAttempts overrides the attempt budget written to new task rows. Values
// <= 0 are ignored so a misconfigured value never produces an unclaimable task.
func (s *ProjectService) SetMaxAttempts(n int) {
	if n > 0 {
		s.maxAttempts = n
	}
}

// SubmitPlan inserts one task breakdown and moves the project from planning to
// plan_review (tech design §4.5, §5.8).
//
// Validation happens before the transaction, so a bad payload has no side
// effect at all. The transaction then reads the project, guards its status,
// takes a contiguous seq range with NextSeqNo, inserts tasks, resolves the
// batch sequence numbers to physical ids, inserts task_deps, and finally moves
// the project (status + version + last_actor + update_time). Any error rolls
// the whole batch back: no task, no edge, no version bump.
//
// reason is deliberately preserved: a rejection objection stays readable while
// the CTO re-plans (the same policy as spec submission and rework).
func (s *ProjectService) SubmitPlan(ctx context.Context, projectID, actor string, tasks []PlanTask) (PlanResult, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return PlanResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	normalized, verr := validatePlan(tasks)
	if verr != nil {
		return PlanResult{}, verr
	}

	actor = actorOrDefault(actor)

	var result PlanResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}
		// Submitting is only legal in planning. plan_review rejects a repeat
		// submission here, before any write, so a duplicate request is a clean
		// 409 invalid_state with zero side effects.
		if row.Status != domain.StatusPlanning {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q is in status %s; tasks can only be submitted in planning",
				projectID, domain.StatusName(row.Status))}
		}

		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))

		// One NextSeqNo call for the whole batch: fill-in order means
		// seq, seq+1, … and task_id derives from the same number, so the two
		// can never drift apart or leave a hole.
		firstSeq, err := store.NextSeqNo(ctx, tx, "tasks", "project_id", row.ID)
		if err != nil {
			return err
		}

		created := make([]int64, len(normalized))
		for i, task := range normalized {
			seq := firstSeq + i
			taskID := fmt.Sprintf("%s-T%04d", row.ProjectID, seq)
			id, err := store.CreateTask(ctx, tx, store.NewTask{
				TaskID:      taskID,
				ProjectID:   row.ID,
				SeqNo:       seq,
				Title:       task.Title,
				BodyMD:      task.BodyMD,
				Priority:    task.Priority,
				Status:      domain.TaskBacklog,
				MaxAttempts: s.maxAttempts,
				LastActor:   actor,
				CreateTime:  updateTime,
				UpdateTime:  updateTime,
			})
			if err != nil {
				if errors.Is(err, store.ErrUniqueViolation) {
					return &domain.ConflictError{Reason: fmt.Sprintf(
						"task %q already exists in project %q", taskID, projectID)}
				}
				return err
			}
			created[i] = id
		}

		for i, task := range normalized {
			for _, dependsOn := range task.DependsOn {
				if err := store.InsertTaskDependency(ctx, tx, created[i], created[dependsOn-1], updateTime); err != nil {
					return err
				}
			}
		}

		// reason is passed through unchanged (not cleared): the objection that
		// produced this re-plan must stay readable.
		affected, err := store.TransitionProject(ctx, tx, row.ID,
			row.Status, row.Version, domain.StatusPlanReview, row.Reason, actor, updateTime)
		if err != nil {
			return err
		}
		if affected == 0 {
			// Unreachable with the single-writer pool, but reporting the fresh
			// version is the right answer if it ever happens.
			fresh, readErr := store.GetProjectByProjectID(ctx, tx, projectID)
			if readErr != nil {
				return readErr
			}
			return &domain.InvalidVersionError{Current: fresh.Version, Expected: row.Version}
		}

		result, err = s.planResult(ctx, tx, row.ID, created)
		return err
	})
	if err != nil {
		return PlanResult{}, err
	}
	return result, nil
}

// planResult reads the committed batch back inside the transaction, so the
// response reports rows (and dependency edges) that really exist rather than
// echoing the request. created narrows the result to this submission: a project
// can legitimately hold older rows (a rework task, for example) and they must
// not leak into the response.
func (s *ProjectService) planResult(ctx context.Context, q store.Querier, projectPhysicalID int64, created []int64) (PlanResult, error) {
	fresh, err := store.GetProjectByID(ctx, q, projectPhysicalID)
	if err != nil {
		return PlanResult{}, err
	}
	project, err := projectFromRow(fresh)
	if err != nil {
		return PlanResult{}, err
	}

	rows, err := store.ListTasksByProject(ctx, q, projectPhysicalID)
	if err != nil {
		return PlanResult{}, err
	}
	deps, err := store.ListTaskDependencies(ctx, q, projectPhysicalID)
	if err != nil {
		return PlanResult{}, err
	}

	inBatch := make(map[int64]struct{}, len(created))
	for _, id := range created {
		inBatch[id] = struct{}{}
	}

	codeByID := make(map[int64]string, len(rows))
	for _, row := range rows {
		codeByID[row.ID] = row.TaskID
	}
	depsByTask := make(map[int64][]string, len(created))
	for _, dep := range deps {
		if _, ok := inBatch[dep.TaskID]; !ok {
			continue
		}
		code, ok := codeByID[dep.DependsOn]
		if !ok {
			// A dangling edge would be a foreign-key violation; report it
			// instead of emitting an empty string to the client.
			return PlanResult{}, fmt.Errorf("store: task_deps %d -> %d references a task outside project %d",
				dep.TaskID, dep.DependsOn, projectPhysicalID)
		}
		depsByTask[dep.TaskID] = append(depsByTask[dep.TaskID], code)
	}

	planned := make([]PlannedTask, 0, len(created))
	for _, row := range rows {
		if _, ok := inBatch[row.ID]; !ok {
			continue
		}
		dependsOn := depsByTask[row.ID]
		if dependsOn == nil {
			dependsOn = []string{}
		}
		planned = append(planned, PlannedTask{
			ID:          row.TaskID,
			SeqNo:       row.SeqNo,
			Title:       row.Title,
			Priority:    row.Priority,
			Status:      row.Status,
			DependsOn:   dependsOn,
			BodyMD:      row.BodyMD,
			LastActor:   stringValue(row.LastActor),
			MaxAttempts: row.MaxAttempts,
			Version:     row.Version,
		})
	}
	return PlanResult{Project: project, Tasks: planned}, nil
}

// validatePlan normalises the batch and returns every field-level failure.
//
// Structural checks (count, title, body, priority, depends_on shape) run first;
// the graph check only runs when the shape is valid, because a cycle message
// that names a sequence number is meaningless while some numbers are out of
// range.
func validatePlan(tasks []PlanTask) ([]PlanTask, *domain.ValidationError) {
	var fields []domain.FieldError

	switch {
	case len(tasks) == 0:
		fields = append(fields, domain.FieldError{Field: "tasks", Reason: "must contain at least 1 task"})
	case len(tasks) > MaxPlanTasks:
		fields = append(fields, domain.FieldError{
			Field:  "tasks",
			Reason: fmt.Sprintf("must contain at most %d tasks", MaxPlanTasks),
		})
		return nil, &domain.ValidationError{Fields: fields}
	}

	normalized := make([]PlanTask, len(tasks))
	for i, task := range tasks {
		prefix := fmt.Sprintf("tasks[%d]", i)
		normalized[i] = task

		normalized[i].Title = strings.TrimSpace(task.Title)
		switch {
		case normalized[i].Title == "":
			fields = append(fields, domain.FieldError{Field: prefix + ".title", Reason: "is required"})
		case utf8.RuneCountInString(normalized[i].Title) > MaxTaskTitleLen:
			fields = append(fields, domain.FieldError{
				Field:  prefix + ".title",
				Reason: fmt.Sprintf("must be at most %d characters", MaxTaskTitleLen),
			})
		}

		if strings.TrimSpace(task.BodyMD) == "" {
			fields = append(fields, domain.FieldError{Field: prefix + ".body_md", Reason: "is required"})
		} else if len(task.BodyMD) > MaxTaskBodyBytes {
			fields = append(fields, domain.FieldError{
				Field:  prefix + ".body_md",
				Reason: fmt.Sprintf("must be at most %d bytes", MaxTaskBodyBytes),
			})
		}

		if task.Priority < MinTaskPriority || task.Priority > MaxTaskPriority {
			fields = append(fields, domain.FieldError{
				Field:  prefix + ".priority",
				Reason: fmt.Sprintf("must be between %d and %d", MinTaskPriority, MaxTaskPriority),
			})
		}

		fields = append(fields, validateDependsOn(prefix, i, task.DependsOn, len(tasks))...)
	}
	if len(fields) > 0 {
		return nil, &domain.ValidationError{Fields: fields}
	}

	if cycle := findDependencyCycle(normalized); len(cycle) > 0 {
		message := formatDependencyCycle(cycle)
		return nil, &domain.ValidationError{
			Message: message,
			Fields: []domain.FieldError{{
				Field:  fmt.Sprintf("tasks[%d].depends_on", cycle[0]-1),
				Reason: message,
			}},
		}
	}
	return normalized, nil
}

// validateDependsOn checks one depends_on array: range, self-reference and
// duplicates. Forward references are explicitly allowed (the graph check deals
// with the resulting cycles).
func validateDependsOn(prefix string, index int, dependsOn []int, total int) []domain.FieldError {
	field := prefix + ".depends_on"
	seen := make(map[int]struct{}, len(dependsOn))
	fields := make([]domain.FieldError, 0)

	for _, dep := range dependsOn {
		switch {
		case dep < 1 || dep > total:
			fields = append(fields, domain.FieldError{
				Field:  field,
				Reason: fmt.Sprintf("must be a batch sequence number between 1 and %d", total),
			})
			continue
		case dep == index+1:
			fields = append(fields, domain.FieldError{Field: field, Reason: "must not depend on itself"})
			continue
		}
		if _, dup := seen[dep]; dup {
			fields = append(fields, domain.FieldError{
				Field:  field,
				Reason: fmt.Sprintf("must not contain duplicate sequence number %d", dep),
			})
			continue
		}
		seen[dep] = struct{}{}
	}
	return fields
}

// findDependencyCycle runs a DFS over the batch graph (node = 1-based batch
// sequence number, edge = "depends on") and returns the cycle as a closed path
// such as [1 2 3 1], or nil when the graph is acyclic.
//
// Colour marks: white = unvisited, grey = on the current DFS stack, black =
// fully explored. An edge to a grey node closes a cycle; the nodes from that
// node's position in the stack form the path.
func findDependencyCycle(tasks []PlanTask) []int {
	const (
		white = 0
		grey  = 1
		black = 2
	)

	color := make([]int, len(tasks)+1)
	stack := make([]int, 0, len(tasks))

	var visit func(node int) []int
	visit = func(node int) []int {
		color[node] = grey
		stack = append(stack, node)

		for _, dep := range tasks[node-1].DependsOn {
			switch color[dep] {
			case grey:
				start := 0
				for i, stacked := range stack {
					if stacked == dep {
						start = i
						break
					}
				}
				cycle := append([]int(nil), stack[start:]...)
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

	for node := 1; node <= len(tasks); node++ {
		if color[node] != white {
			continue
		}
		if cycle := visit(node); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}

// formatDependencyCycle renders the cycle path the way the API reports it:
// "dependency cycle: 1 → 2 → 3 → 1".
func formatDependencyCycle(cycle []int) string {
	parts := make([]string, len(cycle))
	for i, node := range cycle {
		parts[i] = fmt.Sprintf("%d", node)
	}
	return "dependency cycle: " + strings.Join(parts, " → ")
}
