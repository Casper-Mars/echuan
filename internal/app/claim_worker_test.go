package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// This file covers Task-06: a pull-mode claim that names a worker, the report
// clearing the pointer, and the 30s worker maintenance statements.

// registerWorker registers one idle worker on an empty queue and returns its id.
func (e *workerEnv) registerWorker(t *testing.T) string {
	t.Helper()
	return e.heartbeat(t, HeartbeatInput{}).Worker.WorkerID
}

// claimWorker runs a pull-mode claim bound to workerID.
func (e *claimEnv) claimWorker(t *testing.T, workerID string) (*ClaimResult, error) {
	t.Helper()
	return e.claims.Claim(context.Background(), ClaimInput{
		Product:  "APP",
		WorkerID: workerID,
		Actor:    "worker-1",
	})
}

// TestClaimWithWorkerBindsPointer covers the happy path of §4.2: the card is
// leased and the registry points at it with the same lease id.
func TestClaimWithWorkerBindsPointer(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	projectID := e.newProductProject(t, "APP", "带 worker 领取")
	taskID := e.readyTask(t, projectID, "绑定指针", 0)

	result, err := e.claimWorker(t, workerID)
	if err != nil {
		t.Fatalf("Claim with worker: %v", err)
	}
	if result == nil || result.Task.ID != taskID {
		t.Fatalf("Claim = %+v, want %s", result, taskID)
	}

	row := e.workerRow(t, workerID)
	if row.State != domain.WorkerStateBusy {
		t.Errorf("state = %q, want busy", row.State)
	}
	if row.CurrentTaskID == nil || *row.CurrentTaskID != e.physicalTaskID(t, taskID) {
		t.Fatalf("current_task_id = %v, want the physical id of %s", row.CurrentTaskID, taskID)
	}
	if row.CurrentLeaseID == nil || *row.CurrentLeaseID != result.Lease.LeaseID {
		t.Errorf("current_lease_id = %v, want %s", row.CurrentLeaseID, result.Lease.LeaseID)
	}
}

// TestClaimWithUnknownWorker proves an unregistered id is rejected before any
// card is taken.
func TestClaimWithUnknownWorker(t *testing.T) {
	e := newWorkerEnv(t)
	projectID := e.newProductProject(t, "APP", "未知 worker")
	taskID := e.readyTask(t, projectID, "不该被领走", 0)

	_, err := e.claimWorker(t, "w_00000000000000000000000000")
	var unknown *domain.WorkerUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("error = %v, want WorkerUnknownError", err)
	}
	if row := e.task(t, taskID); row.Status != domain.TaskReady || row.AttemptCount != 0 {
		t.Errorf("task = status %d attempt %d, want it untouched", row.Status, row.AttemptCount)
	}
}

// TestClaimWithBusyWorker proves occupancy is decided by the pointer: a worker
// holding a card cannot lease another one, even when a card is available.
func TestClaimWithBusyWorker(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	projectID := e.newProductProject(t, "APP", "忙碌 worker")
	first := e.readyTask(t, projectID, "第一张", 10)
	second := e.readyTask(t, projectID, "第二张", 0)

	claimed, err := e.claimWorker(t, workerID)
	if err != nil || claimed == nil || claimed.Task.ID != first {
		t.Fatalf("first claim = %+v/%v, want %s", claimed, err, first)
	}

	_, err = e.claimWorker(t, workerID)
	var busy *domain.WorkerBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("second claim error = %v, want WorkerBusyError", err)
	}

	row := e.workerRow(t, workerID)
	if row.CurrentTaskID == nil || *row.CurrentTaskID != e.physicalTaskID(t, first) {
		t.Errorf("pointer = %v, want it to stay on %s", row.CurrentTaskID, first)
	}
	if task := e.task(t, second); task.Status != domain.TaskReady || task.AttemptCount != 0 {
		t.Errorf("second task = status %d attempt %d, want it untouched", task.Status, task.AttemptCount)
	}
}

// TestClaimWithWorkerNoWorkLeavesPointerFree covers the 204 case: asking on an
// empty queue must not bind the worker to anything.
func TestClaimWithWorkerNoWorkLeavesPointerFree(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	e.newProductProject(t, "APP", "空队列")

	result, err := e.claimWorker(t, workerID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if result != nil {
		t.Fatalf("Claim = %+v, want no work", result)
	}
	if row := e.workerRow(t, workerID); row.CurrentTaskID != nil || row.CurrentLeaseID != nil {
		t.Errorf("pointer = %v/%v, want it left free", row.CurrentTaskID, row.CurrentLeaseID)
	}
}

// TestClaimConcurrentSameWorkerSingleWinner is the worker-side concurrency
// guarantee: 20 claims of one worker for two available cards yield exactly one
// binding, and every loser is worker_busy (the card must not be stolen).
func TestClaimConcurrentSameWorkerSingleWinner(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	projectID := e.newProductProject(t, "APP", "并发同一 worker")
	e.readyTask(t, projectID, "第一张", 10)
	second := e.readyTask(t, projectID, "第二张", 0)

	const attempts = 20
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		winners  []*ClaimResult
		busyErrs int
		other    []error
		start    = make(chan struct{})
	)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			<-start
			result, err := e.claims.Claim(context.Background(), ClaimInput{
				Product: "APP", WorkerID: workerID, Actor: "worker-1",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && result != nil:
				winners = append(winners, result)
			case err != nil && errors.Is(err, domain.ErrWorkerBusy):
				busyErrs++
			case err != nil:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(other) != 0 {
		t.Fatalf("unexpected errors: %v", other)
	}
	if len(winners) != 1 || busyErrs != attempts-1 {
		t.Fatalf("winners = %d, busy = %d, want 1/%d", len(winners), busyErrs, attempts-1)
	}
	row := e.workerRow(t, workerID)
	if row.CurrentTaskID == nil || *row.CurrentTaskID != e.physicalTaskID(t, winners[0].Task.ID) {
		t.Errorf("pointer = %v, want the winner %s", row.CurrentTaskID, winners[0].Task.ID)
	}
	if task := e.task(t, second); task.Status != domain.TaskReady {
		t.Errorf("second task status = %d, want ready (no second binding)", task.Status)
	}
}

// TestReportClearsPointerAndFreesWorker covers acceptance 4: the pointer is
// cleared by the report itself, so the very next claim of the same worker can
// take the following card.
func TestReportClearsPointerAndFreesWorker(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	projectID := e.newProductProject(t, "APP", "交报告后清指针")
	first := e.readyTask(t, projectID, "第一张", 10)
	second := e.readyTask(t, projectID, "第二张", 0)

	claimed, err := e.claimWorker(t, workerID)
	if err != nil || claimed == nil || claimed.Task.ID != first {
		t.Fatalf("claim = %+v/%v, want %s", claimed, err, first)
	}

	// The report path shares the fake clock with the claim path, so the lease
	// is still live when it runs.
	tasks := NewTaskService(e.store)
	tasks.SetMaterialWriter(e.writer)
	tasks.now = e.clock.Now
	if _, err := tasks.ReportTask(context.Background(), ReportInput{
		TaskID:    first,
		LeaseID:   claimed.Lease.LeaseID,
		SummaryMD: "# 报告\n\n做完了\n",
		Actor:     "worker-1",
	}); err != nil {
		t.Fatalf("ReportTask: %v", err)
	}

	row := e.workerRow(t, workerID)
	if row.CurrentTaskID != nil || row.CurrentLeaseID != nil || row.State != domain.WorkerStateIdle {
		t.Errorf("pointer/state = %v/%v/%q, want them cleared to idle",
			row.CurrentTaskID, row.CurrentLeaseID, row.State)
	}

	next, err := e.claimWorker(t, workerID)
	if err != nil {
		t.Fatalf("claim after report: %v", err)
	}
	if next == nil || next.Task.ID != second {
		t.Fatalf("claim after report = %+v, want %s", next, second)
	}
}

// TestReclaimClearsPointerOnAllPaths proves the three reclaim entry points
// share one implementation: claim, heartbeat and the background reclaimer all
// release the worker pointer of the card they expired.
func TestReclaimClearsPointerOnAllPaths(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		e := newWorkerEnv(t)
		workerID := e.registerWorker(t)
		projectID := e.newProductProject(t, "APP", "claim 回收")
		taskID := e.readyTask(t, projectID, "回收卡", 0)
		if claimed, err := e.claimWorker(t, workerID); err != nil || claimed == nil {
			t.Fatalf("claim = %+v/%v", claimed, err)
		}
		e.clock.Advance(91 * time.Second)

		if again, err := e.claims.Claim(context.Background(), ClaimInput{Product: "APP"}); err != nil || again == nil {
			t.Fatalf("claim after expiry = %+v/%v, want the reclaimed card", again, err)
		}
		if row := e.workerRow(t, workerID); row.CurrentTaskID != nil || row.CurrentLeaseID != nil {
			t.Errorf("pointer = %v/%v, want cleared", row.CurrentTaskID, row.CurrentLeaseID)
		}
		if task := e.task(t, taskID); task.Status != domain.TaskInProgress || task.AttemptCount != 2 {
			t.Errorf("task = status %d attempt %d, want 2/2", task.Status, task.AttemptCount)
		}
	})

	t.Run("heartbeat", func(t *testing.T) {
		e := newWorkerEnv(t)
		workerID := e.registerWorker(t)
		projectID := e.newProductProject(t, "APP", "心跳回收")
		e.readyTask(t, projectID, "回收卡", 0)
		if claimed, err := e.claimWorker(t, workerID); err != nil || claimed == nil {
			t.Fatalf("claim = %+v/%v", claimed, err)
		}
		e.clock.Advance(91 * time.Second)

		result := e.heartbeat(t, HeartbeatInput{WorkerID: workerID, State: domain.WorkerStateBusy})
		if len(result.Commands) != 0 {
			t.Errorf("commands = %#v, want none for a busy worker", result.Commands)
		}
		if row := e.workerRow(t, workerID); row.CurrentTaskID != nil || row.CurrentLeaseID != nil {
			t.Errorf("pointer = %v/%v, want cleared", row.CurrentTaskID, row.CurrentLeaseID)
		}
	})

	t.Run("reclaimer", func(t *testing.T) {
		e := newWorkerEnv(t)
		workerID := e.registerWorker(t)
		projectID := e.newProductProject(t, "APP", "后台回收")
		e.readyTask(t, projectID, "回收卡", 0)
		if claimed, err := e.claimWorker(t, workerID); err != nil || claimed == nil {
			t.Fatalf("claim = %+v/%v", claimed, err)
		}
		e.clock.Advance(91 * time.Second)

		reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), time.Minute)
		reclaimer.SetClock(e.clock)
		if n, err := reclaimer.Reclaim(context.Background()); err != nil || n != 1 {
			t.Fatalf("Reclaim = %d/%v, want 1", n, err)
		}
		if row := e.workerRow(t, workerID); row.CurrentTaskID != nil || row.CurrentLeaseID != nil {
			t.Errorf("pointer = %v/%v, want cleared", row.CurrentTaskID, row.CurrentLeaseID)
		}
	})
}

// TestReclaimerMaintenancePrunesOnlyStaleIdleWorkers covers the retention
// rules: with worker_retention=1s an offline worker without a card is deleted,
// while an offline worker holding a card and an online worker are kept.
func TestReclaimerMaintenancePrunesOnlyStaleIdleWorkers(t *testing.T) {
	e := newWorkerEnv(t)

	staleID := e.registerWorker(t) // t0, no card
	e.clock.Advance(10 * time.Second)

	projectID := e.newProductProject(t, "APP", "worker 巡检")
	e.readyTask(t, projectID, "被持有", 0)
	dispatched := e.heartbeat(t, HeartbeatInput{})
	if len(dispatched.Commands) != 1 {
		t.Fatalf("commands = %#v, want the queued card", dispatched.Commands)
	}
	busyID := dispatched.Worker.WorkerID

	e.clock.Advance(10 * time.Second)
	onlineID := e.heartbeat(t, HeartbeatInput{}).Worker.WorkerID // t0+20, idle

	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), time.Minute)
	reclaimer.SetClock(e.clock)
	reclaimer.SetWorkerRetention(time.Second)
	reclaimer.sweep(context.Background())

	if n := e.count(t, `SELECT COUNT(*) FROM workers WHERE worker_id = ?`, staleID); n != 0 {
		t.Errorf("stale idle worker rows = %d, want 0 (pruned)", n)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM workers WHERE worker_id = ?`, busyID); n != 1 {
		t.Errorf("busy worker rows = %d, want 1 (kept while holding a card)", n)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM workers WHERE worker_id = ?`, onlineID); n != 1 {
		t.Errorf("online worker rows = %d, want 1 (kept)", n)
	}
	row := e.workerRow(t, busyID)
	if row.CurrentTaskID == nil {
		t.Error("busy worker pointer was cleared, want it untouched")
	}
}

// TestReclaimerMaintenanceClearsDanglingPointer covers the other maintenance
// statement: a pointer to a card that left in_progress is released even when
// the worker itself is online.
func TestReclaimerMaintenanceClearsDanglingPointer(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	projectID := e.newProductProject(t, "APP", "悬空指针")
	taskID := e.readyTask(t, projectID, "已完成", 0)
	if claimed, err := e.claimWorker(t, workerID); err != nil || claimed == nil {
		t.Fatalf("claim = %+v/%v", claimed, err)
	}

	// The card is accepted elsewhere; the worker pointer is now dangling.
	forceTaskState(t, e.projectTestEnv, taskID, domain.TaskDone, 1, nil, nil, nil)

	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), time.Minute)
	reclaimer.SetClock(e.clock)
	reclaimer.SetWorkerRetention(time.Hour)
	reclaimer.sweep(context.Background())

	row := e.workerRow(t, workerID)
	if row.CurrentTaskID != nil || row.CurrentLeaseID != nil || row.State != domain.WorkerStateIdle {
		t.Errorf("pointer/state = %v/%v/%q, want them cleared to idle",
			row.CurrentTaskID, row.CurrentLeaseID, row.State)
	}
}

// TestReclaimerWorkerRetentionDefault pins the fallback: a non-positive value
// keeps the documented 1h window instead of deleting every idle worker.
func TestReclaimerWorkerRetentionDefault(t *testing.T) {
	e := newWorkerEnv(t)
	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), time.Minute)
	if got := reclaimer.WorkerRetention(); got != time.Hour {
		t.Errorf("default retention = %s, want 1h", got)
	}
	reclaimer.SetWorkerRetention(0)
	if got := reclaimer.WorkerRetention(); got != time.Hour {
		t.Errorf("retention after 0 = %s, want 1h", got)
	}
	reclaimer.SetWorkerRetention(time.Second)
	if got := reclaimer.WorkerRetention(); got != time.Second {
		t.Errorf("retention = %s, want 1s", got)
	}
}

// TestReclaimerRunPrunesStaleIdleWorker proves the maintenance statements are
// wired into the loop itself (startup sweep included), not only reachable from
// a test helper.
func TestReclaimerRunPrunesStaleIdleWorker(t *testing.T) {
	e := newWorkerEnv(t)
	workerID := e.registerWorker(t)
	e.clock.Advance(2 * time.Second)

	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), 10*time.Millisecond)
	reclaimer.SetClock(e.clock)
	reclaimer.SetWorkerRetention(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reclaimer.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if e.count(t, `SELECT COUNT(*) FROM workers WHERE worker_id = ?`, workerID) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reclaimer did not prune the stale idle worker within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
	reclaimer.Stop()
}
