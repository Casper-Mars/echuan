package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// walkToTasksReview drives a project through the whole human path:
// create → submit → approve → spec → submit → approve → add cards.
func walkToTasksReview(t *testing.T, env *projectTestEnv, title string, cards int) (string, []string) {
	t.Helper()
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", title)
	ctx := context.Background()

	decide := func(decision domain.Decision) {
		version := env.projectState(t, projectID).Version
		if _, err := env.service.Decide(ctx, DecideInput{
			Kind:          DecideKindProject,
			ID:            projectID,
			Decision:      decision,
			ExpectVersion: &version,
			Actor:         "human:pm",
		}); err != nil {
			t.Fatalf("Decide(%s): %v", decision, err)
		}
	}

	if _, err := env.service.SubmitProject(ctx, projectID, "pm"); err != nil {
		t.Fatalf("SubmitProject(requirement): %v", err)
	}
	decide(domain.DecisionApprove)
	if _, err := env.service.SubmitSpec(ctx, projectID, "cto", nil, "# 方案\n\n正文\n"); err != nil {
		t.Fatalf("SubmitSpec: %v", err)
	}
	if _, err := env.service.SubmitProject(ctx, projectID, "pm"); err != nil {
		t.Fatalf("SubmitProject(spec): %v", err)
	}
	decide(domain.DecisionApprove)

	tasks := NewTaskService(env.store)
	ids := make([]string, 0, cards)
	for i := 0; i < cards; i++ {
		detail, err := tasks.CreateTask(ctx, CreateTaskInput{
			ProjectID: projectID,
			Title:     "卡片",
			BodyMD:    "# 卡片\n",
		}, "agent:cto")
		if err != nil {
			t.Fatalf("CreateTask(%d): %v", i, err)
		}
		ids = append(ids, detail.Task.ID)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusTasksReview {
		t.Fatalf("status = %d, want tasks_review after the first card", state.Status)
	}
	return projectID, ids
}

// TestProjectFullChainToCompleted is the 0 → 7 acceptance walk: the first card
// opens the batch review, the batch approval makes both cards ready, accepting
// one keeps the project in pending_done, and abandoning the last card completes
// it.
func TestProjectFullChainToCompleted(t *testing.T) {
	env := newProjectTestEnv(t)
	ctx := context.Background()
	projectID, ids := walkToTasksReview(t, env, "全链路", 2)

	tasks := NewTaskService(env.store)
	if _, err := tasks.ApproveTasks(ctx, projectID, "cto"); err != nil {
		t.Fatalf("ApproveTasks: %v", err)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusPendingDone {
		t.Fatalf("status = %d, want pending_done", state.Status)
	}

	// Card 1: report accepted directly (the report path is covered elsewhere).
	forceTaskState(t, env, ids[0], domain.TaskPendingAcceptance, 1, nil, nil, nil)
	if _, err := tasks.DecideTask(ctx, TaskDecisionInput{
		TaskID:        ids[0],
		Decision:      domain.TaskDecisionAccept,
		ExpectVersion: intPtr(2),
		Actor:         "qa",
	}); err != nil {
		t.Fatalf("DecideTask(accept card 1): %v", err)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusPendingDone {
		t.Errorf("status after one card = %d, want pending_done", state.Status)
	}

	// Card 2: failed → abandoned. This is the last unsettled card, so the same
	// transaction must close the project.
	forceTaskState(t, env, ids[1], domain.TaskFailed, 1, nil, nil, nil)
	result, err := tasks.DecideTask(ctx, TaskDecisionInput{
		TaskID:        ids[1],
		Decision:      domain.TaskDecisionAbandon,
		Reason:        "环境不可用",
		ExpectVersion: intPtr(2),
		Actor:         "qa",
	})
	if err != nil {
		t.Fatalf("DecideTask(abandon card 2): %v", err)
	}
	if result.Detail.Task.Status != domain.TaskCancelled {
		t.Errorf("card status = %d, want cancelled", result.Detail.Task.Status)
	}
	if result.Project == nil {
		t.Fatal("project advance = nil, want pending_done → completed")
	}
	if result.Project.FromStatus != domain.StatusPendingDone || result.Project.ToStatus != domain.StatusCompleted {
		t.Errorf("project edge = %d→%d, want 6→7", result.Project.FromStatus, result.Project.ToStatus)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusCompleted {
		t.Errorf("final status = %d, want completed", state.Status)
	}
}

// TestLastCardAcceptCompletesProject pins the other terminal path: the last
// accepted card closes the project.
func TestLastCardAcceptCompletesProject(t *testing.T) {
	env := newProjectTestEnv(t)
	ctx := context.Background()
	projectID, ids := walkToTasksReview(t, env, "最后一张", 1)

	tasks := NewTaskService(env.store)
	if _, err := tasks.ApproveTasks(ctx, projectID, "cto"); err != nil {
		t.Fatalf("ApproveTasks: %v", err)
	}
	forceTaskState(t, env, ids[0], domain.TaskPendingAcceptance, 1, nil, nil, nil)

	result, err := tasks.DecideTask(ctx, TaskDecisionInput{
		TaskID:        ids[0],
		Decision:      domain.TaskDecisionAccept,
		ExpectVersion: intPtr(2),
		Actor:         "qa",
	})
	if err != nil {
		t.Fatalf("DecideTask(accept): %v", err)
	}
	if result.Project == nil || result.Project.ToStatus != domain.StatusCompleted {
		t.Errorf("project advance = %+v, want completed", result.Project)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusCompleted {
		t.Errorf("status = %d, want completed", state.Status)
	}
}

// TestCancelPendingDoneProject cancels a project whose cards are not settled
// yet: it goes to cancelled(8) and every unfinished card is cancelled too,
// without falsely completing the project.
func TestCancelPendingDoneProject(t *testing.T) {
	env := newProjectTestEnv(t)
	ctx := context.Background()
	projectID, ids := walkToTasksReview(t, env, "待完成取消", 2)

	tasks := NewTaskService(env.store)
	if _, err := tasks.ApproveTasks(ctx, projectID, "cto"); err != nil {
		t.Fatalf("ApproveTasks: %v", err)
	}
	for _, id := range ids {
		forceTaskState(t, env, id, domain.TaskInProgress, 1, nil, nil, nil)
	}

	result, err := env.service.CancelProject(ctx, projectID, "pm", "需求取消")
	if err != nil {
		t.Fatalf("CancelProject: %v", err)
	}
	if result.ToStatus != domain.StatusCancelled {
		t.Errorf("to = %d, want cancelled(8)", result.ToStatus)
	}
	if result.Project.Reason != "需求取消" {
		t.Errorf("reason = %q", result.Project.Reason)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusCancelled {
		t.Errorf("status = %d, want cancelled", state.Status)
	}
}

// TestApproveTasksOnPendingDone pins the 6 → 6 self-loop: a card added while
// the project waits for its umbrella task lands in backlog, the repeated batch
// approval makes it ready without moving the project, and the project still
// completes once every card is settled.
func TestApproveTasksOnPendingDone(t *testing.T) {
	env := newProjectTestEnv(t)
	ctx := context.Background()
	projectID, ids := walkToTasksReview(t, env, "待完成加卡", 1)

	tasks := NewTaskService(env.store)
	if _, err := tasks.ApproveTasks(ctx, projectID, "cto"); err != nil {
		t.Fatalf("ApproveTasks(5→6): %v", err)
	}
	before := env.projectState(t, projectID)
	if before.Status != domain.StatusPendingDone {
		t.Fatalf("status = %d, want pending_done", before.Status)
	}

	added, err := tasks.CreateTask(ctx, CreateTaskInput{
		ProjectID: projectID,
		Title:     "追加卡",
		BodyMD:    "# 追加卡\n",
	}, "agent:cto")
	if err != nil {
		t.Fatalf("CreateTask(pending_done): %v", err)
	}
	if added.Task.Status != domain.TaskBacklog {
		t.Fatalf("new card status = %d, want backlog", added.Task.Status)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusPendingDone {
		t.Fatalf("status after add = %d, want the project to stay in pending_done", state.Status)
	}

	result, err := tasks.ApproveTasks(ctx, projectID, "cto")
	if err != nil {
		t.Fatalf("ApproveTasks(6→6): %v", err)
	}
	if result.FromStatus != domain.StatusPendingDone || result.ToStatus != domain.StatusPendingDone {
		t.Errorf("edge = %d→%d, want 6→6", result.FromStatus, result.ToStatus)
	}
	if result.ReadyTasks != 1 {
		t.Errorf("ready = %d, want 1", result.ReadyTasks)
	}
	after := env.projectState(t, projectID)
	if after.Status != domain.StatusPendingDone {
		t.Errorf("status = %d, want pending_done", after.Status)
	}
	if after.Version != before.Version+1 {
		t.Errorf("version = %d, want %d", after.Version, before.Version+1)
	}
	if after.LastActor == nil || *after.LastActor != "cto" {
		t.Errorf("last_actor = %v, want cto", after.LastActor)
	}

	// The repeated approval with no backlog card is still a conflict.
	if _, err := tasks.ApproveTasks(ctx, projectID, "cto"); !errors.Is(err, domain.ErrInvalidState) {
		t.Errorf("empty re-approval error = %v, want invalid_state", err)
	}

	// Settling the leftover cards closes the project: the first verdict keeps
	// it in pending_done, the second completes it.
	forceTaskState(t, env, added.Task.ID, domain.TaskPendingAcceptance, 1, nil, nil, nil)
	if _, err := tasks.DecideTask(ctx, TaskDecisionInput{
		TaskID:        added.Task.ID,
		Decision:      domain.TaskDecisionAccept,
		ExpectVersion: intPtr(2),
		Actor:         "qa",
	}); err != nil {
		t.Fatalf("DecideTask(accept added card): %v", err)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusPendingDone {
		t.Fatalf("status after one card = %d, want pending_done", state.Status)
	}

	forceTaskState(t, env, ids[0], domain.TaskPendingAcceptance, 1, nil, nil, nil)
	settled, err := tasks.DecideTask(ctx, TaskDecisionInput{
		TaskID:        ids[0],
		Decision:      domain.TaskDecisionAccept,
		ExpectVersion: intPtr(2),
		Actor:         "qa",
	})
	if err != nil {
		t.Fatalf("DecideTask(accept original card): %v", err)
	}
	if settled.Project == nil || settled.Project.ToStatus != domain.StatusCompleted {
		t.Fatalf("project advance = %+v, want completed", settled.Project)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusCompleted {
		t.Errorf("final status = %d, want completed", state.Status)
	}
}
