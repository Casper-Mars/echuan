package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// reviewState is the task-batch rejection slice of one projects row.
type reviewState struct {
	Status     int
	Version    int
	Reason     *string
	RejectedAt *string
	LastActor  *string
}

func readReviewState(t *testing.T, env *projectTestEnv, projectID string) reviewState {
	t.Helper()
	var state reviewState
	if err := env.store.Read().QueryRowContext(context.Background(), `
		SELECT status, version, tasks_review_reason, tasks_review_rejected_at, last_actor
		  FROM projects WHERE project_id = ?`, projectID).
		Scan(&state.Status, &state.Version, &state.Reason, &state.RejectedAt, &state.LastActor); err != nil {
		t.Fatalf("read review state: %v", err)
	}
	return state
}

func (e *projectTestEnv) taskStatuses(t *testing.T, projectID string) map[string]int {
	t.Helper()
	rows, err := e.store.Read().QueryContext(context.Background(),
		`SELECT t.task_id, t.status FROM tasks t JOIN projects p ON p.id = t.project_id
		  WHERE p.project_id = ? ORDER BY t.task_id`, projectID)
	if err != nil {
		t.Fatalf("query task statuses: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var status int
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan task status: %v", err)
		}
		out[id] = status
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("task statuses: %v", err)
	}
	return out
}

func review(t *testing.T, svc *TaskService, projectID string, decision domain.Decision, reason string) (ReviewTasksResult, error) {
	t.Helper()
	return svc.ReviewTasks(context.Background(), ReviewTasksInput{
		ProjectID: projectID,
		Decision:  decision,
		Reason:    reason,
		Actor:     "human:cto",
	})
}

// TestReviewTasksApproveClearsRejection pins the approve branch: every backlog
// card becomes ready, the project lands in pending_done, the objection is
// cleared and the project version advances exactly once.
func TestReviewTasksApproveClearsRejection(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "审批通过", 2)
	svc := NewTaskService(env.store)

	before := readReviewState(t, env, projectID)
	if before.Status != domain.StatusTasksReview {
		t.Fatalf("status = %d, want tasks_review", before.Status)
	}
	if _, err := review(t, svc, projectID, domain.DecisionReject, "拆分不完整"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected := readReviewState(t, env, projectID)
	if rejected.Version != before.Version+1 {
		t.Fatalf("version after reject = %d, want %d", rejected.Version, before.Version+1)
	}

	result, err := review(t, svc, projectID, domain.DecisionApprove, "")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if result.FromStatus != domain.StatusTasksReview || result.ToStatus != domain.StatusPendingDone {
		t.Errorf("edge = %d→%d, want 5→6", result.FromStatus, result.ToStatus)
	}
	if result.ReadyTasks != 2 {
		t.Errorf("ready = %d, want 2", result.ReadyTasks)
	}
	after := readReviewState(t, env, projectID)
	if after.Status != domain.StatusPendingDone {
		t.Errorf("status = %d, want pending_done", after.Status)
	}
	if after.Version != rejected.Version+1 {
		t.Errorf("version = %d, want %d (one bump for approve)", after.Version, rejected.Version+1)
	}
	if after.Reason != nil || after.RejectedAt != nil {
		t.Errorf("rejection = %v/%v, want cleared", after.Reason, after.RejectedAt)
	}
	if after.LastActor == nil || *after.LastActor != "human:cto" {
		t.Errorf("last_actor = %v, want human:cto", after.LastActor)
	}
	if got := env.taskStatuses(t, projectID); len(got) != 2 {
		t.Errorf("tasks = %v, want 2 rows", got)
	} else {
		for id, status := range got {
			if status != domain.TaskReady {
				t.Errorf("%s status = %d, want ready", id, status)
			}
		}
	}
}

// TestReviewTasksRejectKeepsBatchAndOverwrites covers reject and a repeated
// reject: cards and status stay put, the objection is overwritten, and the
// timestamp is parseable and moves forward.
func TestReviewTasksRejectKeepsBatchAndOverwrites(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "驳回", 2)
	svc := NewTaskService(env.store)
	before := readReviewState(t, env, projectID)

	first, err := review(t, svc, projectID, domain.DecisionReject, "  粒度太粗  ")
	if err != nil {
		t.Fatalf("first reject: %v", err)
	}
	if first.FromStatus != domain.StatusTasksReview || first.ToStatus != domain.StatusTasksReview {
		t.Errorf("edge = %d→%d, want 5→5", first.FromStatus, first.ToStatus)
	}
	if first.ReadyTasks != 0 {
		t.Errorf("ready = %d, want 0", first.ReadyTasks)
	}
	if first.Reason != "粒度太粗" {
		t.Errorf("reason = %q, want trimmed text", first.Reason)
	}
	if _, err := time.Parse(time.RFC3339, first.RejectedAt); err != nil {
		t.Errorf("rejected_at %q: %v", first.RejectedAt, err)
	}
	state := readReviewState(t, env, projectID)
	if state.Status != domain.StatusTasksReview || state.Version != before.Version+1 {
		t.Errorf("project = %d/v%d, want tasks_review/v%d", state.Status, state.Version, before.Version+1)
	}
	if state.Reason == nil || *state.Reason != "粒度太粗" || state.RejectedAt == nil {
		t.Errorf("rejection = %v/%v, want recorded", state.Reason, state.RejectedAt)
	}
	for id, status := range env.taskStatuses(t, projectID) {
		if status != domain.TaskBacklog {
			t.Errorf("%s status = %d, want backlog (cards must not move)", id, status)
		}
	}

	second, err := review(t, svc, projectID, domain.DecisionReject, "缺验收标准")
	if err != nil {
		t.Fatalf("second reject: %v", err)
	}
	overwritten := readReviewState(t, env, projectID)
	if overwritten.Version != state.Version+1 {
		t.Errorf("version = %d, want %d", overwritten.Version, state.Version+1)
	}
	if overwritten.Reason == nil || *overwritten.Reason != "缺验收标准" {
		t.Errorf("reason = %v, want the new text", overwritten.Reason)
	}
	if overwritten.RejectedAt == nil || *overwritten.RejectedAt == *state.RejectedAt {
		t.Errorf("rejected_at = %v, want a new timestamp (old %v)", overwritten.RejectedAt, state.RejectedAt)
	}
	if second.Reason != "缺验收标准" {
		t.Errorf("response reason = %q", second.Reason)
	}
}

// TestReviewTasksRejectRequiresReason pins the 400 on a missing or blank
// reason: nothing is written.
func TestReviewTasksRejectRequiresReason(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "缺原因", 1)
	svc := NewTaskService(env.store)
	before := readReviewState(t, env, projectID)

	for _, reason := range []string{"", "   ", "\n\t"} {
		_, err := review(t, svc, projectID, domain.DecisionReject, reason)
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("reason %q: error = %v, want ValidationError", reason, err)
		}
		if len(validation.Fields) != 1 || validation.Fields[0].Field != "reason" {
			t.Errorf("reason %q: fields = %+v, want reason", reason, validation.Fields)
		}
		if after := readReviewState(t, env, projectID); after.Version != before.Version ||
			after.Reason != nil || after.RejectedAt != nil {
			t.Errorf("reason %q: project changed (%+v)", reason, after)
		}
	}
}

// TestReviewTasksApproveWithoutBacklog pins the 409 and the no-write rule.
func TestReviewTasksApproveWithoutBacklog(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "空批次", 1)
	svc := NewTaskService(env.store)
	if _, err := review(t, svc, projectID, domain.DecisionApprove, ""); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	before := readReviewState(t, env, projectID)

	_, err := review(t, svc, projectID, domain.DecisionApprove, "")
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("error = %v, want invalid_state", err)
	}
	if after := readReviewState(t, env, projectID); after.Version != before.Version || after.Status != before.Status {
		t.Errorf("project changed: %+v → %+v", before, after)
	}
}

// TestTaskWritesClearRejection covers the three card write paths: any create,
// patch or delete drops the objection and bumps the project once; a write with
// no objection leaves the project row alone.
func TestTaskWritesClearRejection(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, ids := walkToTasksReview(t, env, "解锁", 2)
	svc := NewTaskService(env.store)
	ctx := context.Background()

	reject := func() reviewState {
		t.Helper()
		if _, err := review(t, svc, projectID, domain.DecisionReject, "请调整"); err != nil {
			t.Fatalf("reject: %v", err)
		}
		state := readReviewState(t, env, projectID)
		if state.Reason == nil {
			t.Fatal("rejection not recorded")
		}
		return state
	}

	assertCleared := func(t *testing.T, before reviewState, action string) {
		t.Helper()
		after := readReviewState(t, env, projectID)
		if after.Reason != nil || after.RejectedAt != nil {
			t.Errorf("%s: rejection = %v/%v, want cleared", action, after.Reason, after.RejectedAt)
		}
		if after.Version != before.Version+1 {
			t.Errorf("%s: version = %d, want %d", action, after.Version, before.Version+1)
		}
	}

	t.Run("patch", func(t *testing.T) {
		before := reject()
		title := "改后的标题"
		if _, err := svc.UpdateTask(ctx, ids[0], UpdateTaskInput{Title: &title}, "human:pm"); err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
		assertCleared(t, before, "patch")
	})

	t.Run("create", func(t *testing.T) {
		before := reject()
		if _, err := svc.CreateTask(ctx, CreateTaskInput{
			ProjectID: projectID,
			Title:     "补一张卡",
			BodyMD:    "# 补一张卡\n",
		}, "agent:cto"); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		assertCleared(t, before, "create")
	})

	t.Run("delete", func(t *testing.T) {
		before := reject()
		added, err := svc.CreateTask(ctx, CreateTaskInput{
			ProjectID: projectID,
			Title:     "待删除卡",
			BodyMD:    "# 待删除卡\n",
		}, "agent:cto")
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		// The create already cleared the objection; reject again so the
		// delete path is the one being measured.
		before = reject()
		if err := svc.DeleteTask(ctx, added.Task.ID, "human:pm"); err != nil {
			t.Fatalf("DeleteTask: %v", err)
		}
		assertCleared(t, before, "delete")
	})

	t.Run("no rejection leaves project untouched", func(t *testing.T) {
		before := readReviewState(t, env, projectID)
		if before.Reason != nil {
			t.Fatalf("precondition: rejection present")
		}
		title := "再改一次"
		if _, err := svc.UpdateTask(ctx, ids[1], UpdateTaskInput{Title: &title}, "human:pm"); err != nil {
			t.Fatalf("UpdateTask: %v", err)
		}
		if after := readReviewState(t, env, projectID); after.Version != before.Version {
			t.Errorf("version = %d, want %d (no rejection to clear)", after.Version, before.Version)
		}
	})
}

// TestReviewTasksConcurrentApprove pins that only one of two concurrent
// approvals applies: the loser sees a conflict and the cards move once.
func TestReviewTasksConcurrentApprove(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "并发审批", 2)
	svc := NewTaskService(env.store)
	before := readReviewState(t, env, projectID)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
		results []error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ReviewTasks(context.Background(), ReviewTasksInput{
				ProjectID: projectID,
				Decision:  domain.DecisionApprove,
				Actor:     "human:cto",
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else {
				results = append(results, err)
			}
		}()
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("successes = %d, want exactly 1 (errors %v)", success, results)
	}
	if len(results) != 1 {
		t.Fatalf("errors = %v, want one conflict", results)
	}
	if err := results[0]; !errors.Is(err, domain.ErrInvalidState) && !errors.Is(err, domain.ErrInvalidVersion) {
		t.Errorf("loser error = %v, want invalid_state or invalid_version", err)
	}
	after := readReviewState(t, env, projectID)
	if after.Version != before.Version+1 {
		t.Errorf("version = %d, want %d (one applied approval)", after.Version, before.Version+1)
	}
	for id, status := range env.taskStatuses(t, projectID) {
		if status != domain.TaskReady {
			t.Errorf("%s status = %d, want ready", id, status)
		}
	}
}

// TestReviewTasksOnPendingDoneBothDirections pins the 6-state behaviour: a card
// added after the first approval can be rejected and then approved again,
// always leaving the project in pending_done.
func TestReviewTasksOnPendingDoneBothDirections(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "待完成双向", 1)
	svc := NewTaskService(env.store)
	ctx := context.Background()

	if _, err := review(t, svc, projectID, domain.DecisionApprove, ""); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	added, err := svc.CreateTask(ctx, CreateTaskInput{
		ProjectID: projectID,
		Title:     "待完成加卡",
		BodyMD:    "# 待完成加卡\n",
	}, "agent:cto")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if added.Task.Status != domain.TaskBacklog {
		t.Fatalf("new card status = %d, want backlog", added.Task.Status)
	}
	beforeReject := readReviewState(t, env, projectID)

	rejected, err := review(t, svc, projectID, domain.DecisionReject, "补充说明")
	if err != nil {
		t.Fatalf("reject in 6: %v", err)
	}
	if rejected.FromStatus != domain.StatusPendingDone || rejected.ToStatus != domain.StatusPendingDone {
		t.Errorf("edge = %d→%d, want 6→6", rejected.FromStatus, rejected.ToStatus)
	}
	state := readReviewState(t, env, projectID)
	if state.Status != domain.StatusPendingDone || state.Reason == nil || *state.Reason != "补充说明" {
		t.Errorf("state = %d/%v, want pending_done with reason", state.Status, state.Reason)
	}
	if state.Version != beforeReject.Version+1 {
		t.Errorf("version = %d, want %d", state.Version, beforeReject.Version+1)
	}
	if got := env.taskStatuses(t, projectID)[added.Task.ID]; got != domain.TaskBacklog {
		t.Errorf("added card = %d, want backlog after reject", got)
	}

	approved, err := review(t, svc, projectID, domain.DecisionApprove, "")
	if err != nil {
		t.Fatalf("approve in 6: %v", err)
	}
	if approved.FromStatus != domain.StatusPendingDone || approved.ToStatus != domain.StatusPendingDone {
		t.Errorf("edge = %d→%d, want 6→6", approved.FromStatus, approved.ToStatus)
	}
	final := readReviewState(t, env, projectID)
	if final.Status != domain.StatusPendingDone || final.Version != state.Version+1 {
		t.Errorf("final = %d/v%d, want pending_done/v%d", final.Status, final.Version, state.Version+1)
	}
	if final.Reason != nil || final.RejectedAt != nil {
		t.Errorf("rejection = %v/%v, want cleared", final.Reason, final.RejectedAt)
	}
	if got := env.taskStatuses(t, projectID)[added.Task.ID]; got != domain.TaskReady {
		t.Errorf("added card = %d, want ready after approve", got)
	}
}

// TestReviewTasksWrongState pins the 409 for both verdicts outside 5/6.
func TestReviewTasksWrongState(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newTasksDraftProject(t, "状态错误")
	svc := NewTaskService(env.store)
	before := readReviewState(t, env, projectID)

	for _, decision := range []domain.Decision{domain.DecisionApprove, domain.DecisionReject} {
		_, err := review(t, svc, projectID, decision, "原因")
		if !errors.Is(err, domain.ErrInvalidState) {
			t.Errorf("%s: error = %v, want invalid_state", decision, err)
		}
		if after := readReviewState(t, env, projectID); after.Version != before.Version ||
			after.Status != before.Status || after.Reason != nil {
			t.Errorf("%s: project changed: %+v", decision, after)
		}
	}
}

// TestReviewTasksInvalidDecision pins the 400 for an unknown verdict and the
// trim of the project id.
func TestReviewTasksInvalidDecision(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID, _ := walkToTasksReview(t, env, "非法结论", 1)
	svc := NewTaskService(env.store)

	_, err := review(t, svc, projectID, domain.Decision("maybe"), "")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	if len(validation.Fields) != 1 || validation.Fields[0].Field != "decision" {
		t.Errorf("fields = %+v, want decision", validation.Fields)
	}

	_, err = svc.ReviewTasks(context.Background(), ReviewTasksInput{
		ProjectID: "  ",
		Decision:  domain.DecisionApprove,
	})
	if !errors.As(err, &validation) || !strings.Contains(validation.Fields[0].Field, "project_id") {
		t.Errorf("blank project_id error = %v, want project_id field", err)
	}
}
