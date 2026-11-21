package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

const workerColumns = `id, worker_id, metadata_json, state, current_task_id, current_lease_id,
	first_seen, last_seen, create_time, update_time`

// UpsertWorker registers a worker or refreshes its metadata and last_seen.
// State, current task and lease are owned by the claim/report path and are not
// touched here.
func UpsertWorker(ctx context.Context, s *storedb.Store, workerID, metadataJSON, now string) error {
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := s.Write().ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, first_seen, last_seen, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(worker_id) DO UPDATE SET
			metadata_json = excluded.metadata_json,
			last_seen = excluded.last_seen,
			update_time = excluded.update_time`,
		workerID, metadataJSON, enum.WorkerIdle, now, now, now, now)
	if err != nil {
		return fmt.Errorf("v3: upsert worker %s: %w", workerID, err)
	}
	return nil
}

// Heartbeat refreshes last_seen without touching occupancy.
func Heartbeat(ctx context.Context, s *storedb.Store, workerID, now string) error {
	res, err := s.Write().ExecContext(ctx,
		`UPDATE workers SET last_seen = ?, update_time = ? WHERE worker_id = ?`, now, now, workerID)
	if err != nil {
		return fmt.Errorf("v3: heartbeat %s: %w", workerID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("v3: heartbeat rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("v3: worker %s not registered", workerID)
	}
	return nil
}

// GetWorker loads one worker by business id.
func GetWorker(ctx context.Context, q storedb.Querier, workerID string) (Worker, error) {
	worker, err := storedb.One[Worker](ctx, q,
		`SELECT `+workerColumns+` FROM workers WHERE worker_id = ?`, workerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Worker{}, fmt.Errorf("v3: worker %s not found", workerID)
	}
	if err != nil {
		return Worker{}, fmt.Errorf("v3: get worker %s: %w", workerID, err)
	}
	return worker, nil
}

// SetWorkerKinds replaces a worker's declared capabilities in one transaction.
// Duplicate kinds are ignored; an empty list leaves the worker with no kinds.
func SetWorkerKinds(ctx context.Context, s *storedb.Store, workerID string, kinds []enum.TaskKind) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM worker_kinds WHERE worker_id = ?`, workerID); err != nil {
			return fmt.Errorf("v3: clear worker kinds %s: %w", workerID, err)
		}
		seen := make(map[enum.TaskKind]bool, len(kinds))
		for _, kind := range kinds {
			if !kind.Valid() {
				return fmt.Errorf("v3: invalid kind %d", kind)
			}
			if seen[kind] {
				continue
			}
			seen[kind] = true
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO worker_kinds (worker_id, kind) VALUES (?, ?)`, workerID, kind); err != nil {
				return fmt.Errorf("v3: insert worker kind %s/%s: %w", workerID, kind, err)
			}
		}
		return nil
	})
}

// ListWorkerKinds returns a worker's declared kinds in numeric order.
func ListWorkerKinds(ctx context.Context, q storedb.Querier, workerID string) ([]enum.TaskKind, error) {
	rows, err := storedb.All[struct {
		Kind enum.TaskKind `db:"kind"`
	}](ctx, q, `SELECT kind FROM worker_kinds WHERE worker_id = ? ORDER BY kind ASC`, workerID)
	if err != nil {
		return nil, fmt.Errorf("v3: list worker kinds %s: %w", workerID, err)
	}
	out := make([]enum.TaskKind, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Kind)
	}
	return out, nil
}

// workerColumnsAliased is workerColumns qualified with the `w` alias, for the
// queries that JOIN other tables (bare names would be ambiguous with tasks).
const workerColumnsAliased = `w.id, w.worker_id, w.metadata_json, w.state, w.current_task_id,
	w.current_lease_id, w.first_seen, w.last_seen, w.create_time, w.update_time`

// ListWorkers lists workers ordered by last_seen descending.
//
// The held task's business id (`t_...`) is resolved by the same statement
// (LEFT JOIN tasks → Worker.CurrentTaskCode), so one listing costs one query no
// matter how many workers it returns — resolving it per worker was an N+1
// (issue #89 C1/E4 review). A dangling pointer (task row gone) yields NULL,
// i.e. the same "no work item" the per-worker lookup used to return, and never
// fails the whole listing.
func ListWorkers(ctx context.Context, q storedb.Querier, limit int) ([]Worker, error) {
	if limit <= 0 {
		limit = 100
	}
	return storedb.All[Worker](ctx, q, `
		SELECT `+workerColumnsAliased+`, t.task_id AS current_task_code
		  FROM workers w
		  LEFT JOIN tasks t ON t.id = w.current_task_id
		 ORDER BY w.last_seen DESC, w.id ASC
		 LIMIT ?`, limit)
}
