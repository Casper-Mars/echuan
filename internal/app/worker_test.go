package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// workerEnv wires the worker service onto the same real store/project
// environment the claim tests use, with the claim service's TTL as the lease
// duration so both dispatch paths write comparable deadlines.
type workerEnv struct {
	*claimEnv
	workers *WorkerService
}

func newWorkerEnv(t *testing.T) *workerEnv {
	t.Helper()
	return newWorkerEnvWithMetadataLimit(t, defaultWorkerMetadataMaxBytes)
}

func newWorkerEnvWithMetadataLimit(t *testing.T, limit int64) *workerEnv {
	t.Helper()
	e := newClaimEnv(t)
	workers := NewWorkerService(e.store, slog.New(slog.DiscardHandler), WorkerServiceOptions{
		LeaseTTL:         e.claims.LeaseTTL(),
		WorkerTTL:        45 * time.Second,
		MetadataMaxBytes: limit,
		WorkspaceDir:     filepath.Join(e.dataDir, "workspaces"),
	})
	workers.SetClock(e.clock)
	return &workerEnv{claimEnv: e, workers: workers}
}

func (e *workerEnv) heartbeat(t *testing.T, in HeartbeatInput) HeartbeatResult {
	t.Helper()
	result, err := e.workers.Heartbeat(context.Background(), in)
	if err != nil {
		t.Fatalf("Heartbeat(%+v): %v", in, err)
	}
	return result
}

func (e *workerEnv) heartbeatErr(t *testing.T, in HeartbeatInput) error {
	t.Helper()
	_, err := e.workers.Heartbeat(context.Background(), in)
	if err == nil {
		t.Fatalf("Heartbeat(%+v) succeeded, want an error", in)
	}
	return err
}

// registryRow is the persisted state of one workers row.
type registryRow struct {
	State          string
	CurrentTaskID  *int64
	CurrentLeaseID *string
	Metadata       string
	FirstSeen      string
	LastSeen       string
}

func (e *workerEnv) workerRow(t *testing.T, workerID string) registryRow {
	t.Helper()
	var row registryRow
	if err := e.store.Read().QueryRowContext(context.Background(), `
		SELECT state, current_task_id, current_lease_id, metadata_json, first_seen, last_seen
		  FROM workers WHERE worker_id = ?`, workerID).
		Scan(&row.State, &row.CurrentTaskID, &row.CurrentLeaseID, &row.Metadata,
			&row.FirstSeen, &row.LastSeen); err != nil {
		t.Fatalf("read worker %s: %v", workerID, err)
	}
	return row
}

func (e *workerEnv) taskLastActor(t *testing.T, taskID string) string {
	t.Helper()
	var actor *string
	if err := e.store.Read().QueryRowContext(context.Background(),
		`SELECT last_actor FROM tasks WHERE task_id = ?`, taskID).Scan(&actor); err != nil {
		t.Fatalf("read last_actor of %s: %v", taskID, err)
	}
	if actor == nil {
		return ""
	}
	return *actor
}

func (e *workerEnv) physicalTaskID(t *testing.T, taskID string) int64 {
	t.Helper()
	var id int64
	if err := e.store.Read().QueryRowContext(context.Background(),
		`SELECT id FROM tasks WHERE task_id = ?`, taskID).Scan(&id); err != nil {
		t.Fatalf("lookup physical id of %s: %v", taskID, err)
	}
	return id
}

// TestHeartbeatRegistersWorker covers the first call on an empty database: an
// id is minted, the row is created, nothing is claimed and commands is an empty
// array rather than nil.
func TestHeartbeatRegistersWorker(t *testing.T) {
	e := newWorkerEnv(t)

	result := e.heartbeat(t, HeartbeatInput{})
	workerID := result.Worker.WorkerID
	if !strings.HasPrefix(workerID, "w_") || len(workerID) != 28 {
		t.Fatalf("worker_id = %q, want w_ + 26 Crockford characters", workerID)
	}
	if result.Worker.CurrentTaskID != nil {
		t.Errorf("current_task_id = %v, want nil", *result.Worker.CurrentTaskID)
	}
	if result.Commands == nil || len(result.Commands) != 0 {
		t.Errorf("commands = %#v, want an empty non-nil slice", result.Commands)
	}
	if result.Worker.State != domain.WorkerStateIdle {
		t.Errorf("state = %q, want idle", result.Worker.State)
	}
	if result.Worker.Presence != domain.PresenceActive {
		t.Errorf("presence = %q, want active", result.Worker.Presence)
	}
	if string(result.Worker.Metadata) != "{}" {
		t.Errorf("metadata = %s, want the column default {}", result.Worker.Metadata)
	}
	if !result.Worker.FirstSeen.Equal(e.clock.Now()) || !result.Worker.LastSeen.Equal(e.clock.Now()) {
		t.Errorf("first/last seen = %s/%s, want %s",
			result.Worker.FirstSeen, result.Worker.LastSeen, e.clock.Now())
	}

	row := e.workerRow(t, workerID)
	if row.State != domain.WorkerStateIdle || row.CurrentTaskID != nil || row.CurrentLeaseID != nil {
		t.Errorf("persisted row = %+v, want idle without a pointer", row)
	}

	// The second call with the same id is a plain heartbeat: one row, not two.
	again := e.heartbeat(t, HeartbeatInput{WorkerID: workerID})
	if again.Worker.WorkerID != workerID {
		t.Errorf("worker_id changed to %q", again.Worker.WorkerID)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM workers`); n != 1 {
		t.Errorf("worker rows = %d, want 1", n)
	}
}

// TestHeartbeatRejectsUnknownWorker covers an id the registry never minted.
func TestHeartbeatRejectsUnknownWorker(t *testing.T) {
	e := newWorkerEnv(t)

	err := e.heartbeatErr(t, HeartbeatInput{WorkerID: "w_01JBC9Z3K4QW8V0M2X7Y5N6P8R"})
	if !errors.Is(err, domain.ErrWorkerUnknown) {
		t.Fatalf("error = %v, want ErrWorkerUnknown", err)
	}
	var unknown *domain.WorkerUnknownError
	if !errors.As(err, &unknown) || unknown.WorkerID != "w_01JBC9Z3K4QW8V0M2X7Y5N6P8R" {
		t.Errorf("error = %#v, want WorkerUnknownError with the id", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM workers`); n != 0 {
		t.Errorf("worker rows = %d, want none (a rejected heartbeat must not register)", n)
	}
}

// TestHeartbeatDispatchesFirstCard covers the idle → dispatch path: one card
// leased, attempt counted, actor recorded, pointer bound, payload complete.
func TestHeartbeatDispatchesFirstCard(t *testing.T) {
	e := newWorkerEnv(t)
	projectID := e.newProductProject(t, "APP", "心跳派单")
	taskID := e.readyTask(t, projectID, "实现心跳", 10)

	result := e.heartbeat(t, HeartbeatInput{Actor: "worker-1"})
	if len(result.Commands) != 1 {
		t.Fatalf("commands = %#v, want exactly one", result.Commands)
	}
	command := result.Commands[0]
	if command.Kind != CommandRunTask {
		t.Fatalf("kind = %q, want %q", command.Kind, CommandRunTask)
	}
	if command.Task == nil || command.Lease == nil || command.Context == nil {
		t.Fatalf("command payload incomplete: %#v", command)
	}
	if command.Task.ID != taskID || command.Task.ProjectID != projectID {
		t.Errorf("task = %s/%s, want %s/%s", command.Task.ID, command.Task.ProjectID, taskID, projectID)
	}
	if command.Task.Title != "实现心跳" || command.Task.Attempt != 1 || command.Task.Priority != 10 {
		t.Errorf("task payload = %+v", *command.Task)
	}
	if command.Context.Product.ID != "APP" || command.Context.Product.GitURL == "" {
		t.Errorf("context product = %+v", command.Context.Product)
	}
	if want := filepath.Join(e.dataDir, "workspaces", projectID); command.Context.Workdir != want {
		t.Errorf("workdir = %q, want %q", command.Context.Workdir, want)
	}
	if result.Worker.CurrentTaskID == nil || *result.Worker.CurrentTaskID != taskID {
		t.Errorf("current_task_id = %v, want %s", result.Worker.CurrentTaskID, taskID)
	}
	if result.Worker.State != domain.WorkerStateBusy {
		t.Errorf("state = %q, want busy after a dispatch", result.Worker.State)
	}

	row := e.task(t, taskID)
	if row.Status != domain.TaskInProgress {
		t.Errorf("status = %d, want 2", row.Status)
	}
	if row.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", row.AttemptCount)
	}
	if row.LeaseID == nil || *row.LeaseID != command.Lease.LeaseID {
		t.Errorf("persisted lease_id = %v, want %s", row.LeaseID, command.Lease.LeaseID)
	}
	if row.LeaseUntil == nil || *row.LeaseUntil != formatLeaseTime(command.Lease.Until) {
		t.Errorf("persisted lease_until = %v, want %s", row.LeaseUntil, formatLeaseTime(command.Lease.Until))
	}
	if actor := e.taskLastActor(t, taskID); actor != "worker-1" {
		t.Errorf("last_actor = %q, want worker-1", actor)
	}

	registry := e.workerRow(t, result.Worker.WorkerID)
	if registry.State != domain.WorkerStateBusy || registry.CurrentTaskID == nil {
		t.Fatalf("registry row = %+v, want busy with a pointer", registry)
	}
	if *registry.CurrentTaskID != e.physicalTaskID(t, taskID) {
		t.Errorf("pointer = %d, want the physical id of %s", *registry.CurrentTaskID, taskID)
	}
	if registry.CurrentLeaseID == nil || *registry.CurrentLeaseID != command.Lease.LeaseID {
		t.Errorf("pointer lease = %v, want %s", registry.CurrentLeaseID, command.Lease.LeaseID)
	}
}

// TestHeartbeatBusyRenewsLeaseOnly covers a worker that keeps reporting busy:
// the lease moves every time, the card is never dispatched twice and the
// attempt counter stays where the single dispatch left it.
func TestHeartbeatBusyRenewsLeaseOnly(t *testing.T) {
	e := newWorkerEnv(t)
	projectID := e.newProductProject(t, "APP", "续租")
	taskID := e.readyTask(t, projectID, "长任务", 0)

	first := e.heartbeat(t, HeartbeatInput{State: domain.WorkerStateBusy})
	if len(first.Commands) != 0 {
		t.Fatalf("a busy worker without a card got %#v, want nothing", first.Commands)
	}

	// Drive the first dispatch from idle, then heartbeat busy five times.
	workerID := first.Worker.WorkerID
	dispatched := e.heartbeat(t, HeartbeatInput{WorkerID: workerID})
	if len(dispatched.Commands) != 1 {
		t.Fatalf("commands = %#v, want the queued card", dispatched.Commands)
	}
	previous := *e.task(t, taskID).LeaseUntil

	for i := 0; i < 5; i++ {
		e.clock.Advance(10 * time.Second)
		result := e.heartbeat(t, HeartbeatInput{WorkerID: workerID, State: domain.WorkerStateBusy})
		if len(result.Commands) != 0 {
			t.Fatalf("renewal %d returned commands %#v, want none", i, result.Commands)
		}
		if result.Worker.CurrentTaskID == nil || *result.Worker.CurrentTaskID != taskID {
			t.Fatalf("renewal %d lost the pointer: %v", i, result.Worker.CurrentTaskID)
		}
		row := e.task(t, taskID)
		if row.LeaseUntil == nil || *row.LeaseUntil <= previous {
			t.Fatalf("renewal %d: lease_until = %v, want later than %s", i, row.LeaseUntil, previous)
		}
		if want := formatLeaseTime(e.clock.Now().Add(90 * time.Second)); *row.LeaseUntil != want {
			t.Errorf("renewal %d: lease_until = %s, want %s", i, *row.LeaseUntil, want)
		}
		previous = *row.LeaseUntil
	}
	if row := e.task(t, taskID); row.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 (renewals never count as attempts)", row.AttemptCount)
	}
	if row := e.task(t, taskID); row.Status != domain.TaskInProgress {
		t.Errorf("status = %d, want in_progress", row.Status)
	}
}

// TestHeartbeatIdleDoesNotRenewAndReclaimClearsPointer covers the idle-while-
// holding case and the expiry that follows it: no renewal, then the reclaim
// returns the card to the queue and releases the pointer.
func TestHeartbeatIdleDoesNotRenewAndReclaimClearsPointer(t *testing.T) {
	e := newWorkerEnv(t)
	projectID := e.newProductProject(t, "APP", "空闲不续租")
	taskID := e.readyTask(t, projectID, "会过期", 0)

	dispatched := e.heartbeat(t, HeartbeatInput{})
	workerID := dispatched.Worker.WorkerID
	if len(dispatched.Commands) != 1 {
		t.Fatalf("commands = %#v, want the queued card", dispatched.Commands)
	}
	before := *e.task(t, taskID).LeaseUntil

	e.clock.Advance(5 * time.Second)
	idle := e.heartbeat(t, HeartbeatInput{WorkerID: workerID, State: domain.WorkerStateIdle})
	if len(idle.Commands) != 0 {
		t.Errorf("commands = %#v, want none while still holding a card", idle.Commands)
	}
	if idle.Worker.CurrentTaskID == nil || *idle.Worker.CurrentTaskID != taskID {
		t.Errorf("current_task_id = %v, want %s", idle.Worker.CurrentTaskID, taskID)
	}
	if after := *e.task(t, taskID).LeaseUntil; after != before {
		t.Errorf("lease_until = %s, want %s (an idle heartbeat must not renew)", after, before)
	}

	// Past the TTL the lease is dead; the next heartbeat reclaims it. It asks
	// as busy, so it must not immediately take the card again.
	e.clock.Advance(86 * time.Second)
	reclaimed := e.heartbeat(t, HeartbeatInput{WorkerID: workerID, State: domain.WorkerStateBusy})
	if len(reclaimed.Commands) != 0 {
		t.Errorf("commands = %#v, want none for a busy worker", reclaimed.Commands)
	}
	if reclaimed.Worker.CurrentTaskID != nil {
		t.Errorf("current_task_id = %v, want nil after the reclaim", *reclaimed.Worker.CurrentTaskID)
	}
	row := e.task(t, taskID)
	if row.Status != domain.TaskReady {
		t.Errorf("status = %d, want 1 (reclaimed)", row.Status)
	}
	if row.LeaseID != nil || row.LeaseUntil != nil {
		t.Errorf("lease = %v/%v, want it cleared", row.LeaseID, row.LeaseUntil)
	}
	if registry := e.workerRow(t, workerID); registry.CurrentTaskID != nil || registry.CurrentLeaseID != nil {
		t.Errorf("registry pointer = %v/%v, want it cleared", registry.CurrentTaskID, registry.CurrentLeaseID)
	}

	// The reclaimed card is claimable again, with its attempt history intact.
	again := e.heartbeat(t, HeartbeatInput{WorkerID: workerID, State: domain.WorkerStateIdle})
	if len(again.Commands) != 1 || again.Commands[0].Task.ID != taskID {
		t.Fatalf("commands = %#v, want %s again", again.Commands, taskID)
	}
	if again.Commands[0].Task.Attempt != 2 {
		t.Errorf("attempt = %d, want 2 (reclaim keeps the history)", again.Commands[0].Task.Attempt)
	}
}

// TestHeartbeatBusyWithoutCardGetsNothing proves a busy declaration is not a
// request for work: the ready card stays in the queue.
func TestHeartbeatBusyWithoutCardGetsNothing(t *testing.T) {
	e := newWorkerEnv(t)
	projectID := e.newProductProject(t, "APP", "忙碌不派单")
	taskID := e.readyTask(t, projectID, "不该被拿走", 0)

	result := e.heartbeat(t, HeartbeatInput{State: domain.WorkerStateBusy})
	if len(result.Commands) != 0 {
		t.Fatalf("commands = %#v, want none", result.Commands)
	}
	if result.Worker.State != domain.WorkerStateBusy {
		t.Errorf("state = %q, want busy", result.Worker.State)
	}
	if result.Worker.CurrentTaskID != nil {
		t.Errorf("current_task_id = %v, want nil", *result.Worker.CurrentTaskID)
	}
	row := e.task(t, taskID)
	if row.Status != domain.TaskReady || row.AttemptCount != 0 {
		t.Errorf("task = status %d attempt %d, want it untouched", row.Status, row.AttemptCount)
	}
}

// TestHeartbeatDispatchesNextCardAfterReport covers the report → next-card
// hand-off: the pointer to the reported card is cleared and the same heartbeat
// leases the following card.
func TestHeartbeatDispatchesNextCardAfterReport(t *testing.T) {
	e := newWorkerEnv(t)
	projectID := e.newProductProject(t, "APP", "交报告后派下一张")
	first := e.readyTask(t, projectID, "第一张", 10)
	second := e.readyTask(t, projectID, "第二张", 5)

	dispatched := e.heartbeat(t, HeartbeatInput{Actor: "worker-1"})
	workerID := dispatched.Worker.WorkerID
	if len(dispatched.Commands) != 1 || dispatched.Commands[0].Task.ID != first {
		t.Fatalf("first dispatch = %#v, want %s", dispatched.Commands, first)
	}

	// Simulate the report: pending_acceptance with the lease cleared, exactly
	// what store.CompleteTaskReport writes.
	forceTaskState(t, e.projectTestEnv, first, domain.TaskPendingAcceptance, 1, nil, nil, nil)

	result := e.heartbeat(t, HeartbeatInput{WorkerID: workerID, State: domain.WorkerStateIdle})
	if len(result.Commands) != 1 {
		t.Fatalf("commands = %#v, want the next card", result.Commands)
	}
	if result.Commands[0].Task.ID != second {
		t.Errorf("task = %s, want %s", result.Commands[0].Task.ID, second)
	}
	if result.Worker.CurrentTaskID == nil || *result.Worker.CurrentTaskID != second {
		t.Errorf("current_task_id = %v, want %s", result.Worker.CurrentTaskID, second)
	}
	if row := e.task(t, second); row.Status != domain.TaskInProgress || row.AttemptCount != 1 {
		t.Errorf("second task = status %d attempt %d, want 2/1", row.Status, row.AttemptCount)
	}
	if row := e.task(t, first); row.Status != domain.TaskPendingAcceptance {
		t.Errorf("reported task status = %d, want 3", row.Status)
	}
	registry := e.workerRow(t, workerID)
	if registry.CurrentTaskID == nil || *registry.CurrentTaskID != e.physicalTaskID(t, second) {
		t.Errorf("pointer = %v, want the physical id of %s", registry.CurrentTaskID, second)
	}
}

// TestHeartbeatConcurrentExactlyOneDispatch is the core guarantee of §4.1: 20
// heartbeats of the same worker race for one card and exactly one gets it.
func TestHeartbeatConcurrentExactlyOneDispatch(t *testing.T) {
	e := newWorkerEnv(t)

	// Register while the queue is empty, so the racing calls are the only ones
	// that can take the card.
	workerID := e.heartbeat(t, HeartbeatInput{}).Worker.WorkerID
	projectID := e.newProductProject(t, "APP", "并发心跳")
	taskID := e.readyTask(t, projectID, "只派一次", 0)

	const rounds = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []HeartbeatResult
		errs    []error
		start   = make(chan struct{})
	)
	wg.Add(rounds)
	for i := 0; i < rounds; i++ {
		go func() {
			defer wg.Done()
			<-start
			result, err := e.workers.Heartbeat(context.Background(), HeartbeatInput{
				WorkerID: workerID,
				State:    domain.WorkerStateIdle,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			results = append(results, result)
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("concurrent heartbeats failed: %v", errs)
	}
	if len(results) != rounds {
		t.Fatalf("results = %d, want %d", len(results), rounds)
	}
	dispatches := 0
	for _, result := range results {
		if result.Commands == nil {
			t.Fatalf("commands = nil, want an empty slice")
		}
		dispatches += len(result.Commands)
		for _, command := range result.Commands {
			if command.Task.ID != taskID {
				t.Errorf("dispatched %s, want %s", command.Task.ID, taskID)
			}
		}
	}
	if dispatches != 1 {
		t.Fatalf("dispatches = %d, want exactly 1", dispatches)
	}
	if row := e.task(t, taskID); row.Status != domain.TaskInProgress || row.AttemptCount != 1 {
		t.Errorf("task = status %d attempt %d, want 2/1", row.Status, row.AttemptCount)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM workers WHERE worker_id = ? AND current_task_id IS NOT NULL`, workerID); n != 1 {
		t.Errorf("workers holding a card = %d, want 1", n)
	}
}

// TestHeartbeatMetadataValidation covers the metadata contract: a JSON object
// within the cap is accepted, everything else is rejected with the documented
// error type.
func TestHeartbeatMetadataValidation(t *testing.T) {
	objectOfSize := func(n int) []byte {
		// {"pad":"…"} is 10 bytes of scaffolding.
		return []byte(`{"pad":"` + strings.Repeat("a", n-10) + `"}`)
	}

	t.Run("object at 4100 bytes is accepted", func(t *testing.T) {
		e := newWorkerEnv(t)
		doc := objectOfSize(4100)
		if len(doc) != 4100 {
			t.Fatalf("fixture = %d bytes, want 4100", len(doc))
		}
		result := e.heartbeat(t, HeartbeatInput{Metadata: doc})
		if string(result.Worker.Metadata) != string(doc) {
			t.Errorf("metadata = %s, want the supplied document", result.Worker.Metadata)
		}
		if row := e.workerRow(t, result.Worker.WorkerID); row.Metadata != string(doc) {
			t.Errorf("persisted metadata differs from the request (%d bytes)", len(row.Metadata))
		}
	})

	t.Run("object at 9000 bytes is too large", func(t *testing.T) {
		e := newWorkerEnv(t)
		err := e.heartbeatErr(t, HeartbeatInput{Metadata: objectOfSize(9000)})
		if !errors.Is(err, domain.ErrTooLarge) {
			t.Fatalf("error = %v, want ErrTooLarge", err)
		}
		var tooLarge *domain.TooLargeError
		if !errors.As(err, &tooLarge) {
			t.Fatalf("error = %T, want TooLargeError", err)
		}
		if tooLarge.Field != "metadata" || tooLarge.Limit != defaultWorkerMetadataMaxBytes || tooLarge.Actual != 9000 {
			t.Errorf("too large = %+v", tooLarge)
		}
	})

	cases := map[string][]byte{
		"array":     []byte(`[{"a":1}]`),
		"string":    []byte(`"hello"`),
		"number":    []byte(`42`),
		"null":      []byte(`null`),
		"truncated": []byte(`{"a":`),
		"trailing":  []byte(`{} {}`),
	}
	for name, doc := range cases {
		t.Run(name+" is not an object", func(t *testing.T) {
			e := newWorkerEnv(t)
			err := e.heartbeatErr(t, HeartbeatInput{Metadata: doc})
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if len(validation.Fields) != 1 || validation.Fields[0].Field != "metadata" {
				t.Errorf("details = %+v, want metadata", validation.Fields)
			}
		})
	}

	t.Run("invalid state", func(t *testing.T) {
		e := newWorkerEnv(t)
		err := e.heartbeatErr(t, HeartbeatInput{State: "sleeping"})
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("error = %v, want ValidationError", err)
		}
		if len(validation.Fields) != 1 || validation.Fields[0].Field != "state" {
			t.Errorf("details = %+v, want state", validation.Fields)
		}
	})

	t.Run("blank metadata means not supplied", func(t *testing.T) {
		e := newWorkerEnv(t)
		doc := []byte(`{"host":"mac","slot":3}`)
		first := e.heartbeat(t, HeartbeatInput{Metadata: doc})
		second := e.heartbeat(t, HeartbeatInput{WorkerID: first.Worker.WorkerID, Metadata: []byte("   ")})
		if string(second.Worker.Metadata) != string(doc) {
			t.Errorf("metadata = %s, want the document kept by the first call", second.Worker.Metadata)
		}
		if row := e.workerRow(t, first.Worker.WorkerID); row.Metadata != string(doc) {
			t.Errorf("persisted metadata = %s, want it kept", row.Metadata)
		}
	})
}

// TestHeartbeatMetadataLimitIsConfigurable proves the cap comes from the
// service options rather than a hard-coded constant.
func TestHeartbeatMetadataLimitIsConfigurable(t *testing.T) {
	e := newWorkerEnvWithMetadataLimit(t, 64)
	if _, err := e.workers.Heartbeat(context.Background(), HeartbeatInput{
		Metadata: []byte(`{"pad":"` + strings.Repeat("a", 64) + `"}`),
	}); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge for a 74 byte document with a 64 byte cap", err)
	}

	result := e.heartbeat(t, HeartbeatInput{Metadata: []byte(`{"ok":true}`)})
	if string(result.Worker.Metadata) != `{"ok":true}` {
		t.Errorf("metadata = %s", result.Worker.Metadata)
	}
}
