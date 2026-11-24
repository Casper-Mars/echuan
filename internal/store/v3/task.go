package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

const taskColumns = `t.id, t.task_id, t.project_id, t.parent_task_id, t.kind, t.title,
	t.description_md, t.context_md, t.priority, t.status, t.attempt, t.max_attempts,
	t.not_before, t.lease_id, t.lease_until, t.worker_id, t.reason,
	t.create_time, t.update_time, p.project_id AS project_code,
	(SELECT parent.task_id FROM tasks parent WHERE parent.id = t.parent_task_id) AS parent_code`

const taskFrom = ` FROM tasks t JOIN projects p ON p.id = t.project_id`

// CreateTask inserts a task and its dependencies. The initial status is
// computed once: no unmet dependency means ready, otherwise pending.
func CreateTask(ctx context.Context, s *storedb.Store, in CreateTaskInput) (Task, error) {
	var out Task
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = CreateTaskTx(ctx, tx, in)
		return err
	})
	return out, err
}

// CreateTaskTx is CreateTask inside a caller-owned transaction. The flow hook
// uses it to create successors atomically with the approval that triggers them.
func CreateTaskTx(ctx context.Context, tx *sql.Tx, in CreateTaskInput) (Task, error) {
	// The attempt budget is resolved by the service layer
	// (app/v3.Service.attemptLimit): the configured max_attempts for omitted or
	// derived tasks, the explicit request value otherwise. Reaching this
	// fallback means the wiring missed a call site, so keep the historical 3 —
	// a task must never be stored without a bounded budget.
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 3
	}

	var out Task
	err := func() error {
		var (
			projectPK     int64
			projectStatus enum.ProjectStatus
			projectCode   string
		)
		if err := tx.QueryRowContext(ctx,
			`SELECT id, status, project_id FROM projects WHERE project_id = ?`, in.ProjectID).
			Scan(&projectPK, &projectStatus, &projectCode); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrProjectNotFound, in.ProjectID)
			}
			return fmt.Errorf("v3: resolve project %s: %w", in.ProjectID, err)
		}
		if projectStatus != enum.ProjectOpen {
			return fmt.Errorf("%w: %s", ErrProjectClosed, in.ProjectID)
		}

		var parentPK *int64
		if in.ParentTaskID != "" {
			pk, err := resolveTaskPK(ctx, tx, in.ParentTaskID)
			if err != nil {
				return err
			}
			parentPK = &pk
		}

		depPKs := make([]int64, 0, len(in.DependsOn))
		seen := make(map[string]bool, len(in.DependsOn))
		for _, dep := range in.DependsOn {
			if dep == in.TaskID {
				return fmt.Errorf("v3: task %s cannot depend on itself", in.TaskID)
			}
			if seen[dep] {
				continue
			}
			seen[dep] = true
			pk, err := resolveTaskPK(ctx, tx, dep)
			if err != nil {
				return err
			}
			depPKs = append(depPKs, pk)
		}

		status, err := initialTaskStatus(ctx, tx, depPKs)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (task_id, project_id, parent_task_id, kind, title,
			                   description_md, context_md, priority, status, attempt,
			                   max_attempts, create_time, update_time)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
			in.TaskID, projectPK, parentPK, in.Kind, in.Title,
			in.DescriptionMD, in.ContextMD, in.Priority, status, in.MaxAttempts, in.Now, in.Now)
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: %v", storedb.ErrUniqueViolation, err)
			}
			return fmt.Errorf("v3: insert task %s: %w", in.TaskID, err)
		}
		pk, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("v3: task id: %w", err)
		}

		for _, depPK := range depPKs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO task_deps (task_id, depends_on_task_id) VALUES (?, ?)`, pk, depPK); err != nil {
				return fmt.Errorf("v3: insert dependency %s: %w", in.TaskID, err)
			}
		}

		if err := appendEvent(ctx, tx, pk, enum.EventCreated, "", `{}`, in.Now); err != nil {
			return err
		}

		out = Task{
			ID:            pk,
			TaskID:        in.TaskID,
			ProjectID:     projectPK,
			ParentTaskID:  parentPK,
			Kind:          in.Kind,
			Title:         in.Title,
			DescriptionMD: in.DescriptionMD,
			ContextMD:     in.ContextMD,
			Priority:      in.Priority,
			Status:        status,
			MaxAttempts:   in.MaxAttempts,
			CreateTime:    in.Now,
			UpdateTime:    in.Now,
			ProjectCode:   projectCode,
		}
		return nil
	}()
	return out, err
}

// GetTask loads one task by business id.
func GetTask(ctx context.Context, q storedb.Querier, taskID string) (Task, error) {
	task, err := storedb.One[Task](ctx, q,
		`SELECT `+taskColumns+taskFrom+` WHERE t.task_id = ?`, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if err != nil {
		return Task{}, fmt.Errorf("v3: get task %s: %w", taskID, err)
	}
	return task, nil
}

// ListTasks lists tasks with optional project/kind/status filters.
func ListTasks(ctx context.Context, q storedb.Querier, filter TaskFilter) ([]Task, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}

	where := []string{"1 = 1"}
	args := make([]any, 0, 4)
	if filter.ProjectID != "" {
		where = append(where, "p.project_id = ?")
		args = append(args, filter.ProjectID)
	}
	if filter.Kind != nil {
		where = append(where, "t.kind = ?")
		args = append(args, *filter.Kind)
	}
	if filter.Status != nil {
		where = append(where, "t.status = ?")
		args = append(args, *filter.Status)
	}
	args = append(args, filter.Limit)

	return storedb.All[Task](ctx, q,
		`SELECT `+taskColumns+taskFrom+` WHERE `+strings.Join(where, " AND ")+
			` ORDER BY t.priority DESC, t.id ASC LIMIT ?`, args...)
}

// ClaimTask leases the next ready task of one kind. The read, the lease and
// the worker projection happen in one write transaction, so two workers can
// never receive the same task.
func ClaimTask(ctx context.Context, s *storedb.Store, in ClaimInput) (Task, error) {
	var out Task
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var pk int64
		err := tx.QueryRowContext(ctx, `
			SELECT t.id
			  FROM tasks t JOIN projects p ON p.id = t.project_id
			 WHERE t.kind = ? AND t.status = ? AND p.status = ?
			   AND (t.not_before IS NULL OR t.not_before <= ?)
			   AND NOT EXISTS (
			         SELECT 1 FROM task_deps d JOIN tasks u ON u.id = d.depends_on_task_id
			          WHERE d.task_id = t.id AND u.status <> ?)
			 ORDER BY t.priority DESC, t.id ASC
			 LIMIT 1`,
			in.Kind, enum.Ready, enum.ProjectOpen, in.Now, enum.Done).Scan(&pk)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoWork
		}
		if err != nil {
			return fmt.Errorf("v3: pick task: %w", err)
		}

		// One worker, one lease (ux_tasks_worker_lease). Occupancy is checked
		// before the UPDATE, inside the same transaction as the write, because
		// the unique index would otherwise abort the statement and the driver
		// error ("UNIQUE constraint failed: tasks.worker_id") reads as a 500
		// (issue #165 D-1). The read is the index's own predicate, so a stale
		// workers.current_task_id cannot hide a held card.
		var held string
		err = tx.QueryRowContext(ctx, `
			SELECT task_id FROM tasks
			 WHERE worker_id = ? AND status = ?
			 LIMIT 1`, in.WorkerID, enum.InProgress).Scan(&held)
		switch {
		case err == nil:
			return fmt.Errorf("%w: %s holds %s", ErrWorkerBusy, in.WorkerID, held)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("v3: check worker %s occupancy: %w", in.WorkerID, err)
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, lease_id = ?, lease_until = ?, worker_id = ?,
			       attempt = attempt + 1, update_time = ?
			 WHERE id = ? AND status = ?`,
			enum.InProgress, in.LeaseID, in.LeaseUntil, in.WorkerID, in.Now, pk, enum.Ready)
		if err != nil {
			return fmt.Errorf("v3: lease task: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: lease task rows: %w", err)
		}
		if n == 0 {
			return ErrNoWork
		}

		// The projection must match a registry row: an UPDATE that matches
		// nothing silently succeeds, which would lease the card to a holder
		// that no listing, heartbeat or prune can ever see (the dangling
		// pointer of issue #165 §2 E-5). Returning the sentinel rolls the
		// whole transaction back — task row, worker row and claimed event —
		// so the invariant "a leased task has a registered holder" holds even
		// when the sweep deletes the row between the pre-check and this write.
		res, err = tx.ExecContext(ctx, `
			UPDATE workers
			   SET state = ?, current_task_id = ?, current_lease_id = ?, last_seen = ?, update_time = ?
			 WHERE worker_id = ?`,
			enum.WorkerBusy, pk, in.LeaseID, in.Now, in.Now, in.WorkerID)
		if err != nil {
			return fmt.Errorf("v3: bind worker %s: %w", in.WorkerID, err)
		}
		bound, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: bind worker %s rows: %w", in.WorkerID, err)
		}
		if bound == 0 {
			return fmt.Errorf("%w: %s", ErrWorkerNotRegistered, in.WorkerID)
		}

		if err := appendEvent(ctx, tx, pk, enum.EventClaimed, in.WorkerID,
			fmt.Sprintf(`{"lease_id":%q}`, in.LeaseID), in.Now); err != nil {
			return err
		}

		out, err = loadTaskTx(ctx, tx, pk)
		return err
	})
	return out, err
}

// RenewLease extends a lease. A missing or stale lease is ErrLeaseLost.
func RenewLease(ctx context.Context, s *storedb.Store, taskID, leaseID, until, now string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks SET lease_until = ?, update_time = ?
			 WHERE task_id = ? AND lease_id = ? AND status = ?`,
			until, now, taskID, leaseID, enum.InProgress)
		if err != nil {
			return fmt.Errorf("v3: renew lease %s: %w", taskID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: renew lease rows: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrLeaseLost, taskID)
		}
		pk, err := resolveTaskPK(ctx, tx, taskID)
		if err != nil {
			return err
		}
		return appendEvent(ctx, tx, pk, enum.EventRenewed, "", fmt.Sprintf(`{"lease_id":%q}`, leaseID), now)
	})
}

// ReportTask moves an in-progress task to in_review and releases its lease.
func ReportTask(ctx context.Context, s *storedb.Store, taskID, leaseID, now string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var (
			pk       int64
			status   enum.TaskStatus
			workerID *string
			current  *string
		)
		if err := tx.QueryRowContext(ctx,
			`SELECT id, status, worker_id, lease_id FROM tasks WHERE task_id = ?`, taskID).
			Scan(&pk, &status, &workerID, &current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
			}
			return fmt.Errorf("v3: report task %s: %w", taskID, err)
		}
		if status != enum.InProgress || current == nil || *current != leaseID {
			return fmt.Errorf("%w: %s", ErrLeaseLost, taskID)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, lease_id = NULL, lease_until = NULL, worker_id = NULL, update_time = ?
			 WHERE id = ?`,
			enum.InReview, now, pk); err != nil {
			return fmt.Errorf("v3: report task %s: %w", taskID, err)
		}
		if workerID != nil {
			if _, err := tx.ExecContext(ctx, `
				UPDATE workers
				   SET state = ?, current_task_id = NULL, current_lease_id = NULL, last_seen = ?, update_time = ?
				 WHERE worker_id = ? AND current_task_id = ?`,
				enum.WorkerIdle, now, now, *workerID, pk); err != nil {
				return fmt.Errorf("v3: release worker %s: %w", *workerID, err)
			}
		}
		return appendEvent(ctx, tx, pk, enum.EventReported, "", `{}`, now)
	})
}

// DecideTask records a gate decision. Approve moves the task to done and
// promotes every direct dependent whose dependencies are now satisfied, all in
// one transaction. Reject moves the task back to ready.
func DecideTask(ctx context.Context, s *storedb.Store, in DecideInput) (Task, error) {
	var out Task
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var (
			pk     int64
			kind   enum.TaskKind
			status enum.TaskStatus
		)
		if err := tx.QueryRowContext(ctx,
			`SELECT id, kind, status FROM tasks WHERE task_id = ?`, in.TaskID).
			Scan(&pk, &kind, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrTaskNotFound, in.TaskID)
			}
			return fmt.Errorf("v3: decide task %s: %w", in.TaskID, err)
		}
		if status != enum.InReview {
			return fmt.Errorf("%w: task %s is %s", ErrInvalidTransition, in.TaskID, status)
		}

		next := enum.Done
		event := enum.EventApproved
		if in.Decision == enum.Reject {
			next = enum.Ready
			event = enum.EventRejected
		}

		var reason any
		if in.Reason != "" {
			reason = in.Reason
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks SET status = ?, reason = ?, update_time = ? WHERE id = ?`,
			next, reason, in.Now, pk); err != nil {
			return fmt.Errorf("v3: decide task %s: %w", in.TaskID, err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO gate_decisions (task_id, gate, material_id, decision, decided_by, reason, create_time)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			in.TaskID, kind, in.MaterialID, in.Decision, in.DecidedBy, reason, in.Now); err != nil {
			return fmt.Errorf("v3: record decision %s: %w", in.TaskID, err)
		}

		if err := appendEvent(ctx, tx, pk, event, in.DecidedBy,
			fmt.Sprintf(`{"decision":%q}`, in.Decision), in.Now); err != nil {
			return err
		}

		task, err := loadTaskTx(ctx, tx, pk)
		if err != nil {
			return err
		}

		if in.Decision == enum.Approve {
			if err := promoteDependents(ctx, tx, pk, in.TaskID, in.Now); err != nil {
				return err
			}
			if in.AfterApprove != nil {
				if err := in.AfterApprove(ctx, tx, task); err != nil {
					return err
				}
			}
		}

		out = task
		return nil
	})
	return out, err
}

// FailTask marks a task failed and releases its lease.
func FailTask(ctx context.Context, s *storedb.Store, taskID, reason, now string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		pk, err := resolveTaskPK(ctx, tx, taskID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, reason = ?, lease_id = NULL, lease_until = NULL, worker_id = NULL, update_time = ?
			 WHERE id = ? AND status IN (?, ?, ?)`,
			enum.Failed, reason, now, pk, enum.Pending, enum.Ready, enum.InProgress)
		if err != nil {
			return fmt.Errorf("v3: fail task %s: %w", taskID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: fail task rows: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrInvalidTransition, taskID)
		}
		// The failure ends the occupancy: a worker still pointing at this card
		// would keep a current_task_id no prune can retire (issue #165 §1).
		if err := releaseWorkerForTask(ctx, tx, pk, now); err != nil {
			return err
		}
		return appendEvent(ctx, tx, pk, enum.EventFailed, "", fmt.Sprintf(`{"reason":%q}`, reason), now)
	})
}

// ResetTask re-queues a failed task. Its dependencies decide ready vs pending.
func ResetTask(ctx context.Context, s *storedb.Store, taskID, now string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var status enum.TaskStatus
		pk, err := resolveTaskPK(ctx, tx, taskID)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT status FROM tasks WHERE id = ?`, pk).Scan(&status); err != nil {
			return fmt.Errorf("v3: reset task %s: %w", taskID, err)
		}
		if status != enum.Failed {
			return fmt.Errorf("%w: task %s is %s", ErrInvalidTransition, taskID, status)
		}
		deps, err := depPKs(ctx, tx, pk)
		if err != nil {
			return err
		}
		next, err := initialTaskStatus(ctx, tx, deps)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE tasks SET status = ?, reason = NULL, update_time = ? WHERE id = ?`, next, now, pk); err != nil {
			return fmt.Errorf("v3: reset task %s: %w", taskID, err)
		}
		return appendEvent(ctx, tx, pk, enum.EventReset, "", `{}`, now)
	})
}

// CancelTask cancels a task that is not terminal.
func CancelTask(ctx context.Context, s *storedb.Store, taskID, actor, now string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		pk, err := resolveTaskPK(ctx, tx, taskID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, lease_id = NULL, lease_until = NULL, worker_id = NULL, update_time = ?
			 WHERE id = ? AND status IN (?, ?, ?, ?, ?)`,
			enum.Cancelled, now, pk,
			enum.Pending, enum.Ready, enum.InProgress, enum.InReview, enum.Failed)
		if err != nil {
			return fmt.Errorf("v3: cancel task %s: %w", taskID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: cancel task rows: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %s", ErrInvalidTransition, taskID)
		}
		// A cancelled task is nobody's occupancy, so a worker still pointing at
		// it must stop pointing at it (issue #165 §1).
		if err := releaseWorkerForTask(ctx, tx, pk, now); err != nil {
			return err
		}
		return appendEvent(ctx, tx, pk, enum.EventCancelled, actor, `{}`, now)
	})
}

// ── helpers ─────────────────────────────────────────────────────────────

func loadTaskTx(ctx context.Context, tx *sql.Tx, pk int64) (Task, error) {
	task, err := storedb.One[Task](ctx, tx,
		`SELECT `+taskColumns+taskFrom+` WHERE t.id = ?`, pk)
	if err != nil {
		return Task{}, fmt.Errorf("v3: load task %d: %w", pk, err)
	}
	return task, nil
}

func resolveTaskPK(ctx context.Context, tx *sql.Tx, taskID string) (int64, error) {
	var pk int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tasks WHERE task_id = ?`, taskID).Scan(&pk); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		return 0, fmt.Errorf("v3: resolve task %s: %w", taskID, err)
	}
	return pk, nil
}

func initialTaskStatus(ctx context.Context, tx *sql.Tx, deps []int64) (enum.TaskStatus, error) {
	if len(deps) == 0 {
		return enum.Ready, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(deps)), ",")
	args := make([]any, 0, len(deps)+1)
	for _, dep := range deps {
		args = append(args, dep)
	}
	args = append(args, enum.Done)
	var unmet int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE id IN (`+placeholders+`) AND status <> ?`, args...).Scan(&unmet); err != nil {
		return 0, fmt.Errorf("v3: count unmet dependencies: %w", err)
	}
	if unmet == 0 {
		return enum.Ready, nil
	}
	return enum.Pending, nil
}

func depPKs(ctx context.Context, tx *sql.Tx, taskPK int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT depends_on_task_id FROM task_deps WHERE task_id = ?`, taskPK)
	if err != nil {
		return nil, fmt.Errorf("v3: list dependencies: %w", err)
	}
	defer rows.Close()
	var deps []int64
	for rows.Next() {
		var dep int64
		if err := rows.Scan(&dep); err != nil {
			return nil, fmt.Errorf("v3: scan dependency: %w", err)
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

// promoteDependents moves every direct dependent from pending to ready when
// all of its dependencies are done. The update is conditional, so a concurrent
// pass or a dependency added later cannot promote a task early.
func promoteDependents(ctx context.Context, tx *sql.Tx, upstreamPK int64, upstreamTaskID, now string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, t.task_id
		  FROM task_deps d JOIN tasks t ON t.id = d.task_id
		 WHERE d.depends_on_task_id = ? AND t.status = ?`,
		upstreamPK, enum.Pending)
	if err != nil {
		return fmt.Errorf("v3: list dependents: %w", err)
	}
	type dependent struct {
		pk  int64
		key string
	}
	var items []dependent
	for rows.Next() {
		var item dependent
		if err := rows.Scan(&item.pk, &item.key); err != nil {
			rows.Close()
			return fmt.Errorf("v3: scan dependent: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("v3: iterate dependents: %w", err)
	}
	rows.Close()

	for _, item := range items {
		res, err := tx.ExecContext(ctx, `
			UPDATE tasks SET status = ?, update_time = ?
			 WHERE id = ? AND status = ?
			   AND NOT EXISTS (
			         SELECT 1 FROM task_deps d JOIN tasks u ON u.id = d.depends_on_task_id
			          WHERE d.task_id = ? AND u.status <> ?)`,
			enum.Ready, now, item.pk, enum.Pending, item.pk, enum.Done)
		if err != nil {
			return fmt.Errorf("v3: promote task %s: %w", item.key, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("v3: promote task rows: %w", err)
		}
		if n == 1 {
			detail := fmt.Sprintf(`{"upstream":%q}`, upstreamTaskID)
			if err := appendEvent(ctx, tx, item.pk, enum.EventPromoted, "flow", detail, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// AppendEventTx appends one task event inside a caller-owned transaction. The
// flow engine uses it to mark automatically created tasks.
func AppendEventTx(ctx context.Context, tx *sql.Tx, taskPK int64, event enum.Event, actor, detail, now string) error {
	return appendEvent(ctx, tx, taskPK, event, actor, detail, now)
}

// ListDependencies returns the business ids a task depends on, sorted.
func ListDependencies(ctx context.Context, q storedb.Querier, taskID string) ([]string, error) {
	rows, err := storedb.All[struct {
		TaskID string `db:"task_id"`
	}](ctx, q, `
		SELECT u.task_id
		  FROM task_deps d
		  JOIN tasks t ON t.id = d.task_id
		  JOIN tasks u ON u.id = d.depends_on_task_id
		 WHERE t.task_id = ?
		 ORDER BY u.task_id ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("v3: list dependencies of %s: %w", taskID, err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.TaskID)
	}
	return out, nil
}

func appendEvent(ctx context.Context, tx *sql.Tx, taskPK int64, event enum.Event, actor, detail, now string) error {
	var actorValue any
	if actor != "" {
		actorValue = actor
	}
	if detail == "" {
		detail = "{}"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_events (task_id, event, actor, detail_json, create_time)
		VALUES (?, ?, ?, ?, ?)`, taskPK, event, actorValue, detail, now); err != nil {
		return fmt.Errorf("v3: append event %s: %w", event, err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "constraint failed: tasks.task_id")
}
