package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// ErrTaskHasDependents blocks deleting a task another task still depends on.
var ErrTaskHasDependents = errors.New("v3: task has dependents")

// UpdateTaskContentInput is the write model for editing an unclaimed task.
type UpdateTaskContentInput struct {
	TaskID        string
	Title         string
	DescriptionMD string
	ContextMD     string
	Priority      int
	Now           string
}

// UpdateTaskContent edits title/description/context/priority of a pending or
// ready task. Started tasks are immutable: their context is already in a
// worker's hands.
func UpdateTaskContent(ctx context.Context, s *storedb.Store, in UpdateTaskContentInput) (Task, error) {
	var out Task
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		pk, err := resolveTaskPK(ctx, tx, in.TaskID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET title = ?, description_md = ?, context_md = ?, priority = ?, update_time = ?
			 WHERE id = ? AND status IN (?, ?)`,
			in.Title, in.DescriptionMD, in.ContextMD, in.Priority, in.Now,
			pk, enum.Pending, enum.Ready)
		if err != nil {
			return fmt.Errorf("v3: update task %s: %w", in.TaskID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: update task rows: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: task %s is not pending or ready", ErrInvalidTransition, in.TaskID)
		}
		out, err = loadTaskTx(ctx, tx, pk)
		return err
	})
	return out, err
}

// DeleteTask removes an unclaimed task and its owned rows. A task another task
// depends on cannot be deleted: the dependency would dangle.
func DeleteTask(ctx context.Context, s *storedb.Store, taskID string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var (
			pk     int64
			status enum.TaskStatus
		)
		if err := tx.QueryRowContext(ctx,
			`SELECT id, status FROM tasks WHERE task_id = ?`, taskID).Scan(&pk, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
			}
			return fmt.Errorf("v3: delete task %s: %w", taskID, err)
		}
		if status != enum.Pending && status != enum.Ready {
			return fmt.Errorf("%w: task %s is %s", ErrInvalidTransition, taskID, status)
		}

		var dependents int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM task_deps WHERE depends_on_task_id = ?`, pk).Scan(&dependents); err != nil {
			return fmt.Errorf("v3: count dependents of %s: %w", taskID, err)
		}
		if dependents > 0 {
			return fmt.Errorf("%w: %s (%d)", ErrTaskHasDependents, taskID, dependents)
		}

		for _, query := range []string{
			`DELETE FROM task_events WHERE task_id = ?`,
			`DELETE FROM attachments WHERE task_id = ?`,
			`DELETE FROM task_deps WHERE task_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, query, pk); err != nil {
				return fmt.Errorf("v3: delete task %s child rows: %w", taskID, err)
			}
		}
		// gate_decisions references tasks(task_id), so remove those by string.
		if _, err := tx.ExecContext(ctx, `DELETE FROM gate_decisions WHERE task_id = ?`, taskID); err != nil {
			return fmt.Errorf("v3: delete decisions of %s: %w", taskID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, pk); err != nil {
			return fmt.Errorf("v3: delete task %s: %w", taskID, err)
		}
		return nil
	})
}

// ReportInput is the write model for ReportTaskWithOutput.
type ReportInput struct {
	TaskID     string
	LeaseID    string
	MaterialID int64
	Now        string
}

// ReportTaskWithOutput writes the output attachment and moves the task to
// in_review in one transaction. This is the only report path the V3 service
// uses: a report without a deliverable is not a report.
func ReportTaskWithOutput(ctx context.Context, s *storedb.Store, in ReportInput) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var (
			pk       int64
			status   enum.TaskStatus
			workerID *string
			current  *string
		)
		if err := tx.QueryRowContext(ctx,
			`SELECT id, status, worker_id, lease_id FROM tasks WHERE task_id = ?`, in.TaskID).
			Scan(&pk, &status, &workerID, &current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrTaskNotFound, in.TaskID)
			}
			return fmt.Errorf("v3: report task %s: %w", in.TaskID, err)
		}
		if status != enum.InProgress || current == nil || *current != in.LeaseID {
			return fmt.Errorf("%w: %s", ErrLeaseLost, in.TaskID)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attachments (task_id, material_id, role, sort_no, create_time)
			VALUES (?, ?, ?, 0, ?)`, pk, in.MaterialID, enum.Output, in.Now); err != nil {
			return fmt.Errorf("v3: attach output %s: %w", in.TaskID, err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, lease_id = NULL, lease_until = NULL, worker_id = NULL, update_time = ?
			 WHERE id = ?`, enum.InReview, in.Now, pk); err != nil {
			return fmt.Errorf("v3: report task %s: %w", in.TaskID, err)
		}
		if workerID != nil {
			if _, err := tx.ExecContext(ctx, `
				UPDATE workers
				   SET state = ?, current_task_id = NULL, current_lease_id = NULL, last_seen = ?, update_time = ?
				 WHERE worker_id = ? AND current_task_id = ?`,
				enum.WorkerIdle, in.Now, in.Now, *workerID, pk); err != nil {
				return fmt.Errorf("v3: release worker %s: %w", *workerID, err)
			}
		}
		return appendEvent(ctx, tx, pk, enum.EventReported, "",
			fmt.Sprintf(`{"material_id":%d}`, in.MaterialID), in.Now)
	})
}

// ReclaimExpired returns expired in-progress tasks to the pool: ready when
// attempts remain, failed when the budget is exhausted.
func ReclaimExpired(ctx context.Context, s *storedb.Store, now string) (int, error) {
	reclaimed := 0
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, attempt, max_attempts, worker_id
			  FROM tasks
			 WHERE status = ? AND lease_until IS NOT NULL AND lease_until < ?`,
			enum.InProgress, now)
		if err != nil {
			return fmt.Errorf("v3: list expired leases: %w", err)
		}
		type expired struct {
			pk          int64
			attempt     int
			maxAttempts int
			workerID    *string
		}
		var items []expired
		for rows.Next() {
			var item expired
			if err := rows.Scan(&item.pk, &item.attempt, &item.maxAttempts, &item.workerID); err != nil {
				rows.Close()
				return fmt.Errorf("v3: scan expired lease: %w", err)
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("v3: iterate expired leases: %w", err)
		}
		rows.Close()

		for _, item := range items {
			next := enum.Ready
			event := enum.EventReset
			if item.attempt >= item.maxAttempts {
				next = enum.Failed
				event = enum.EventFailed
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE tasks
				   SET status = ?, lease_id = NULL, lease_until = NULL, worker_id = NULL,
				       reason = 'lease expired', update_time = ?
				 WHERE id = ?`,
				next, now, item.pk); err != nil {
				return fmt.Errorf("v3: reclaim task %d: %w", item.pk, err)
			}
			if item.workerID != nil {
				if _, err := tx.ExecContext(ctx, `
					UPDATE workers
					   SET state = ?, current_task_id = NULL, current_lease_id = NULL, update_time = ?
					 WHERE worker_id = ? AND current_task_id = ?`,
					enum.WorkerIdle, now, *item.workerID, item.pk); err != nil {
					return fmt.Errorf("v3: release worker %s: %w", *item.workerID, err)
				}
			}
			if err := appendEvent(ctx, tx, item.pk, event, "reclaimer",
				`{"reason":"lease expired"}`, now); err != nil {
				return err
			}
			reclaimed++
		}
		return nil
	})
	return reclaimed, err
}
