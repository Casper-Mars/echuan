package store

import (
	"context"
	"database/sql"
	"testing"
)

// TestTransitionTaskGuards pins the conditional UPDATE behind every task state
// change: a stale version or a different current status matches nothing.
func TestTransitionTaskGuards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 1},
	})

	transition := func(expectStatus, expectVersion, next int) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = TransitionTask(ctx, tx, ids["APP-1-T0001"],
				expectStatus, expectVersion, next, nil, "worker", leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("TransitionTask: %v", err)
		}
		return affected
	}

	if got := transition(1, 2, 2); got != 0 {
		t.Errorf("stale version affected = %d, want 0", got)
	}
	if got := transition(0, 1, 2); got != 0 {
		t.Errorf("wrong status affected = %d, want 0", got)
	}
	if got := transition(1, 1, 2); got != 1 {
		t.Errorf("valid transition affected = %d, want 1", got)
	}

	var (
		status  int
		version int
		actor   *string
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT status, version, last_actor FROM tasks WHERE task_id = 'APP-1-T0001'`).
		Scan(&status, &version, &actor); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != 2 || version != 2 {
		t.Errorf("status/version = %d/%d, want 2/2", status, version)
	}
	if actor == nil || *actor != "worker" {
		t.Errorf("last_actor = %v, want worker", actor)
	}
}

// TestTransitionTaskDecisionResetsAttempts checks the retry branch: only a
// retry verdict clears attempt_count, the other verdicts leave it alone.
func TestTransitionTaskDecisionResetsAttempts(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 3, attempts: 2},
		{taskID: "APP-1-T0002", seq: 2, status: 5, attempts: 3},
	})

	reason := "缺少回归测试"
	apply := func(w TaskDecisionWrite) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = TransitionTaskDecision(ctx, tx, w)
			return err
		}); err != nil {
			t.Fatalf("TransitionTaskDecision: %v", err)
		}
		return affected
	}

	if got := apply(TaskDecisionWrite{
		ID: ids["APP-1-T0001"], ExpectStatus: 3, ExpectVersion: 1,
		NextStatus: 4, Reason: &reason, LastActor: "reviewer", UpdateTime: leaseTestTime,
	}); got != 1 {
		t.Fatalf("accept affected = %d, want 1", got)
	}
	if got := apply(TaskDecisionWrite{
		ID: ids["APP-1-T0002"], ExpectStatus: 5, ExpectVersion: 1,
		NextStatus: 1, ResetAttempts: true, LastActor: "reviewer", UpdateTime: leaseTestTime,
	}); got != 1 {
		t.Fatalf("retry affected = %d, want 1", got)
	}
	if got := apply(TaskDecisionWrite{
		ID: ids["APP-1-T0002"], ExpectStatus: 5, ExpectVersion: 1,
		NextStatus: 7, LastActor: "reviewer", UpdateTime: leaseTestTime,
	}); got != 0 {
		t.Errorf("stale decision affected = %d, want 0", got)
	}

	var acceptedAttempt, retriedAttempt int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT attempt_count FROM tasks WHERE task_id = 'APP-1-T0001'`).Scan(&acceptedAttempt); err != nil {
		t.Fatalf("read accepted attempt: %v", err)
	}
	if err := st.Read().QueryRowContext(ctx,
		`SELECT attempt_count FROM tasks WHERE task_id = 'APP-1-T0002'`).Scan(&retriedAttempt); err != nil {
		t.Fatalf("read retried attempt: %v", err)
	}
	if acceptedAttempt != 2 {
		t.Errorf("accept attempt_count = %d, want 2 (unchanged)", acceptedAttempt)
	}
	if retriedAttempt != 0 {
		t.Errorf("retry attempt_count = %d, want 0 (reset)", retriedAttempt)
	}

	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Error("boolInt must render 1/0")
	}
}

// TestCompleteTaskReportGuards covers the report write predicate: right lease
// and live deadline move the card, everything else is a no-op (the caller turns
// 0 rows into lease_lost).
func TestCompleteTaskReportGuards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 2, attempts: 1, leaseID: "l_live", leaseTill: leaseFuture},
		{taskID: "APP-1-T0002", seq: 2, status: 2, attempts: 1, leaseID: "l_other", leaseTill: leaseFuture},
		{taskID: "APP-1-T0003", seq: 3, status: 2, attempts: 1, leaseID: "l_old", leaseTill: leasePast},
	})
	materialID := insertMaterial(t, st, "m_report_1", 3, 1, nil)

	complete := func(id int64, leaseID string) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = CompleteTaskReport(ctx, tx, id, materialID,
				leaseID, leaseNow, "worker", leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("CompleteTaskReport: %v", err)
		}
		return affected
	}

	if got := complete(ids["APP-1-T0002"], "l_live"); got != 0 {
		t.Errorf("wrong lease affected = %d, want 0", got)
	}
	if got := complete(ids["APP-1-T0003"], "l_old"); got != 0 {
		t.Errorf("expired lease affected = %d, want 0", got)
	}
	if got := complete(ids["APP-1-T0001"], "l_live"); got != 1 {
		t.Errorf("live lease affected = %d, want 1", got)
	}

	var (
		status, version int
		leaseID         *string
		leaseUntil      *string
		reportID        *int64
	)
	if err := st.Read().QueryRowContext(ctx, `
		SELECT status, version, lease_id, lease_until, report_material_id
		  FROM tasks WHERE task_id = 'APP-1-T0001'`).
		Scan(&status, &version, &leaseID, &leaseUntil, &reportID); err != nil {
		t.Fatalf("read reported task: %v", err)
	}
	if status != 3 || version != 2 {
		t.Errorf("status/version = %d/%d, want 3/2", status, version)
	}
	if leaseID != nil || leaseUntil != nil {
		t.Errorf("lease = %v/%v, want NULL/NULL", leaseID, leaseUntil)
	}
	if reportID == nil || *reportID != materialID {
		t.Errorf("report_material_id = %v, want %d", reportID, materialID)
	}
}

// TestProjectBulkTaskWrites covers the three set-based helpers the project
// decision uses (ready, cancel, delete) plus the unsettled counter.
func TestProjectBulkTaskWrites(t *testing.T) {
	ctx := context.Background()

	t.Run("set ready only touches backlog", func(t *testing.T) {
		st := openTestStore(t, 1)
		projectID := seedProjectRow(t, st)
		insertSeededTasks(t, st, projectID, []taskSeed{
			{taskID: "APP-1-T0001", seq: 1, status: 0},
			{taskID: "APP-1-T0002", seq: 2, status: 0},
			{taskID: "APP-1-T0003", seq: 3, status: 1},
			{taskID: "APP-1-T0004", seq: 4, status: 4},
			{taskID: "APP-1-T0005", seq: 5, status: 7},
		})

		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = SetTasksReadyByProject(ctx, tx, projectID, "cto", leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("SetTasksReadyByProject: %v", err)
		}
		if affected != 2 {
			t.Errorf("affected = %d, want 2", affected)
		}

		var ready, readyAlready, done int
		for taskID, target := range map[string]*int{
			"APP-1-T0001": &ready,
			"APP-1-T0003": &readyAlready,
			"APP-1-T0004": &done,
		} {
			if err := st.Read().QueryRowContext(ctx,
				`SELECT status FROM tasks WHERE task_id = ?`, taskID).Scan(target); err != nil {
				t.Fatalf("read %s: %v", taskID, err)
			}
		}
		if ready != 1 || readyAlready != 1 || done != 4 {
			t.Errorf("statuses = %d/%d/%d, want ready 1/1/4", ready, readyAlready, done)
		}
	})

	t.Run("cancel finishes every non-terminal task", func(t *testing.T) {
		st := openTestStore(t, 1)
		projectID := seedProjectRow(t, st)
		insertSeededTasks(t, st, projectID, []taskSeed{
			{taskID: "APP-1-T0001", seq: 1, status: 0},
			{taskID: "APP-1-T0002", seq: 2, status: 2, leaseID: "l_a", leaseTill: leaseFuture},
			{taskID: "APP-1-T0003", seq: 3, status: 4},
			{taskID: "APP-1-T0004", seq: 4, status: 7},
		})

		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = CancelUnfinishedTasks(ctx, tx, projectID, "pm", leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("CancelUnfinishedTasks: %v", err)
		}
		if affected != 2 {
			t.Errorf("affected = %d, want 2", affected)
		}
		var cancelled, stillDone int
		if err := st.Read().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks WHERE project_id = ? AND status = 7`, projectID).Scan(&cancelled); err != nil {
			t.Fatalf("count cancelled: %v", err)
		}
		if err := st.Read().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tasks WHERE project_id = ? AND status = 4`, projectID).Scan(&stillDone); err != nil {
			t.Fatalf("count done: %v", err)
		}
		if cancelled != 3 || stillDone != 1 {
			t.Errorf("cancelled/done = %d/%d, want 3/1", cancelled, stillDone)
		}

		unsettled, err := CountUnsettledTasksByProject(ctx, st.Read(), projectID)
		if err != nil {
			t.Fatalf("CountUnsettledTasksByProject: %v", err)
		}
		if unsettled != 0 {
			t.Errorf("unsettled = %d, want 0", unsettled)
		}
	})

	t.Run("delete removes tasks and their dependency edges", func(t *testing.T) {
		st := openTestStore(t, 1)
		projectID := seedProjectRow(t, st)
		ids := insertSeededTasks(t, st, projectID, []taskSeed{
			{taskID: "APP-1-T0001", seq: 1, status: 1},
			{taskID: "APP-1-T0002", seq: 2, status: 1},
		})
		if _, err := st.Write().ExecContext(ctx, `
			INSERT INTO task_deps (task_id, depends_on, create_time, update_time)
			VALUES (?, ?, 't', 't'), (?, ?, 't', 't')`,
			ids["APP-1-T0002"], ids["APP-1-T0001"],
			ids["APP-1-T0001"], ids["APP-1-T0002"]); err != nil {
			t.Fatalf("insert deps: %v", err)
		}

		var deleted int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			deleted, err = DeleteTasksByProject(ctx, tx, projectID)
			return err
		}); err != nil {
			t.Fatalf("DeleteTasksByProject: %v", err)
		}
		if deleted != 2 {
			t.Errorf("deleted = %d, want 2", deleted)
		}
		if n, err := CountTasksByProject(ctx, st.Read(), projectID); err != nil || n != 0 {
			t.Errorf("tasks = %d (%v), want 0", n, err)
		}
		var deps int
		if err := st.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM task_deps`).Scan(&deps); err != nil {
			t.Fatalf("count deps: %v", err)
		}
		if deps != 0 {
			t.Errorf("deps = %d, want 0", deps)
		}
	})
}

// TestCountUnfinishedDependencies pins the dependency query: done (4) and
// cancelled (7) are treated as settled; a still-running or failed dependency
// blocks.
func TestCountUnfinishedDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 4},
		{taskID: "APP-1-T0002", seq: 2, status: 1},
	})
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO task_deps (task_id, depends_on, create_time, update_time)
		VALUES (?, ?, 't', 't')`, ids["APP-1-T0002"], ids["APP-1-T0001"]); err != nil {
		t.Fatalf("insert dep: %v", err)
	}

	unfinished, err := CountUnfinishedDependencies(ctx, st.Read(), ids["APP-1-T0002"])
	if err != nil {
		t.Fatalf("CountUnfinishedDependencies: %v", err)
	}
	if unfinished != 0 {
		t.Errorf("unfinished = %d, want 0 for a done dependency", unfinished)
	}

	if _, err := st.Write().ExecContext(ctx,
		`UPDATE tasks SET status = 5 WHERE task_id = 'APP-1-T0001'`); err != nil {
		t.Fatalf("fail dependency: %v", err)
	}
	unfinished, err = CountUnfinishedDependencies(ctx, st.Read(), ids["APP-1-T0002"])
	if err != nil {
		t.Fatalf("CountUnfinishedDependencies after failure: %v", err)
	}
	if unfinished != 1 {
		t.Errorf("unfinished = %d, want 1 for a failed dependency", unfinished)
	}
}

func TestPlaceholders(t *testing.T) {
	cases := map[int]string{
		0:  "",
		-1: "",
		1:  "?",
		3:  "?, ?, ?",
	}
	for n, want := range cases {
		if got := placeholders(n); got != want {
			t.Errorf("placeholders(%d) = %q, want %q", n, got, want)
		}
	}
}
