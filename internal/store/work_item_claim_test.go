package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// leaseTestTimes are fixed-width lease stamps so the string comparison in the
// claim/heartbeat/reclaim SQL behaves exactly as in production.
const (
	leaseNow      = "2026-10-27T00:00:00.000Z"
	leaseFuture   = "2026-10-27T00:01:30.000Z"
	leasePast     = "2026-10-26T23:58:30.000Z"
	leaseTestTime = "2026-10-27T00:00:00Z"
)

// workItemSeed describes one work_items row for the claim tests.
type workItemSeed struct {
	workID    string
	kind      string
	seq       int
	priority  int
	state     string
	attempts  int
	max       int
	workerID  string
	leaseID   string
	leaseTill string // empty means NULL
}

// insertSeededWorkItems writes the given rows straight into one project and
// returns a work_id → physical id map.
func insertSeededWorkItems(t *testing.T, st *Store, projectID int64, seeds []workItemSeed) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	ids := make(map[string]int64, len(seeds))
	for i, seed := range seeds {
		kind := seed.kind
		if kind == "" {
			kind = "dev"
		}
		state := seed.state
		if state == "" {
			state = WorkItemStateQueued
		}
		max := seed.max
		if max == 0 {
			max = 3
		}
		var leaseID, leaseUntil, workerID any
		if seed.leaseID != "" {
			leaseID = seed.leaseID
		}
		if seed.leaseTill != "" {
			leaseUntil = seed.leaseTill
		}
		if seed.workerID != "" {
			workerID = seed.workerID
		} else if state == WorkItemStateLeased {
			// ux_work_worker allows one leased row per worker; give every leased
			// seed its own holder unless the test asked for a specific one.
			workerID = fmt.Sprintf("w_seed_%02d", i+1)
		}
		res, err := st.Write().ExecContext(ctx, `
			INSERT INTO work_items (work_id, project_id, kind, seq_no, title, body_md,
			                        priority, state, attempt, max_attempts,
			                        lease_id, lease_until, worker_id, version,
			                        create_time, update_time)
			VALUES (?, ?, ?, ?, ?, 'body', ?, ?, ?, ?, ?, ?, ?, 1,
			        '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`,
			seed.workID, projectID, kind, seed.seq, seed.workID, seed.priority,
			state, seed.attempts, max, leaseID, leaseUntil, workerID)
		if err != nil {
			t.Fatalf("insert work item %s: %v", seed.workID, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("work item id of %s: %v", seed.workID, err)
		}
		ids[seed.workID] = id
	}
	return ids
}

// seedKindWorker registers one worker with the given kind declaration.
func seedKindWorker(t *testing.T, st *Store, workerID string, kinds ...string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := UpsertWorker(ctx, tx, WorkerUpsert{
			WorkerID: workerID, State: "idle",
			Kinds:    kinds,
			LastSeen: "2026-10-27T00:00:00.000Z", UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("seed worker %s: %v", workerID, err)
	}
}

func TestClaimWorkItemOrderKindsAndDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	ids := insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_01", seq: 1, priority: 0},
		{workID: "w_02", seq: 2, priority: 10},
		{workID: "w_03", seq: 3, priority: 5},
		// Dependency not accepted: must never be handed out.
		{workID: "w_04", seq: 4, priority: 99},
		// Attempt budget already used up.
		{workID: "w_05", seq: 5, priority: 50, attempts: 3, max: 3},
		// Another kind: a dev-only worker must not see it.
		{workID: "w_06", seq: 6, priority: 100, kind: "design"},
	})
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return InsertWorkDependency(ctx, tx, ids["w_04"], ids["w_01"])
	}); err != nil {
		t.Fatalf("InsertWorkDependency: %v", err)
	}
	for _, worker := range []string{"w_a", "w_b", "w_c", "w_d"} {
		seedKindWorker(t, st, worker, "dev")
	}

	steps := []struct {
		worker string
		workID string
	}{
		{"w_a", "w_02"}, // priority 10
		{"w_b", "w_03"}, // priority 5
		{"w_c", "w_01"}, // priority 0 (also unblocks w_04)
	}

	for i, step := range steps {
		var (
			claimed WorkItem
			ok      bool
		)
		err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			claimed, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
				ProductID: 1, WorkerID: step.worker,
				LeaseID: "l_" + step.workID, LeaseUntil: leaseFuture,
				UpdateTime: leaseTestTime,
			})
			return err
		})
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("claim %d: no item, want %s", i, step.workID)
		}
		if claimed.WorkID != step.workID {
			t.Fatalf("claim %d = %s, want %s", i, claimed.WorkID, step.workID)
		}
		if claimed.State != WorkItemStateLeased || claimed.Attempt != 1 {
			t.Errorf("%s state/attempt = %s/%d, want leased/1", claimed.WorkID, claimed.State, claimed.Attempt)
		}
		if claimed.WorkerID == nil || *claimed.WorkerID != step.worker {
			t.Errorf("%s worker = %v, want %s", claimed.WorkID, claimed.WorkerID, step.worker)
		}
	}

	// The blocked card becomes claimable once its dependency is accepted.
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE work_items SET state = 'accepted' WHERE work_id = 'w_01'`); err != nil {
		t.Fatalf("accept dependency: %v", err)
	}
	var (
		claimed WorkItem
		ok      bool
	)
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		claimed, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 1, WorkerID: "w_d",
			LeaseID: "l_after_dep", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		return err
	})
	if err != nil {
		t.Fatalf("claim after dependency accepted: %v", err)
	}
	if !ok || claimed.WorkID != "w_04" {
		t.Fatalf("claim after dependency accepted = %s/%v, want w_04", claimed.WorkID, ok)
	}

	// Only the exhausted card and the design card are left: neither is
	// claimable by a dev worker with one attempt budget left.
	for _, worker := range []string{"w_a", "w_b"} {
		claimed, ok, err := claimOnce(ctx, st, ClaimRequest{
			ProductID: 1, WorkerID: worker,
			LeaseID: "l_none_" + worker, LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		if err != nil {
			t.Fatalf("claim exhausted: %v", err)
		}
		if ok {
			t.Errorf("worker %s claimed %s, a card that is exhausted or of another kind", worker, claimed.WorkID)
		}
	}
}

func TestClaimWorkItemRequiresDeclaredKind(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_dev", seq: 1, kind: "dev"},
		{workID: "w_design", seq: 2, kind: "design"},
	})
	seedKindWorker(t, st, "w_designer", "design")
	seedKindWorker(t, st, "w_nodecl" /* no kinds */)

	claim := func(worker string) (string, bool) {
		t.Helper()
		var (
			claimed WorkItem
			ok      bool
		)
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			claimed, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
				ProductID: 1, WorkerID: worker,
				LeaseID: "l_" + worker, LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
			})
			return err
		}); err != nil {
			t.Fatalf("claim %s: %v", worker, err)
		}
		return claimed.WorkID, ok
	}

	// A worker that declared nothing can never claim, even with work waiting.
	if workID, ok := claim("w_nodecl"); ok {
		t.Fatalf("worker without kinds claimed %s", workID)
	}

	// The designer only sees the design card, never the higher-priority dev one.
	if workID, ok := claim("w_designer"); !ok || workID != "w_design" {
		t.Fatalf("designer claimed %q/%v, want w_design", workID, ok)
	}

	// Declaring the dev kind afterwards makes the remaining card visible.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return SetWorkerKinds(ctx, tx, "w_designer", []string{"dev", "design"})
	}); err != nil {
		t.Fatalf("SetWorkerKinds: %v", err)
	}
	kinds, err := ListWorkerKinds(ctx, st.Read(), "w_designer")
	if err != nil {
		t.Fatalf("ListWorkerKinds: %v", err)
	}
	if len(kinds) != 2 || kinds[0] != "design" || kinds[1] != "dev" {
		t.Fatalf("kinds = %v, want [design dev]", kinds)
	}

	// The designer still holds w_design, so the single-lease index refuses a
	// second concurrent lease: that surfaces as ErrUniqueViolation, not a
	// silent double claim.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, _, err := ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 1, WorkerID: "w_designer",
			LeaseID: "l_second", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		return err
	})
	if err == nil {
		t.Fatal("a worker holding one lease claimed a second card")
	}
}

func TestSetWorkerKindsRejectsUnknownKind(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		return SetWorkerKinds(ctx, tx, "w_bad", []string{"dev", "merge"})
	})
	if err == nil {
		t.Fatal("SetWorkerKinds accepted an unknown kind")
	}
	// The rejected call must not have written anything.
	kinds, listErr := ListWorkerKinds(ctx, st.Read(), "w_bad")
	if listErr != nil {
		t.Fatalf("ListWorkerKinds: %v", listErr)
	}
	if len(kinds) != 0 {
		t.Errorf("kinds = %v, want empty after a rejected call", kinds)
	}
}

func TestClaimWorkItemDirectedAndProductIsolation(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	// A second product with its own project and card.
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('WEB', 'WEB 产品', 'git@example.com:WEB.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert second product: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, version, create_time, update_time)
		VALUES ('WEB-1', 2, 1, '另一个项目', 2, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert second project: %v", err)
	}
	var webProjectID int64
	if err := st.Read().QueryRowContext(ctx, `SELECT id FROM projects WHERE project_id = 'WEB-1'`).Scan(&webProjectID); err != nil {
		t.Fatalf("read second project id: %v", err)
	}
	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_01", seq: 1, priority: 1},
		{workID: "w_02", seq: 2, priority: 2},
	})
	insertSeededWorkItems(t, st, webProjectID, []workItemSeed{
		{workID: "w_web", seq: 1, priority: 100},
	})
	for _, worker := range []string{"w_direct", "w_direct2", "w_web"} {
		seedKindWorker(t, st, worker, "dev")
	}

	// A directed claim only ever touches the named card, even when a
	// higher-priority card of the same product exists.
	var directed WorkItem
	ok := false
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		directed, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 1, WorkID: "w_01", WorkerID: "w_direct",
			LeaseID: "l_directed", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("directed claim: %v", err)
	}
	if !ok || directed.WorkID != "w_01" {
		t.Fatalf("directed claim = %s/%v, want w_01", directed.WorkID, ok)
	}

	// The same directed claim is no-work now that the card is leased.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		_, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 1, WorkID: "w_01", WorkerID: "w_direct2",
			LeaseID: "l_again", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("repeat directed claim: %v", err)
	}
	if ok {
		t.Fatal("a directed claim picked a card that is already leased")
	}

	// Product A never sees product B's card, and vice versa.
	var web WorkItem
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		web, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 2, WorkerID: "w_web",
			LeaseID: "l_web", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("claim product WEB: %v", err)
	}
	if !ok || web.WorkID != "w_web" {
		t.Fatalf("claim product WEB = %s/%v, want w_web", web.WorkID, ok)
	}
}

func TestHeartbeatLeaseGuards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_live", seq: 1, state: WorkItemStateLeased, attempts: 1, leaseID: "l_live", leaseTill: leaseFuture},
		{workID: "w_past", seq: 2, state: WorkItemStateLeased, attempts: 1, leaseID: "l_past", leaseTill: leasePast},
		{workID: "w_queued", seq: 3, attempts: 1},
	})

	cases := []struct {
		name    string
		workID  string
		leaseID string
		wantOK  bool
	}{
		{"live lease", "w_live", "l_live", true},
		{"wrong lease id", "w_live", "l_other", false},
		{"expired lease", "w_past", "l_past", false},
		{"not leased", "w_queued", "l_live", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				until string
				ok    bool
			)
			err := st.Tx(ctx, func(tx *sql.Tx) error {
				var err error
				until, ok, err = HeartbeatLease(ctx, tx, WorkItemHeartbeatRequest{
					WorkID: tc.workID, LeaseID: tc.leaseID,
					Now: leaseNow, Until: "2026-10-27T00:01:30.000Z",
					UpdateTime: leaseTestTime,
				})
				return err
			})
			if err != nil {
				t.Fatalf("HeartbeatLease: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && until != "2026-10-27T00:01:30.000Z" {
				t.Errorf("until = %q, want the new deadline", until)
			}
		})
	}
}

func TestReclaimExpiredWorkItemsIsSingleShot(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		// Expired with budget left → queued, attempt kept.
		{workID: "w_retry", seq: 1, state: WorkItemStateLeased, attempts: 1, max: 3,
			workerID: "w_r1", leaseID: "l_a", leaseTill: leasePast},
		// Expired with the budget used up → failed.
		{workID: "w_dead", seq: 2, state: WorkItemStateLeased, attempts: 3, max: 3,
			workerID: "w_r2", leaseID: "l_b", leaseTill: leasePast},
		// Still live → untouched.
		{workID: "w_live", seq: 3, state: WorkItemStateLeased, attempts: 1, max: 3,
			workerID: "w_r3", leaseID: "l_c", leaseTill: leaseFuture},
	})

	var reclaimed []ReclaimedWorkItem
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		reclaimed, err = ReclaimExpiredWorkItems(ctx, tx, leaseNow, leaseTestTime)
		return err
	}); err != nil {
		t.Fatalf("ReclaimExpiredWorkItems: %v", err)
	}
	if len(reclaimed) != 2 {
		t.Fatalf("reclaimed = %d rows, want 2: %+v", len(reclaimed), reclaimed)
	}

	byID := map[string]ReclaimedWorkItem{}
	for _, row := range reclaimed {
		byID[row.WorkID] = row
	}
	if got := byID["w_retry"]; got.State != WorkItemStateQueued || got.Attempt != 1 || got.Reason != nil {
		t.Errorf("requeued row = %+v, want queued, attempt 1, nil reason", got)
	}
	if got := byID["w_dead"]; got.State != WorkItemStateFailed || got.Attempt != 3 ||
		got.Reason == nil || *got.Reason != "attempt 用尽" {
		t.Errorf("exhausted row = %+v, want failed, attempt 3, reason 用尽", got)
	}

	// The live lease is untouched, the reclaimed ones lost their lease and
	// worker stamp.
	var (
		state1, state2, state3       string
		leaseID1, leaseID2, leaseID3 *string
		until1, until2, until3       *string
		worker1, worker2, worker3    *string
		attempt1                     int
	)
	if err := st.Read().QueryRowContext(ctx, `
		SELECT state, attempt, lease_id, lease_until, worker_id FROM work_items WHERE work_id = 'w_retry'`).
		Scan(&state1, &attempt1, &leaseID1, &until1, &worker1); err != nil {
		t.Fatalf("read w_retry: %v", err)
	}
	if err := st.Read().QueryRowContext(ctx, `
		SELECT state, lease_id, lease_until, worker_id FROM work_items WHERE work_id = 'w_dead'`).
		Scan(&state2, &leaseID2, &until2, &worker2); err != nil {
		t.Fatalf("read w_dead: %v", err)
	}
	if err := st.Read().QueryRowContext(ctx, `
		SELECT state, lease_id, lease_until, worker_id FROM work_items WHERE work_id = 'w_live'`).
		Scan(&state3, &leaseID3, &until3, &worker3); err != nil {
		t.Fatalf("read w_live: %v", err)
	}
	if state1 != WorkItemStateQueued || attempt1 != 1 || leaseID1 != nil || until1 != nil || worker1 != nil {
		t.Errorf("w_retry = %s attempt %d lease %v/%v worker %v", state1, attempt1, leaseID1, until1, worker1)
	}
	if state2 != WorkItemStateFailed || leaseID2 != nil || until2 != nil || worker2 != nil {
		t.Errorf("w_dead = %s lease %v/%v worker %v", state2, leaseID2, until2, worker2)
	}
	if state3 != WorkItemStateLeased || leaseID3 == nil || *leaseID3 != "l_c" ||
		until3 == nil || *until3 != leaseFuture || worker3 == nil || *worker3 != "w_r3" {
		t.Errorf("w_live = %s lease %v/%v worker %v, want it untouched", state3, leaseID3, until3, worker3)
	}

	// The requeued card can be claimed again, and the attempt increment is what
	// eventually drives it into failed.
	seedKindWorker(t, st, "w_retry_claimer", "dev")
	for want := 2; want <= 3; want++ {
		var claimed WorkItem
		ok := false
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			claimed, ok, err = ClaimWorkItem(ctx, tx, ClaimRequest{
				ProductID: 1, WorkID: "w_retry", WorkerID: "w_retry_claimer",
				LeaseID: fmt.Sprintf("l_retry_%d", want), LeaseUntil: leasePast,
				UpdateTime: leaseTestTime,
			})
			return err
		}); err != nil {
			t.Fatalf("reclaim claim %d: %v", want, err)
		}
		if !ok || claimed.Attempt != want {
			t.Fatalf("reclaim claim = %v/%d, want ok with attempt %d", ok, claimed.Attempt, want)
		}
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			_, err := ReclaimExpiredWorkItems(ctx, tx, leaseNow, leaseTestTime)
			return err
		}); err != nil {
			t.Fatalf("reclaim after attempt %d: %v", want, err)
		}
	}

	var final string
	if err := st.Read().QueryRowContext(ctx,
		`SELECT state FROM work_items WHERE work_id = 'w_retry'`).Scan(&final); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if final != WorkItemStateFailed {
		t.Errorf("state after exhausting attempts = %s, want failed", final)
	}

	// A failed card is never handed out again.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, ok, err := ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 1, WorkerID: "w_retry_claimer",
			LeaseID: "l_after_fail", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		if err != nil {
			return err
		}
		if ok {
			t.Error("a failed card was claimed again")
		}
		return nil
	}); err != nil {
		t.Fatalf("claim after failure: %v", err)
	}

	// A second sweep — the concurrent or repeated case — moves nothing.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		again, err := ReclaimExpiredWorkItems(ctx, tx, leaseNow, leaseTestTime)
		if err != nil {
			return err
		}
		if len(again) != 0 {
			t.Errorf("second sweep reclaimed %d rows, want 0", len(again))
		}
		return nil
	}); err != nil {
		t.Fatalf("second ReclaimExpiredWorkItems: %v", err)
	}
}

// TestClaimNextWorkItemIgnoresProductScope covers the worker heartbeat entry
// point: the same statement and order as ClaimWorkItem, but with a
// constant-true scope predicate instead of one product.
func TestClaimNextWorkItemIgnoresProductScope(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	seedProjectRow(t, st) // product APP / project APP-1, deliberately empty

	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('WEB', 'WEB 产品', 'git@example.com:WEB.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert second product: %v", err)
	}
	res, err := st.Write().ExecContext(ctx, `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, version, create_time, update_time)
		VALUES ('WEB-1', 2, 1, '项目', 2, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert second project: %v", err)
	}
	secondProject, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("second project id: %v", err)
	}
	insertSeededWorkItems(t, st, secondProject, []workItemSeed{
		{workID: "w_web", seq: 1},
	})
	seedKindWorker(t, st, "w_scoped", "dev")
	seedKindWorker(t, st, "w_next", "dev")
	seedKindWorker(t, st, "w_more", "dev")

	// A product-scoped claim of the empty product finds nothing.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, ok, err := ClaimWorkItem(ctx, tx, ClaimRequest{
			ProductID: 1, WorkerID: "w_scoped",
			LeaseID: "l_scoped", LeaseUntil: leaseFuture, UpdateTime: leaseTestTime,
		})
		if err != nil {
			return err
		}
		if ok {
			t.Error("ClaimWorkItem(product 1) claimed a card of another product")
		}
		return nil
	}); err != nil {
		t.Fatalf("scoped claim: %v", err)
	}

	var claimed WorkItem
	var ok bool
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		claimed, ok, err = ClaimNextWorkItem(ctx, tx, ClaimNextRequest{
			WorkerID: "w_next", LeaseID: "l_next", LeaseUntil: leaseFuture,
			UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("ClaimNextWorkItem: %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextWorkItem found no work, want w_web")
	}
	if claimed.WorkID != "w_web" || claimed.ProjectCode != "WEB-1" {
		t.Errorf("claimed = %s/%s, want w_web/WEB-1", claimed.WorkID, claimed.ProjectCode)
	}
	if claimed.State != WorkItemStateLeased || claimed.Attempt != 1 {
		t.Errorf("state/attempt = %s/%d, want leased/1", claimed.State, claimed.Attempt)
	}
	if claimed.LeaseID == nil || *claimed.LeaseID != "l_next" {
		t.Errorf("lease_id = %v, want l_next", claimed.LeaseID)
	}
	if claimed.WorkerID == nil || *claimed.WorkerID != "w_next" {
		t.Errorf("worker_id = %v, want w_next", claimed.WorkerID)
	}

	// The queue is empty now: ok is false, not an error.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, ok, err := ClaimNextWorkItem(ctx, tx, ClaimNextRequest{
			WorkerID: "w_more", LeaseID: "l_more", LeaseUntil: leaseFuture,
			UpdateTime: leaseTestTime,
		})
		if err != nil {
			return err
		}
		if ok {
			t.Error("ClaimNextWorkItem found work in an empty queue")
		}
		return nil
	}); err != nil {
		t.Fatalf("second ClaimNextWorkItem: %v", err)
	}
}
