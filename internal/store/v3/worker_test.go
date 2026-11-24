package v3

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// seedWorker registers one idle worker whose last_seen is the given timestamp
// and declares a single kind, so the kind list can be checked after the prune.
func seedWorker(t *testing.T, st *storedb.Store, workerID, lastSeen string) {
	t.Helper()
	ctx := context.Background()
	if err := UpsertWorker(ctx, st, workerID, "{}", lastSeen); err != nil {
		t.Fatalf("upsert worker %s: %v", workerID, err)
	}
	if err := SetWorkerKinds(ctx, st, workerID, []enum.TaskKind{enum.Dev}); err != nil {
		t.Fatalf("set kinds %s: %v", workerID, err)
	}
}

// countWorkers counts worker rows (and their kind rows) by business id.
func countWorkers(t *testing.T, st *storedb.Store, workerID string) (workers, kinds int) {
	t.Helper()
	ctx := context.Background()
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workers WHERE worker_id = ?`, workerID).Scan(&workers); err != nil {
		t.Fatalf("count worker %s: %v", workerID, err)
	}
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM worker_kinds WHERE worker_id = ?`, workerID).Scan(&kinds); err != nil {
		t.Fatalf("count kinds %s: %v", workerID, err)
	}
	return workers, kinds
}

// seedHeldTask inserts one in_progress dev task held by workerID, which is the
// state ClaimTask leaves behind (pointer + lease set).
func seedHeldTask(t *testing.T, st *storedb.Store, workerID string) {
	t.Helper()
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "保留期", Now: testNow})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (task_id, project_id, kind, title, status, attempt, max_attempts,
			                   lease_id, lease_until, worker_id, create_time, update_time)
			VALUES ('t_prune_0001', ?, ?, 'dev', ?, 1, 3, 'l_prune', ?, ?, ?, ?)`,
			project.ID, enum.Dev, enum.InProgress, testUntil, workerID, testNow, testNow)
		if err != nil {
			return err
		}
		pk, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE workers
			   SET state = ?, current_task_id = ?, current_lease_id = 'l_prune'
			 WHERE worker_id = ?`, enum.WorkerBusy, pk, workerID)
		return err
	})
	if err != nil {
		t.Fatalf("seed held task for %s: %v", workerID, err)
	}
}

// TestPruneStaleWorkersDeletesIdleRow covers the happy path: an idle row whose
// last_seen is older than the cutoff disappears together with its kind list.
func TestPruneStaleWorkersDeletesIdleRow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedWorker(t, st, "w_stale", "2026-11-17T22:00:00.000Z")

	deleted, err := PruneStaleWorkers(ctx, st, "2026-11-17T23:00:00.000Z")
	if err != nil {
		t.Fatalf("PruneStaleWorkers: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	workers, kinds := countWorkers(t, st, "w_stale")
	if workers != 0 {
		t.Errorf("worker rows = %d, want 0", workers)
	}
	if kinds != 0 {
		t.Errorf("worker_kinds rows = %d, want 0 (kind list must not outlive the row)", kinds)
	}
}

// TestPruneStaleWorkersKeepsRecentRow covers the other side of the cutoff: a
// worker that reported after it survives.
func TestPruneStaleWorkersKeepsRecentRow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedWorker(t, st, "w_recent", "2026-11-17T23:30:00.000Z")

	deleted, err := PruneStaleWorkers(ctx, st, "2026-11-17T23:00:00.000Z")
	if err != nil {
		t.Fatalf("PruneStaleWorkers: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	workers, kinds := countWorkers(t, st, "w_recent")
	if workers != 1 {
		t.Errorf("worker rows = %d, want 1", workers)
	}
	if kinds != 1 {
		t.Errorf("worker_kinds rows = %d, want 1", kinds)
	}
}

// TestPruneStaleWorkersKeepsRowAtCutoff pins the boundary: the cutoff is
// exclusive (last_seen < before), so a row exactly at the cutoff stays.
func TestPruneStaleWorkersKeepsRowAtCutoff(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	const cutoff = "2026-11-17T23:00:00.000Z"
	seedWorker(t, st, "w_boundary", cutoff)

	deleted, err := PruneStaleWorkers(ctx, st, cutoff)
	if err != nil {
		t.Fatalf("PruneStaleWorkers: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 for last_seen == before", deleted)
	}
	if workers, _ := countWorkers(t, st, "w_boundary"); workers != 1 {
		t.Errorf("worker rows = %d, want 1", workers)
	}
}

// TestPruneStaleWorkersKeepsRowHoldingTask guards the issue #163 rule that a
// node holding a card is never pruned, however old its last_seen is.
func TestPruneStaleWorkersKeepsRowHoldingTask(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedWorker(t, st, "w_busy", "2026-11-17T20:00:00.000Z")
	seedHeldTask(t, st, "w_busy")

	deleted, err := PruneStaleWorkers(ctx, st, "2026-11-17T23:00:00.000Z")
	if err != nil {
		t.Fatalf("PruneStaleWorkers: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 for a row holding a task", deleted)
	}
	if workers, _ := countWorkers(t, st, "w_busy"); workers != 1 {
		t.Errorf("worker rows = %d, want 1", workers)
	}
}
