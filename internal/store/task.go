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

// NewTask is the write model for INSERT.
type NewTask struct {
	TaskID     string
	ProjectID  int64
	SeqNo      int
	Title      string
	BodyMD     string
	Priority   int
	Status     int
	CreateTime string
	UpdateTime string
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
	res, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, priority,
		                   status, attempt_count, version, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 1, ?, ?)`,
		t.TaskID, t.ProjectID, t.SeqNo, t.Title, t.BodyMD, t.Priority,
		t.Status, t.CreateTime, t.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return 0, fmt.Errorf("store: insert task %s: %w", t.TaskID, err)
	}
	return res.LastInsertId()
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
