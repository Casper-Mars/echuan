package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// workerTimestamp is a stable base instant for the worker tests; every value is
// produced through FormatWorkerTime so the fixed-width comparison is exercised
// end to end.
var workerTestBase = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

// insertWorkerTask inserts one tasks row with the given status and returns its
// physical id. projectID comes from seedProject.
func insertWorkerTask(t *testing.T, st *Store, projectID int64, seq int, taskID string, status int) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
			 VALUES (?, ?, ?, 't', 'b', ?, 'T', 'T')`,
			taskID, projectID, seq, status)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		t.Fatalf("insert task %s: %v", taskID, err)
	}
	return id
}

// upsertTestWorker registers (or refreshes) one worker and returns the stored
// row.
func upsertTestWorker(t *testing.T, st *Store, w WorkerUpsert) Worker {
	t.Helper()
	ctx := context.Background()
	var row Worker
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		row, err = UpsertWorker(ctx, tx, w)
		return err
	}); err != nil {
		t.Fatalf("UpsertWorker(%s): %v", w.WorkerID, err)
	}
	return row
}

// countWorkers returns the number of workers rows.
func countWorkers(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM workers`).Scan(&n); err != nil {
		t.Fatalf("count workers: %v", err)
	}
	return n
}

// TestUpsertWorkerIsIdempotent pins the registration contract: a second upsert
// for the same worker_id updates the mutable columns instead of adding a row,
// and first_seen as well as create_time keep their original values.
func TestUpsertWorkerIsIdempotent(t *testing.T) {
	st := openTestStore(t, 2)

	firstSeen := FormatWorkerTime(workerTestBase)
	first := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID:     "w_01JA0000000000000000000001",
		MetadataJSON: `{"host":"mbp"}`,
		State:        "idle",
		LastSeen:     firstSeen,
		UpdateTime:   "2026-10-27T12:00:00.000000001Z",
	})
	if first.FirstSeen != firstSeen || first.LastSeen != firstSeen {
		t.Fatalf("first upsert first_seen/last_seen = %q/%q, want %q", first.FirstSeen, first.LastSeen, firstSeen)
	}

	laterSeen := FormatWorkerTime(workerTestBase.Add(30 * time.Second))
	second := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID:     "w_01JA0000000000000000000001",
		MetadataJSON: `{"host":"mbp","cpu":8}`,
		State:        "busy",
		LastSeen:     laterSeen,
		UpdateTime:   "2026-10-27T12:00:30.000000001Z",
	})

	if n := countWorkers(t, st); n != 1 {
		t.Errorf("workers rows = %d, want 1 after two upserts of one id", n)
	}
	if second.ID != first.ID {
		t.Errorf("row id = %d, want %d (same row)", second.ID, first.ID)
	}
	if second.FirstSeen != firstSeen {
		t.Errorf("first_seen = %q, want unchanged %q", second.FirstSeen, firstSeen)
	}
	if second.CreateTime != first.CreateTime {
		t.Errorf("create_time = %q, want unchanged %q", second.CreateTime, first.CreateTime)
	}
	if second.LastSeen != laterSeen {
		t.Errorf("last_seen = %q, want %q", second.LastSeen, laterSeen)
	}
	if second.MetadataJSON != `{"host":"mbp","cpu":8}` {
		t.Errorf("metadata_json = %q, want the refreshed document", second.MetadataJSON)
	}
	if second.State != "busy" {
		t.Errorf("state = %q, want busy", second.State)
	}
	if second.UpdateTime != "2026-10-27T12:00:30.000000001Z" {
		t.Errorf("update_time = %q, want the refreshed timestamp", second.UpdateTime)
	}
}

// TestUpsertWorkerEmptyMetadataKeepsDocument pins the heartbeat rule: an upsert
// without a metadata document leaves the registered one in place, while the very
// first insert still stores the column default '{}'.
func TestUpsertWorkerEmptyMetadataKeepsDocument(t *testing.T) {
	st := openTestStore(t, 1)

	first := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA0000000000000000000004", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})
	if first.MetadataJSON != "{}" {
		t.Errorf("metadata_json = %q, want the column default {}", first.MetadataJSON)
	}

	upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA0000000000000000000004", MetadataJSON: `{"host":"mbp"}`, State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase.Add(time.Second)), UpdateTime: "T",
	})
	after := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA0000000000000000000004", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase.Add(2 * time.Second)), UpdateTime: "T",
	})
	if after.MetadataJSON != `{"host":"mbp"}` {
		t.Errorf("metadata_json = %q, want the registered document to survive an empty update", after.MetadataJSON)
	}
}

// TestUpsertWorkerKeepsBinding checks that a heartbeat never disturbs the task
// pointer: refreshing a busy worker leaves current_task_id/current_lease_id
// alone.
func TestUpsertWorkerKeepsBinding(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)
	taskID := insertWorkerTask(t, st, projectID, 1, "APP-1-T0001", 2)

	seen := FormatWorkerTime(workerTestBase)
	worker := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA0000000000000000000002", State: "idle",
		LastSeen: seen, UpdateTime: "2026-10-27T12:00:00Z",
	})
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return BindTask(ctx, tx, worker.WorkerID, taskID, "lease-1", "2026-10-27T12:00:01Z")
	}); err != nil {
		t.Fatalf("BindTask: %v", err)
	}

	heartbeat := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: worker.WorkerID, State: "busy",
		LastSeen:   FormatWorkerTime(workerTestBase.Add(15 * time.Second)),
		UpdateTime: "2026-10-27T12:00:15Z",
	})
	if heartbeat.CurrentTaskID == nil || *heartbeat.CurrentTaskID != taskID {
		t.Fatalf("current_task_id = %v, want %d after heartbeat", heartbeat.CurrentTaskID, taskID)
	}
	if heartbeat.CurrentLeaseID == nil || *heartbeat.CurrentLeaseID != "lease-1" {
		t.Fatalf("current_lease_id = %v, want lease-1 after heartbeat", heartbeat.CurrentLeaseID)
	}
}

// TestBindTaskAndClear covers the happy path plus both idempotency
// requirements: binding the same pair twice succeeds, and clearing twice does
// not fail.
func TestBindTaskAndClear(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)
	taskID := insertWorkerTask(t, st, projectID, 1, "APP-1-T0001", 2)

	worker := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA0000000000000000000003", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "2026-10-27T12:00:00Z",
	})

	bind := func(leaseID string) error {
		return st.Tx(ctx, func(tx *sql.Tx) error {
			return BindTask(ctx, tx, worker.WorkerID, taskID, leaseID, "2026-10-27T12:00:05Z")
		})
	}
	if err := bind("lease-a"); err != nil {
		t.Fatalf("BindTask: %v", err)
	}

	busy, err := GetWorker(ctx, st.Read(), worker.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if busy.CurrentTaskID == nil || *busy.CurrentTaskID != taskID {
		t.Errorf("current_task_id = %v, want %d", busy.CurrentTaskID, taskID)
	}
	if busy.CurrentLeaseID == nil || *busy.CurrentLeaseID != "lease-a" {
		t.Errorf("current_lease_id = %v, want lease-a", busy.CurrentLeaseID)
	}
	if busy.State != "busy" {
		t.Errorf("state = %q, want busy", busy.State)
	}

	// Re-binding the same pair is a retry, not a conflict.
	if err := bind("lease-b"); err != nil {
		t.Fatalf("re-BindTask same task: %v", err)
	}
	rebound, err := GetWorker(ctx, st.Read(), worker.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker after rebind: %v", err)
	}
	if rebound.CurrentLeaseID == nil || *rebound.CurrentLeaseID != "lease-b" {
		t.Errorf("current_lease_id = %v, want lease-b after rebind", rebound.CurrentLeaseID)
	}

	for i := 0; i < 2; i++ {
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			return ClearTaskByWorker(ctx, tx, worker.WorkerID, "2026-10-27T12:00:10Z")
		}); err != nil {
			t.Fatalf("ClearTaskByWorker call %d: %v", i+1, err)
		}
	}

	idle, err := GetWorker(ctx, st.Read(), worker.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker after clear: %v", err)
	}
	if idle.CurrentTaskID != nil || idle.CurrentLeaseID != nil {
		t.Errorf("pointers = %v/%v, want NULL after clear", idle.CurrentTaskID, idle.CurrentLeaseID)
	}
	if idle.State != "idle" {
		t.Errorf("state = %q, want idle", idle.State)
	}
}

// TestClearTaskByWorkerMissingWorker pins the "targeted write" contract: the
// by-worker release reports a missing row, unlike the by-task sweep.
func TestClearTaskByWorkerMissingWorker(t *testing.T) {
	st := openTestStore(t, 1)

	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		return ClearTaskByWorker(context.Background(), tx, "w_missing", "T")
	})
	if !IsNotFound(err) {
		t.Fatalf("ClearTaskByWorker(missing) = %v, want sql.ErrNoRows", err)
	}
}

// TestBindTaskConflicts covers both sides of the one-task-per-worker rule. The
// task side is enforced by the partial UNIQUE index; the worker side has no
// index and must be rejected by BindTask itself. Both must be recognisable as
// ErrUniqueViolation so the HTTP layer renders one worker_busy code.
func TestBindTaskConflicts(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)
	taskOne := insertWorkerTask(t, st, projectID, 1, "APP-1-T0001", 2)
	taskTwo := insertWorkerTask(t, st, projectID, 2, "APP-1-T0002", 2)

	workerA := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000000A", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})
	workerB := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000000B", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})

	bind := func(workerID string, taskID int64) error {
		return st.Tx(ctx, func(tx *sql.Tx) error {
			return BindTask(ctx, tx, workerID, taskID, "lease", "2026-10-27T12:00:05Z")
		})
	}

	if err := bind(workerA.WorkerID, taskOne); err != nil {
		t.Fatalf("first bind: %v", err)
	}

	// Task side: the unique index rejects a second owner of taskOne.
	err := bind(workerB.WorkerID, taskOne)
	if !errors.Is(err, ErrUniqueViolation) {
		t.Errorf("second worker on the same task = %v, want ErrUniqueViolation", err)
	}

	// Worker side: no index guards it, so BindTask must reject it explicitly.
	err = bind(workerA.WorkerID, taskTwo)
	if !errors.Is(err, ErrUniqueViolation) {
		t.Errorf("busy worker taking another task = %v, want ErrUniqueViolation", err)
	}

	// Neither failure may disturb the existing binding.
	still, err := GetWorker(ctx, st.Read(), workerA.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker(A): %v", err)
	}
	if still.CurrentTaskID == nil || *still.CurrentTaskID != taskOne {
		t.Errorf("worker A task = %v, want %d to survive the conflicts", still.CurrentTaskID, taskOne)
	}
	idleB, err := GetWorker(ctx, st.Read(), workerB.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker(B): %v", err)
	}
	if idleB.CurrentTaskID != nil {
		t.Errorf("worker B task = %v, want NULL after the rejected bind", idleB.CurrentTaskID)
	}
}

// TestGetWorkerByTaskID checks both lookup directions and the release path.
func TestGetWorkerByTaskID(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)
	taskID := insertWorkerTask(t, st, projectID, 1, "APP-1-T0001", 2)

	worker := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000000C", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})
	if _, err := GetWorkerByTaskID(ctx, st.Read(), taskID); !IsNotFound(err) {
		t.Fatalf("GetWorkerByTaskID before bind = %v, want not found", err)
	}

	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return BindTask(ctx, tx, worker.WorkerID, taskID, "lease-c", "T")
	}); err != nil {
		t.Fatalf("BindTask: %v", err)
	}

	byTask, err := GetWorkerByTaskID(ctx, st.Read(), taskID)
	if err != nil {
		t.Fatalf("GetWorkerByTaskID: %v", err)
	}
	if byTask.WorkerID != worker.WorkerID {
		t.Errorf("worker of task = %q, want %q", byTask.WorkerID, worker.WorkerID)
	}
	byID, err := GetWorker(ctx, st.Read(), worker.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if byID.ID != byTask.ID {
		t.Errorf("forward/reverse lookups disagree: %d vs %d", byID.ID, byTask.ID)
	}

	n, err := func() (int64, error) {
		var changed int64
		err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			changed, err = ClearTaskByTaskID(ctx, tx, taskID, "T")
			return err
		})
		return changed, err
	}()
	if err != nil {
		t.Fatalf("ClearTaskByTaskID: %v", err)
	}
	if n != 1 {
		t.Errorf("ClearTaskByTaskID changed %d rows, want 1", n)
	}

	if _, err := GetWorkerByTaskID(ctx, st.Read(), taskID); !IsNotFound(err) {
		t.Errorf("GetWorkerByTaskID after clear = %v, want not found", err)
	}
	// Clearing again is a no-op, not an error: the sweep may run twice.
	if n, err := func() (int64, error) {
		var changed int64
		err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			changed, err = ClearTaskByTaskID(ctx, tx, taskID, "T")
			return err
		})
		return changed, err
	}(); err != nil || n != 0 {
		t.Errorf("second ClearTaskByTaskID = %d, %v; want 0, nil", n, err)
	}
}

// TestListWorkersOrderAndPagination pins the last_seen DESC, id DESC order and
// the keyset pagination: three pages of limit 2 over five workers must cover
// every row exactly once.
func TestListWorkersOrderAndPagination(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()

	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		workerID := "w_01JA000000000000000000010" + string(rune('0'+i))
		ids = append(ids, workerID)
		upsertTestWorker(t, st, WorkerUpsert{
			WorkerID:   workerID,
			State:      "idle",
			LastSeen:   FormatWorkerTime(workerTestBase.Add(time.Duration(i) * time.Second)),
			UpdateTime: "T",
		})
	}
	// Newest first: the last inserted worker has the greatest last_seen.
	want := []string{ids[4], ids[3], ids[2], ids[1], ids[0]}

	all, err := ListWorkers(ctx, st.Read(), 0, 10)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("workers = %d, want 5", len(all))
	}
	for i, row := range all {
		if row.WorkerID != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, row.WorkerID, want[i])
		}
	}

	var (
		after  int64
		paged  []string
		pageNo int
	)
	for {
		page, err := ListWorkers(ctx, st.Read(), after, 2)
		if err != nil {
			t.Fatalf("ListWorkers page %d: %v", pageNo+1, err)
		}
		pageNo++
		if len(page) == 0 {
			break
		}
		for _, row := range page {
			paged = append(paged, row.WorkerID)
		}
		after = page[len(page)-1].ID
		if pageNo > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pageNo != 4 { // 2 + 2 + 1 + empty
		t.Errorf("pages read = %d, want 3 data pages plus an empty one", pageNo)
	}
	if len(paged) != len(want) {
		t.Fatalf("paged %d workers, want %d: %v", len(paged), len(want), paged)
	}
	for i := range want {
		if paged[i] != want[i] {
			t.Errorf("paged[%d] = %q, want %q (no duplicates, no gaps)", i, paged[i], want[i])
		}
	}
}

// TestListWorkersTieBreakByID covers the second sort key: equal last_seen is
// ordered by id DESC, which is what makes the keyset cursor unambiguous.
func TestListWorkersTieBreakByID(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()

	seen := FormatWorkerTime(workerTestBase)
	var rowIDs []int64
	for i := 0; i < 3; i++ {
		row := upsertTestWorker(t, st, WorkerUpsert{
			WorkerID:   "w_01JA000000000000000000020" + string(rune('0'+i)),
			State:      "idle",
			LastSeen:   seen,
			UpdateTime: "T",
		})
		rowIDs = append(rowIDs, row.ID)
	}

	page, err := ListWorkers(ctx, st.Read(), 0, 10)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("workers = %d, want 3", len(page))
	}
	for i, row := range page {
		wantID := rowIDs[len(rowIDs)-1-i]
		if row.ID != wantID {
			t.Errorf("order[%d] id = %d, want %d (id DESC on equal last_seen)", i, row.ID, wantID)
		}
	}
}

// TestListStaleWorkers checks the sweep selection (offline and unbound) and
// that DeleteStaleWorker refuses to drop a row that has just taken a task.
func TestListStaleWorkers(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)
	taskID := insertWorkerTask(t, st, projectID, 1, "APP-1-T0001", 2)

	ttl := 45 * time.Second
	before := FormatWorkerTime(workerTestBase.Add(-ttl))

	staleIdle := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000030", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase.Add(-2 * time.Minute)), UpdateTime: "T",
	})
	staleBusy := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000031", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase.Add(-2 * time.Minute)), UpdateTime: "T",
	})
	freshIdle := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000032", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return BindTask(ctx, tx, staleBusy.WorkerID, taskID, "lease", "T")
	}); err != nil {
		t.Fatalf("BindTask: %v", err)
	}

	stale, err := ListStaleWorkers(ctx, st.Read(), before)
	if err != nil {
		t.Fatalf("ListStaleWorkers: %v", err)
	}
	if len(stale) != 1 || stale[0].WorkerID != staleIdle.WorkerID {
		t.Fatalf("stale workers = %+v, want only %s", stale, staleIdle.WorkerID)
	}

	// A stale row that still holds a task must survive the delete.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return DeleteStaleWorker(ctx, tx, staleBusy.ID)
	})
	if !IsNotFound(err) {
		t.Errorf("DeleteStaleWorker(busy) = %v, want sql.ErrNoRows", err)
	}
	if _, err := GetWorker(ctx, st.Read(), staleBusy.WorkerID); err != nil {
		t.Fatalf("busy worker disappeared: %v", err)
	}

	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return DeleteStaleWorker(ctx, tx, staleIdle.ID)
	}); err != nil {
		t.Fatalf("DeleteStaleWorker(idle): %v", err)
	}
	if _, err := GetWorker(ctx, st.Read(), staleIdle.WorkerID); !IsNotFound(err) {
		t.Errorf("stale idle worker still present: %v", err)
	}
	if _, err := GetWorker(ctx, st.Read(), freshIdle.WorkerID); err != nil {
		t.Errorf("fresh worker disappeared: %v", err)
	}
}

// TestClearDanglingPointers covers the §5 maintenance statement: pointers to
// tasks that are no longer in progress are cleared, pointers to a live claim
// are preserved, and a second sweep changes nothing.
func TestClearDanglingPointers(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)

	liveTask := insertWorkerTask(t, st, projectID, 1, "APP-1-T0001", 2)   // in_progress
	doneTask := insertWorkerTask(t, st, projectID, 2, "APP-1-T0002", 4)   // done
	cancelTask := insertWorkerTask(t, st, projectID, 3, "APP-1-T0003", 7) // cancelled

	live := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000040", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})
	done := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000041", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})
	cancelled := upsertTestWorker(t, st, WorkerUpsert{
		WorkerID: "w_01JA000000000000000000042", State: "idle",
		LastSeen: FormatWorkerTime(workerTestBase), UpdateTime: "T",
	})

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		for _, pair := range []struct {
			worker string
			taskID int64
		}{
			{live.WorkerID, liveTask},
			{done.WorkerID, doneTask},
			{cancelled.WorkerID, cancelTask},
		} {
			if err := BindTask(ctx, tx, pair.worker, pair.taskID, "lease-"+pair.worker, "T"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("BindTask(batch): %v", err)
	}

	var cleared int64
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		cleared, err = ClearDanglingPointers(ctx, tx, "2026-10-27T12:00:30Z")
		return err
	}); err != nil {
		t.Fatalf("ClearDanglingPointers: %v", err)
	}
	if cleared != 2 {
		t.Errorf("cleared = %d, want 2 (done + cancelled)", cleared)
	}

	liveRow, err := GetWorker(ctx, st.Read(), live.WorkerID)
	if err != nil {
		t.Fatalf("GetWorker(live): %v", err)
	}
	if liveRow.CurrentTaskID == nil || *liveRow.CurrentTaskID != liveTask || liveRow.State != "busy" {
		t.Errorf("live worker = %v/%s, want task %d and busy", liveRow.CurrentTaskID, liveRow.State, liveTask)
	}

	for _, workerID := range []string{done.WorkerID, cancelled.WorkerID} {
		row, err := GetWorker(ctx, st.Read(), workerID)
		if err != nil {
			t.Fatalf("GetWorker(%s): %v", workerID, err)
		}
		if row.CurrentTaskID != nil || row.CurrentLeaseID != nil || row.State != "idle" {
			t.Errorf("worker %s = %v/%v/%s, want NULL/NULL/idle",
				workerID, row.CurrentTaskID, row.CurrentLeaseID, row.State)
		}
	}

	// Idempotent: nothing dangles any more.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		cleared, err = ClearDanglingPointers(ctx, tx, "2026-10-27T12:00:31Z")
		return err
	}); err != nil {
		t.Fatalf("second ClearDanglingPointers: %v", err)
	}
	if cleared != 0 {
		t.Errorf("second sweep cleared %d rows, want 0", cleared)
	}
}
