package store

import (
	"context"
	"database/sql"
	"fmt"
)

// taskColumns selects one task with its project's business id.
//
// tasks.status values are domain.Task* integers; the store spells the numbers
// out as literals (4 = done, 7 = cancelled) because the dependency runs
// domain ← store.
const taskColumns = `t.id, t.task_id, p.project_id AS project_code, t.seq_no,
	t.title, t.body_md, t.priority, t.status, t.attempt_count, t.max_attempts,
	t.lease_id, t.lease_until, t.last_actor, t.reason, t.version,
	t.create_time, t.update_time`

// Task is the read model of one tasks row.
type Task struct {
	ID           int64   `db:"id"`
	TaskID       string  `db:"task_id"`
	ProjectCode  string  `db:"project_code"`
	SeqNo        int     `db:"seq_no"`
	Title        string  `db:"title"`
	BodyMD       string  `db:"body_md"`
	Priority     int     `db:"priority"`
	Status       int     `db:"status"`
	AttemptCount int     `db:"attempt_count"`
	MaxAttempts  int     `db:"max_attempts"`
	LeaseID      *string `db:"lease_id"`
	LeaseUntil   *string `db:"lease_until"`
	LastActor    *string `db:"last_actor"`
	Reason       *string `db:"reason"`
	Version      int     `db:"version"`
	CreateTime   string  `db:"create_time"`
	UpdateTime   string  `db:"update_time"`
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
		  WHERE t.task_id = ?`, taskID)
}

// ListTasksByProject returns the tasks of one project ordered by seq_no.
func ListTasksByProject(ctx context.Context, q Querier, projectID int64) ([]Task, error) {
	return All[Task](ctx, q,
		`SELECT `+taskColumns+`
		   FROM tasks t JOIN projects p ON p.id = t.project_id
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
