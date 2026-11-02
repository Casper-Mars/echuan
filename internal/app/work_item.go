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
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Work-item use cases (tech design v0.8 §4.2 / §4.3).
//
// A work item is the single dispatch entity of v2: requirement, design,
// breakdown and dev cards share one table, one queue and one seven-state text
// machine. The API exposes the state verbatim; the app layer only enforces the
// business rules around creation, editing and removal.

const (
	// MaxWorkItemTitleLen bounds a title in characters.
	MaxWorkItemTitleLen = 200

	// MaxWorkItemBodyBytes bounds one body_md. Bodies are working notes, not
	// material uploads, so they do not share material_max_bytes.
	MaxWorkItemBodyBytes = 64 * 1024

	// MinWorkItemPriority / MaxWorkItemPriority bound the optional scheduling
	// hint.
	MinWorkItemPriority = -1000
	MaxWorkItemPriority = 1000

	// defaultWorkItemMaxAttempts mirrors work_items.max_attempts.
	defaultWorkItemMaxAttempts = 3
)

// WorkItemLease is the live lease of a leased work item.
type WorkItemLease struct {
	LeaseID string
	Until   time.Time
}

// WorkItemView is the read model the work-item endpoints render.
//
// DependsOn holds business work ids ordered by the dependency's seq_no; Lease
// is nil unless the item is leased and the deadline is still valid. WorkerID is
// the executor of a leased card and nil for every other state.
type WorkItemView struct {
	WorkID           string
	ProjectID        string
	ParentID         *string
	Kind             string
	SeqNo            int
	Title            string
	BodyMD           string
	Priority         int
	State            string
	WorkerID         *string
	Attempt          int
	MaxAttempts      int
	Reason           string
	Version          int
	DependsOn        []string
	ResultMaterialID *string
	Lease            *WorkItemLease
	CreateTime       time.Time
	UpdateTime       time.Time
}

// WorkItemDetail adds the result material to a work item. Result is nil until
// a report points the item at one.
type WorkItemDetail struct {
	Item   WorkItemView
	Result *domain.Material
}

// WorkItemCursor is the decoded keyset position of the last row of a page.
type WorkItemCursor struct {
	Priority int
	SeqNo    int
	ID       int64
}

// ListWorkItemsInput is the use-case input of GET /v1/work-items.
type ListWorkItemsInput struct {
	ProjectID string
	Kinds     []string
	States    []string
	After     *WorkItemCursor
	Limit     int
}

// WorkItemPage is one page of work items plus the position of the last returned
// row when more rows exist.
type WorkItemPage struct {
	Items []WorkItemView
	Next  *WorkItemCursor
}

// CreateWorkItemInput is the use-case input of POST /v1/work-items.
type CreateWorkItemInput struct {
	ProjectID string
	ParentID  string
	Kind      string
	Title     string
	BodyMD    string
	Priority  int
	DependsOn []string
}

// UpdateWorkItemInput is the use-case input of PATCH /v1/work-items. Nil means
// "field not supplied"; only these three fields are business-writable.
type UpdateWorkItemInput struct {
	Title    *string
	BodyMD   *string
	Priority *int
}

// WorkItemService implements the work-item use cases. It owns the business
// rules and the transaction boundaries; SQL stays in the store package.
type WorkItemService struct {
	store *store.Store
	// materials writes the kind=4 plan material of a breakdown report. It may
	// be nil in tests that only exercise the store; production always injects
	// the same writer the project service uses.
	materials   *material.Writer
	now         func() time.Time
	maxAttempts int
}

// NewWorkItemService builds the work-item service on top of an open store.
func NewWorkItemService(st *store.Store, materials *material.Writer) *WorkItemService {
	return &WorkItemService{
		store:       st,
		materials:   materials,
		now:         func() time.Time { return time.Now().UTC() },
		maxAttempts: defaultWorkItemMaxAttempts,
	}
}

// SetMaxAttempts overrides the attempt budget written to new items. Values
// <= 0 are ignored so a misconfigured value never produces an unclaimable card.
func (s *WorkItemService) SetMaxAttempts(n int) {
	if n > 0 {
		s.maxAttempts = n
	}
}

// CreateWorkItem inserts one card into a project that is still being built
// (tasks_draft, tasks_review) or has already been approved (pending_done).
//
// A new card lands in blocked: the plan review gate is what queues it. When the
// project is still in tasks_draft and the card is a dev card, the insert opens
// the batch review gate in the same transaction (4 → 5), so the first dev card
// always makes the batch reviewable. Validation of the payload runs first: a bad
// request leaves no row behind.
func (s *WorkItemService) CreateWorkItem(ctx context.Context, in CreateWorkItemInput, actor string) (WorkItemDetail, error) {
	in.ProjectID = strings.TrimSpace(in.ProjectID)
	if in.ProjectID == "" {
		return WorkItemDetail{}, invalidField("project_id", "is required")
	}

	normalized, fields := validateCreateWorkItem(in)
	if len(fields) > 0 {
		return WorkItemDetail{}, &domain.ValidationError{Fields: fields}
	}
	actor = actorOrDefault(actor)

	var out WorkItemDetail
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		project, err := store.GetProjectByProjectID(ctx, tx, normalized.ProjectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: normalized.ProjectID}
			}
			return err
		}
		if project.Status != domain.StatusTasksDraft &&
			project.Status != domain.StatusTasksReview &&
			project.Status != domain.StatusPendingDone {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q is in status %s; work items can only be created in tasks_draft, tasks_review or pending_done",
				normalized.ProjectID, domain.StatusName(project.Status))}
		}

		now, err := platform.NewWorkID(s.timestamp())
		if err != nil {
			return err
		}

		var parentID *int64
		if normalized.ParentID != "" {
			parent, err := store.GetWorkItemByWorkID(ctx, tx, normalized.ParentID)
			if err != nil {
				if store.IsNotFound(err) {
					return invalidField("parent_id", fmt.Sprintf("%q does not exist", normalized.ParentID))
				}
				return err
			}
			if parent.ProjectCode != project.ProjectID {
				return invalidField("parent_id", fmt.Sprintf("%q is not part of project %q", normalized.ParentID, project.ProjectID))
			}
			parentID = &parent.ID
		}

		seq, err := store.NextSeqNo(ctx, tx, "work_items", "project_id", project.ID)
		if err != nil {
			return err
		}

		for _, dependsOn := range normalized.DependsOn {
			if dependsOn == now {
				return &domain.ValidationError{
					Fields: []domain.FieldError{{Field: "depends_on", Reason: "must not depend on itself"}},
				}
			}
		}

		if verr := s.validateDependencyGraph(ctx, tx, project, normalized.DependsOn); verr != nil {
			return verr
		}

		ts := formatTimestamp(s.timestamp())
		id, err := store.CreateWorkItem(ctx, tx, store.NewWorkItem{
			WorkID:      now,
			ProjectID:   project.ID,
			ParentID:    parentID,
			Kind:        normalized.Kind,
			SeqNo:       seq,
			Title:       normalized.Title,
			BodyMD:      normalized.BodyMD,
			Priority:    normalized.Priority,
			State:       store.WorkItemStateBlocked,
			MaxAttempts: s.maxAttempts,
			CreateTime:  ts,
			UpdateTime:  ts,
		})
		if err != nil {
			if errors.Is(err, store.ErrUniqueViolation) {
				return &domain.ConflictError{Reason: fmt.Sprintf(
					"work item %q already exists in project %q", now, normalized.ProjectID)}
			}
			return err
		}

		for _, dependsOn := range normalized.DependsOn {
			if err := store.CreateWorkDependencyByWorkIDs(ctx, tx, now, dependsOn); err != nil {
				return err
			}
		}

		if project.Status == domain.StatusTasksDraft && normalized.Kind == "dev" {
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

		if err := s.clearTasksReviewRejection(ctx, tx, project.ProjectID, actor); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, id)
		return err
	})
	if err != nil {
		return WorkItemDetail{}, err
	}
	return out, nil
}

// validateDependencyGraph checks that every dependency exists in the same
// project and that adding the edges keeps the graph acyclic.
func (s *WorkItemService) validateDependencyGraph(ctx context.Context, tx *sql.Tx, project store.Project, dependsOn []string) error {
	if len(dependsOn) == 0 {
		return nil
	}

	refs, err := store.WorkItemRefsByProject(ctx, tx, project.ID)
	if err != nil {
		return err
	}
	codeByID := make(map[int64]string, len(refs))
	exists := make(map[string]struct{}, len(refs))
	graph := make(map[string][]string, len(refs)+1)
	for _, ref := range refs {
		codeByID[ref.ID] = ref.WorkID
		exists[ref.WorkID] = struct{}{}
		graph[ref.WorkID] = nil
	}

	edges, err := store.ListWorkDependencies(ctx, tx, project.ID)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		owner, ok := codeByID[edge.WorkItemID]
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

	// The new card is not inserted yet: a cycle can only appear through edges
	// that point back at an existing card, so checking the existing graph is
	// enough. A self edge was rejected by the caller.
	if cycle := findWorkItemCycle(graph); len(cycle) > 0 {
		message := "dependency cycle: " + strings.Join(cycle, " → ")
		return &domain.ValidationError{
			Message: message,
			Fields:  []domain.FieldError{{Field: "depends_on", Reason: message}},
		}
	}
	return nil
}

// findWorkItemCycle runs a DFS over the project graph and returns the first
// cycle as a closed path such as [A B C A], or nil when acyclic.
func findWorkItemCycle(graph map[string][]string) []string {
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

	for node := range graph {
		if color[node] == white {
			if cycle := visit(node); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

// GetWorkItem returns one item with its dependencies and, when present, the
// result material. Lease is only rendered for an unexpired lease.
func (s *WorkItemService) GetWorkItem(ctx context.Context, workID string) (WorkItemDetail, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return WorkItemDetail{}, invalidField("work_id", "is required")
	}

	row, err := store.GetWorkItemByWorkID(ctx, s.store.Read(), workID)
	if err != nil {
		if store.IsNotFound(err) {
			return WorkItemDetail{}, &domain.NotFoundError{Resource: "work_item", Key: workID}
		}
		return WorkItemDetail{}, err
	}
	return s.detail(ctx, s.store.Read(), row.ID)
}

// ListWorkItems returns one page ordered by priority DESC, seq_no ASC, id ASC.
//
// A project_id that does not exist is a 404 rather than an empty page: a
// mistyped project number must not look like a project without work items.
func (s *WorkItemService) ListWorkItems(ctx context.Context, in ListWorkItemsInput) (WorkItemPage, error) {
	if in.Limit < 1 || in.Limit > maxListLimit {
		return WorkItemPage{}, invalidField("limit", fmt.Sprintf("must be between 1 and %d", maxListLimit))
	}
	if in.After != nil {
		if in.After.SeqNo < 0 || in.After.ID < 0 {
			return WorkItemPage{}, invalidField("cursor", "is invalid")
		}
	}

	kinds, fields := normalizeEnums("kind", in.Kinds, store.IsValidWorkItemKind)
	if len(fields) > 0 {
		return WorkItemPage{}, &domain.ValidationError{Fields: fields}
	}
	states, stateFields := normalizeEnums("state", in.States, isValidWorkItemState)
	fields = append(fields, stateFields...)
	if len(fields) > 0 {
		return WorkItemPage{}, &domain.ValidationError{Fields: fields}
	}

	filter := store.WorkItemFilter{Kinds: kinds, States: states, Limit: in.Limit + 1}
	if strings.TrimSpace(in.ProjectID) != "" {
		project, err := store.GetProjectByProjectID(ctx, s.store.Read(), strings.TrimSpace(in.ProjectID))
		if err != nil {
			if store.IsNotFound(err) {
				return WorkItemPage{}, &domain.NotFoundError{Resource: "project", Key: strings.TrimSpace(in.ProjectID)}
			}
			return WorkItemPage{}, err
		}
		filter.ProjectID = project.ID
	}
	if in.After != nil {
		filter.After = &store.WorkItemCursor{
			Priority: in.After.Priority,
			SeqNo:    in.After.SeqNo,
			ID:       in.After.ID,
		}
	}

	rows, err := store.ListWorkItems(ctx, s.store.Read(), filter)
	if err != nil {
		return WorkItemPage{}, err
	}

	page := WorkItemPage{Items: make([]WorkItemView, 0, min(len(rows), in.Limit))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		last := rows[len(rows)-1]
		page.Next = &WorkItemCursor{Priority: last.Priority, SeqNo: last.SeqNo, ID: last.ID}
	}

	deps, err := s.dependencies(ctx, s.store.Read(), rows)
	if err != nil {
		return WorkItemPage{}, err
	}

	// Resolve every referenced result material in one query: the list exposes
	// result_material_id so the UI can show/refresh the reviewed version, and a
	// per-row lookup would be an N+1 on the board.
	physicalIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.ResultMaterialID != nil {
			physicalIDs = append(physicalIDs, *row.ResultMaterialID)
		}
	}
	materials, err := store.GetMaterialsByIDs(ctx, s.store.Read(), physicalIDs)
	if err != nil {
		return WorkItemPage{}, err
	}

	for _, row := range rows {
		var result *domain.Material
		if row.ResultMaterialID != nil {
			mat, ok := materials[*row.ResultMaterialID]
			if !ok {
				return WorkItemPage{}, fmt.Errorf("work item %s: result material %d not found", row.WorkID, *row.ResultMaterialID)
			}
			mapped, err := materialFromRow(mat)
			if err != nil {
				return WorkItemPage{}, err
			}
			result = &mapped
		}
		view, err := s.viewFromRow(ctx, s.store.Read(), row, deps[row.ID], result)
		if err != nil {
			return WorkItemPage{}, err
		}
		page.Items = append(page.Items, view)
	}
	return page, nil
}

// UpdateWorkItem patches the three business-writable fields of one item.
func (s *WorkItemService) UpdateWorkItem(ctx context.Context, workID string, in UpdateWorkItemInput, actor string) (WorkItemDetail, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return WorkItemDetail{}, invalidField("work_id", "is required")
	}
	normalized, fields := validateUpdateWorkItem(in)
	if len(fields) > 0 {
		return WorkItemDetail{}, &domain.ValidationError{Fields: fields}
	}
	actor = actorOrDefault(actor)

	var out WorkItemDetail
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetWorkItemByWorkID(ctx, tx, workID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "work_item", Key: workID}
			}
			return err
		}

		fieldsToWrite := store.WorkItemFields{
			Title:      row.Title,
			BodyMD:     row.BodyMD,
			Priority:   row.Priority,
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

		if err := store.UpdateWorkItemFields(ctx, tx, row.ID, fieldsToWrite); err != nil {
			return err
		}

		// Editing a card is a batch change too: a recorded rejection stops
		// describing the batch, so it is dropped in the same transaction.
		if err := s.clearTasksReviewRejection(ctx, tx, row.ProjectCode, actor); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, row.ID)
		return err
	})
	if err != nil {
		return WorkItemDetail{}, err
	}
	return out, nil
}

// DeleteWorkItem removes a card that was never picked up: state must still be
// blocked and attempt must be 0. The guards, the dependency cleanup and the row
// removal share one write transaction.
func (s *WorkItemService) DeleteWorkItem(ctx context.Context, workID, actor string) error {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return invalidField("work_id", "is required")
	}
	actor = actorOrDefault(actor)

	return s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetWorkItemByWorkID(ctx, tx, workID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "work_item", Key: workID}
			}
			return err
		}

		switch {
		case row.State != store.WorkItemStateBlocked && row.Attempt != 0:
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"work item %q has been claimed %d time(s) and is in state %s; only blocked items that were never claimed can be deleted",
				workID, row.Attempt, row.State)}
		case row.State != store.WorkItemStateBlocked:
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"work item %q is in state %s; only blocked items can be deleted", workID, row.State)}
		case row.Attempt != 0:
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"work item %q has been claimed %d time(s); only items that were never claimed can be deleted",
				workID, row.Attempt)}
		}

		if _, err := store.DeleteDependenciesTouching(ctx, tx, row.ID); err != nil {
			return err
		}
		if err := s.clearTasksReviewRejection(ctx, tx, row.ProjectCode, actor); err != nil {
			return err
		}
		return store.DeleteWorkItem(ctx, tx, row.ID)
	})
}

// detail loads one item plus its dependencies and result material.
func (s *WorkItemService) detail(ctx context.Context, q store.Querier, physicalID int64) (WorkItemDetail, error) {
	row, err := store.GetWorkItemByID(ctx, q, physicalID)
	if err != nil {
		return WorkItemDetail{}, err
	}
	dependsOn, err := store.ListDependenciesByWorkItem(ctx, q, row.ID)
	if err != nil {
		return WorkItemDetail{}, err
	}

	detail := WorkItemDetail{}
	if row.ResultMaterialID != nil {
		result, err := store.GetMaterialByID(ctx, q, *row.ResultMaterialID)
		if err != nil {
			return WorkItemDetail{}, err
		}
		mapped, err := materialFromRow(result)
		if err != nil {
			return WorkItemDetail{}, err
		}
		detail.Result = &mapped
	}

	view, err := s.viewFromRow(ctx, q, row, dependsOn, detail.Result)
	if err != nil {
		return WorkItemDetail{}, err
	}
	detail.Item = view
	return detail, nil
}

// dependencies resolves the dependencies of a whole page in one query.
func (s *WorkItemService) dependencies(ctx context.Context, q store.Querier, rows []store.WorkItem) (map[int64][]string, error) {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return store.WorkDependenciesByWorkItemIDs(ctx, q, ids)
}

// viewFromRow converts the store read model into the use-case view.
func (s *WorkItemService) viewFromRow(ctx context.Context, q store.Querier, row store.WorkItem, dependsOn []string, result *domain.Material) (WorkItemView, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return WorkItemView{}, err
	}
	updateTime, err := parseTimestamp(row.UpdateTime)
	if err != nil {
		return WorkItemView{}, err
	}
	if dependsOn == nil {
		dependsOn = []string{}
	}

	var parentID *string
	if row.ParentID != nil {
		parent, err := store.GetWorkItemByID(ctx, q, *row.ParentID)
		if err != nil {
			return WorkItemView{}, err
		}
		parentID = &parent.WorkID
	}

	view := WorkItemView{
		WorkID:      row.WorkID,
		ProjectID:   row.ProjectCode,
		ParentID:    parentID,
		Kind:        row.Kind,
		SeqNo:       row.SeqNo,
		Title:       row.Title,
		BodyMD:      row.BodyMD,
		Priority:    row.Priority,
		State:       row.State,
		WorkerID:    row.WorkerID,
		Attempt:     row.Attempt,
		MaxAttempts: row.MaxAttempts,
		Reason:      stringValue(row.Reason),
		Version:     row.Version,
		DependsOn:   dependsOn,
		CreateTime:  createTime,
		UpdateTime:  updateTime,
	}
	if result != nil {
		materialID := result.MaterialID
		view.ResultMaterialID = &materialID
	}

	if row.State == store.WorkItemStateLeased && row.LeaseID != nil && row.LeaseUntil != nil {
		until, err := parseTimestamp(*row.LeaseUntil)
		if err != nil {
			return WorkItemView{}, err
		}
		if !s.now().UTC().After(until) {
			view.Lease = &WorkItemLease{LeaseID: *row.LeaseID, Until: until}
		}
	}
	return view, nil
}

// validateCreateWorkItem normalises one create request.
func validateCreateWorkItem(in CreateWorkItemInput) (CreateWorkItemInput, []domain.FieldError) {
	var fields []domain.FieldError

	in.Kind = strings.TrimSpace(in.Kind)
	switch {
	case in.Kind == "":
		fields = append(fields, domain.FieldError{Field: "kind", Reason: "is required"})
	case !store.IsValidWorkItemKind(in.Kind):
		fields = append(fields, domain.FieldError{Field: "kind",
			Reason: fmt.Sprintf("must be one of %s", strings.Join(store.WorkItemKinds, ", "))})
	}

	title, titleFields := validateWorkItemTitle("title", in.Title)
	fields = append(fields, titleFields...)
	in.Title = title

	body, bodyFields := validateWorkItemBody(in.BodyMD)
	fields = append(fields, bodyFields...)
	in.BodyMD = body

	fields = append(fields, validateWorkItemPriority(in.Priority)...)

	dependsOn := make([]string, 0, len(in.DependsOn))
	seen := make(map[string]struct{}, len(in.DependsOn))
	for _, dep := range in.DependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			fields = append(fields, domain.FieldError{Field: "depends_on", Reason: "must contain non-empty work ids"})
			continue
		}
		if _, dup := seen[dep]; dup {
			fields = append(fields, domain.FieldError{
				Field:  "depends_on",
				Reason: fmt.Sprintf("must not contain duplicate work id %q", dep),
			})
			continue
		}
		seen[dep] = struct{}{}
		dependsOn = append(dependsOn, dep)
	}
	in.DependsOn = dependsOn

	return in, fields
}

// validateUpdateWorkItem normalises a patch and requires at least one writable
// field.
func validateUpdateWorkItem(in UpdateWorkItemInput) (UpdateWorkItemInput, []domain.FieldError) {
	var fields []domain.FieldError

	if in.Title == nil && in.BodyMD == nil && in.Priority == nil {
		fields = append(fields, domain.FieldError{
			Field:  "body",
			Reason: "at least one of title, body_md, priority is required",
		})
	}

	if in.Title != nil {
		title, titleFields := validateWorkItemTitle("title", *in.Title)
		fields = append(fields, titleFields...)
		in.Title = &title
	}
	if in.BodyMD != nil {
		body, bodyFields := validateWorkItemBody(*in.BodyMD)
		fields = append(fields, bodyFields...)
		in.BodyMD = &body
	}
	if in.Priority != nil {
		fields = append(fields, validateWorkItemPriority(*in.Priority)...)
	}
	return in, fields
}

// validateWorkItemTitle trims a title and enforces the character bound.
func validateWorkItemTitle(field, title string) (string, []domain.FieldError) {
	trimmed := strings.TrimSpace(title)
	switch {
	case trimmed == "":
		return trimmed, []domain.FieldError{{Field: field, Reason: "is required"}}
	case utf8.RuneCountInString(trimmed) > MaxWorkItemTitleLen:
		return trimmed, []domain.FieldError{{
			Field:  field,
			Reason: fmt.Sprintf("must be at most %d characters", MaxWorkItemTitleLen),
		}}
	}
	return trimmed, nil
}

// validateWorkItemBody rejects an empty body and enforces the byte cap.
func validateWorkItemBody(body string) (string, []domain.FieldError) {
	switch {
	case strings.TrimSpace(body) == "":
		return body, []domain.FieldError{{Field: "body_md", Reason: "is required"}}
	case len(body) > MaxWorkItemBodyBytes:
		return body, []domain.FieldError{{
			Field:  "body_md",
			Reason: fmt.Sprintf("must be at most %d bytes", MaxWorkItemBodyBytes),
		}}
	}
	return body, nil
}

// validateWorkItemPriority enforces the optional scheduling hint range.
func validateWorkItemPriority(priority int) []domain.FieldError {
	if priority < MinWorkItemPriority || priority > MaxWorkItemPriority {
		return []domain.FieldError{{
			Field:  "priority",
			Reason: fmt.Sprintf("must be between %d and %d", MinWorkItemPriority, MaxWorkItemPriority),
		}}
	}
	return nil
}

// isValidWorkItemState reports whether state is one of the seven text states.
func isValidWorkItemState(state string) bool {
	switch state {
	case store.WorkItemStateBlocked, store.WorkItemStateQueued,
		store.WorkItemStateLeased, store.WorkItemStateSubmitted,
		store.WorkItemStateAccepted, store.WorkItemStateFailed,
		store.WorkItemStateCancelled:
		return true
	}
	return false
}

// normalizeEnums trims, validates and de-duplicates a filter set while keeping
// the request order.
func normalizeEnums(field string, values []string, valid func(string) bool) ([]string, []domain.FieldError) {
	var fields []domain.FieldError
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !valid(value) {
			fields = append(fields, domain.FieldError{Field: field, Reason: fmt.Sprintf("unsupported value %q", value)})
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, fields
}

// timestamp returns the current time truncated to milliseconds, the precision
// persisted in create_time/update_time.
func (s *WorkItemService) timestamp() time.Time {
	return timestampAt(s.now())
}

// nextTimestamp returns a timestamp strictly after the persisted update_time.
func (s *WorkItemService) nextTimestamp(previous string) time.Time {
	return nextTimestampAfter(s.now(), previous)
}

// clearTasksReviewRejection drops a recorded objection after the batch changed
// (card added, edited or removed). The store only bumps the project when an
// objection was actually recorded, so a no-op leaves the version alone.
func (s *WorkItemService) clearTasksReviewRejection(ctx context.Context, tx *sql.Tx, projectCode, actor string) error {
	if strings.TrimSpace(projectCode) == "" {
		return nil
	}
	project, err := store.GetProjectByProjectID(ctx, tx, projectCode)
	if err != nil {
		return err
	}
	// nextTimestamp keeps the project's update_time monotonic even when the
	// card write happens in the same millisecond as the rejection.
	updateTime := formatTimestamp(s.nextTimestamp(project.UpdateTime))
	_, err = store.ClearTasksReviewRejection(ctx, tx, project.ID, actor, updateTime)
	return err
}
