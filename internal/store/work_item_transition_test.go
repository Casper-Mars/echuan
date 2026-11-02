package store

import (
	"context"
	"database/sql"
	"testing"
)

// TestTransitionWorkItemGuards pins the conditional UPDATE behind every
// work-item state change: a stale version or a different current state matches
// nothing.
func TestTransitionWorkItemGuards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_01", seq: 1},
	})

	transition := func(expectState string, expectVersion int, next string) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = TransitionWorkItem(ctx, tx, ids["w_01"],
				expectState, expectVersion, next, nil, leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("TransitionWorkItem: %v", err)
		}
		return affected
	}

	if got := transition(WorkItemStateQueued, 2, WorkItemStateLeased); got != 0 {
		t.Errorf("stale version affected = %d, want 0", got)
	}
	if got := transition(WorkItemStateBlocked, 1, WorkItemStateLeased); got != 0 {
		t.Errorf("wrong state affected = %d, want 0", got)
	}
	if got := transition(WorkItemStateQueued, 1, WorkItemStateLeased); got != 1 {
		t.Errorf("valid transition affected = %d, want 1", got)
	}

	var (
		state   string
		version int
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT state, version FROM work_items WHERE work_id = 'w_01'`).
		Scan(&state, &version); err != nil {
		t.Fatalf("read work item: %v", err)
	}
	if state != WorkItemStateLeased || version != 2 {
		t.Errorf("state/version = %s/%d, want leased/2", state, version)
	}
}

// TestTransitionWorkItemDecisionResetsAttempts checks the retry branch: only a
// retry verdict clears attempt, the other verdicts leave it alone.
func TestTransitionWorkItemDecisionResetsAttempts(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_submitted", seq: 1, state: WorkItemStateSubmitted, attempts: 2},
		{workID: "w_failed", seq: 2, state: WorkItemStateFailed, attempts: 3},
	})

	reason := "缺少回归测试"
	apply := func(w WorkItemDecisionWrite) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = TransitionWorkItemDecision(ctx, tx, w)
			return err
		}); err != nil {
			t.Fatalf("TransitionWorkItemDecision: %v", err)
		}
		return affected
	}

	if got := apply(WorkItemDecisionWrite{
		ID: ids["w_submitted"], ExpectState: WorkItemStateSubmitted, ExpectVersion: 1,
		NextState: WorkItemStateAccepted, Reason: &reason, UpdateTime: leaseTestTime,
	}); got != 1 {
		t.Fatalf("accept affected = %d, want 1", got)
	}
	if got := apply(WorkItemDecisionWrite{
		ID: ids["w_failed"], ExpectState: WorkItemStateFailed, ExpectVersion: 1,
		NextState: WorkItemStateQueued, ResetAttempts: true, UpdateTime: leaseTestTime,
	}); got != 1 {
		t.Fatalf("retry affected = %d, want 1", got)
	}
	if got := apply(WorkItemDecisionWrite{
		ID: ids["w_failed"], ExpectState: WorkItemStateFailed, ExpectVersion: 1,
		NextState: WorkItemStateCancelled, UpdateTime: leaseTestTime,
	}); got != 0 {
		t.Errorf("stale decision affected = %d, want 0", got)
	}

	var acceptedAttempt, retriedAttempt int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT attempt FROM work_items WHERE work_id = 'w_submitted'`).Scan(&acceptedAttempt); err != nil {
		t.Fatalf("read accepted attempt: %v", err)
	}
	if err := st.Read().QueryRowContext(ctx,
		`SELECT attempt FROM work_items WHERE work_id = 'w_failed'`).Scan(&retriedAttempt); err != nil {
		t.Fatalf("read retried attempt: %v", err)
	}
	if acceptedAttempt != 2 {
		t.Errorf("accept attempt = %d, want 2 (unchanged)", acceptedAttempt)
	}
	if retriedAttempt != 0 {
		t.Errorf("retry attempt = %d, want 0 (reset)", retriedAttempt)
	}

	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Error("boolInt must render 1/0")
	}
}

// TestCompleteWorkItemReportGuards covers the report write predicate: right
// lease and live deadline move the card, everything else is a no-op (the caller
// turns 0 rows into lease_lost).
func TestCompleteWorkItemReportGuards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_live", seq: 1, state: WorkItemStateLeased, attempts: 1, leaseID: "l_live", leaseTill: leaseFuture},
		{workID: "w_other", seq: 2, state: WorkItemStateLeased, attempts: 1, leaseID: "l_other", leaseTill: leaseFuture},
		{workID: "w_old", seq: 3, state: WorkItemStateLeased, attempts: 1, leaseID: "l_old", leaseTill: leasePast},
	})
	materialID := insertMaterial(t, st, "m_report_1", 3, 1, nil)

	complete := func(id int64, leaseID string) int64 {
		t.Helper()
		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = CompleteWorkItemReport(ctx, tx, id, materialID,
				leaseID, leaseNow, leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("CompleteWorkItemReport: %v", err)
		}
		return affected
	}

	if got := complete(ids["w_other"], "l_live"); got != 0 {
		t.Errorf("wrong lease affected = %d, want 0", got)
	}
	if got := complete(ids["w_old"], "l_old"); got != 0 {
		t.Errorf("expired lease affected = %d, want 0", got)
	}
	if got := complete(ids["w_live"], "l_live"); got != 1 {
		t.Errorf("live lease affected = %d, want 1", got)
	}

	var (
		stateText  string
		version    int
		leaseID    *string
		leaseUntil *string
		resultID   *int64
	)
	if err := st.Read().QueryRowContext(ctx, `
		SELECT state, version, lease_id, lease_until, result_material_id
		  FROM work_items WHERE work_id = 'w_live'`).
		Scan(&stateText, &version, &leaseID, &leaseUntil, &resultID); err != nil {
		t.Fatalf("read reported item: %v", err)
	}
	if stateText != WorkItemStateSubmitted || version != 2 {
		t.Errorf("state/version = %s/%d, want submitted/2", stateText, version)
	}
	if leaseID != nil || leaseUntil != nil {
		t.Errorf("lease = %v/%v, want NULL/NULL", leaseID, leaseUntil)
	}
	if resultID == nil || *resultID != materialID {
		t.Errorf("result_material_id = %v, want %d", resultID, materialID)
	}
}

// TestProjectBulkWorkItemWrites covers the three set-based helpers the project
// decision uses (queue, cancel, delete) plus the unsettled counter.
func TestProjectBulkWorkItemWrites(t *testing.T) {
	ctx := context.Background()

	t.Run("queue only touches blocked", func(t *testing.T) {
		st := openTestStore(t, 1)
		projectID := seedProjectRow(t, st)
		insertSeededWorkItems(t, st, projectID, []workItemSeed{
			{workID: "w_01", seq: 1, state: WorkItemStateBlocked},
			{workID: "w_02", seq: 2, state: WorkItemStateBlocked},
			{workID: "w_03", seq: 3, state: WorkItemStateQueued},
			{workID: "w_04", seq: 4, state: WorkItemStateAccepted},
			{workID: "w_05", seq: 5, state: WorkItemStateCancelled},
		})

		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = SetWorkItemsQueuedByProject(ctx, tx, projectID, leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("SetWorkItemsQueuedByProject: %v", err)
		}
		if affected != 2 {
			t.Errorf("affected = %d, want 2", affected)
		}

		states := map[string]string{}
		rows, err := st.Read().QueryContext(ctx, `SELECT work_id, state FROM work_items`)
		if err != nil {
			t.Fatalf("read states: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				t.Fatalf("scan state: %v", err)
			}
			states[id] = state
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		if states["w_01"] != WorkItemStateQueued || states["w_02"] != WorkItemStateQueued ||
			states["w_03"] != WorkItemStateQueued || states["w_04"] != WorkItemStateAccepted ||
			states["w_05"] != WorkItemStateCancelled {
			t.Errorf("states = %v", states)
		}
	})

	t.Run("cancel finishes every non-terminal item", func(t *testing.T) {
		st := openTestStore(t, 1)
		projectID := seedProjectRow(t, st)
		insertSeededWorkItems(t, st, projectID, []workItemSeed{
			{workID: "w_01", seq: 1, state: WorkItemStateBlocked},
			{workID: "w_02", seq: 2, state: WorkItemStateLeased, leaseID: "l_a", leaseTill: leaseFuture},
			{workID: "w_03", seq: 3, state: WorkItemStateAccepted},
			{workID: "w_04", seq: 4, state: WorkItemStateCancelled},
		})

		var affected int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			affected, err = CancelUnfinishedWorkItems(ctx, tx, projectID, leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("CancelUnfinishedWorkItems: %v", err)
		}
		if affected != 2 {
			t.Errorf("affected = %d, want 2", affected)
		}
		var cancelled, stillAccepted int
		if err := st.Read().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM work_items WHERE project_id = ? AND state = 'cancelled'`, projectID).Scan(&cancelled); err != nil {
			t.Fatalf("count cancelled: %v", err)
		}
		if err := st.Read().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM work_items WHERE project_id = ? AND state = 'accepted'`, projectID).Scan(&stillAccepted); err != nil {
			t.Fatalf("count accepted: %v", err)
		}
		if cancelled != 3 || stillAccepted != 1 {
			t.Errorf("cancelled/accepted = %d/%d, want 3/1", cancelled, stillAccepted)
		}

		unsettled, err := CountUnsettledWorkItemsByProject(ctx, st.Read(), projectID)
		if err != nil {
			t.Fatalf("CountUnsettledWorkItemsByProject: %v", err)
		}
		if unsettled != 0 {
			t.Errorf("unsettled = %d, want 0", unsettled)
		}
	})

	t.Run("delete removes items and their dependency edges", func(t *testing.T) {
		st := openTestStore(t, 1)
		projectID := seedProjectRow(t, st)
		ids := insertSeededWorkItems(t, st, projectID, []workItemSeed{
			{workID: "w_01", seq: 1},
			{workID: "w_02", seq: 2},
		})
		if _, err := st.Write().ExecContext(ctx, `
			INSERT INTO work_deps (work_item_id, depends_on_id)
			VALUES (?, ?), (?, ?)`,
			ids["w_02"], ids["w_01"],
			ids["w_01"], ids["w_02"]); err != nil {
			t.Fatalf("insert deps: %v", err)
		}
		materialID := insertMaterial(t, st, "m_input_1", 3, 1, nil)
		if _, err := st.Write().ExecContext(ctx,
			`INSERT INTO work_inputs (work_item_id, material_id) VALUES (?, ?)`,
			ids["w_01"], materialID); err != nil {
			t.Fatalf("insert work input: %v", err)
		}
		if _, err := st.Write().ExecContext(ctx, `
			INSERT INTO workers (worker_id, metadata_json, state, current_work_item_id, first_seen, last_seen, create_time, update_time)
			VALUES ('w_holder', '{}', 'busy', ?, 'T', 'T', 'T', 'T')`,
			ids["w_01"]); err != nil {
			t.Fatalf("insert bound worker: %v", err)
		}

		var deleted int64
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			deleted, err = DeleteWorkItemsByProject(ctx, tx, projectID)
			return err
		}); err != nil {
			t.Fatalf("DeleteWorkItemsByProject: %v", err)
		}
		if deleted != 2 {
			t.Errorf("deleted = %d, want 2", deleted)
		}
		if n, err := CountWorkItemsByProject(ctx, st.Read(), projectID); err != nil || n != 0 {
			t.Errorf("work items = %d (%v), want 0", n, err)
		}
		for _, table := range []string{"work_deps", "work_inputs"} {
			var count int
			if err := st.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
				t.Fatalf("count %s: %v", table, err)
			}
			if count != 0 {
				t.Errorf("%s = %d, want 0", table, count)
			}
		}
		worker, err := GetWorker(ctx, st.Read(), "w_holder")
		if err != nil {
			t.Fatalf("GetWorker: %v", err)
		}
		if worker.CurrentWorkItemID != nil || worker.State != "idle" {
			t.Errorf("worker = %v/%s, want NULL/idle after the delete", worker.CurrentWorkItemID, worker.State)
		}
	})
}

// TestCountUnfinishedDependencies pins the dependency query: only accepted
// counts as settled; failed, cancelled, queued and every other state blocks.
func TestCountUnfinishedDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()
	ids := insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_dep", seq: 1, state: WorkItemStateAccepted},
		{workID: "w_owner", seq: 2},
	})
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO work_deps (work_item_id, depends_on_id) VALUES (?, ?)`,
		ids["w_owner"], ids["w_dep"]); err != nil {
		t.Fatalf("insert dep: %v", err)
	}

	unfinished, err := CountUnfinishedDependencies(ctx, st.Read(), ids["w_owner"])
	if err != nil {
		t.Fatalf("CountUnfinishedDependencies: %v", err)
	}
	if unfinished != 0 {
		t.Errorf("unfinished = %d, want 0 for an accepted dependency", unfinished)
	}

	for _, state := range []string{
		WorkItemStateFailed, WorkItemStateCancelled, WorkItemStateQueued,
		WorkItemStateSubmitted, WorkItemStateLeased,
	} {
		if _, err := st.Write().ExecContext(ctx,
			`UPDATE work_items SET state = ? WHERE work_id = 'w_dep'`, state); err != nil {
			t.Fatalf("set dependency %s: %v", state, err)
		}
		unfinished, err = CountUnfinishedDependencies(ctx, st.Read(), ids["w_owner"])
		if err != nil {
			t.Fatalf("CountUnfinishedDependencies(%s): %v", state, err)
		}
		if unfinished != 1 {
			t.Errorf("unfinished = %d for a %s dependency, want 1", unfinished, state)
		}
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
