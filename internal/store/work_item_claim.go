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
// The queue is one work_items table filtered by kind. A worker may only take a
// kind it declared in worker_kinds; the subquery is the dispatch rule, so a
// worker with no declared kinds simply claims nothing.
//
// lease_until is compared as a string, so every writer must use the same
// fixed-width UTC layout (millisecond precision, e.g.
// "2026-10-27T00:40:00.000Z"): with variable-width RFC3339Nano a lease ending
// ".500" would sort before one ending "Z" in the same second.

// claimableWorkItemSQL picks the next claimable item of one queue scope.
//
// The first %s is the scope predicate: a product filter for ClaimWorkItem or
// the constant-true predicate productAnyScope for
// ClaimNextWorkItem (the worker heartbeat, which is not scoped to a product).
// The second %s is the optional directed-claim filter. Both fragments are
// compile-time constants owned by this file, and every caller value travels as
// a bound parameter, so the template never interpolates request data.
//
// Conditions (tech design §5.4): state=queued, kind declared by the worker, all
// dependencies accepted, attempt budget left; ordered by priority DESC,
// seq_no ASC, id ASC so the queue is deterministic when two cards share a
// priority and sequence.
const claimableWorkItemSQL = `
	WITH next AS (
	  SELECT w.id
	    FROM work_items w
	    JOIN projects p ON p.id = w.project_id
	   WHERE %s
	     AND w.state = 'queued'
	     AND w.attempt < w.max_attempts
	     AND w.kind IN (SELECT kind FROM worker_kinds WHERE worker_id = ?)
	     AND NOT EXISTS (
	           SELECT 1
	             FROM work_deps d
	             JOIN work_items dep ON dep.id = d.depends_on_id
	            WHERE d.work_item_id = w.id AND dep.state <> 'accepted')
	     %s
	   ORDER BY w.priority DESC, w.seq_no ASC, w.id ASC
	   LIMIT 1
	)
	UPDATE work_items
	   SET state = 'leased',
	       lease_id = ?,
	       lease_until = ?,
	       worker_id = ?,
	       attempt = attempt + 1,
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
// ProductID is the physical products.id; WorkID is an optional business id that
// restricts the attempt to that single card. WorkerID identifies the claimant:
// its worker_kinds rows decide which kinds are claimable, and the leased row is
// stamped with that worker. LeaseUntil must already be rendered in the
// fixed-width lease layout (see the package section above).
type ClaimRequest struct {
	ProductID  int64
	WorkID     string
	WorkerID   string
	LeaseID    string
	LeaseUntil string
	UpdateTime string
}

// ClaimWorkItem leases the next claimable item of one product and returns the
// item as it looks after the write.
//
// The selection and the UPDATE are one statement, so the "read the queue then
// write" race does not exist. ok is false when nothing is claimable — an empty
// queue (or a worker whose declared kinds have no queued work) is a normal
// outcome, not an error.
func ClaimWorkItem(ctx context.Context, tx *sql.Tx, req ClaimRequest) (WorkItem, bool, error) {
	// The work_id filter is appended rather than written as "(? = '' OR
	// work_id = ?)": the planner then uses the UNIQUE(work_id) index for a
	// directed claim instead of scanning the product's queued cards.
	filter := ""
	args := []any{req.ProductID, req.WorkerID}
	if req.WorkID != "" {
		filter = "AND w.work_id = ?"
		args = append(args, req.WorkID)
	}
	args = append(args, req.LeaseID, req.LeaseUntil, req.WorkerID, req.UpdateTime)

	query := fmt.Sprintf(claimableWorkItemSQL, productScoped, filter)
	return runClaim(ctx, tx, query, args, fmt.Sprintf("work item of product %d", req.ProductID))
}

// ClaimNextRequest is one atomic claim against the whole queue, with no product
// scope. It is what a worker heartbeat uses: the worker is handed the next card
// of any product, because its registration is not bound to one product.
type ClaimNextRequest struct {
	// WorkID optionally restricts the attempt to one business id. Empty means
	// "any claimable card"; a set id that is not claimable yields no work.
	WorkID     string
	WorkerID   string
	LeaseID    string
	LeaseUntil string
	UpdateTime string
}

// ClaimNextWorkItem leases the next claimable card across every product.
//
// It shares claimableWorkItemSQL with ClaimWorkItem, so the queue order, the
// kind filter, the dependency check and the attempt budget keep one definition.
// Only the scope predicate differs: productAnyScope instead of a product
// filter. ok is false when the queue is empty.
func ClaimNextWorkItem(ctx context.Context, tx *sql.Tx, req ClaimNextRequest) (WorkItem, bool, error) {
	filter := ""
	args := []any{req.WorkerID}
	if req.WorkID != "" {
		filter = "AND w.work_id = ?"
		args = append(args, req.WorkID)
	}
	args = append(args, req.LeaseID, req.LeaseUntil, req.WorkerID, req.UpdateTime)

	query := fmt.Sprintf(claimableWorkItemSQL, productAnyScope, filter)
	return runClaim(ctx, tx, query, args, "next work item")
}

// runClaim executes one rendered claim statement and reads the claimed row
// back. subject names the queue scope in error messages.
func runClaim(ctx context.Context, tx *sql.Tx, query string, args []any, subject string) (WorkItem, bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkItem{}, false, nil
		}
		// ux_work_worker keeps one leased card per worker; a worker that still
		// holds one hits that index instead of silently stealing a second.
		if isUniqueViolation(err) {
			return WorkItem{}, false, fmt.Errorf("%w: worker already holds a lease", ErrUniqueViolation)
		}
		return WorkItem{}, false, fmt.Errorf("store: claim %s: %w", subject, err)
	}

	row, err := GetWorkItemByID(ctx, tx, id)
	if err != nil {
		return WorkItem{}, false, fmt.Errorf("store: read claimed work item %d: %w", id, err)
	}
	return row, true, nil
}

// WorkItemHeartbeatRequest is one lease renewal. Now and Until use the
// fixed-width lease layout; UpdateTime is the regular persisted timestamp
// format.
type WorkItemHeartbeatRequest struct {
	WorkID     string
	LeaseID    string
	Now        string
	Until      string
	UpdateTime string
}

// HeartbeatLease extends the lease of one leased item.
//
// The UPDATE only matches a live lease: right item, right lease id, state
// leased and an unexpired deadline. ok is false when the lease was lost
// (expired, reclaimed, or never held by this worker); the caller re-reads the
// item to tell "item does not exist" from "lease lost".
func HeartbeatLease(ctx context.Context, tx *sql.Tx, req WorkItemHeartbeatRequest) (string, bool, error) {
	var until string
	err := tx.QueryRowContext(ctx, `
		UPDATE work_items
		   SET lease_until = ?, version = version + 1, update_time = ?
		 WHERE work_id = ? AND state = 'leased' AND lease_id = ?
		   AND lease_until IS NOT NULL AND lease_until > ?
		RETURNING lease_until`,
		req.Until, req.UpdateTime, req.WorkID, req.LeaseID, req.Now).Scan(&until)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("store: heartbeat work item %s: %w", req.WorkID, err)
	}
	return until, true, nil
}

// ReclaimedWorkItem is one item moved by ReclaimExpiredWorkItems. ID is the
// physical work_items.id, which a caller needs to release the worker pointer
// that may still reference the reclaimed row (ClearWorkItemByWorkItemID).
type ReclaimedWorkItem struct {
	ID      int64   `db:"id"`
	WorkID  string  `db:"work_id"`
	Attempt int     `db:"attempt"`
	State   string  `db:"state"`
	Reason  *string `db:"reason"`
}

// ReclaimExpiredWorkItems returns expired leases to the queue and reports one
// row per item that moved.
//
// One statement does the whole sweep: the state is chosen per row by the
// attempt budget (failed once attempts are used up, queued otherwise), and the
// lease columns and the worker stamp are cleared either way. Because the
// predicate is part of the UPDATE, a second sweep — or a concurrent one —
// matches nothing for an item that already moved, so every reclaim is applied
// exactly once.
//
// A second predicate keeps the queue honest: a queued card whose budget is
// already used up can never be claimed (the queue requires attempt <
// max_attempts), so the sweep moves it to failed with the same note. That
// repairs cards left behind by an older requeue-on-reject path, which kept the
// attempt and parked them in a queue nothing dispatches and the inbox does not
// show.
//
// The reason of a requeued item is cleared: a failed attempt's reason must not
// linger on a card that is queued again. now and updateTime are rendered by the
// caller (now in the fixed-width lease layout, updateTime as a regular
// timestamp).
func ReclaimExpiredWorkItems(ctx context.Context, tx *sql.Tx, now, updateTime string) ([]ReclaimedWorkItem, error) {
	rows, err := tx.QueryContext(ctx, `
		UPDATE work_items
		   SET state = CASE WHEN attempt >= max_attempts THEN 'failed' ELSE 'queued' END,
		       reason = CASE WHEN attempt >= max_attempts
		                     THEN '本轮 ' || attempt || '/' || max_attempts ||
		                          ' 次尝试已用完，需人工驳回该卡重开'
		                     ELSE NULL END,
		       lease_id = NULL,
		       lease_until = NULL,
		       worker_id = NULL,
		       version = version + 1,
		       update_time = ?
		 WHERE (state = 'leased' AND lease_until IS NOT NULL AND lease_until < ?)
		    OR (state = 'queued' AND attempt >= max_attempts)
		RETURNING id, work_id, attempt, state, reason`, updateTime, now)
	if err != nil {
		return nil, fmt.Errorf("store: reclaim expired leases: %w", err)
	}
	return scanRows[ReclaimedWorkItem](rows)
}
