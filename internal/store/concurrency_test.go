package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

// claimOnce runs one claim attempt inside its own write transaction, the same
// way the application service does.
func claimOnce(ctx context.Context, st *Store, req ClaimRequest) (Task, bool, error) {
	var (
		task Task
		ok   bool
	)
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		task, ok, err = ClaimTask(ctx, tx, req)
		return err
	})
	return task, ok, err
}

// TestClaimTaskConcurrentSingleCard is the store-level concurrency guarantee:
// 20 goroutines race for one ready card and exactly one UPDATE returns it.
//
// It is deliberately store-level (no service layer) so a regression in the
// claim SQL's status/lease predicate is pinned independently of the app.
func TestClaimTaskConcurrentSingleCard(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 1},
	})

	const workers = 20
	type outcome struct {
		taskID  string
		leaseID string
		ok      bool
		err     error
	}
	outcomes := make([]outcome, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			leaseID := fmt.Sprintf("l_%016d", i)
			<-start
			task, ok, err := claimOnce(ctx, st, ClaimRequest{
				ProductID:  1,
				LeaseID:    leaseID,
				LeaseUntil: leaseFuture,
				Actor:      "racer",
				UpdateTime: leaseTestTime,
			})
			outcomes[i] = outcome{taskID: task.TaskID, leaseID: leaseID, ok: ok, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, out := range outcomes {
		if out.err != nil {
			t.Fatalf("worker %d: %v", i, out.err)
		}
		if out.ok {
			winners++
			if out.taskID != "APP-1-T0001" {
				t.Errorf("worker %d claimed %q, want APP-1-T0001", i, out.taskID)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}

	var (
		status, attempt int
		leaseID         *string
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT status, attempt_count, lease_id FROM tasks WHERE task_id = 'APP-1-T0001'`).
		Scan(&status, &attempt, &leaseID); err != nil {
		t.Fatalf("read claimed task: %v", err)
	}
	if status != 2 || attempt != 1 {
		t.Errorf("status/attempt = %d/%d, want 2/1", status, attempt)
	}
	var winnerLease string
	for _, out := range outcomes {
		if out.ok {
			winnerLease = out.leaseID
		}
	}
	if leaseID == nil || *leaseID != winnerLease {
		t.Errorf("lease_id = %v, want the winner's %q", leaseID, winnerLease)
	}
}

// TestClaimTaskConcurrentTwentyCards runs 20 workers against 20 ready cards:
// every worker gets exactly one card, and no card is handed out twice.
func TestClaimTaskConcurrentTwentyCards(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	const cards = 20
	seeds := make([]taskSeed, cards)
	want := make(map[string]bool, cards)
	for i := 0; i < cards; i++ {
		taskID := fmt.Sprintf("APP-1-T%04d", i+1)
		seeds[i] = taskSeed{taskID: taskID, seq: i + 1, status: 1}
		want[taskID] = true
	}
	insertSeededTasks(t, st, projectID, seeds)

	type outcome struct {
		taskID string
		ok     bool
		err    error
	}
	outcomes := make([]outcome, cards)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < cards; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			task, ok, err := claimOnce(ctx, st, ClaimRequest{
				ProductID:  1,
				LeaseID:    fmt.Sprintf("l_%016d", i),
				LeaseUntil: leaseFuture,
				Actor:      "pool",
				UpdateTime: leaseTestTime,
			})
			outcomes[i] = outcome{taskID: task.TaskID, ok: ok, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	claimed := make(map[string]int, cards)
	for i, out := range outcomes {
		if out.err != nil {
			t.Fatalf("worker %d: %v", i, out.err)
		}
		if !out.ok {
			t.Errorf("worker %d got no card, want one of the 20", i)
			continue
		}
		claimed[out.taskID]++
	}
	if len(claimed) != cards {
		t.Errorf("distinct claimed cards = %d, want %d", len(claimed), cards)
	}
	for taskID := range want {
		if got := claimed[taskID]; got != 1 {
			t.Errorf("card %s claimed %d times, want 1", taskID, got)
		}
	}

	var (
		inProgress int
		maxAttempt int
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(attempt_count) FROM tasks WHERE project_id = ? AND status = 2`,
		projectID).Scan(&inProgress, &maxAttempt); err != nil {
		t.Fatalf("aggregate tasks: %v", err)
	}
	if inProgress != cards {
		t.Errorf("in_progress = %d, want %d", inProgress, cards)
	}
	if maxAttempt != 1 {
		t.Errorf("max attempt_count = %d, want 1", maxAttempt)
	}
}

// TestClaimTaskDirectedConcurrent races 20 directed claims for the same task
// id: the directed filter must not weaken the single-winner guarantee.
func TestClaimTaskDirectedConcurrent(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 1},
		{taskID: "APP-1-T0002", seq: 2, status: 1},
	})

	const workers = 20
	oks := make([]bool, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, ok, err := claimOnce(ctx, st, ClaimRequest{
				ProductID:  1,
				TaskID:     "APP-1-T0002",
				LeaseID:    fmt.Sprintf("l_%016d", i),
				LeaseUntil: leaseFuture,
				Actor:      "directed",
				UpdateTime: leaseTestTime,
			})
			oks[i], errs[i] = ok, err
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := range oks {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if oks[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("directed winners = %d, want 1", winners)
	}

	var status2 int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT status FROM tasks WHERE task_id = 'APP-1-T0002'`).Scan(&status2); err != nil {
		t.Fatalf("read T0002: %v", err)
	}
	if status2 != 2 {
		t.Errorf("T0002 status = %d, want in_progress", status2)
	}
}

// TestReclaimExpiredConcurrentExactlyOnce runs two sweeps at the same time and
// proves every expired row is moved exactly once: the predicate is part of the
// UPDATE, so the loser matches nothing.
func TestReclaimExpiredConcurrentExactlyOnce(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededTasks(t, st, projectID, []taskSeed{
		{taskID: "APP-1-T0001", seq: 1, status: 2, attempts: 1, max: 3, leaseID: "l_a", leaseTill: leasePast},
		{taskID: "APP-1-T0002", seq: 2, status: 2, attempts: 3, max: 3, leaseID: "l_b", leaseTill: leasePast},
		{taskID: "APP-1-T0003", seq: 3, status: 2, attempts: 1, max: 3, leaseID: "l_c", leaseTill: leaseFuture},
	})

	type sweep struct {
		rows []ReclaimedTask
		err  error
	}
	sweeps := make([]sweep, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			sweeps[i].err = st.Tx(ctx, func(tx *sql.Tx) error {
				var err error
				sweeps[i].rows, err = ReclaimExpired(ctx, tx, leaseNow, leaseTestTime)
				return err
			})
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]int{}
	total := 0
	for i, s := range sweeps {
		if s.err != nil {
			t.Fatalf("sweep %d: %v", i, s.err)
		}
		total += len(s.rows)
		for _, row := range s.rows {
			seen[row.TaskID]++
		}
	}
	if total != 2 {
		t.Errorf("reclaimed rows = %d, want 2 (one per expired task)", total)
	}
	for taskID, count := range seen {
		if count != 1 {
			t.Errorf("task %s reclaimed %d times, want 1", taskID, count)
		}
	}

	var live int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE task_id = 'APP-1-T0003' AND status = 2 AND lease_id = 'l_c'`).
		Scan(&live); err != nil {
		t.Fatalf("read live lease: %v", err)
	}
	if live != 1 {
		t.Error("the live lease was touched by a concurrent sweep")
	}
}
