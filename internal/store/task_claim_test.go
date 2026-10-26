package store

import (
	"context"
	"database/sql"
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

// taskSeed describes one task row for the claim tests.
type taskSeed struct {
	taskID    string
	seq       int
	priority  int
	status    int
	attempts  int
	max       int
	leaseID   string
	leaseTill string // empty means NULL
}

// insertSeededTasks writes the given rows straight into one project and returns
// a task_id → physical id map.
func insertSeededTasks(t *testing.T, st *Store, projectID int64, seeds []taskSeed) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	ids := make(map[string]int64, len(seeds))
	for _, seed := range seeds {
		max := seed.max
		if max == 0 {
			max = 3
		}
		var leaseID, leaseUntil any
		if seed.leaseID != "" {
			leaseID = seed.leaseID
		}
		if seed.leaseTill != "" {
			leaseUntil = seed.leaseTill
		}
		res, err := st.Write().ExecContext(ctx, `
			INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, priority, status,
			                   attempt_count, max_attempts, lease_id, lease_until, version,
			                   create_time, update_time)
			VALUES (?, ?, ?, ?, 'body', ?, ?, ?, ?, ?, ?, 1, '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`,
			seed.taskID, projectID, seed.seq, seed.taskID, seed.priority, seed.status,
			seed.attempts, max, leaseID, leaseUntil)
		if err != nil {
			t.Fatalf("insert task %s: %v", seed.taskID, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("task id of %s: %v", seed.taskID, err)
		}
		ids[seed.taskID] = id
	}
	return ids
}

func TestClaimTaskPicksByPriorityAndSkipsBlockedCards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	ids := insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, priority: 0, status: 1},
		{taskID: "APP-1-T0002", seq: 2, priority: 10, status: 1},
		{taskID: "APP-1-T0003", seq: 3, priority: 5, status: 1},
		// Dependency not done: must never be handed out.
		{taskID: "APP-1-T0004", seq: 4, priority: 99, status: 1},
		// Attempt budget already used up.
		{taskID: "APP-1-T0005", seq: 5, priority: 50, status: 1, attempts: 3, max: 3},
	})
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return InsertTaskDependency(ctx, tx, ids["APP-1-T0004"], ids["APP-1-T0001"], leaseTestTime)
	}); err != nil {
		t.Fatalf("InsertTaskDependency: %v", err)
	}

	type claimStep struct {
		taskID string
	}
	steps := []claimStep{
		{taskID: "APP-1-T0002"}, // priority 10
		{taskID: "APP-1-T0003"}, // priority 5
		{taskID: "APP-1-T0001"}, // priority 0 (also unblocks T0004)
	}

	for i, step := range steps {
		var (
			claimed Task
			ok      bool
		)
		err := st.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			claimed, ok, err = ClaimTask(ctx, tx, ClaimRequest{
				ProductID: 1, LeaseID: "l_" + step.taskID, LeaseUntil: leaseFuture,
				Actor: "worker", UpdateTime: leaseTestTime,
			})
			return err
		})
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("claim %d: no task, want %s", i, step.taskID)
		}
		if claimed.TaskID != step.taskID {
			t.Fatalf("claim %d = %s, want %s", i, claimed.TaskID, step.taskID)
		}
		if claimed.Status != 2 || claimed.AttemptCount != 1 {
			t.Errorf("%s status/attempt = %d/%d, want 2/1", claimed.TaskID, claimed.Status, claimed.AttemptCount)
		}
	}

	// The blocked card becomes claimable once its dependency is done.
	if _, err := st.Write().ExecContext(ctx, `UPDATE tasks SET status = 4 WHERE task_id = 'APP-1-T0001'`); err != nil {
		t.Fatalf("mark dependency done: %v", err)
	}
	var (
		claimed Task
		ok      bool
	)
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		claimed, ok, err = ClaimTask(ctx, tx, ClaimRequest{
			ProductID: 1, LeaseID: "l_after_dep", LeaseUntil: leaseFuture,
			Actor: "worker", UpdateTime: leaseTestTime,
		})
		return err
	})
	if err != nil {
		t.Fatalf("claim after dependency done: %v", err)
	}
	if !ok || claimed.TaskID != "APP-1-T0004" {
		t.Fatalf("claim after dependency done = %s/%v, want APP-1-T0004", claimed.TaskID, ok)
	}

	// Only the exhausted card is left: the attempt budget keeps it out.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		_, ok, err = ClaimTask(ctx, tx, ClaimRequest{
			ProductID: 1, LeaseID: "l_none", LeaseUntil: leaseFuture,
			Actor: "worker", UpdateTime: leaseTestTime,
		})
		return err
	})
	if err != nil {
		t.Fatalf("claim exhausted: %v", err)
	}
	if ok {
		t.Fatal("claimed a card with no attempts left")
	}
}

func TestClaimTaskDirectedAndProductIsolation(t *testing.T) {
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
	insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, priority: 1, status: 1},
		{taskID: "APP-1-T0002", seq: 2, priority: 2, status: 1},
	})
	insertSeededTasks(t, st, webProjectID, []taskSeed{
		{taskID: "WEB-1-T0001", seq: 1, priority: 100, status: 1},
	})

	// A directed claim only ever touches the named card, even when a
	// higher-priority card of the same product exists.
	var directed Task
	ok := false
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		directed, ok, err = ClaimTask(ctx, tx, ClaimRequest{
			ProductID: 1, TaskID: "APP-1-T0001", LeaseID: "l_directed",
			LeaseUntil: leaseFuture, Actor: "worker", UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("directed claim: %v", err)
	}
	if !ok || directed.TaskID != "APP-1-T0001" {
		t.Fatalf("directed claim = %s/%v, want APP-1-T0001", directed.TaskID, ok)
	}

	// The same directed claim is no-work now that the card is in progress.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		_, ok, err = ClaimTask(ctx, tx, ClaimRequest{
			ProductID: 1, TaskID: "APP-1-T0001", LeaseID: "l_again",
			LeaseUntil: leaseFuture, Actor: "worker", UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("repeat directed claim: %v", err)
	}
	if ok {
		t.Fatal("a directed claim picked a card that is already in progress")
	}

	// Product A never sees product B's card, and vice versa.
	var web Task
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		web, ok, err = ClaimTask(ctx, tx, ClaimRequest{
			ProductID: 2, LeaseID: "l_web", LeaseUntil: leaseFuture,
			Actor: "worker", UpdateTime: leaseTestTime,
		})
		return err
	}); err != nil {
		t.Fatalf("claim product WEB: %v", err)
	}
	if !ok || web.TaskID != "WEB-1-T0001" {
		t.Fatalf("claim product WEB = %s/%v, want WEB-1-T0001", web.TaskID, ok)
	}
}

func TestHeartbeatLeaseGuards(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 2, attempts: 1, leaseID: "l_live", leaseTill: leaseFuture},
		{taskID: "APP-1-T0002", seq: 2, status: 2, attempts: 1, leaseID: "l_past", leaseTill: leasePast},
		{taskID: "APP-1-T0003", seq: 3, status: 1, attempts: 1},
	})

	cases := []struct {
		name       string
		taskID     string
		leaseID    string
		wantOK     bool
		wantReason string
	}{
		{"live lease", "APP-1-T0001", "l_live", true, ""},
		{"wrong lease id", "APP-1-T0001", "l_other", false, "mismatch"},
		{"expired lease", "APP-1-T0002", "l_past", false, "expired"},
		{"not in progress", "APP-1-T0003", "l_live", false, "mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				until string
				ok    bool
			)
			err := st.Tx(ctx, func(tx *sql.Tx) error {
				var err error
				until, ok, err = HeartbeatLease(ctx, tx, HeartbeatRequest{
					TaskID: tc.taskID, LeaseID: tc.leaseID,
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

func TestReclaimExpiredIsSingleShot(t *testing.T) {
	st := openTestStore(t, 1)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededTasks(t, st, projectID, []taskSeed{
		// Expired with budget left → ready, attempt kept.
		{taskID: "APP-1-T0001", seq: 1, status: 2, attempts: 1, max: 3, leaseID: "l_a", leaseTill: leasePast},
		// Expired with the budget used up → failed.
		{taskID: "APP-1-T0002", seq: 2, status: 2, attempts: 3, max: 3, leaseID: "l_b", leaseTill: leasePast},
		// Still live → untouched.
		{taskID: "APP-1-T0003", seq: 3, status: 2, attempts: 1, max: 3, leaseID: "l_c", leaseTill: leaseFuture},
	})

	var reclaimed []ReclaimedTask
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		reclaimed, err = ReclaimExpired(ctx, tx, leaseNow, leaseTestTime)
		return err
	}); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if len(reclaimed) != 2 {
		t.Fatalf("reclaimed = %d rows, want 2: %+v", len(reclaimed), reclaimed)
	}

	byID := map[string]ReclaimedTask{}
	for _, row := range reclaimed {
		byID[row.TaskID] = row
	}
	if got := byID["APP-1-T0001"]; got.Status != 1 || got.AttemptCount != 1 || got.Reason != nil {
		t.Errorf("requeued row = %+v, want status 1, attempt 1, nil reason", got)
	}
	if got := byID["APP-1-T0002"]; got.Status != 5 || got.AttemptCount != 3 ||
		got.Reason == nil || *got.Reason != "attempt 用尽" {
		t.Errorf("exhausted row = %+v, want status 5, attempt 3, reason 用尽", got)
	}

	// The live lease is untouched, the reclaimed ones lost their lease.
	var (
		status1, status2, status3    int
		leaseID1, leaseID2, leaseID3 *string
		until1, until2, until3       *string
		attempt1                     int
	)
	if err := st.Read().QueryRowContext(ctx, `
		SELECT status, attempt_count, lease_id, lease_until FROM tasks WHERE task_id = 'APP-1-T0001'`).
		Scan(&status1, &attempt1, &leaseID1, &until1); err != nil {
		t.Fatalf("read T0001: %v", err)
	}
	if err := st.Read().QueryRowContext(ctx, `
		SELECT status, lease_id, lease_until FROM tasks WHERE task_id = 'APP-1-T0002'`).
		Scan(&status2, &leaseID2, &until2); err != nil {
		t.Fatalf("read T0002: %v", err)
	}
	if err := st.Read().QueryRowContext(ctx, `
		SELECT status, lease_id, lease_until FROM tasks WHERE task_id = 'APP-1-T0003'`).
		Scan(&status3, &leaseID3, &until3); err != nil {
		t.Fatalf("read T0003: %v", err)
	}
	if status1 != 1 || attempt1 != 1 || leaseID1 != nil || until1 != nil {
		t.Errorf("T0001 = status %d attempt %d lease %v/%v", status1, attempt1, leaseID1, until1)
	}
	if status2 != 5 || leaseID2 != nil || until2 != nil {
		t.Errorf("T0002 = status %d lease %v/%v", status2, leaseID2, until2)
	}
	if status3 != 2 || leaseID3 == nil || *leaseID3 != "l_c" || until3 == nil || *until3 != leaseFuture {
		t.Errorf("T0003 = status %d lease %v/%v, want it untouched", status3, leaseID3, until3)
	}

	// A second sweep — the concurrent or repeated case — moves nothing.
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		again, err := ReclaimExpired(ctx, tx, leaseNow, leaseTestTime)
		if err != nil {
			return err
		}
		if len(again) != 0 {
			t.Errorf("second sweep reclaimed %d rows, want 0", len(again))
		}
		return nil
	}); err != nil {
		t.Fatalf("second ReclaimExpired: %v", err)
	}
}
