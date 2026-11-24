package v3

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// countDanglingTaskWorkers is AC8's invariant as a query: no leased task may
// name a worker_id that has no registry row.
func countDanglingTaskWorkers(t *testing.T, st *storedb.Store) int {
	t.Helper()
	var n int
	if err := st.Read().QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		  FROM tasks t
		 WHERE t.worker_id IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM workers w WHERE w.worker_id = t.worker_id)`).Scan(&n); err != nil {
		t.Fatalf("count dangling task→worker pointers: %v", err)
	}
	return n
}

// TestClaimTaskRejectsUnregisteredWorker pins the transaction guard: the worker
// projection must match a registry row, otherwise the whole claim is rolled
// back so a card is never leased to an id no listing, heartbeat or prune can
// see (issue #165 §2 AC7, E-5).
func TestClaimTaskRejectsUnregisteredWorker(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "未注册", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_ghost", Kind: enum.Dev, Title: "幽灵", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Dev, WorkerID: "w_ghost", LeaseID: "l_ghost", Now: testNow, LeaseUntil: testUntil,
	})
	if !errors.Is(err, ErrWorkerNotRegistered) {
		t.Fatalf("ClaimTask(unregistered) = %v, want ErrWorkerNotRegistered", err)
	}

	// Everything the transaction touched was rolled back: the card is still
	// ready with no holder, no claimed event was appended, and the invariant
	// query stays at 0.
	task, err := GetTask(ctx, st.Read(), "t_ghost")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Ready || task.WorkerID != nil || task.LeaseID != nil {
		t.Errorf("task after refused claim = status %s, worker %v, lease %v; want ready/nil/nil",
			task.Status, task.WorkerID, task.LeaseID)
	}
	events, err := ListEvents(ctx, st.Read(), "t_ghost", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, event := range events {
		if event.Event == enum.EventClaimed {
			t.Errorf("claimed event persisted after the rollback: %+v", event)
		}
	}
	if n := countDanglingTaskWorkers(t, st); n != 0 {
		t.Fatalf("dangling task→worker pointers = %d, want 0", n)
	}

	// One heartbeat (the same UpsertWorker the heartbeat endpoint calls) puts the
	// id in the registry and the very same claim succeeds.
	seedWorker(t, st, "w_ghost", testNow)
	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Dev, WorkerID: "w_ghost", LeaseID: "l_ghost", Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("ClaimTask after registration: %v", err)
	}
	if n := countDanglingTaskWorkers(t, st); n != 0 {
		t.Fatalf("dangling task→worker pointers after claim = %d, want 0", n)
	}
}

// TestClaimAndPruneNeverLeaveDanglingPointers races claims with the retention
// sweep: the sweep deletes idle registry rows, so a claim that started before
// the delete must fail instead of leasing the card to the vanished id (AC8).
func TestClaimAndPruneNeverLeaveDanglingPointers(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "并发清理", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	const workers = 8
	const tasks = 40
	for i := 0; i < workers; i++ {
		seedWorker(t, st, fmt.Sprintf("w_race_%02d", i), testNow)
	}
	for i := 0; i < tasks; i++ {
		if _, err := CreateTask(ctx, st, CreateTaskInput{
			ProjectID: project.ProjectID,
			TaskID:    fmt.Sprintf("t_race_%02d", i),
			Kind:      enum.Dev,
			Title:     "race",
			Now:       testNow,
		}); err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		fails []error
	)
	record := func(err error) {
		mu.Lock()
		fails = append(fails, err)
		mu.Unlock()
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// One card per worker: ux_tasks_worker_lease allows a single leased
			// task per worker_id, so a second claim is not part of this race.
			_, err := ClaimTask(ctx, st, ClaimInput{
				Kind:       enum.Dev,
				WorkerID:   fmt.Sprintf("w_race_%02d", i),
				LeaseID:    fmt.Sprintf("l_race_%02d", i),
				Now:        testNow,
				LeaseUntil: testUntil,
			})
			if err != nil && !errors.Is(err, ErrNoWork) && !errors.Is(err, ErrWorkerNotRegistered) {
				record(fmt.Errorf("claim w_race_%02d: %w", i, err))
			}
		}(i)
	}

	// A cutoff far in the future retires every row the claims have not yet
	// occupied, which is exactly the window the transaction guard covers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for k := 0; k < 20; k++ {
			if _, err := PruneStaleWorkers(ctx, st, testUntil); err != nil {
				record(fmt.Errorf("prune %d: %w", k, err))
				return
			}
		}
	}()

	wg.Wait()
	for _, err := range fails {
		t.Fatalf("concurrent claim/prune failed: %v", err)
	}
	if n := countDanglingTaskWorkers(t, st); n != 0 {
		t.Fatalf("dangling task→worker pointers after claim/prune race = %d, want 0", n)
	}
}
