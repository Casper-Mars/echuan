package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Work item persistence (tech design v0.8 §3.2 / §3.5).
//
// work_items is the only dispatch entity in v2: the four kinds (requirement,
// design, breakdown, dev) share one table and one queue, and the seven-state
// text machine (blocked / queued / leased / submitted / accepted / failed /
// cancelled) replaces v1's integer task status. work_deps replaces task_deps
// and carries no timestamps of its own: the owning rows supply them.
//
// worker_id is rendered only while an item is leased, even though the column
// itself can survive a state change: the state is the single authority, so a
// stale pointer can never leak into a response.

// workItemColumns selects one work item with its project's business id.
const workItemColumns = `t.id, t.work_id, p.project_id AS project_code, t.parent_id,
	t.kind, t.seq_no, t.title, t.body_md, t.priority, t.state, t.attempt,
	t.max_attempts, t.not_before, t.lease_id, t.lease_until,
	CASE WHEN t.state = 'leased' THEN t.worker_id END AS worker_id,
	t.result_material_id, t.reason, t.version, t.create_time, t.update_time`

// WorkItem is the read model of one work_items row.
type WorkItem struct {
	ID               int64   `db:"id"`
	WorkID           string  `db:"work_id"`
	ProjectCode      string  `db:"project_code"`
	ParentID         *int64  `db:"parent_id"`
	Kind             string  `db:"kind"`
	SeqNo            int     `db:"seq_no"`
	Title            string  `db:"title"`
	BodyMD           string  `db:"body_md"`
	Priority         int     `db:"priority"`
	State            string  `db:"state"`
	WorkerID         *string `db:"worker_id"`
	Attempt          int     `db:"attempt"`
	MaxAttempts      int     `db:"max_attempts"`
	NotBefore        *string `db:"not_before"`
	LeaseID          *string `db:"lease_id"`
	LeaseUntil       *string `db:"lease_until"`
	ResultMaterialID *int64  `db:"result_material_id"`
	Reason           *string `db:"reason"`
	Version          int     `db:"version"`
	CreateTime       string  `db:"create_time"`
	UpdateTime       string  `db:"update_time"`
}

// WorkItemStateQueued is the claimable state. The other six states are spelled
// out inline where they are needed; the dependency runs domain ← store, so the
// store cannot import the domain constants.
const (
	WorkItemStateBlocked   = "blocked"
	WorkItemStateQueued    = "queued"
	WorkItemStateLeased    = "leased"
	WorkItemStateSubmitted = "submitted"
	WorkItemStateAccepted  = "accepted"
	WorkItemStateFailed    = "failed"
	WorkItemStateCancelled = "cancelled"
)

// WorkItemKinds are the four kinds accepted by the schema CHECK constraint and
// by worker_kinds.
var WorkItemKinds = []string{"requirement", "design", "breakdown", "dev"}

// defaultWorkItemMaxAttempts mirrors the work_items.max_attempts column
// default. It is only used when the caller leaves MaxAttempts unset, so a
// plain CreateWorkItem keeps the schema default instead of writing 0.
const defaultWorkItemMaxAttempts = 3

// IsValidWorkItemKind reports whether kind is one of the four schema kinds.
func IsValidWorkItemKind(kind string) bool {
	for _, known := range WorkItemKinds {
		if kind == known {
			return true
		}
	}
	return false
}

// NewWorkItem is the write model for INSERT.
//
// State is written as given; an empty State falls back to the column default
// 'queued'. MaxAttempts is the per-item attempt budget; values <= 0 fall back
// to the column default. ParentID is nil for a top-level item.
type NewWorkItem struct {
	WorkID      string
	ProjectID   int64
	ParentID    *int64
	Kind        string
	SeqNo       int
	Title       string
	BodyMD      string
	Priority    int
	State       string
	MaxAttempts int
	NotBefore   *string
	CreateTime  string
	UpdateTime  string
}

// GetWorkItemByWorkID returns the item with the given business id.
func GetWorkItemByWorkID(ctx context.Context, q Querier, workID string) (WorkItem, error) {
	return One[WorkItem](ctx, q,
		`SELECT `+workItemColumns+`
		   FROM work_items t JOIN projects p ON p.id = t.project_id
		  WHERE t.work_id = ?`, workID)
}

// GetWorkItemByID returns the item with the given physical id.
func GetWorkItemByID(ctx context.Context, q Querier, id int64) (WorkItem, error) {
	return One[WorkItem](ctx, q,
		`SELECT `+workItemColumns+`
		   FROM work_items t JOIN projects p ON p.id = t.project_id
		  WHERE t.id = ?`, id)
}

// GetActivePhaseWorkItem returns the one active item of a phase kind
// (requirement/design/breakdown) owned by a project.
//
// "Active" repeats the predicate of ux_work_active_phase: blocked, queued,
// leased or submitted. A project may own several finished (accepted/cancelled)
// items of the same kind, so this query returns at most one row; the caller
// reads "not found" as "no active card, safe to create".
func GetActivePhaseWorkItem(ctx context.Context, q Querier, projectID int64, kind string) (WorkItem, error) {
	return One[WorkItem](ctx, q,
		`SELECT `+workItemColumns+`
		   FROM work_items t JOIN projects p ON p.id = t.project_id
		  WHERE t.project_id = ? AND t.kind = ?
		    AND t.state IN (?, ?, ?, ?)`,
		projectID, kind,
		WorkItemStateBlocked, WorkItemStateQueued,
		WorkItemStateLeased, WorkItemStateSubmitted)
}

// ListWorkItemsByProject returns the items of one project ordered by seq_no.
func ListWorkItemsByProject(ctx context.Context, q Querier, projectID int64) ([]WorkItem, error) {
	return All[WorkItem](ctx, q,
		`SELECT `+workItemColumns+`
		   FROM work_items t JOIN projects p ON p.id = t.project_id
		  WHERE t.project_id = ?
		  ORDER BY t.seq_no ASC`, projectID)
}

// CreateWorkItem inserts one item and returns its physical id.
//
// The state is stored as given: callers that need an edge (blocked → queued)
// apply it through TransitionWorkItem so the state machine stays the single
// authority. An unknown kind is rejected by the schema CHECK constraint.
func CreateWorkItem(ctx context.Context, tx *sql.Tx, w NewWorkItem) (int64, error) {
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultWorkItemMaxAttempts
	}
	state := w.State
	if state == "" {
		state = WorkItemStateQueued
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO work_items (work_id, project_id, parent_id, kind, seq_no,
		                        title, body_md, priority, state, attempt,
		                        max_attempts, not_before, version,
		                        create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 1, ?, ?)`,
		w.WorkID, w.ProjectID, w.ParentID, w.Kind, w.SeqNo,
		w.Title, w.BodyMD, w.Priority, state,
		maxAttempts, w.NotBefore, w.CreateTime, w.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return 0, fmt.Errorf("store: insert work item %s: %w", w.WorkID, err)
	}
	return res.LastInsertId()
}

// WorkDependency is one persisted work_deps row. Both columns are physical
// work_items.id values; the API layer maps them back to business ids.
type WorkDependency struct {
	WorkItemID int64 `db:"work_item_id"`
	DependsOn  int64 `db:"depends_on_id"`
}

// InsertWorkDependency records that workItemID depends on dependsOn.
//
// The caller must resolve batch sequence numbers to physical ids first; the
// UNIQUE(work_item_id, depends_on_id) primary key keeps a duplicate edge out
// even if a caller forgets to de-duplicate.
func InsertWorkDependency(ctx context.Context, tx *sql.Tx, workItemID, dependsOn int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO work_deps (work_item_id, depends_on_id)
		VALUES (?, ?)`, workItemID, dependsOn)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return fmt.Errorf("store: insert work dependency %d -> %d: %w", workItemID, dependsOn, err)
	}
	return nil
}

// ListWorkDependencies returns every dependency edge whose owning item belongs
// to projectID, ordered by owning item and dependency.
func ListWorkDependencies(ctx context.Context, q Querier, projectID int64) ([]WorkDependency, error) {
	return All[WorkDependency](ctx, q, `
		SELECT d.work_item_id, d.depends_on_id
		  FROM work_deps d
		  JOIN work_items w ON w.id = d.work_item_id
		 WHERE w.project_id = ?
		 ORDER BY d.work_item_id ASC, d.depends_on_id ASC`, projectID)
}

// CountUnfinishedDependencies returns how many dependencies of workItemID are
// not accepted. "0 unfinished" is what makes an item claimable; it is kept
// here so the readiness rule has one SQL home.
func CountUnfinishedDependencies(ctx context.Context, q Querier, workItemID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM work_deps d
		  JOIN work_items dep ON dep.id = d.depends_on_id
		 WHERE d.work_item_id = ? AND dep.state <> ?`,
		workItemID, WorkItemStateAccepted)
	if err != nil {
		return 0, fmt.Errorf("store: count unfinished dependencies of work item %d: %w", workItemID, err)
	}
	return row.Count, nil
}

// TransitionWorkItem applies one state-machine edge with a conditional UPDATE
// on (state, version). It returns 0 when the row no longer matches.
func TransitionWorkItem(ctx context.Context, tx *sql.Tx, id int64, expectState string, expectVersion int, nextState string, reason *string, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET state = ?, reason = ?, version = version + 1, update_time = ?
		 WHERE id = ? AND state = ? AND version = ?`,
		nextState, reason, updateTime, id, expectState, expectVersion)
	if err != nil {
		return 0, fmt.Errorf("store: transition work item %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: transition work item %d: rows affected: %w", id, err)
	}
	return n, nil
}

// WorkItemDecisionWrite is one verdict applied to one row.
//
// ExpectState/ExpectVersion repeat the values read before the write, so the
// UPDATE cannot overwrite a row that moved in between. ResetAttempts clears
// attempt (retry); the other verdicts leave it alone.
type WorkItemDecisionWrite struct {
	ID            int64
	ExpectState   string
	ExpectVersion int
	NextState     string
	Reason        *string
	ResetAttempts bool
	UpdateTime    string
}

// TransitionWorkItemDecision applies one verdict with a conditional UPDATE on
// (state, version) and returns how many rows matched.
//
// It is TransitionWorkItem plus the optional attempt reset, because retry is
// the only edge that touches attempt. state and reason remain owned by the
// state machine: callers pass the target state and the reason column value
// they already validated.
func TransitionWorkItemDecision(ctx context.Context, tx *sql.Tx, w WorkItemDecisionWrite) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET state = ?,
		       reason = ?,
		       attempt = CASE WHEN ? THEN 0 ELSE attempt END,
		       version = version + 1,
		       update_time = ?
		 WHERE id = ? AND state = ? AND version = ?`,
		w.NextState, w.Reason, boolInt(w.ResetAttempts),
		w.UpdateTime, w.ID, w.ExpectState, w.ExpectVersion)
	if err != nil {
		return 0, fmt.Errorf("store: decide work item %d: %w", w.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: decide work item %d: rows affected: %w", w.ID, err)
	}
	return n, nil
}

// CompleteWorkItemReport moves a leased item to submitted and points it at the
// freshly written result version.
//
// The WHERE clause repeats the lease predicate (state, lease id, unexpired
// deadline) so the report cannot be attached to a lease that was reclaimed
// between the read and the write. A zero row count means the lease is gone; the
// caller turns that into 409 lease_lost and rolls the transaction back.
// now uses the fixed-width lease layout (see work_item_claim.go).
func CompleteWorkItemReport(ctx context.Context, tx *sql.Tx, id, materialID int64, leaseID, now, updateTime string) (int64, error) {
	return CompleteWorkItemReportOptionalMaterial(ctx, tx, id, &materialID, leaseID, now, updateTime)
}

// CompleteWorkItemReportOptionalMaterial is CompleteWorkItemReport with a
// nullable result material.
//
// A breakdown card is the one report that may be answered with a plan instead
// of an artifact: its result_material_id stays NULL while the generated child
// cards carry the actual work. Every other kind still passes a material, so
// this variant is additive and the material-id signature above is preserved.
// CompleteWorkItemReportOptionalMaterial completes a report.
//
// reason is cleared in the same write: a reason on a card is the unanswered
// objection that sent it back, and the report answers it. Leaving it behind
// would make the next dispatch of this card look like a rework it is not, and
// would show a stale objection next to a freshly submitted result.
func CompleteWorkItemReportOptionalMaterial(ctx context.Context, tx *sql.Tx, id int64, materialID *int64, leaseID, now, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET result_material_id = ?,
		       state = ?,
		       lease_id = NULL,
		       lease_until = NULL,
		       reason = NULL,
		       version = version + 1,
		       update_time = ?
		 WHERE id = ? AND state = ? AND lease_id = ?
		   AND lease_until IS NOT NULL AND lease_until > ?`,
		materialID, WorkItemStateSubmitted, updateTime,
		id, WorkItemStateLeased, leaseID, now)
	if err != nil {
		return 0, fmt.Errorf("store: complete report of work item %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: complete report of work item %d: rows affected: %w", id, err)
	}
	return n, nil
}

// CountWorkItemsByParent returns how many items point their parent_id at
// parentID. It is the duplicate-submission guard of a breakdown plan: the
// second report of the same card must not create a second batch of children.
func CountWorkItemsByParent(ctx context.Context, q Querier, parentID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM work_items
		 WHERE parent_id = ?`, parentID)
	if err != nil {
		return 0, fmt.Errorf("store: count work items of parent %d: %w", parentID, err)
	}
	return row.Count, nil
}

// CountUnsettledWorkItemsByProject counts the items of one project that are
// neither accepted nor cancelled — the terminal states of the work-item
// machine. Zero is the trigger for pending_done → completed.
func CountUnsettledWorkItemsByProject(ctx context.Context, q Querier, projectID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM work_items
		 WHERE project_id = ? AND state NOT IN (?, ?)`,
		projectID, WorkItemStateAccepted, WorkItemStateCancelled)
	if err != nil {
		return 0, fmt.Errorf("store: count unsettled work items of project %d: %w", projectID, err)
	}
	return row.Count, nil
}

// boolInt renders a bool as the 0/1 SQLite uses in CASE expressions.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// SetWorkItemsQueuedByProject moves every blocked item of one project to
// queued. It is the side effect of an approved plan review; already-queued
// items are left untouched.
func SetWorkItemsQueuedByProject(ctx context.Context, tx *sql.Tx, projectID int64, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET state = ?, version = version + 1, update_time = ?
		 WHERE project_id = ? AND state = ?`,
		WorkItemStateQueued, updateTime, projectID, WorkItemStateBlocked)
	if err != nil {
		return 0, fmt.Errorf("store: queue work items of project %d: %w", projectID, err)
	}
	return res.RowsAffected()
}

// CancelUnfinishedWorkItems moves every item of one project that is neither
// accepted nor cancelled to cancelled. It is the side effect of cancelling the
// project.
func CancelUnfinishedWorkItems(ctx context.Context, tx *sql.Tx, projectID int64, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET state = ?, version = version + 1, update_time = ?
		 WHERE project_id = ? AND state NOT IN (?, ?)`,
		WorkItemStateCancelled, updateTime,
		projectID, WorkItemStateAccepted, WorkItemStateCancelled)
	if err != nil {
		return 0, fmt.Errorf("store: cancel work items of project %d: %w", projectID, err)
	}
	return res.RowsAffected()
}

// CancelWorkItem cancels one unfinished item. It is the single-card form of
// CancelUnfinishedWorkItems, used when a gate is satisfied without the stage
// card having been reported (a manual submit/approve path).
func CancelWorkItem(ctx context.Context, tx *sql.Tx, id int64, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET state = ?, version = version + 1, update_time = ?
		 WHERE id = ? AND state NOT IN (?, ?)`,
		WorkItemStateCancelled, updateTime, id,
		WorkItemStateAccepted, WorkItemStateCancelled)
	if err != nil {
		return 0, fmt.Errorf("store: cancel work item %d: %w", id, err)
	}
	return res.RowsAffected()
}

// DeleteWorkItemsByProject removes every item of one project plus every row
// that references them (dependencies, inputs, events and worker pointers). It
// is the side effect of a rejected plan review: the plan is rebuilt from
// scratch, so the previous rows must not linger and block UNIQUE(project_id,
// seq_no) or a foreign key.
func DeleteWorkItemsByProject(ctx context.Context, tx *sql.Tx, projectID int64) (int64, error) {
	const items = `SELECT id FROM work_items WHERE project_id = ?`

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM work_deps WHERE work_item_id IN (`+items+`) OR depends_on_id IN (`+items+`)`,
		projectID, projectID); err != nil {
		return 0, fmt.Errorf("store: delete work deps of project %d: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM work_inputs WHERE work_item_id IN (`+items+`)`, projectID); err != nil {
		return 0, fmt.Errorf("store: delete work inputs of project %d: %w", projectID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM work_item_events WHERE work_item_id IN (`+items+`)`, projectID); err != nil {
		return 0, fmt.Errorf("store: delete work events of project %d: %w", projectID, err)
	}
	// workers.current_work_item_id references work_items(id); a pointer left
	// behind would make the DELETE fail with a foreign key violation.
	if _, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_work_item_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle'
		 WHERE current_work_item_id IN (`+items+`)`, projectID); err != nil {
		return 0, fmt.Errorf("store: clear workers of project %d: %w", projectID, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM work_items WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: delete work items of project %d: %w", projectID, err)
	}
	return res.RowsAffected()
}

// CountUserWorkItemsByProject counts the items of one project that were not
// generated by the platform's phase pipeline.
//
// The phase cards (requirement/design/breakdown) are scaffolding created by the
// project state machine itself, so the delete guard must not treat them as
// user-owned children; they are removed together with the project. Dev cards
// and any hand-created card still block deletion.
func CountUserWorkItemsByProject(ctx context.Context, q Querier, projectID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM work_items
		 WHERE project_id = ? AND kind NOT IN (?, ?, ?)`,
		projectID, "requirement", "design", "breakdown")
	if err != nil {
		return 0, fmt.Errorf("store: count user work items of project %d: %w", projectID, err)
	}
	return row.Count, nil
}

// WorkItemCursor is the keyset position of one row in the
// (priority DESC, seq_no ASC, id ASC) ordering used by the board.
type WorkItemCursor struct {
	Priority int
	SeqNo    int
	ID       int64
}

// WorkItemFilter narrows a work-item list (tech design §4.2 / §5.4).
//
// ProjectID is the physical projects.id; 0 means "every project". Kinds and
// States are optional equality sets; After is the keyset position of the last
// row of the previous page (nil for the first page), and Limit is the page size
// plus one when the caller wants to know whether a next page exists.
type WorkItemFilter struct {
	ProjectID int64
	Kinds     []string
	States    []string
	After     *WorkItemCursor
	Limit     int
}

// ListWorkItems returns one page of items ordered by priority DESC, seq_no ASC,
// id ASC — the same order as the claim queue, so the board can reuse it.
//
// The cursor is a keyset position rather than an offset: an item inserted or
// re-prioritised between two page reads can never shift another row across a
// page boundary and be skipped or repeated.
func ListWorkItems(ctx context.Context, q Querier, filter WorkItemFilter) ([]WorkItem, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 12)

	if filter.ProjectID != 0 {
		where = append(where, "t.project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if len(filter.Kinds) > 0 {
		where = append(where, "t.kind IN ("+placeholders(len(filter.Kinds))+")")
		for _, kind := range filter.Kinds {
			args = append(args, kind)
		}
	}
	if len(filter.States) > 0 {
		where = append(where, "t.state IN ("+placeholders(len(filter.States))+")")
		for _, state := range filter.States {
			args = append(args, state)
		}
	}
	if filter.After != nil {
		after := filter.After
		where = append(where, `(t.priority < ? OR (t.priority = ? AND (t.seq_no > ? OR (t.seq_no = ? AND t.id > ?))))`)
		args = append(args, after.Priority, after.Priority, after.SeqNo, after.SeqNo, after.ID)
	}
	if len(where) == 0 {
		where = append(where, "1 = 1")
	}
	args = append(args, filter.Limit)

	query := `SELECT ` + workItemColumns + `
	            FROM work_items t JOIN projects p ON p.id = t.project_id
	           WHERE ` + strings.Join(where, " AND ") + `
	           ORDER BY t.priority DESC, t.seq_no ASC, t.id ASC
	           LIMIT ?`
	return All[WorkItem](ctx, q, query, args...)
}

// WorkItemFields is the business-writable subset of one work_items row.
//
// state, attempt, lease_*, worker_id, result_material_id and reason are absent
// on purpose: they are maintained by the platform (tech design §4.3).
type WorkItemFields struct {
	Title      string
	BodyMD     string
	Priority   int
	UpdateTime string
}

// UpdateWorkItemFields writes the three business-writable columns and bumps
// version/update_time. state and reason are deliberately left untouched:
// editing a card must not move it through the state machine.
func UpdateWorkItemFields(ctx context.Context, tx *sql.Tx, id int64, fields WorkItemFields) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE work_items
		   SET title = ?, body_md = ?, priority = ?,
		       version = version + 1, update_time = ?
		 WHERE id = ?`,
		fields.Title, fields.BodyMD, fields.Priority, fields.UpdateTime, id)
	if err != nil {
		return fmt.Errorf("store: update work item %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update work item %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update work item %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// WorkItemRef is the minimal identity of one work_items row, used to resolve
// business ids to physical ids (dependency validation, cycle detection).
type WorkItemRef struct {
	ID     int64  `db:"id"`
	WorkID string `db:"work_id"`
	SeqNo  int    `db:"seq_no"`
}

// WorkItemRefsByProject returns every item of one project ordered by seq_no.
// The caller builds a business-id → physical-id map from the result.
func WorkItemRefsByProject(ctx context.Context, q Querier, projectID int64) ([]WorkItemRef, error) {
	return All[WorkItemRef](ctx, q, `
		SELECT id, work_id, seq_no
		  FROM work_items
		 WHERE project_id = ?
		 ORDER BY seq_no ASC, id ASC`, projectID)
}

// CreateWorkDependencyByWorkIDs records that workID depends on dependsOn, both
// business ids. It resolves the two physical ids in SQL so callers that only
// hold business ids need no extra round trip; a missing id makes the statement
// insert nothing, which is reported as sql.ErrNoRows.
func CreateWorkDependencyByWorkIDs(ctx context.Context, tx *sql.Tx, workID, dependsOn string) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO work_deps (work_item_id, depends_on_id)
		SELECT owner.id, dep.id
		  FROM work_items owner, work_items dep
		 WHERE owner.work_id = ? AND dep.work_id = ?`,
		workID, dependsOn)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return fmt.Errorf("store: insert work dependency %s -> %s: %w", workID, dependsOn, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: insert work dependency %s -> %s: rows affected: %w", workID, dependsOn, err)
	}
	if n == 0 {
		return fmt.Errorf("store: insert work dependency %s -> %s: %w", workID, dependsOn, sql.ErrNoRows)
	}
	return nil
}

// DeleteDependenciesTouching removes every work_deps row that points at
// workItemID or is owned by it. It runs before DeleteWorkItem in the same
// transaction: the foreign keys would otherwise block the removal.
func DeleteDependenciesTouching(ctx context.Context, tx *sql.Tx, workItemID int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		DELETE FROM work_deps WHERE work_item_id = ? OR depends_on_id = ?`, workItemID, workItemID)
	if err != nil {
		return 0, fmt.Errorf("store: delete work deps touching %d: %w", workItemID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete work deps touching %d: rows affected: %w", workItemID, err)
	}
	return n, nil
}

// DeleteWorkItem removes one work_items row by physical id. Dependency, input
// and event rows must already be gone (DeleteDependenciesTouching in the same
// transaction).
func DeleteWorkItem(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM work_items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete work item %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete work item %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete work item %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// ListDependenciesByWorkItem returns the business ids workItemID depends on,
// ordered by the dependency's seq_no so the response is stable (tech design
// §4.2).
func ListDependenciesByWorkItem(ctx context.Context, q Querier, workItemID int64) ([]string, error) {
	rows, err := All[struct {
		WorkID string `db:"work_id"`
	}](ctx, q, `
		SELECT dep.work_id
		  FROM work_deps d
		  JOIN work_items dep ON dep.id = d.depends_on_id
		 WHERE d.work_item_id = ?
		 ORDER BY dep.seq_no ASC, dep.id ASC`, workItemID)
	if err != nil {
		return nil, fmt.Errorf("store: list dependencies of work item %d: %w", workItemID, err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.WorkID)
	}
	return out, nil
}

// WorkDependenciesByWorkItemIDs returns the dependency business ids of every
// given physical id. Items without dependencies are absent from the map, so a
// missing key and an empty slice mean the same thing to the caller.
//
// One statement covers a whole page, which keeps the list endpoint free of an
// N+1 query regardless of page size.
func WorkDependenciesByWorkItemIDs(ctx context.Context, q Querier, ids []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := All[struct {
		Owner  int64  `db:"owner_id"`
		WorkID string `db:"work_id"`
	}](ctx, q, `
		SELECT d.work_item_id AS owner_id, dep.work_id
		  FROM work_deps d
		  JOIN work_items dep ON dep.id = d.depends_on_id
		 WHERE d.work_item_id IN (`+placeholders(len(ids))+`)
		 ORDER BY d.work_item_id ASC, dep.seq_no ASC, dep.id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list dependencies of %d work items: %w", len(ids), err)
	}
	for _, row := range rows {
		out[row.Owner] = append(out[row.Owner], row.WorkID)
	}
	return out, nil
}

// placeholders renders "?, ?, ?" for an IN clause of n values.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
