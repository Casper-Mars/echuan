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

// hardDeleteTask removes a task row behind the API's back: the V3 delete path
// only accepts pending/ready tasks, so removing a row that carried a lease is
// what an operator's manual DELETE does — foreign keys suspended for the
// statement. The write pool is a single connection, so the PRAGMA reaches the
// same connection as the DELETE.
func hardDeleteTask(t *testing.T, st *storedb.Store, taskID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Write().ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `DELETE FROM tasks WHERE task_id = ?`, taskID); err != nil {
		t.Fatalf("delete task %s: %v", taskID, err)
	}
	if _, err := st.Write().ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}
}

// TestClearStaleWorkerPointersClearsDanglingTaskRow is AC5 at the store layer: a
// worker pointing at a task row that no longer exists is released, which is the
// only way its registry row can ever age out — PruneStaleWorkers keeps every row
// that still carries a pointer.
func TestClearStaleWorkerPointersClearsDanglingTaskRow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	const lastSeen = "2026-11-17T20:00:00.000Z"
	seedWorker(t, st, "w_orphan", lastSeen)
	seedHeldTask(t, st, "w_orphan")
	hardDeleteTask(t, st, "t_prune_0001")

	released, err := ClearStaleWorkerPointers(ctx, st, "2026-11-18T00:00:00.000Z")
	if err != nil {
		t.Fatalf("ClearStaleWorkerPointers: %v", err)
	}
	if released != 1 {
		t.Errorf("released = %d, want 1", released)
	}
	worker, err := GetWorker(ctx, st.Read(), "w_orphan")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if worker.State != enum.WorkerIdle {
		t.Errorf("state = %s, want idle", worker.State)
	}
	if worker.CurrentTaskID != nil {
		t.Errorf("current_task_id = %d, want NULL", *worker.CurrentTaskID)
	}
	if worker.CurrentLeaseID != nil {
		t.Errorf("current_lease_id = %q, want NULL", *worker.CurrentLeaseID)
	}
	// Releasing is not reporting: last_seen must not be refreshed, or a dead
	// node would keep looking active and its retention would slip.
	if worker.LastSeen != lastSeen {
		t.Errorf("last_seen = %q, want unchanged %q", worker.LastSeen, lastSeen)
	}

	// With the pointer gone the same row ages out normally.
	deleted, err := PruneStaleWorkers(ctx, st, "2026-11-17T23:00:00.000Z")
	if err != nil {
		t.Fatalf("PruneStaleWorkers: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 once the dangling pointer is released", deleted)
	}
	if workers, kinds := countWorkers(t, st, "w_orphan"); workers != 0 || kinds != 0 {
		t.Errorf("workers=%d kinds=%d, want 0/0", workers, kinds)
	}
}

// TestClearStaleWorkerPointersClearsTerminalTaskPointer covers the rows written
// before cancel/fail released their holder: the task row still exists but left
// in_progress, so the pointer is stale occupancy all the same.
func TestClearStaleWorkerPointersClearsTerminalTaskPointer(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedWorker(t, st, "w_legacy", "2026-11-17T20:00:00.000Z")
	seedHeldTask(t, st, "w_legacy")
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE tasks SET status = ? WHERE task_id = 't_prune_0001'`, enum.Cancelled); err != nil {
		t.Fatalf("force cancelled status: %v", err)
	}

	released, err := ClearStaleWorkerPointers(ctx, st, "2026-11-18T00:00:00.000Z")
	if err != nil {
		t.Fatalf("ClearStaleWorkerPointers: %v", err)
	}
	if released != 1 {
		t.Errorf("released = %d, want 1", released)
	}
	worker, err := GetWorker(ctx, st.Read(), "w_legacy")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if worker.CurrentTaskID != nil || worker.CurrentLeaseID != nil || worker.State != enum.WorkerIdle {
		t.Errorf("worker = %+v, want idle with no pointer", worker)
	}
}

// TestClearStaleWorkerPointersKeepsLiveTaskRow is AC3: a worker that still holds
// an in_progress card is never released, however stale its last_seen is — that
// pointer is the only record of the occupancy.
func TestClearStaleWorkerPointersKeepsLiveTaskRow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedWorker(t, st, "w_busy", "2026-11-17T20:00:00.000Z")
	seedHeldTask(t, st, "w_busy")

	released, err := ClearStaleWorkerPointers(ctx, st, "2026-11-18T00:00:00.000Z")
	if err != nil {
		t.Fatalf("ClearStaleWorkerPointers: %v", err)
	}
	if released != 0 {
		t.Errorf("released = %d, want 0 for a worker holding an in_progress task", released)
	}
	worker, err := GetWorker(ctx, st.Read(), "w_busy")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if worker.State != enum.WorkerBusy || worker.CurrentTaskID == nil || worker.CurrentLeaseID == nil {
		t.Errorf("worker = %+v, want busy with pointer intact", worker)
	}

	deleted, err := PruneStaleWorkers(ctx, st, "2026-11-17T23:00:00.000Z")
	if err != nil {
		t.Fatalf("PruneStaleWorkers: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 while the card is still in_progress", deleted)
	}
}
