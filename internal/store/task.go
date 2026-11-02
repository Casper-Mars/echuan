package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// taskColumns selects one task with its project's business id.
//
// tasks.status values are domain.Task* integers; the store spells the numbers
// out as literals (2 = in_progress, 4 = done, 7 = cancelled) because the
// dependency runs domain ← store.
//
// worker_id is derived from the task status rather than read straight off the
// join: workers.current_task_id is a denormalized pointer that can dangle (a
// reclaimed card, a hand-edited row), and only an in-progress task has an
// executor to show. The CASE makes the status the single authority, so a stale
// pointer can never leak into a response.
//
// Every statement using this list must therefore join the workers table as
//
//	LEFT JOIN workers w ON w.current_task_id = t.id
//
// ux_workers_current_task (partial UNIQUE) keeps the join at one row per task,
// so the extra column adds no duplicate rows to a list or detail read.
const taskColumns = `t.id, t.task_id, p.project_id AS project_code, t.seq_no,
	t.title, t.body_md, t.priority, t.status, t.attempt_count, t.max_attempts,
	t.lease_id, t.lease_until, t.last_actor, t.report_material_id, t.reason, t.version,
	CASE WHEN t.status = 2 THEN w.worker_id END AS worker_id,
	t.create_time, t.update_time`

// Task is the read model of one tasks row.
type Task struct {
	ID               int64   `db:"id"`
	TaskID           string  `db:"task_id"`
	ProjectCode      string  `db:"project_code"`
	SeqNo            int     `db:"seq_no"`
	Title            string  `db:"title"`
	BodyMD           string  `db:"body_md"`
	Priority         int     `db:"priority"`
	Status           int     `db:"status"`
	WorkerID         *string `db:"worker_id"`
	AttemptCount     int     `db:"attempt_count"`
	MaxAttempts      int     `db:"max_attempts"`
	LeaseID          *string `db:"lease_id"`
	LeaseUntil       *string `db:"lease_until"`
	LastActor        *string `db:"last_actor"`
	ReportMaterialID *int64  `db:"report_material_id"`
	Reason           *string `db:"reason"`
	Version          int     `db:"version"`
	CreateTime       string  `db:"create_time"`
	UpdateTime       string  `db:"update_time"`
}

// defaultTaskMaxAttempts mirrors the tasks.max_attempts column default. It is
// only used when the caller leaves MaxAttempts unset, so a plain CreateTask
// keeps the schema default instead of writing 0.
const defaultTaskMaxAttempts = 3

// NewTask is the write model for INSERT.
//
// MaxAttempts is the per-task attempt budget (configurable via max_attempts);
// values <= 0 fall back to the column default. LastActor records who created
// the row and is stored as NULL when empty.
type NewTask struct {
	TaskID      string
	ProjectID   int64
	SeqNo       int
	Title       string
	BodyMD      string
	Priority    int
	Status      int
	MaxAttempts int
	LastActor   string
	CreateTime  string
	UpdateTime  string
}

// GetTaskByTaskID returns the task with the given business id.
func GetTaskByTaskID(ctx context.Context, q Querier, taskID string) (Task, error) {
	return One[Task](ctx, q,
		`SELECT `+taskColumns+`
		   FROM tasks t JOIN projects p ON p.id = t.project_id
		   LEFT JOIN workers w ON w.current_task_id = t.id
		  WHERE t.task_id = ?`, taskID)
}

// ListTasksByProject returns the tasks of one project ordered by seq_no.
func ListTasksByProject(ctx context.Context, q Querier, projectID int64) ([]Task, error) {
	return All[Task](ctx, q,
		`SELECT `+taskColumns+`
		   FROM tasks t JOIN projects p ON p.id = t.project_id
		   LEFT JOIN workers w ON w.current_task_id = t.id
		  WHERE t.project_id = ?
		  ORDER BY t.seq_no ASC`, projectID)
}

// CreateTask inserts one task and returns its physical id. The status is
// stored as given: callers that need an edge (backlog → ready) apply it
// through TransitionTask so the state machine stays the single authority.
func CreateTask(ctx context.Context, tx *sql.Tx, t NewTask) (int64, error) {
	maxAttempts := t.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultTaskMaxAttempts
	}
	var lastActor *string
	if t.LastActor != "" {
		lastActor = &t.LastActor
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, priority,
		                   status, attempt_count, max_attempts, last_actor, version,
		                   create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 1, ?, ?)`,
		t.TaskID, t.ProjectID, t.SeqNo, t.Title, t.BodyMD, t.Priority,
		t.Status, maxAttempts, lastActor, t.CreateTime, t.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return 0, fmt.Errorf("store: insert task %s: %w", t.TaskID, err)
	}
	return res.LastInsertId()
}

// TaskDependency is one persisted task_deps row. Both columns are physical
// tasks.id values; the API layer maps them back to business task ids.
type TaskDependency struct {
	TaskID    int64 `db:"task_id"`
	DependsOn int64 `db:"depends_on"`
}

// InsertTaskDependency records that taskID depends on dependsOn.
//
// The caller must resolve batch sequence numbers to physical ids first; the
// UNIQUE(task_id, depends_on) constraint is what keeps a duplicate edge out
// even if a caller forgets to de-duplicate. createTime/updateTime are the same
// instant as the task rows of the batch.
func InsertTaskDependency(ctx context.Context, tx *sql.Tx, taskID, dependsOn int64, createTime string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO task_deps (task_id, depends_on, create_time, update_time)
		VALUES (?, ?, ?, ?)`, taskID, dependsOn, createTime, createTime)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return fmt.Errorf("store: insert task dependency %d -> %d: %w", taskID, dependsOn, err)
	}
	return nil
}

// ListTaskDependencies returns every dependency edge whose owning task belongs
// to projectID, ordered by owning task and dependency.
//
// Task-08 uses it to rebuild the submit response from what was actually
// persisted; Task-09 (claim queue) can use the same rows to compute readiness.
func ListTaskDependencies(ctx context.Context, q Querier, projectID int64) ([]TaskDependency, error) {
	return All[TaskDependency](ctx, q, `
		SELECT td.task_id, td.depends_on
		  FROM task_deps td
		  JOIN tasks t ON t.id = td.task_id
		 WHERE t.project_id = ?
		 ORDER BY td.task_id ASC, td.depends_on ASC`, projectID)
}

// CountUnfinishedDependencies returns how many dependencies of taskID are not
// done (4) or cancelled (7). Task-09 turns "0 unfinished" into "ready"; it is
// kept here so the readiness rule has one SQL home.
func CountUnfinishedDependencies(ctx context.Context, q Querier, taskID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM task_deps td
		  JOIN tasks t ON t.id = td.depends_on
		 WHERE td.task_id = ? AND t.status NOT IN (4, 7)`, taskID)
	if err != nil {
		return 0, fmt.Errorf("store: count unfinished dependencies of task %d: %w", taskID, err)
	}
	return row.Count, nil
}

// TransitionTask applies one task state-machine edge with a conditional UPDATE
// on (status, version). It returns 0 when the row no longer matches.
func TransitionTask(ctx context.Context, tx *sql.Tx, id int64, expectStatus, expectVersion, nextStatus int, reason *string, lastActor, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET status = ?, reason = ?, last_actor = ?, version = version + 1, update_time = ?
		 WHERE id = ? AND status = ? AND version = ?`,
		nextStatus, reason, lastActor, updateTime, id, expectStatus, expectVersion)
	if err != nil {
		return 0, fmt.Errorf("store: transition task %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: transition task %d: rows affected: %w", id, err)
	}
	return n, nil
}

// TaskDecisionWrite is one task verdict applied to one row.
//
// ExpectStatus/ExpectVersion repeat the values read before the write, so the
// UPDATE cannot overwrite a row that moved in between. ResetAttempts clears
// attempt_count (retry); the other verdicts leave it alone.
type TaskDecisionWrite struct {
	ID            int64
	ExpectStatus  int
	ExpectVersion int
	NextStatus    int
	Reason        *string
	ResetAttempts bool
	LastActor     string
	UpdateTime    string
}

// TransitionTaskDecision applies one task verdict with a conditional UPDATE on
// (status, version) and returns how many rows matched.
//
// It is TransitionTask plus the optional attempt reset, because retry is the
// only edge that touches attempt_count. status and reason remain owned by the
// state machine: callers pass the target status and the reason column value
// they already validated.
func TransitionTaskDecision(ctx context.Context, tx *sql.Tx, w TaskDecisionWrite) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET status = ?,
		       reason = ?,
		       attempt_count = CASE WHEN ? THEN 0 ELSE attempt_count END,
		       last_actor = ?,
		       version = version + 1,
		       update_time = ?
		 WHERE id = ? AND status = ? AND version = ?`,
		w.NextStatus, w.Reason, boolInt(w.ResetAttempts), w.LastActor,
		w.UpdateTime, w.ID, w.ExpectStatus, w.ExpectVersion)
	if err != nil {
		return 0, fmt.Errorf("store: decide task %d: %w", w.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: decide task %d: rows affected: %w", w.ID, err)
	}
	return n, nil
}

// CompleteTaskReport moves an in-progress task to pending_acceptance and points
// it at the freshly written report version.
//
// The WHERE clause repeats the lease predicate (status, lease_id, unexpired
// deadline) so the report cannot be attached to a lease that was reclaimed
// between the read and the write. A zero row count means the lease is gone;
// the caller turns that into 409 lease_lost and rolls the transaction back.
// now uses the fixed-width lease layout (see task_claim.go).
func CompleteTaskReport(ctx context.Context, tx *sql.Tx, id, materialID int64, leaseID, now, lastActor, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET report_material_id = ?,
		       status = 3,
		       lease_id = NULL,
		       lease_until = NULL,
		       last_actor = ?,
		       version = version + 1,
		       update_time = ?
		 WHERE id = ? AND status = 2 AND lease_id = ?
		   AND lease_until IS NOT NULL AND lease_until > ?`,
		materialID, lastActor, updateTime, id, leaseID, now)
	if err != nil {
		return 0, fmt.Errorf("store: complete report of task %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: complete report of task %d: rows affected: %w", id, err)
	}
	return n, nil
}

// CountUnsettledTasksByProject counts the tasks of one project that are
// neither done (4) nor cancelled (7) — the terminal statuses of the task
// machine. Zero is the trigger for pending_done → completed.
func CountUnsettledTasksByProject(ctx context.Context, q Querier, projectID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM tasks
		 WHERE project_id = ? AND status NOT IN (4, 7)`, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: count unsettled tasks of project %d: %w", projectID, err)
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

// SetTasksReadyByProject moves every backlog task of one project to ready. It
// is the side effect of an approved plan review; already-ready tasks are left
// untouched.
func SetTasksReadyByProject(ctx context.Context, tx *sql.Tx, projectID int64, lastActor, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET status = 1, last_actor = ?, version = version + 1, update_time = ?
		 WHERE project_id = ? AND status = 0`, lastActor, updateTime, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: ready tasks of project %d: %w", projectID, err)
	}
	return res.RowsAffected()
}

// CancelUnfinishedTasks moves every task of one project that is neither done
// (4) nor cancelled (7) to cancelled. It is the side effect of cancelling the
// project.
func CancelUnfinishedTasks(ctx context.Context, tx *sql.Tx, projectID int64, lastActor, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET status = 7, last_actor = ?, version = version + 1, update_time = ?
		 WHERE project_id = ? AND status NOT IN (4, 7)`, lastActor, updateTime, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: cancel tasks of project %d: %w", projectID, err)
	}
	return res.RowsAffected()
}

// DeleteTasksByProject removes every task of one project plus every dependency
// edge that touches them. It is the side effect of a rejected plan review:
// the plan is rebuilt from scratch, so the previous rows must not linger and
// block UNIQUE(project_id, seq_no).
func DeleteTasksByProject(ctx context.Context, tx *sql.Tx, projectID int64) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM task_deps
		 WHERE task_id IN (SELECT id FROM tasks WHERE project_id = ?)
		    OR depends_on IN (SELECT id FROM tasks WHERE project_id = ?)`,
		projectID, projectID); err != nil {
		return 0, fmt.Errorf("store: delete task deps of project %d: %w", projectID, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: delete tasks of project %d: %w", projectID, err)
	}
	return res.RowsAffected()
}

// GetTaskByID returns the task with the given physical id.
func GetTaskByID(ctx context.Context, q Querier, id int64) (Task, error) {
	return One[Task](ctx, q,
		`SELECT `+taskColumns+`
		   FROM tasks t JOIN projects p ON p.id = t.project_id
		   LEFT JOIN workers w ON w.current_task_id = t.id
		  WHERE t.id = ?`, id)
}

// TaskCursor is the keyset position of one row in the
// (priority DESC, seq_no ASC, id ASC) ordering used by the board.
type TaskCursor struct {
	Priority int
	SeqNo    int
	ID       int64
}

// TaskFilter narrows a task list (tech design §4.2 / §5.4).
//
// ProjectID is the physical projects.id; 0 means "every project". After is the
// position of the last row of the previous page (nil for the first page), and
// Limit is the page size plus one when the caller wants to know whether a next
// page exists.
type TaskFilter struct {
	ProjectID int64
	Statuses  []int
	After     *TaskCursor
	Limit     int
}

// ListTasks returns one page of tasks ordered by priority DESC, seq_no ASC,
// id ASC — the same order as the claim queue, so the board can reuse it.
//
// The cursor is a keyset position rather than an offset: a task inserted or
// re-prioritised between two page reads can never shift another row across a
// page boundary and be skipped or repeated.
func ListTasks(ctx context.Context, q Querier, filter TaskFilter) ([]Task, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)

	if filter.ProjectID != 0 {
		where = append(where, "t.project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if len(filter.Statuses) > 0 {
		where = append(where, "t.status IN ("+placeholders(len(filter.Statuses))+")")
		for _, status := range filter.Statuses {
			args = append(args, status)
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

	query := `SELECT ` + taskColumns + `
	            FROM tasks t JOIN projects p ON p.id = t.project_id
	            LEFT JOIN workers w ON w.current_task_id = t.id
	           WHERE ` + strings.Join(where, " AND ") + `
	           ORDER BY t.priority DESC, t.seq_no ASC, t.id ASC
	           LIMIT ?`
	return All[Task](ctx, q, query, args...)
}

// TaskFields is the business-writable subset of one tasks row.
//
// status, attempt_count, lease_*, report_material_id and reason are absent on
// purpose: they are maintained by the platform (tech design §4.3).
type TaskFields struct {
	Title      string
	BodyMD     string
	Priority   int
	LastActor  string
	UpdateTime string
}

// UpdateTaskFields writes the three business-writable columns and bumps
// version/update_time/last_actor. status and reason are deliberately left
// untouched: editing a card must not move it through the state machine.
func UpdateTaskFields(ctx context.Context, tx *sql.Tx, id int64, fields TaskFields) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE tasks
		   SET title = ?, body_md = ?, priority = ?, last_actor = ?,
		       version = version + 1, update_time = ?
		 WHERE id = ?`,
		fields.Title, fields.BodyMD, fields.Priority, fields.LastActor,
		fields.UpdateTime, id)
	if err != nil {
		return fmt.Errorf("store: update task %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update task %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update task %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// TaskRef is the minimal identity of one task row, used to resolve business
// task ids to physical ids (dependency validation, cycle detection).
type TaskRef struct {
	ID     int64  `db:"id"`
	TaskID string `db:"task_id"`
	SeqNo  int    `db:"seq_no"`
}

// TaskIDsByProject returns every task of one project ordered by seq_no. The
// caller builds a business-id → physical-id map from the result.
func TaskIDsByProject(ctx context.Context, q Querier, projectID int64) ([]TaskRef, error) {
	return All[TaskRef](ctx, q, `
		SELECT id, task_id, seq_no
		  FROM tasks
		 WHERE project_id = ?
		 ORDER BY seq_no ASC, id ASC`, projectID)
}

// CreateTaskDependencyByTaskIDs records that taskID depends on dependsOn,
// both business ids. It resolves the two physical ids in SQL so callers that
// only hold business ids need no extra round trip; a missing task id makes the
// statement insert nothing, which is reported as sql.ErrNoRows.
func CreateTaskDependencyByTaskIDs(ctx context.Context, tx *sql.Tx, taskID, dependsOn, createTime string) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO task_deps (task_id, depends_on, create_time, update_time)
		SELECT owner.id, dep.id, ?, ?
		  FROM tasks owner, tasks dep
		 WHERE owner.task_id = ? AND dep.task_id = ?`,
		createTime, createTime, taskID, dependsOn)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return fmt.Errorf("store: insert task dependency %s -> %s: %w", taskID, dependsOn, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: insert task dependency %s -> %s: rows affected: %w", taskID, dependsOn, err)
	}
	if n == 0 {
		return fmt.Errorf("store: insert task dependency %s -> %s: %w", taskID, dependsOn, sql.ErrNoRows)
	}
	return nil
}

// DeleteDependenciesTouching removes every task_deps row that points at taskID
// or is owned by it. It runs before DeleteTask in the same transaction: the
// foreign keys from task_deps would otherwise block the removal.
func DeleteDependenciesTouching(ctx context.Context, tx *sql.Tx, taskID int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		DELETE FROM task_deps WHERE task_id = ? OR depends_on = ?`, taskID, taskID)
	if err != nil {
		return 0, fmt.Errorf("store: delete task deps touching %d: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete task deps touching %d: rows affected: %w", taskID, err)
	}
	return n, nil
}

// DeleteTask removes one tasks row by physical id. Dependency rows must already
// be gone (DeleteDependenciesTouching in the same transaction).
func DeleteTask(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete task %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete task %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete task %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// ListDependenciesByTask returns the business ids taskID depends on, ordered by
// the dependency's seq_no so the response is stable (tech design §4.2).
func ListDependenciesByTask(ctx context.Context, q Querier, taskID int64) ([]string, error) {
	rows, err := All[struct {
		TaskID string `db:"task_id"`
	}](ctx, q, `
		SELECT dep.task_id
		  FROM task_deps td
		  JOIN tasks dep ON dep.id = td.depends_on
		 WHERE td.task_id = ?
		 ORDER BY dep.seq_no ASC, dep.id ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: list dependencies of task %d: %w", taskID, err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.TaskID)
	}
	return out, nil
}

// TaskDependenciesByTaskIDs returns the dependency business ids of every given
// physical task id. Tasks without dependencies are absent from the map, so a
// missing key and an empty slice mean the same thing to the caller.
//
// One statement covers a whole page, which keeps the list endpoint free of an
// N+1 query regardless of page size.
func TaskDependenciesByTaskIDs(ctx context.Context, q Querier, ids []int64) (map[int64][]string, error) {
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
		TaskID string `db:"task_id"`
	}](ctx, q, `
		SELECT td.task_id AS owner_id, dep.task_id
		  FROM task_deps td
		  JOIN tasks dep ON dep.id = td.depends_on
		 WHERE td.task_id IN (`+placeholders(len(ids))+`)
		 ORDER BY td.task_id ASC, dep.seq_no ASC, dep.id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list dependencies of %d tasks: %w", len(ids), err)
	}
	for _, row := range rows {
		out[row.Owner] = append(out[row.Owner], row.TaskID)
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
