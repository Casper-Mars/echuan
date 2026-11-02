package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Worker persistence (tech design v0.6 §2.1 / §2.3 / §5).
//
// One row per registered worker. The pointer pair (current_task_id,
// current_lease_id) is what makes a worker busy; the partial UNIQUE index
// ux_workers_current_task guarantees that a task is owned by at most one
// worker, and NULL pointers are unrestricted so any number of idle workers can
// coexist.
//
// Time columns are strings, compared lexicographically by SQL, so both layouts
// are load-bearing:
//
//	first_seen / last_seen — fixed-width millisecond UTC (WorkerTimeLayout),
//	                          because ListWorkers orders by last_seen and
//	                          ListStaleWorkers compares last_seen < ?. Same
//	                          reasoning as tasks.lease_until: RFC3339Nano trims
//	                          trailing zeros and would mis-order two rows inside
//	                          the same second.
//	create_time/update_time — the regular persisted timestamp layout, like every
//	                          other table; the application layer renders them.
//
// workers.state is CHECK-constrained to 'idle'/'busy'; the store spells the two
// values out as literals because the dependency runs domain ← store.

// WorkerTimeLayout is the fixed-width UTC layout of workers.first_seen and
// workers.last_seen.
//
// It is exported so the application layer renders heartbeats with exactly the
// same character width the store compares; a mismatched layout would silently
// break both ordering and the stale sweep.
const WorkerTimeLayout = "2006-01-02T15:04:05.000Z"

// FormatWorkerTime renders t in WorkerTimeLayout. Every writer of first_seen /
// last_seen must go through it (or produce byte-identical output).
func FormatWorkerTime(t time.Time) string { return t.UTC().Format(WorkerTimeLayout) }

// workerColumns is the full column list of workers, in table order.
const workerColumns = `id, worker_id, metadata_json, state, current_task_id,
	current_lease_id, first_seen, last_seen, create_time, update_time`

// Worker is the read model of one workers row.
//
// MetadataJSON is kept as the raw document: the store never parses or rewrites
// it, so an unknown metadata field survives a round trip.
type Worker struct {
	ID             int64   `db:"id"`
	WorkerID       string  `db:"worker_id"`
	MetadataJSON   string  `db:"metadata_json"`
	State          string  `db:"state"`
	CurrentTaskID  *int64  `db:"current_task_id"`
	CurrentLeaseID *string `db:"current_lease_id"`
	FirstSeen      string  `db:"first_seen"`
	LastSeen       string  `db:"last_seen"`
	CreateTime     string  `db:"create_time"`
	UpdateTime     string  `db:"update_time"`
}

// WorkerUpsert is the write model for registration and heartbeat.
//
// LastSeen must be rendered with FormatWorkerTime. On the first insert it also
// becomes first_seen, so a worker's "registered at" is the instant of its first
// heartbeat rather than a separate clock read.
//
// An empty MetadataJSON means "no document supplied": the insert stores '{}'
// (the column default) and a later update keeps whatever is already stored.
// That distinction matters because registration and heartbeat share this
// function — a heartbeat that carries no metadata must not wipe the document a
// registration wrote.
//
// State is written as given: workers.state is a denormalized mirror of the
// pointer, and only BindTask/ClearTask own the transitions. A heartbeat passes
// the state it believes it is in; the store does not second-guess it.
type WorkerUpsert struct {
	WorkerID     string
	MetadataJSON string
	State        string
	LastSeen     string
	UpdateTime   string
}

// UpsertWorker registers a worker or refreshes its heartbeat, and returns the
// stored row.
//
// The conflict branch updates metadata/state/last_seen/update_time only:
// first_seen and create_time stay at their initial values, which is what makes
// the call idempotent (N calls for one worker_id leave exactly one row). The
// binding columns are deliberately untouched — a heartbeat must never clear or
// steal a task pointer; that is BindTask/ClearTask's job.
func UpsertWorker(ctx context.Context, tx *sql.Tx, w WorkerUpsert) (Worker, error) {
	insertMetadata := w.MetadataJSON
	if insertMetadata == "" {
		insertMetadata = "{}"
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, first_seen, last_seen, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(worker_id) DO UPDATE SET
			metadata_json = CASE WHEN ? = '' THEN workers.metadata_json ELSE excluded.metadata_json END,
			state         = excluded.state,
			last_seen     = excluded.last_seen,
			update_time   = excluded.update_time`,
		w.WorkerID, insertMetadata, w.State, w.LastSeen, w.LastSeen, w.UpdateTime, w.UpdateTime,
		w.MetadataJSON)
	if err != nil {
		return Worker{}, fmt.Errorf("store: upsert worker %s: %w", w.WorkerID, err)
	}

	row, err := GetWorker(ctx, tx, w.WorkerID)
	if err != nil {
		return Worker{}, fmt.Errorf("store: read worker %s after upsert: %w", w.WorkerID, err)
	}
	return row, nil
}

// GetWorker returns the worker with the given business id; a missing row is
// reported as sql.ErrNoRows (see IsNotFound).
func GetWorker(ctx context.Context, q Querier, workerID string) (Worker, error) {
	return One[Worker](ctx, q,
		`SELECT `+workerColumns+` FROM workers WHERE worker_id = ?`, workerID)
}

// ListWorkers returns one page of workers ordered by last_seen DESC, id DESC.
//
// The cursor is a keyset position, not an offset: afterID is the physical id of
// the last row of the previous page (0 starts from the beginning) and page two
// continues strictly after that row's (last_seen, id) pair. The caller reads
// limit+1 rows to learn whether another page exists, exactly like
// ListProducts.
//
// Known limitation: last_seen moves with every heartbeat, so the cursor of a
// row that beats between two page reads shifts with it. A worker that heartbeats
// while the client is paging can therefore be repeated or skipped. That is
// inherent to ordering a live-registry by a mutable column; the operator UI
// pages a snapshot and re-reads it, it does not page a moving target forever.
// A row deleted between pages makes the subquery return NULL, which ends the
// listing instead of restarting it.
func ListWorkers(ctx context.Context, q Querier, afterID int64, limit int) ([]Worker, error) {
	query := `SELECT ` + workerColumns + ` FROM workers`
	args := make([]any, 0, 2)
	if afterID != 0 {
		// Row-value comparison (SQLite 3.15+): (last_seen, id) < (cursor)
		// walks the (last_seen DESC, id DESC) order without an OFFSET.
		query += ` WHERE (last_seen, id) < (SELECT last_seen, id FROM workers WHERE id = ?)`
		args = append(args, afterID)
	}
	query += ` ORDER BY last_seen DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := All[Worker](ctx, q, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list workers: %w", err)
	}
	return rows, nil
}

// BindTask points a worker at a task and marks it busy.
//
// Two conflicts must be reported, and both surface as ErrUniqueViolation so the
// HTTP layer maps them to one code (worker_busy):
//
//   - the task is already owned by another worker — caught by the partial
//     UNIQUE index ux_workers_current_task;
//   - the worker is already busy with a different task — not covered by any
//     index (the unique index is on current_task_id, one column), so it is
//     checked explicitly before the UPDATE. The write transaction is immediate,
//     hence single-writer, so the check cannot race a concurrent bind.
//
// Re-binding the same (worker, task) pair is idempotent: a retried claim must
// not fail, it just refreshes the lease id and the timestamps.
func BindTask(ctx context.Context, tx *sql.Tx, workerID string, taskID int64, leaseID, updateTime string) error {
	current, err := One[struct {
		ID            int64  `db:"id"`
		CurrentTaskID *int64 `db:"current_task_id"`
	}](ctx, tx, `SELECT id, current_task_id FROM workers WHERE worker_id = ?`, workerID)
	if err != nil {
		return fmt.Errorf("store: bind task %d to worker %s: %w", taskID, workerID, err)
	}
	if current.CurrentTaskID != nil && *current.CurrentTaskID != taskID {
		return fmt.Errorf("%w: worker %s is already bound to task %d",
			ErrUniqueViolation, workerID, *current.CurrentTaskID)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_task_id = ?,
		       current_lease_id = ?,
		       state = 'busy',
		       update_time = ?
		 WHERE id = ?`, taskID, leaseID, updateTime, current.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: task %d is already owned by another worker", ErrUniqueViolation, taskID)
		}
		return fmt.Errorf("store: bind task %d to worker %s: %w", taskID, workerID, err)
	}
	return nil
}

// ClearTaskByWorker releases the task held by one worker and marks it idle.
//
// It is idempotent: clearing an already idle worker matches the row again and
// succeeds. A missing worker is sql.ErrNoRows, because "release a worker that
// does not exist" is a caller bug, not a no-op.
func ClearTaskByWorker(ctx context.Context, tx *sql.Tx, workerID, updateTime string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_task_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle',
		       update_time = ?
		 WHERE worker_id = ?`, updateTime, workerID)
	if err != nil {
		return fmt.Errorf("store: clear task of worker %s: %w", workerID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: clear task of worker %s: rows affected: %w", workerID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: clear task of worker %s: %w", workerID, sql.ErrNoRows)
	}
	return nil
}

// ClearTaskByTaskID releases whichever worker holds taskID and returns how many
// rows changed.
//
// It is the reverse direction of ClearTaskByWorker, used by the report and
// reclaim paths that only know the task. Zero rows is a normal outcome — the
// card may have been claimed by a worker that never bound it — so it is not an
// error.
func ClearTaskByTaskID(ctx context.Context, tx *sql.Tx, taskID int64, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_task_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle',
		       update_time = ?
		 WHERE current_task_id = ?`, updateTime, taskID)
	if err != nil {
		return 0, fmt.Errorf("store: clear worker of task %d: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear worker of task %d: rows affected: %w", taskID, err)
	}
	return n, nil
}

// GetWorkerByTaskID returns the worker currently holding taskID. A task nobody
// holds is sql.ErrNoRows.
func GetWorkerByTaskID(ctx context.Context, q Querier, taskID int64) (Worker, error) {
	return One[Worker](ctx, q,
		`SELECT `+workerColumns+` FROM workers WHERE current_task_id = ?`, taskID)
}

// ListStaleWorkers returns the workers that have not been seen since before the
// cut-off and hold no task, oldest first.
//
// before must be rendered with FormatWorkerTime. The boundary matches
// domain.Presence: a worker is offline once now-last_seen > ttl, i.e. once
// last_seen < now-ttl, so callers pass now-ttl unchanged. Rows that still hold
// a task are excluded even when stale: an unreachable worker's card belongs to
// the lease reclaimer first, and deleting the row would drop the only record of
// what it was doing.
func ListStaleWorkers(ctx context.Context, q Querier, before string) ([]Worker, error) {
	rows, err := All[Worker](ctx, q,
		`SELECT `+workerColumns+`
		   FROM workers
		  WHERE last_seen < ?
		    AND current_task_id IS NULL
		  ORDER BY last_seen ASC, id ASC`, before)
	if err != nil {
		return nil, fmt.Errorf("store: list stale workers before %s: %w", before, err)
	}
	return rows, nil
}

// DeleteStaleWorker removes one stale worker row by physical id.
//
// The current_task_id IS NULL predicate is repeated here as a second line of
// defence: even if a caller passes an id straight from ListStaleWorkers and the
// worker has claimed a card in between, the DELETE matches nothing and reports
// sql.ErrNoRows instead of destroying a live binding.
func DeleteStaleWorker(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM workers WHERE id = ? AND current_task_id IS NULL`, id)
	if err != nil {
		return fmt.Errorf("store: delete stale worker %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete stale worker %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete stale worker %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// PruneStaleWorkers deletes the workers that have not been seen since before
// the cut-off and hold no task, and returns how many rows were removed.
//
// before must be rendered with FormatWorkerTime (see ListStaleWorkers, which
// applies the same boundary one row at a time). The bulk form exists for the
// 30s maintenance sweep: a registry of long-dead agents must not grow forever,
// while a stale row that still holds a card is kept because it is the only
// record of who was working on the card until the lease reclaimer moves it.
func PruneStaleWorkers(ctx context.Context, tx *sql.Tx, before string) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM workers WHERE current_task_id IS NULL AND last_seen < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("store: prune stale workers before %s: %w", before, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune stale workers before %s: rows affected: %w", before, err)
	}
	return n, nil
}

// ClearDanglingPointers releases every worker whose pointer references a task
// that is no longer in progress (status 2), and returns how many rows changed.
//
// It is the maintenance statement of tech design §5: a card that was completed,
// reclaimed or cancelled outside the worker's control must not leave the worker
// busy forever, or the registry would claim capacity that no longer exists. A
// pointer to a missing task row is cleared too; the NOT EXISTS form covers both
// cases in one statement and stays correct if the foreign key is ever relaxed.
func ClearDanglingPointers(ctx context.Context, tx *sql.Tx, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_task_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle',
		       update_time = ?
		 WHERE current_task_id IS NOT NULL
		   AND NOT EXISTS (
		         SELECT 1 FROM tasks t
		          WHERE t.id = workers.current_task_id
		            AND t.status = 2) /* 2 = in_progress */`, updateTime)
	if err != nil {
		return 0, fmt.Errorf("store: clear dangling worker pointers: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear dangling worker pointers: rows affected: %w", err)
	}
	return n, nil
}

// The state CHECK constraint accepts exactly 'idle' (no task held) and 'busy'
// (current_task_id set); the writers above use those two literals inline.
