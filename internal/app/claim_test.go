package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// fakeClock is a manually advanced platform.Clock, so lease expiry is driven by
// the test instead of by sleeping.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// claimEnv wires the claim service onto the real store/project environment.
type claimEnv struct {
	*projectTestEnv
	tasks  *TaskService
	claims *ClaimService
	clock  *fakeClock
}

func newClaimEnv(t *testing.T) *claimEnv {
	t.Helper()
	return newClaimEnvWithTTL(t, 90*time.Second)
}

func newClaimEnvWithTTL(t *testing.T, ttl time.Duration) *claimEnv {
	t.Helper()
	e := newProjectTestEnv(t)
	clock := newFakeClock()
	claims := NewClaimService(e.store, slog.New(slog.DiscardHandler), ttl,
		filepath.Join(e.dataDir, "workspaces"))
	claims.SetClock(clock)
	return &claimEnv{
		projectTestEnv: e,
		tasks:          NewTaskService(e.store),
		claims:         claims,
		clock:          clock,
	}
}

// newProductProject creates a product and drives one project of it to
// planning — the only status in which cards can be added, and the status a
// project is in when its cards are ready to be claimed.
func (e *claimEnv) newProductProject(t *testing.T, productID, title string) string {
	t.Helper()
	e.createProduct(t, productID)
	detail, err := e.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     productID,
		Title:         title,
		RequirementMD: "# 需求\n\n正文\n",
		UploadedBy:    "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", title, err)
	}
	projectID := detail.Project.ProjectID
	e.toPlanning(t, projectID)
	return projectID
}

// toPlanning accepts the project and approves its spec review, which is the
// production path from draft to planning.
func (e *claimEnv) toPlanning(t *testing.T, projectID string) {
	t.Helper()
	ctx := context.Background()
	if e.projectState(t, projectID).Status == domain.StatusDraft {
		if _, err := e.service.AcceptProject(ctx, projectID, "pm"); err != nil {
			t.Fatalf("AcceptProject(%s): %v", projectID, err)
		}
	}
	version := e.projectState(t, projectID).Version
	if _, err := e.service.Decide(ctx, DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: &version,
		Actor:         "human:pm",
	}); err != nil {
		t.Fatalf("Decide(approve spec) of %s: %v", projectID, err)
	}
}

// readyTask creates one card and moves it to ready, the state the queue hands
// out. The state machine edge is applied through SQL because plan approval
// (the production path) is Task-08's own test surface.
func (e *claimEnv) readyTask(t *testing.T, projectID, title string, priority int, dependsOn ...string) string {
	t.Helper()
	detail, err := e.tasks.CreateTask(context.Background(), CreateTaskInput{
		ProjectID: projectID,
		Title:     title,
		BodyMD:    "# " + title + "\n",
		Priority:  priority,
		DependsOn: dependsOn,
	}, "agent:cto")
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", title, err)
	}
	forceTaskState(t, e.projectTestEnv, detail.Task.ID, domain.TaskReady, 0, nil, nil, nil)
	return detail.Task.ID
}

// taskRow is the persisted state of one task, read back for assertions.
type taskRow struct {
	Status       int
	AttemptCount int
	LeaseID      *string
	LeaseUntil   *string
	Reason       *string
}

func (e *claimEnv) task(t *testing.T, taskID string) taskRow {
	t.Helper()
	var row taskRow
	if err := e.store.Read().QueryRowContext(context.Background(), `
		SELECT status, attempt_count, lease_id, lease_until, reason
		  FROM tasks WHERE task_id = ?`, taskID).
		Scan(&row.Status, &row.AttemptCount, &row.LeaseID, &row.LeaseUntil, &row.Reason); err != nil {
		t.Fatalf("read task %s: %v", taskID, err)
	}
	return row
}

func (e *claimEnv) setMaxAttempts(t *testing.T, taskID string, max int) {
	t.Helper()
	if _, err := e.store.Write().ExecContext(context.Background(),
		`UPDATE tasks SET max_attempts = ? WHERE task_id = ?`, max, taskID); err != nil {
		t.Fatalf("set max_attempts of %s: %v", taskID, err)
	}
}

func (e *claimEnv) mustClaim(t *testing.T, product, taskID string) *ClaimResult {
	t.Helper()
	result, err := e.claims.Claim(context.Background(), ClaimInput{
		Product: product, TaskID: taskID, Actor: "worker-1",
	})
	if err != nil {
		t.Fatalf("Claim(%s, %s): %v", product, taskID, err)
	}
	return result
}

// TestClaimIssuesLeaseAndReportsNoWork covers the happy path and the 204 case.
func TestClaimIssuesLeaseAndReportsNoWork(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "领取")
	taskID := e.readyTask(t, projectID, "实现领取", 10)

	result := e.mustClaim(t, "APP", "")
	if result == nil {
		t.Fatal("Claim returned no work, want the ready card")
	}
	if result.Task.ID != taskID || result.Task.ProjectID != projectID {
		t.Errorf("task = %s/%s, want %s/%s", result.Task.ID, result.Task.ProjectID, taskID, projectID)
	}
	if result.Task.Title != "实现领取" || result.Task.BodyMD != "# 实现领取\n" || result.Task.Priority != 10 {
		t.Errorf("task payload = %+v", result.Task)
	}
	if result.Task.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", result.Task.Attempt)
	}
	if result.Lease.TTL != 90*time.Second {
		t.Errorf("ttl = %s, want 90s", result.Lease.TTL)
	}
	if want := e.clock.Now().Add(90 * time.Second); !result.Lease.Until.Equal(want) {
		t.Errorf("until = %s, want %s", result.Lease.Until, want)
	}
	if !regexp.MustCompile(`^l_[0-9a-f]{16}$`).MatchString(result.Lease.LeaseID) {
		t.Errorf("lease_id = %q, want l_ + 16 hex characters", result.Lease.LeaseID)
	}

	row := e.task(t, taskID)
	if row.Status != domain.TaskInProgress {
		t.Errorf("status = %d, want 2", row.Status)
	}
	if row.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", row.AttemptCount)
	}
	if row.LeaseID == nil || *row.LeaseID != result.Lease.LeaseID {
		t.Errorf("persisted lease_id = %v, want %s", row.LeaseID, result.Lease.LeaseID)
	}
	if row.LeaseUntil == nil || *row.LeaseUntil != formatLeaseTime(result.Lease.Until) {
		t.Errorf("persisted lease_until = %v, want %s", row.LeaseUntil, formatLeaseTime(result.Lease.Until))
	}

	// Same product, no other ready card: no work, and the API would answer 204.
	again, err := e.claims.Claim(context.Background(), ClaimInput{Product: "APP"})
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if again != nil {
		t.Fatalf("second Claim = %+v, want no work", again)
	}

	// Unknown product is a 404, not an empty queue.
	_, err = e.claims.Claim(context.Background(), ClaimInput{Product: "NOPE"})
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) || notFound.Resource != "product" {
		t.Errorf("unknown product error = %v, want NotFoundError(product)", err)
	}

	// Missing product is a 400.
	_, err = e.claims.Claim(context.Background(), ClaimInput{Product: "  "})
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Fields[0].Field != "product" {
		t.Errorf("blank product error = %v, want ValidationError(product)", err)
	}
}

// TestClaimConcurrentSingleWinner is the core concurrency guarantee: 20 workers
// race for one card and exactly one gets it.
func TestClaimConcurrentSingleWinner(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "并发领取")
	taskID := e.readyTask(t, projectID, "只发一次", 0)

	const workers = 20
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []*ClaimResult
		errs    []error
		start   = make(chan struct{})
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			result, err := e.claims.Claim(context.Background(), ClaimInput{Product: "APP", Actor: "worker"})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if result != nil {
				winners = append(winners, result)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("concurrent claims failed: %v", errs)
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %d, want exactly 1", len(winners))
	}
	if winners[0].Task.ID != taskID {
		t.Errorf("winner task = %s, want %s", winners[0].Task.ID, taskID)
	}

	row := e.task(t, taskID)
	if row.Status != domain.TaskInProgress || row.AttemptCount != 1 {
		t.Errorf("status/attempt = %d/%d, want 2/1", row.Status, row.AttemptCount)
	}
	if row.LeaseID == nil || *row.LeaseID != winners[0].Lease.LeaseID {
		t.Errorf("lease_id = %v, want %s", row.LeaseID, winners[0].Lease.LeaseID)
	}
}

// TestClaimRespectsDependencies proves a card is withheld until every
// dependency is done, and that a directed claim never falls back to another
// card.
func TestClaimRespectsDependencies(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "依赖")
	first := e.readyTask(t, projectID, "前置", 0)
	second := e.readyTask(t, projectID, "后继", 0, first)

	result := e.mustClaim(t, "APP", second)
	if result != nil {
		t.Fatalf("directed claim of a blocked card = %+v, want no work", result)
	}
	if row := e.task(t, second); row.Status != domain.TaskReady {
		t.Errorf("blocked card status = %d, want 1 (untouched)", row.Status)
	}

	// Finishing the dependency releases the card.
	forceTaskState(t, e.projectTestEnv, first, domain.TaskDone, 1, nil, nil, nil)
	result = e.mustClaim(t, "APP", second)
	if result == nil || result.Task.ID != second {
		t.Fatalf("claim after dependency done = %+v, want %s", result, second)
	}
}

// TestClaimPriorityOrder checks the queue order priority DESC, seq_no ASC.
func TestClaimPriorityOrder(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "优先级")
	low := e.readyTask(t, projectID, "低", 0)
	high := e.readyTask(t, projectID, "高", 10)
	mid := e.readyTask(t, projectID, "中", 5)

	for i, want := range []string{high, mid, low} {
		result := e.mustClaim(t, "APP", "")
		if result == nil {
			t.Fatalf("claim %d returned no work, want %s", i, want)
		}
		if result.Task.ID != want {
			t.Fatalf("claim %d = %s, want %s", i, result.Task.ID, want)
		}
	}
	if result := e.mustClaim(t, "APP", ""); result != nil {
		t.Fatalf("claim after the queue drained = %+v, want no work", result)
	}
}

// TestClaimByTaskID covers the directed form: it takes the named card even when
// a higher-priority one exists, and never takes a different card.
func TestClaimByTaskID(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "指定领取")
	named := e.readyTask(t, projectID, "指定", 0)
	other := e.readyTask(t, projectID, "其它", 10)

	result := e.mustClaim(t, "APP", named)
	if result == nil || result.Task.ID != named {
		t.Fatalf("directed claim = %+v, want %s", result, named)
	}
	if result := e.mustClaim(t, "APP", named); result != nil {
		t.Fatalf("repeat directed claim = %+v, want no work (must not take another card)", result)
	}
	if row := e.task(t, other); row.Status != domain.TaskReady || row.AttemptCount != 0 {
		t.Errorf("unrelated card = %+v, want untouched", row)
	}

	if result := e.mustClaim(t, "APP", ""); result == nil || result.Task.ID != other {
		t.Fatalf("queue claim = %+v, want %s", result, other)
	}
}

// TestClaimProductIsolation proves a product only ever gets its own cards.
func TestClaimProductIsolation(t *testing.T) {
	e := newClaimEnv(t)
	appProject := e.newProductProject(t, "APP", "APP 项目")
	appTask := e.readyTask(t, appProject, "APP 卡", 0)

	webProject := e.newProductProject(t, "WEB", "WEB 项目")
	webTask := e.readyTask(t, webProject, "WEB 卡", 100)

	result := e.mustClaim(t, "APP", "")
	if result == nil || result.Task.ID != appTask {
		t.Fatalf("APP claim = %+v, want %s", result, appTask)
	}
	if result := e.mustClaim(t, "APP", ""); result != nil {
		t.Fatalf("second APP claim = %+v, want no work (WEB card is not APP's)", result)
	}
	if row := e.task(t, webTask); row.Status != domain.TaskReady {
		t.Errorf("WEB card status = %d, want 1", row.Status)
	}
}

// TestHeartbeatRenewsAndRejectsLease covers the renewal and the lease_lost
// matrix.
func TestHeartbeatRenewsAndRejectsLease(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "心跳")
	taskID := e.readyTask(t, projectID, "心跳卡", 0)

	claimed := e.mustClaim(t, "APP", "")
	if claimed == nil {
		t.Fatal("Claim returned no work")
	}
	first := claimed.Lease.Until

	e.clock.Advance(30 * time.Second)
	renewal, err := e.claims.Heartbeat(context.Background(), taskID, claimed.Lease.LeaseID)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !renewal.Until.After(first) {
		t.Errorf("renewed until = %s, want after %s", renewal.Until, first)
	}
	if want := e.clock.Now().Add(90 * time.Second); !renewal.Until.Equal(want) {
		t.Errorf("renewed until = %s, want %s", renewal.Until, want)
	}
	if renewal.LeaseID != claimed.Lease.LeaseID || renewal.TTL != 90*time.Second {
		t.Errorf("renewal = %+v", renewal)
	}
	row := e.task(t, taskID)
	if row.LeaseUntil == nil || *row.LeaseUntil != formatLeaseTime(renewal.Until) {
		t.Errorf("persisted lease_until = %v, want %s", row.LeaseUntil, formatLeaseTime(renewal.Until))
	}

	// Wrong lease id → lease_lost.
	_, err = e.claims.Heartbeat(context.Background(), taskID, "l_0000000000000000")
	var lost *domain.LeaseLostError
	if !errors.As(err, &lost) || !strings.Contains(lost.Reason, "does not match") {
		t.Errorf("wrong lease error = %v, want LeaseLostError(mismatch)", err)
	}

	// Expired lease → lease_lost, even though the lease id still matches.
	e.clock.Advance(91 * time.Second)
	_, err = e.claims.Heartbeat(context.Background(), taskID, claimed.Lease.LeaseID)
	if !errors.As(err, &lost) || !strings.Contains(lost.Reason, "expired") {
		t.Errorf("expired lease error = %v, want LeaseLostError(expired)", err)
	}

	// Not in progress → lease_lost.
	forceTaskState(t, e.projectTestEnv, taskID, domain.TaskPendingAcceptance, 1, nil, nil, nil)
	_, err = e.claims.Heartbeat(context.Background(), taskID, claimed.Lease.LeaseID)
	if !errors.As(err, &lost) || !strings.Contains(lost.Reason, "not in_progress") {
		t.Errorf("not-in-progress error = %v, want LeaseLostError(status)", err)
	}

	// Missing lease_id → 400.
	_, err = e.claims.Heartbeat(context.Background(), taskID, " ")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Fields[0].Field != "lease_id" {
		t.Errorf("missing lease_id error = %v, want ValidationError(lease_id)", err)
	}

	// Unknown task → 404.
	_, err = e.claims.Heartbeat(context.Background(), "APP-9-T9999", "l_x")
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) || notFound.Resource != "task" {
		t.Errorf("unknown task error = %v, want NotFoundError(task)", err)
	}
}

// TestLazyReclaimRequeuesAndReclaims proves the claim-time reclaim: an expired
// lease returns to ready with the attempt count kept, and the very same claim
// call takes it again.
func TestLazyReclaimRequeuesAndReclaims(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "惰性回收")
	taskID := e.readyTask(t, projectID, "回收卡", 0)

	first := e.mustClaim(t, "APP", "")
	if first == nil {
		t.Fatal("first Claim returned no work")
	}

	e.clock.Advance(91 * time.Second)
	second := e.mustClaim(t, "APP", "")
	if second == nil {
		t.Fatal("claim after expiry returned no work, want the reclaimed card")
	}
	if second.Task.ID != taskID {
		t.Errorf("reclaimed claim = %s, want %s", second.Task.ID, taskID)
	}
	if second.Task.Attempt != 2 {
		t.Errorf("attempt = %d, want 2 (attempt history kept)", second.Task.Attempt)
	}
	if second.Lease.LeaseID == first.Lease.LeaseID {
		t.Error("reclaim reused the old lease id")
	}
	row := e.task(t, taskID)
	if row.Status != domain.TaskInProgress || row.AttemptCount != 2 {
		t.Errorf("status/attempt = %d/%d, want 2/2", row.Status, row.AttemptCount)
	}
}

// TestReclaimClearsLeaseAndKeepsAttempt runs the store-level reclaim through the
// service path and asserts the intermediate ready state.
func TestReclaimClearsLeaseAndKeepsAttempt(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "回收中间态")
	taskID := e.readyTask(t, projectID, "中间态", 0)

	claimed := e.mustClaim(t, "APP", "")
	if claimed == nil {
		t.Fatal("Claim returned no work")
	}
	e.clock.Advance(91 * time.Second)

	// Drive the sweep on its own (the reclaimer's pass) before any claim.
	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), time.Minute)
	reclaimer.SetClock(e.clock)
	n, err := reclaimer.Reclaim(context.Background())
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}

	row := e.task(t, taskID)
	if row.Status != domain.TaskReady {
		t.Errorf("status = %d, want 1 ready", row.Status)
	}
	if row.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", row.AttemptCount)
	}
	if row.LeaseID != nil || row.LeaseUntil != nil {
		t.Errorf("lease = %v/%v, want cleared", row.LeaseID, row.LeaseUntil)
	}

	// A second sweep moves nothing: the reclaim is applied exactly once.
	if n, err := reclaimer.Reclaim(context.Background()); err != nil || n != 0 {
		t.Fatalf("second Reclaim = %d/%v, want 0", n, err)
	}
}

// TestReclaimExhaustedAttemptsFailsTask covers the attempt budget: the second
// expiry of a two-attempt task lands in failed with the documented reason.
func TestReclaimExhaustedAttemptsFailsTask(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "用尽")
	taskID := e.readyTask(t, projectID, "用尽卡", 0)
	e.setMaxAttempts(t, taskID, 2)

	if first := e.mustClaim(t, "APP", ""); first == nil || first.Task.Attempt != 1 {
		t.Fatalf("first claim = %+v, want attempt 1", first)
	}
	e.clock.Advance(91 * time.Second)
	if second := e.mustClaim(t, "APP", ""); second == nil || second.Task.Attempt != 2 {
		t.Fatalf("second claim = %+v, want attempt 2", second)
	}
	e.clock.Advance(91 * time.Second)

	if result := e.mustClaim(t, "APP", ""); result != nil {
		t.Fatalf("claim after attempts exhausted = %+v, want no work", result)
	}

	row := e.task(t, taskID)
	if row.Status != domain.TaskFailed {
		t.Errorf("status = %d, want 5 failed", row.Status)
	}
	if row.AttemptCount != 2 {
		t.Errorf("attempt_count = %d, want 2", row.AttemptCount)
	}
	if row.Reason == nil || *row.Reason != "attempt 用尽" {
		t.Errorf("reason = %v, want 用尽", row.Reason)
	}
	if row.LeaseID != nil || row.LeaseUntil != nil {
		t.Errorf("lease = %v/%v, want cleared", row.LeaseID, row.LeaseUntil)
	}
}

// TestReclaimerRunStopsOnContext proves the background loop sweeps without a
// claim and exits when its context is cancelled, which is what main relies on
// for a clean SIGTERM.
func TestReclaimerRunStopsOnContext(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "巡检")
	taskID := e.readyTask(t, projectID, "巡检卡", 0)

	if claimed := e.mustClaim(t, "APP", ""); claimed == nil {
		t.Fatal("Claim returned no work")
	}
	e.clock.Advance(91 * time.Second)

	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), 10*time.Millisecond)
	reclaimer.SetClock(e.clock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reclaimer.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if e.task(t, taskID).Status == domain.TaskReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reclaimer did not requeue the expired card within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}

	// Stop is idempotent and safe after Run already returned.
	reclaimer.Stop()
	reclaimer.Stop()
}

// TestReclaimerStopWaitsForRun proves Stop (the App.Close path) also ends the
// loop when its context is still live.
func TestReclaimerStopWaitsForRun(t *testing.T) {
	e := newClaimEnv(t)
	reclaimer := NewReclaimer(e.store, slog.New(slog.DiscardHandler), time.Hour)

	done := make(chan struct{})
	go func() {
		reclaimer.Run(context.Background())
		close(done)
	}()

	// Give Run a moment to enter its select loop so Stop really joins a live
	// goroutine instead of winning a race against startup.
	time.Sleep(20 * time.Millisecond)
	reclaimer.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not stop a running reclaimer")
	}
}

// TestClaimContextWithoutSpec covers the documented zero values: a project with
// no spec still claims, with spec_version=0 and spec_digest="".
func TestClaimContextWithoutSpec(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "无方案")
	e.readyTask(t, projectID, "无方案卡", 0)

	result := e.mustClaim(t, "APP", "")
	if result == nil {
		t.Fatal("Claim returned no work")
	}
	if result.Context.SpecVersion != 0 || result.Context.SpecDigest != "" {
		t.Errorf("spec = %d/%q, want 0/\"\"", result.Context.SpecVersion, result.Context.SpecDigest)
	}
	if result.Context.Product.ID != "APP" || result.Context.Product.Name != "APP 产品" ||
		result.Context.Product.GitURL != "git@example.com:APP.git" {
		t.Errorf("product = %+v", result.Context.Product)
	}
	if want := filepath.Join(e.dataDir, "workspaces", projectID); result.Context.Workdir != want {
		t.Errorf("workdir = %q, want %q", result.Context.Workdir, want)
	}
}

// TestClaimContextWithSpec proves the context reports the project's current spec
// version and digest.
func TestClaimContextWithSpec(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProjectIDWithSpec(t)

	result := e.mustClaim(t, "APP", "")
	if result == nil {
		t.Fatal("Claim returned no work")
	}
	if result.Context.SpecVersion != 1 {
		t.Errorf("spec_version = %d, want 1", result.Context.SpecVersion)
	}

	var sha string
	if err := e.store.Read().QueryRowContext(context.Background(), `
		SELECT m.sha256
		  FROM projects p JOIN materials m ON m.id = p.spec_material_id
		 WHERE p.project_id = ?`, projectID).Scan(&sha); err != nil {
		t.Fatalf("read spec sha: %v", err)
	}
	if want := "sha256:" + sha; result.Context.SpecDigest != want {
		t.Errorf("spec_digest = %q, want %q", result.Context.SpecDigest, want)
	}
}

// newProjectIDWithSpec builds a project that carries one spec version and one
// ready card.
func (e *claimEnv) newProjectIDWithSpec(t *testing.T) string {
	t.Helper()
	projectID := e.newSpecReviewProject(t, "有方案")
	if _, err := e.service.SubmitSpec(context.Background(), projectID, "pm", nil, "# 方案\n\n正文\n"); err != nil {
		t.Fatalf("SubmitSpec(%s): %v", projectID, err)
	}
	e.toPlanning(t, projectID)
	e.readyTask(t, projectID, "有方案卡", 0)
	return projectID
}

// TestReclaimLogFields pins the reclaim log contract the design asks for:
// task_id, attempt, to_status and reason.
func TestReclaimLogFields(t *testing.T) {
	e := newClaimEnv(t)
	projectID := e.newProductProject(t, "APP", "回收日志")
	taskID := e.readyTask(t, projectID, "日志卡", 0)
	if claimed := e.mustClaim(t, "APP", ""); claimed == nil {
		t.Fatal("Claim returned no work")
	}
	e.clock.Advance(91 * time.Second)

	var buf bytes.Buffer
	claims := NewClaimService(e.store, slog.New(slog.NewJSONHandler(&buf, nil)), 90*time.Second, "")
	claims.SetClock(e.clock)
	if result, err := claims.Claim(context.Background(), ClaimInput{Product: "APP"}); err != nil || result == nil {
		t.Fatalf("Claim after expiry = %+v/%v, want the reclaimed card", result, err)
	}

	var entry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if decoded["msg"] == "lease expired; task reclaimed" {
			entry = decoded
			break
		}
	}
	if entry == nil {
		t.Fatalf("no reclaim log line; output = %q", buf.String())
	}
	for _, field := range []string{"task_id", "attempt", "to_status", "reason"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("reclaim log missing %q: %v", field, entry)
		}
	}
	if entry["task_id"] != taskID || entry["to_status"] != float64(domain.TaskReady) {
		t.Errorf("reclaim log = %v", entry)
	}
}
