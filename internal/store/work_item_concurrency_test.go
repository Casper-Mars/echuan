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
func claimOnce(ctx context.Context, st *Store, req ClaimRequest) (WorkItem, bool, error) {
	var (
		item WorkItem
		ok   bool
	)
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		item, ok, err = ClaimWorkItem(ctx, tx, req)
		return err
	})
	return item, ok, err
}

// seedRacingWorkers registers n idle workers, each declaring dev.
func seedRacingWorkers(t *testing.T, st *Store, n int, prefix string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			if _, err := UpsertWorker(ctx, tx, WorkerUpsert{
				WorkerID: fmt.Sprintf("%s_%02d", prefix, i),
				State:    "idle",
				Kinds:    []string{"dev"},
				LastSeen: "2026-10-27T00:00:00.000Z", UpdateTime: leaseTestTime,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed racing workers: %v", err)
	}
}

// TestClaimWorkItemConcurrentSingleCard is the store-level concurrency
// guarantee: 20 workers race for one queued card and exactly one UPDATE
// returns it.
//
// It is deliberately store-level (no service layer) so a regression in the
// claim SQL's state/kind/dependency predicate is pinned independently of the
// app.
func TestClaimWorkItemConcurrentSingleCard(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_race", seq: 1},
	})
	seedRacingWorkers(t, st, 20, "w_racer")

	const workers = 20
	type outcome struct {
		workID  string
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
			item, ok, err := claimOnce(ctx, st, ClaimRequest{
				ProductID:  1,
				WorkerID:   fmt.Sprintf("w_racer_%02d", i),
				LeaseID:    leaseID,
				LeaseUntil: leaseFuture,
				UpdateTime: leaseTestTime,
			})
			outcomes[i] = outcome{workID: item.WorkID, leaseID: leaseID, ok: ok, err: err}
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
			if out.workID != "w_race" {
				t.Errorf("worker %d claimed %q, want w_race", i, out.workID)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}

	var (
		state, worker string
		attempt       int
		leaseID       *string
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT state, attempt, lease_id, worker_id FROM work_items WHERE work_id = 'w_race'`).
		Scan(&state, &attempt, &leaseID, &worker); err != nil {
		t.Fatalf("read claimed item: %v", err)
	}
	if state != WorkItemStateLeased || attempt != 1 {
		t.Errorf("state/attempt = %s/%d, want leased/1", state, attempt)
	}
	var winnerLease, winnerWorker string
	for i, out := range outcomes {
		if out.ok {
			winnerLease = out.leaseID
			winnerWorker = fmt.Sprintf("w_racer_%02d", i)
		}
	}
	if leaseID == nil || *leaseID != winnerLease {
		t.Errorf("lease_id = %v, want the winner's %q", leaseID, winnerLease)
	}
	if worker != winnerWorker {
		t.Errorf("worker_id = %q, want the winner's %q", worker, winnerWorker)
	}
}

// TestClaimWorkItemConcurrentTwentyItems runs 20 workers against 20 queued
// items: every worker gets exactly one item, and no item is handed out twice.
func TestClaimWorkItemConcurrentTwentyItems(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	const items = 20
	seeds := make([]workItemSeed, items)
	want := make(map[string]bool, items)
	for i := 0; i < items; i++ {
		workID := fmt.Sprintf("w_%04d", i+1)
		seeds[i] = workItemSeed{workID: workID, seq: i + 1}
		want[workID] = true
	}
	insertSeededWorkItems(t, st, projectID, seeds)
	seedRacingWorkers(t, st, items, "w_pool")

	type outcome struct {
		workID string
		ok     bool
		err    error
	}
	outcomes := make([]outcome, items)
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < items; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			item, ok, err := claimOnce(ctx, st, ClaimRequest{
				ProductID:  1,
				WorkerID:   fmt.Sprintf("w_pool_%02d", i),
				LeaseID:    fmt.Sprintf("l_%016d", i),
				LeaseUntil: leaseFuture,
				UpdateTime: leaseTestTime,
			})
			outcomes[i] = outcome{workID: item.WorkID, ok: ok, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	claimed := make(map[string]int, items)
	for i, out := range outcomes {
		if out.err != nil {
			t.Fatalf("worker %d: %v", i, out.err)
		}
		if !out.ok {
			t.Errorf("worker %d got no item, want one of the %d", i, items)
			continue
		}
		claimed[out.workID]++
	}
	if len(claimed) != items {
		t.Errorf("distinct claimed items = %d, want %d", len(claimed), items)
	}
	for workID := range want {
		if got := claimed[workID]; got != 1 {
			t.Errorf("item %s claimed %d times, want 1", workID, got)
		}
	}

	var (
		leased     int
		maxAttempt int
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(attempt) FROM work_items WHERE project_id = ? AND state = 'leased'`,
		projectID).Scan(&leased, &maxAttempt); err != nil {
		t.Fatalf("aggregate work items: %v", err)
	}
	if leased != items {
		t.Errorf("leased = %d, want %d", leased, items)
	}
	if maxAttempt != 1 {
		t.Errorf("max attempt = %d, want 1", maxAttempt)
	}
}

// TestClaimWorkItemDirectedConcurrent races 20 directed claims for the same
// work id: the directed filter must not weaken the single-winner guarantee.
func TestClaimWorkItemDirectedConcurrent(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_01", seq: 1},
		{workID: "w_02", seq: 2},
	})
	seedRacingWorkers(t, st, 20, "w_directed")

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
				WorkID:     "w_02",
				WorkerID:   fmt.Sprintf("w_directed_%02d", i),
				LeaseID:    fmt.Sprintf("l_%016d", i),
				LeaseUntil: leaseFuture,
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

	var state string
	if err := st.Read().QueryRowContext(ctx,
		`SELECT state FROM work_items WHERE work_id = 'w_02'`).Scan(&state); err != nil {
		t.Fatalf("read w_02: %v", err)
	}
	if state != WorkItemStateLeased {
		t.Errorf("w_02 state = %s, want leased", state)
	}
}

// TestReclaimExpiredConcurrentExactlyOnce runs two sweeps at the same time and
// proves every expired row is moved exactly once: the predicate is part of the
// UPDATE, so the loser matches nothing.
func TestReclaimExpiredConcurrentExactlyOnce(t *testing.T) {
	st := openTestStore(t, 4)
	projectID := seedProjectRow(t, st)
	ctx := context.Background()

	insertSeededWorkItems(t, st, projectID, []workItemSeed{
		{workID: "w_01", seq: 1, state: WorkItemStateLeased, attempts: 1, max: 3,
			workerID: "w_a", leaseID: "l_a", leaseTill: leasePast},
		{workID: "w_02", seq: 2, state: WorkItemStateLeased, attempts: 3, max: 3,
			workerID: "w_b", leaseID: "l_b", leaseTill: leasePast},
		{workID: "w_03", seq: 3, state: WorkItemStateLeased, attempts: 1, max: 3,
			workerID: "w_c", leaseID: "l_c", leaseTill: leaseFuture},
	})

	type sweep struct {
		rows []ReclaimedWorkItem
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
				sweeps[i].rows, err = ReclaimExpiredWorkItems(ctx, tx, leaseNow, leaseTestTime)
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
			seen[row.WorkID]++
		}
	}
	if total != 2 {
		t.Errorf("reclaimed rows = %d, want 2 (one per expired item)", total)
	}
	for workID, count := range seen {
		if count != 1 {
			t.Errorf("item %s reclaimed %d times, want 1", workID, count)
		}
	}

	var live int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM work_items WHERE work_id = 'w_03' AND state = 'leased' AND lease_id = 'l_c'`).
		Scan(&live); err != nil {
		t.Fatalf("read live lease: %v", err)
	}
	if live != 1 {
		t.Error("the live lease was touched by a concurrent sweep")
	}
}
