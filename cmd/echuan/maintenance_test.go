package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// timeFormat is the fixed-width UTC layout the V3 layer stores timestamps in.
// It is duplicated here on purpose: the test seeds rows by hand, so it must not
// borrow the production formatter (a change there has to fail this test).
const timeFormat = "2006-01-02T15:04:05.000Z"

// staleStamp is far enough in the past to be older than any test retention.
const staleStamp = "2020-01-01T00:00:00.000Z"

// maintenanceStore opens and migrates a throwaway database.
func maintenanceStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// seedWorkerRow inserts one worker with an explicit last_seen.
func seedWorkerRow(t *testing.T, st *store.Store, workerID, lastSeen string) {
	t.Helper()
	ctx := context.Background()
	if err := storev3.UpsertWorker(ctx, st, workerID, "{}", lastSeen); err != nil {
		t.Fatalf("upsert worker %s: %v", workerID, err)
	}
	if err := storev3.SetWorkerKinds(ctx, st, workerID, []enum.TaskKind{enum.Dev}); err != nil {
		t.Fatalf("set kinds %s: %v", workerID, err)
	}
}

// seedProject inserts one product + project pair and returns the project row id.
func seedProject(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := store.CreateProduct(ctx, tx, store.NewProduct{
			ProductID: "APP", Name: "APP", GitURL: "https://example.com/app.git",
			CreateTime: staleStamp, UpdateTime: staleStamp,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}
	project, err := storev3.CreateProject(ctx, st, storev3.CreateProjectInput{
		ProductID: "APP", Name: "维护", Now: staleStamp,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project.ID
}

// seedLeasedTask inserts one in_progress dev task leased to workerID until
// leaseUntil and points the worker row at it — the state ClaimTask leaves.
func seedLeasedTask(t *testing.T, st *store.Store, projectPK int64, taskID, workerID, leaseUntil string) {
	t.Helper()
	ctx := context.Background()
	leaseID := "l_" + taskID
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		// kind_parallel = 1: dev is open_cards: multiple in the default flow, and
		// this seed puts several open dev cards in one project (0003 index).
		res, err := tx.ExecContext(ctx, `
			INSERT INTO tasks (task_id, project_id, kind, kind_parallel, title, status, attempt, max_attempts,
			                   lease_id, lease_until, worker_id, create_time, update_time)
			VALUES (?, ?, ?, 1, 'dev', ?, 1, 3, ?, ?, ?, ?, ?)`,
			taskID, projectPK, enum.Dev, enum.InProgress, leaseID, leaseUntil, workerID, staleStamp, staleStamp)
		if err != nil {
			return err
		}
		pk, err := res.LastInsertId()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE workers
			   SET state = ?, current_task_id = ?, current_lease_id = ?
			 WHERE worker_id = ?`, enum.WorkerBusy, pk, leaseID, workerID)
		return err
	})
	if err != nil {
		t.Fatalf("seed leased task %s: %v", taskID, err)
	}
}

// workerExists reports whether a worker row is still in the registry.
func workerExists(t *testing.T, st *store.Store, workerID string) bool {
	t.Helper()
	var count int
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM workers WHERE worker_id = ?`, workerID).Scan(&count); err != nil {
		t.Fatalf("count worker %s: %v", workerID, err)
	}
	return count > 0
}

// TestSweepOnceReclaimsAndPrunes is the wiring test for the maintenance loop: a
// single sweep must do both jobs of issue #163 — return an expired lease to the
// pool and delete a retired idle worker — while leaving alone both a worker that
// reported recently and a worker that still holds a live lease.
//
// It also pins the order documented on sweepOnce: the expired lease's own row
// (w_expired) is released and then pruned in the same pass, because the prune
// runs after the reclaim.
func TestSweepOnceReclaimsAndPrunes(t *testing.T) {
	st := maintenanceStore(t)
	ctx := context.Background()
	projectPK := seedProject(t, st)
	recent := time.Now().UTC().Format(timeFormat)

	seedWorkerRow(t, st, "w_stale", staleStamp)
	seedWorkerRow(t, st, "w_recent", recent)
	seedWorkerRow(t, st, "w_expired", staleStamp)
	seedLeasedTask(t, st, projectPK, "t_sweep_0001", "w_expired", staleStamp)
	seedWorkerRow(t, st, "w_held", staleStamp)
	seedLeasedTask(t, st, projectPK, "t_sweep_0002", "w_held", "2099-01-01T00:00:00.000Z")

	tasks := appv3.New(st)
	workers := appv3.NewWorkerService(st)
	var buf bytes.Buffer
	logger := platform.NewLogger("info", &buf)

	sweepOnce(ctx, tasks, workers, time.Hour, logger)

	if workerExists(t, st, "w_stale") {
		t.Error("w_stale (idle, last_seen 2020) survived the sweep, want deleted")
	}
	if !workerExists(t, st, "w_recent") {
		t.Error("w_recent (last_seen now) was deleted, want kept")
	}
	if !workerExists(t, st, "w_held") {
		t.Error("w_held (still holding a live lease) was deleted, want kept")
	}
	if workerExists(t, st, "w_expired") {
		t.Error("w_expired (lease reclaimed in this sweep, last_seen 2020) survived, want pruned after the reclaim")
	}

	// The expired lease must be back in the pool.
	var status enum.TaskStatus
	var leaseID sql.NullString
	if err := st.Read().QueryRowContext(ctx,
		`SELECT status, lease_id FROM tasks WHERE task_id = 't_sweep_0001'`).Scan(&status, &leaseID); err != nil {
		t.Fatalf("read reclaimed task: %v", err)
	}
	if status != enum.Ready {
		t.Errorf("reclaimed task status = %d, want ready (%d)", status, enum.Ready)
	}
	if leaseID.Valid {
		t.Errorf("reclaimed task lease_id = %q, want NULL", leaseID.String)
	}
	// The held task must be untouched.
	if err := st.Read().QueryRowContext(ctx,
		`SELECT status FROM tasks WHERE task_id = 't_sweep_0002'`).Scan(&status); err != nil {
		t.Fatalf("read held task: %v", err)
	}
	if status != enum.InProgress {
		t.Errorf("held task status = %d, want in_progress (%d)", status, enum.InProgress)
	}

	// Both jobs must be visible in the log of the same sweep.
	counts := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, line)
		}
		msg, _ := entry["msg"].(string)
		count, _ := entry["count"].(float64)
		counts[msg] = count
	}
	if got, ok := counts["reclaimed expired leases"]; !ok || got != 1 {
		t.Errorf("log count of %q = %v (present=%v), want 1; output = %s",
			"reclaimed expired leases", got, ok, buf.String())
	}
	// w_stale and w_expired: both idle and stale by the time the prune runs.
	if got, ok := counts["pruned stale workers"]; !ok || got != 2 {
		t.Errorf("log count of %q = %v (present=%v), want 2; output = %s",
			"pruned stale workers", got, ok, buf.String())
	}
}

// readWorker loads the registry row of one worker.
func readWorker(t *testing.T, st *store.Store, workerID string) storev3.Worker {
	t.Helper()
	worker, err := storev3.GetWorker(context.Background(), st.Read(), workerID)
	if err != nil {
		t.Fatalf("get worker %s: %v", workerID, err)
	}
	return worker
}

// assertHolderReleased pins the occupancy projection of one worker: idle, no
// task, no lease.
func assertHolderReleased(t *testing.T, st *store.Store, workerID string) {
	t.Helper()
	worker := readWorker(t, st, workerID)
	if worker.State != enum.WorkerIdle {
		t.Errorf("%s state = %s, want idle", workerID, worker.State)
	}
	if worker.CurrentTaskID != nil {
		t.Errorf("%s current_task_id = %d, want NULL", workerID, *worker.CurrentTaskID)
	}
	if worker.CurrentLeaseID != nil {
		t.Errorf("%s current_lease_id = %q, want NULL", workerID, *worker.CurrentLeaseID)
	}
}

// hardDeleteTaskRow removes an in_progress task row the way an operator's manual
// DELETE would: the V3 API refuses to delete a leased task, so the test suspends
// foreign keys for the statement (single write connection, so the PRAGMA lands on
// the connection that runs the DELETE).
func hardDeleteTaskRow(t *testing.T, st *store.Store, taskID string) {
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

// logCounts decodes the JSON log lines of one sweep into msg → count.
func logCounts(t *testing.T, buf *bytes.Buffer) map[string]float64 {
	t.Helper()
	counts := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, line)
		}
		msg, _ := entry["msg"].(string)
		count, _ := entry["count"].(float64)
		counts[msg] = count
	}
	return counts
}

// TestSweepOncePrunesCancelledTaskHolder covers AC1 + AC2 through the
// maintenance loop: cancelling a card releases its holder immediately, and the
// holder — idle and past retention — is gone after one sweep. Before issue #165
// §1 the pointer stayed set, so the prune skipped the row on every pass.
func TestSweepOncePrunesCancelledTaskHolder(t *testing.T) {
	st := maintenanceStore(t)
	ctx := context.Background()
	projectPK := seedProject(t, st)
	seedWorkerRow(t, st, "w_cancel", staleStamp)
	seedLeasedTask(t, st, projectPK, "t_cancel_0001", "w_cancel", staleStamp)

	tasks := appv3.New(st)
	workers := appv3.NewWorkerService(st)

	if err := tasks.Cancel(ctx, "t_cancel_0001", "cto"); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	// AC1: the cancel itself released the pointer, before any sweep.
	assertHolderReleased(t, st, "w_cancel")

	var buf bytes.Buffer
	sweepOnce(ctx, tasks, workers, time.Hour, platform.NewLogger("info", &buf))

	// AC2: the registry no longer lists the worker (GET /v1/workers reads this
	// table), so one pass after retention is enough.
	if workerExists(t, st, "w_cancel") {
		t.Error("w_cancel (card cancelled, last_seen 2020) survived one sweep, want pruned")
	}
	counts := logCounts(t, &buf)
	if got, ok := counts["pruned stale workers"]; !ok || got != 1 {
		t.Errorf("log count of %q = %v (present=%v), want 1; output = %s",
			"pruned stale workers", got, ok, buf.String())
	}
}

// TestSweepOnceReleasesDanglingPointer is AC5 through the maintenance loop: a
// task row hard-deleted under a live pointer is released by the sweep, and the
// released row then ages out in the very same pass.
func TestSweepOnceReleasesDanglingPointer(t *testing.T) {
	st := maintenanceStore(t)
	ctx := context.Background()
	projectPK := seedProject(t, st)
	seedWorkerRow(t, st, "w_orphan", staleStamp)
	// Lease far in the future: the reclaim step must not be what clears this
	// pointer, only the release step may.
	seedLeasedTask(t, st, projectPK, "t_orphan_0001", "w_orphan", "2099-01-01T00:00:00.000Z")
	hardDeleteTaskRow(t, st, "t_orphan_0001")

	tasks := appv3.New(st)
	workers := appv3.NewWorkerService(st)
	var buf bytes.Buffer
	sweepOnce(ctx, tasks, workers, time.Hour, platform.NewLogger("info", &buf))

	if workerExists(t, st, "w_orphan") {
		t.Error("w_orphan (dangling pointer, last_seen 2020) survived one sweep, want released then pruned")
	}
	counts := logCounts(t, &buf)
	if got, ok := counts["released stale worker pointers"]; !ok || got != 1 {
		t.Errorf("log count of %q = %v (present=%v), want 1; output = %s",
			"released stale worker pointers", got, ok, buf.String())
	}
	if got, ok := counts["pruned stale workers"]; !ok || got != 1 {
		t.Errorf("log count of %q = %v (present=%v), want 1; output = %s",
			"pruned stale workers", got, ok, buf.String())
	}
}
