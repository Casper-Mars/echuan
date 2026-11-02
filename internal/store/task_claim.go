package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Claim queue and lease maintenance (tech design §4.4, §5.1–§5.3).
//
// Every statement here is a single conditional write: the row is chosen and
// mutated by the same statement, so two workers (or two processes sharing the
// file) can never claim the same card. The write pool is a single connection
// and the DSN sets _txlock=immediate, which serialises the writers on top of
// that.
//
// tasks.status is spelled out as a literal (1 ready, 2 in_progress, 4 done,
// 5 failed) because the dependency runs domain ← store.
//
// lease_until is compared as a string, so every writer must use the same
// fixed-width UTC layout (millisecond precision, e.g.
// "2026-10-27T00:40:00.000Z"): with variable-width RFC3339Nano a lease ending
// ".500" would sort before one ending "Z" in the same second.

// claimableTaskSQL picks the next claimable task of one queue scope.
//
// The first %s is the scope predicate: a product filter for ClaimTask or the
// constant-true predicate productAnyScope for ClaimNextTask (the worker
// heartbeat, which is not scoped to a product). The second %s is the optional
// directed-claim filter. Both fragments are compile-time constants owned by
// this file, and every caller value travels as a bound parameter, so the
// template never interpolates request data.
//
// Conditions (tech design §5.4): status=ready, all dependencies done, attempt
// budget left; ordered by priority DESC, seq_no ASC, id ASC so the queue is
// deterministic when two cards share a priority and sequence.
const claimableTaskSQL = `
	WITH next AS (
	  SELECT t.id
	    FROM tasks t
	    JOIN projects p ON p.id = t.project_id
	   WHERE %s
	     AND t.status = 1
	     AND t.attempt_count < t.max_attempts
	     AND NOT EXISTS (
	           SELECT 1
	             FROM task_deps td
	             JOIN tasks dep ON dep.id = td.depends_on
	            WHERE td.task_id = t.id AND dep.status <> 4)
	     %s
	   ORDER BY t.priority DESC, t.seq_no ASC, t.id ASC
	   LIMIT 1
	)
	UPDATE tasks
	   SET status = 2,
	       lease_id = ?,
	       lease_until = ?,
	       attempt_count = attempt_count + 1,
	       last_actor = ?,
	       version = version + 1,
	       update_time = ?
	 WHERE id = (SELECT id FROM next)
	RETURNING id`

// The two queue scopes. productScoped rests on the products foreign key;
// productAnyScope is a literal TRUE, so the planner drops the predicate instead
// of comparing a column to itself.
const (
	productScoped   = "p.product_id = ?"
	productAnyScope = "1 = 1"
)

// ClaimRequest is one atomic claim attempt.
//
// ProductID is the physical products.id; TaskID is an optional business task
// id that restricts the attempt to that single card. LeaseUntil must already
// be rendered in the fixed-width lease layout (see the package comment above).
type ClaimRequest struct {
	ProductID  int64
	TaskID     string
	LeaseID    string
	LeaseUntil string
	Actor      string
	UpdateTime string
}

// ClaimTask leases the next claimable task of one product and returns the task
// as it looks after the write.
//
// The selection and the UPDATE are one statement, so the "read the queue then
// write" race does not exist. ok is false when nothing is claimable — an empty
// queue is a normal outcome, not an error.
func ClaimTask(ctx context.Context, tx *sql.Tx, req ClaimRequest) (Task, bool, error) {
	// The task_id filter is appended rather than written as "(? = '' OR
	// task_id = ?)": the planner then uses the UNIQUE(task_id) index for a
	// directed claim instead of scanning the product's ready cards.
	filter := ""
	args := []any{req.ProductID}
	if req.TaskID != "" {
		filter = "AND t.task_id = ?"
		args = append(args, req.TaskID)
	}
	args = append(args, req.LeaseID, req.LeaseUntil, req.Actor, req.UpdateTime)

	query := fmt.Sprintf(claimableTaskSQL, productScoped, filter)
	return runClaim(ctx, tx, query, args, fmt.Sprintf("task of product %d", req.ProductID))
}

// ClaimNextRequest is one atomic claim against the whole queue, with no product
// scope. It is what a worker heartbeat uses: the worker is handed the next card
// of any product, because its registration is not bound to one product.
type ClaimNextRequest struct {
	LeaseID    string
	LeaseUntil string
	Actor      string
	UpdateTime string
}

// ClaimNextTask leases the next claimable task across every product.
//
// It shares claimableTaskSQL with ClaimTask, so the queue order, the dependency
// check and the attempt budget keep one definition. Only the scope predicate
// differs: productAnyScope instead of a product filter. ok is false when the
// queue is empty.
func ClaimNextTask(ctx context.Context, tx *sql.Tx, req ClaimNextRequest) (Task, bool, error) {
	query := fmt.Sprintf(claimableTaskSQL, productAnyScope, "")
	args := []any{req.LeaseID, req.LeaseUntil, req.Actor, req.UpdateTime}
	return runClaim(ctx, tx, query, args, "next task")
}

// runClaim executes one rendered claim statement and reads the claimed row
// back. subject names the queue scope in error messages.
func runClaim(ctx context.Context, tx *sql.Tx, query string, args []any, subject string) (Task, bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, false, nil
		}
		return Task{}, false, fmt.Errorf("store: claim %s: %w", subject, err)
	}

	row, err := GetTaskByID(ctx, tx, id)
	if err != nil {
		return Task{}, false, fmt.Errorf("store: read claimed task %d: %w", id, err)
	}
	return row, true, nil
}

// HeartbeatRequest is one lease renewal. Now and Until use the fixed-width
// lease layout; UpdateTime is the regular persisted timestamp format.
type HeartbeatRequest struct {
	TaskID     string
	LeaseID    string
	Now        string
	Until      string
	UpdateTime string
}

// HeartbeatLease extends the lease of one in-progress task.
//
// The UPDATE only matches a live lease: right task, right lease id, status
// in_progress and an unexpired deadline. ok is false when the lease was lost
// (expired, reclaimed, or never held by this worker); the caller re-reads the
// task to tell "task does not exist" from "lease lost".
func HeartbeatLease(ctx context.Context, tx *sql.Tx, req HeartbeatRequest) (string, bool, error) {
	var until string
	err := tx.QueryRowContext(ctx, `
		UPDATE tasks
		   SET lease_until = ?, version = version + 1, update_time = ?
		 WHERE task_id = ? AND status = 2 AND lease_id = ?
		   AND lease_until IS NOT NULL AND lease_until > ?
		RETURNING lease_until`,
		req.Until, req.UpdateTime, req.TaskID, req.LeaseID, req.Now).Scan(&until)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("store: heartbeat task %s: %w", req.TaskID, err)
	}
	return until, true, nil
}

// ReclaimedTask is one task moved by ReclaimExpired. ID is the physical
// tasks.id, which a caller needs to release the worker pointer that may still
// reference the reclaimed row (ClearTaskByTaskID).
type ReclaimedTask struct {
	ID           int64   `db:"id"`
	TaskID       string  `db:"task_id"`
	AttemptCount int     `db:"attempt_count"`
	Status       int     `db:"status"`
	Reason       *string `db:"reason"`
}

// ReclaimExpired returns expired leases to the queue and reports one row per
// task that moved.
//
// One statement does the whole sweep: the status is chosen per row by the
// attempt budget (5 failed once attempts are used up, 1 ready otherwise), and
// the lease columns are cleared either way. Because the predicate is part of
// the UPDATE, a second sweep — or a concurrent one — matches nothing for a
// task that already moved, so every reclaim is applied exactly once.
//
// The reason of a requeued task is cleared: a failed attempt's reason must not
// linger on a card that is ready again. now and updateTime are rendered by the
// caller (now in the fixed-width lease layout, updateTime as a regular
// timestamp).
func ReclaimExpired(ctx context.Context, tx *sql.Tx, now, updateTime string) ([]ReclaimedTask, error) {
	rows, err := tx.QueryContext(ctx, `
		UPDATE tasks
		   SET status = CASE WHEN attempt_count >= max_attempts THEN 5 ELSE 1 END,
		       reason = CASE WHEN attempt_count >= max_attempts THEN 'attempt 用尽' ELSE NULL END,
		       lease_id = NULL,
		       lease_until = NULL,
		       version = version + 1,
		       update_time = ?
		 WHERE status = 2
		   AND lease_until IS NOT NULL
		   AND lease_until < ?
		RETURNING id, task_id, attempt_count, status, reason`, updateTime, now)
	if err != nil {
		return nil, fmt.Errorf("store: reclaim expired leases: %w", err)
	}
	return scanRows[ReclaimedTask](rows)
}
