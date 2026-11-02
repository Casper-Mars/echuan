package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Worker persistence (tech design v0.8 §2.1 / §2.3 / §5).
//
// One row per registered worker. The pointer pair (current_work_item_id,
// current_lease_id) is what makes a worker busy; the partial UNIQUE index
// ux_workers_current_work_item guarantees that a card is owned by at most one
// worker, and NULL pointers are unrestricted so any number of idle workers can
// coexist.
//
// worker_kinds declares the kinds a worker can take. It is the only dispatch
// input: the claim SQL filters kind by this table, so a worker that declared
// nothing claims nothing.
//
// Time columns are strings, compared lexicographically by SQL, so both layouts
// are load-bearing:
//
//	first_seen / last_seen — fixed-width millisecond UTC (WorkerTimeLayout),
//	                          because ListWorkers orders by last_seen and
//	                          ListStaleWorkers compares last_seen < ?. Same
//	                          reasoning as work_items.lease_until: RFC3339Nano
//	                          trims trailing zeros and would mis-order two rows
//	                          inside the same second.
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
const workerColumns = `id, worker_id, metadata_json, state, current_work_item_id,
	current_lease_id, first_seen, last_seen, create_time, update_time`

// Worker is the read model of one workers row.
//
// MetadataJSON is kept as the raw document: the store never parses or rewrites
// it, so an unknown metadata field survives a round trip.
type Worker struct {
	ID                int64   `db:"id"`
	WorkerID          string  `db:"worker_id"`
	MetadataJSON      string  `db:"metadata_json"`
	State             string  `db:"state"`
	CurrentWorkItemID *int64  `db:"current_work_item_id"`
	CurrentLeaseID    *string `db:"current_lease_id"`
	FirstSeen         string  `db:"first_seen"`
	LastSeen          string  `db:"last_seen"`
	CreateTime        string  `db:"create_time"`
	UpdateTime        string  `db:"update_time"`
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
// pointer, and only BindWorkItem/ClearWorkItem own the transitions. A heartbeat
// passes the state it believes it is in; the store does not second-guess it.
//
// Kinds is the declaration set for this worker. nil means "leave the stored
// declaration untouched" (the heartbeat case); a non-nil slice replaces it
// wholesale, and an empty non-nil slice clears it.
type WorkerUpsert struct {
	WorkerID     string
	MetadataJSON string
	State        string
	Kinds        []string
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
// steal a card pointer; that is BindWorkItem/ClearWorkItem's job.
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

	if w.Kinds != nil {
		if err := SetWorkerKinds(ctx, tx, w.WorkerID, w.Kinds); err != nil {
			return Worker{}, err
		}
	}

	row, err := GetWorker(ctx, tx, w.WorkerID)
	if err != nil {
		return Worker{}, fmt.Errorf("store: read worker %s after upsert: %w", w.WorkerID, err)
	}
	return row, nil
}

// SetWorkerKinds replaces the kind declaration of one worker inside tx.
//
// The write is a delete-then-insert so the stored set is exactly kinds; it must
// run in a transaction, otherwise a reader could observe the intermediate empty
// state. An unknown kind is rejected before any row is touched. Duplicate
// entries are collapsed (worker_kinds has a composite primary key).
func SetWorkerKinds(ctx context.Context, tx *sql.Tx, workerID string, kinds []string) error {
	seen := make(map[string]bool, len(kinds))
	unique := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		if !IsValidWorkItemKind(kind) {
			return fmt.Errorf("store: set worker kinds of %s: invalid kind %q", workerID, kind)
		}
		if seen[kind] {
			continue
		}
		seen[kind] = true
		unique = append(unique, kind)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM worker_kinds WHERE worker_id = ?`, workerID); err != nil {
		return fmt.Errorf("store: clear worker kinds of %s: %w", workerID, err)
	}
	for _, kind := range unique {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO worker_kinds (worker_id, kind) VALUES (?, ?)`, workerID, kind); err != nil {
			return fmt.Errorf("store: insert worker kind %s of %s: %w", kind, workerID, err)
		}
	}
	return nil
}

// ListWorkerKinds returns the kinds declared by one worker ordered by kind. A
// worker with no declaration yields an empty slice, never nil.
func ListWorkerKinds(ctx context.Context, q Querier, workerID string) ([]string, error) {
	rows, err := All[struct {
		Kind string `db:"kind"`
	}](ctx, q, `
		SELECT kind FROM worker_kinds WHERE worker_id = ? ORDER BY kind ASC`, workerID)
	if err != nil {
		return nil, fmt.Errorf("store: list worker kinds of %s: %w", workerID, err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Kind)
	}
	return out, nil
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

// BindWorkItem points a worker at a card and marks it busy.
//
// Two conflicts must be reported, and both surface as ErrUniqueViolation so the
// HTTP layer maps them to one code (worker_busy):
//
//   - the card is already owned by another worker — caught by the partial
//     UNIQUE index ux_workers_current_work_item;
//   - the worker is already busy with a different card — not covered by any
//     index (the unique index is on current_work_item_id, one column), so it is
//     checked explicitly before the UPDATE. The write transaction is immediate,
//     hence single-writer, so the check cannot race a concurrent bind.
//
// Re-binding the same (worker, card) pair is idempotent: a retried claim must
// not fail, it just refreshes the lease id and the timestamps.
func BindWorkItem(ctx context.Context, tx *sql.Tx, workerID string, workItemID int64, leaseID, updateTime string) error {
	current, err := One[struct {
		ID                int64  `db:"id"`
		CurrentWorkItemID *int64 `db:"current_work_item_id"`
	}](ctx, tx, `SELECT id, current_work_item_id FROM workers WHERE worker_id = ?`, workerID)
	if err != nil {
		return fmt.Errorf("store: bind work item %d to worker %s: %w", workItemID, workerID, err)
	}
	if current.CurrentWorkItemID != nil && *current.CurrentWorkItemID != workItemID {
		return fmt.Errorf("%w: worker %s is already bound to work item %d",
			ErrUniqueViolation, workerID, *current.CurrentWorkItemID)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_work_item_id = ?,
		       current_lease_id = ?,
		       state = 'busy',
		       update_time = ?
		 WHERE id = ?`, workItemID, leaseID, updateTime, current.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: work item %d is already owned by another worker", ErrUniqueViolation, workItemID)
		}
		return fmt.Errorf("store: bind work item %d to worker %s: %w", workItemID, workerID, err)
	}
	return nil
}

// ClearWorkItemByWorker releases the card held by one worker and marks it idle.
//
// It is idempotent: clearing an already idle worker matches the row again and
// succeeds. A missing worker is sql.ErrNoRows, because "release a worker that
// does not exist" is a caller bug, not a no-op.
func ClearWorkItemByWorker(ctx context.Context, tx *sql.Tx, workerID, updateTime string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_work_item_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle',
		       update_time = ?
		 WHERE worker_id = ?`, updateTime, workerID)
	if err != nil {
		return fmt.Errorf("store: clear work item of worker %s: %w", workerID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: clear work item of worker %s: rows affected: %w", workerID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: clear work item of worker %s: %w", workerID, sql.ErrNoRows)
	}
	return nil
}

// ClearWorkItemByWorkItemID releases whichever worker holds workItemID and
// returns how many rows changed.
//
// It is the reverse direction of ClearWorkItemByWorker, used by the report and
// reclaim paths that only know the card. Zero rows is a normal outcome — the
// card may have been claimed by a worker that never bound it — so it is not an
// error.
func ClearWorkItemByWorkItemID(ctx context.Context, tx *sql.Tx, workItemID int64, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_work_item_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle',
		       update_time = ?
		 WHERE current_work_item_id = ?`, updateTime, workItemID)
	if err != nil {
		return 0, fmt.Errorf("store: clear worker of work item %d: %w", workItemID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear worker of work item %d: rows affected: %w", workItemID, err)
	}
	return n, nil
}

// GetWorkerByWorkItemID returns the worker currently holding workItemID. A card
// nobody holds is sql.ErrNoRows.
func GetWorkerByWorkItemID(ctx context.Context, q Querier, workItemID int64) (Worker, error) {
	return One[Worker](ctx, q,
		`SELECT `+workerColumns+` FROM workers WHERE current_work_item_id = ?`, workItemID)
}

// ListStaleWorkers returns the workers that have not been seen since before the
// cut-off and hold no card, oldest first.
//
// before must be rendered with FormatWorkerTime. The boundary matches
// domain.Presence: a worker is offline once now-last_seen > ttl, i.e. once
// last_seen < now-ttl, so callers pass now-ttl unchanged. Rows that still hold
// a card are excluded even when stale: an unreachable worker's card belongs to
// the lease reclaimer first, and deleting the row would drop the only record of
// what it was doing.
func ListStaleWorkers(ctx context.Context, q Querier, before string) ([]Worker, error) {
	rows, err := All[Worker](ctx, q,
		`SELECT `+workerColumns+`
		   FROM workers
		  WHERE last_seen < ?
		    AND current_work_item_id IS NULL
		  ORDER BY last_seen ASC, id ASC`, before)
	if err != nil {
		return nil, fmt.Errorf("store: list stale workers before %s: %w", before, err)
	}
	return rows, nil
}

// DeleteStaleWorker removes one stale worker row by physical id.
//
// The current_work_item_id IS NULL predicate is repeated here as a second line
// of defence: even if a caller passes an id straight from ListStaleWorkers and
// the worker has claimed a card in between, the DELETE matches nothing and
// reports sql.ErrNoRows instead of destroying a live binding.
func DeleteStaleWorker(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM workers WHERE id = ? AND current_work_item_id IS NULL`, id)
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
// the cut-off and hold no card, and returns how many rows were removed.
//
// before must be rendered with FormatWorkerTime (see ListStaleWorkers, which
// applies the same boundary one row at a time). The bulk form exists for the
// 30s maintenance sweep: a registry of long-dead agents must not grow forever,
// while a stale row that still holds a card is kept because it is the only
// record of who was working on the card until the lease reclaimer moves it.
func PruneStaleWorkers(ctx context.Context, tx *sql.Tx, before string) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`DELETE FROM workers WHERE current_work_item_id IS NULL AND last_seen < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("store: prune stale workers before %s: %w", before, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune stale workers before %s: rows affected: %w", before, err)
	}
	return n, nil
}

// ClearDanglingPointers releases every worker whose pointer references an item
// that is no longer leased, and returns how many rows changed.
//
// It is the maintenance statement of tech design §5: a card that was completed,
// reclaimed or cancelled outside the worker's control must not leave the worker
// busy forever, or the registry would claim capacity that no longer exists. A
// pointer to a missing row is cleared too; the NOT EXISTS form covers both cases
// in one statement and stays correct if the foreign key is ever relaxed.
func ClearDanglingPointers(ctx context.Context, tx *sql.Tx, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE workers
		   SET current_work_item_id = NULL,
		       current_lease_id = NULL,
		       state = 'idle',
		       update_time = ?
		 WHERE current_work_item_id IS NOT NULL
		   AND NOT EXISTS (
		         SELECT 1 FROM work_items w
		          WHERE w.id = workers.current_work_item_id
		            AND w.state = 'leased')`, updateTime)
	if err != nil {
		return 0, fmt.Errorf("store: clear dangling worker pointers: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: clear dangling worker pointers: rows affected: %w", err)
	}
	return n, nil
}

// The state CHECK constraint accepts exactly 'idle' (no card held) and 'busy'
// (current_work_item_id set); the writers above use those two literals inline.
