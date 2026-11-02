package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// setProjectState forces a project into a given status/version, optionally
// with a pre-existing reason, so each branch of the state machine can be
// exercised without walking the whole pipeline.
func (e *projectTestEnv) setProjectState(t *testing.T, projectID string, status, version int, reason *string) {
	t.Helper()
	if _, err := e.store.Write().ExecContext(context.Background(), `
		UPDATE projects SET status = ?, version = ?, reason = ? WHERE project_id = ?`,
		status, version, reason, projectID); err != nil {
		t.Fatalf("set project state: %v", err)
	}
}

type projectState struct {
	Status    int
	Version   int
	Reason    *string
	LastActor *string
}

func (e *projectTestEnv) projectState(t *testing.T, projectID string) projectState {
	t.Helper()
	var state projectState
	if err := e.store.Read().QueryRowContext(context.Background(), `
		SELECT status, version, reason, last_actor FROM projects WHERE project_id = ?`,
		projectID).Scan(&state.Status, &state.Version, &state.Reason, &state.LastActor); err != nil {
		t.Fatalf("read project state: %v", err)
	}
	return state
}

// insertTaskRow inserts one task with an explicit status/version and returns
// its physical id.
func (e *projectTestEnv) insertTaskRow(t *testing.T, projectID, taskID string, seq, status, version int) int64 {
	t.Helper()
	physical := e.physicalProjectID(t, projectID)
	res, err := e.store.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, version, create_time, update_time)
		VALUES (?, ?, ?, ?, '正文', ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		taskID, physical, seq, "任务 "+taskID, status, version)
	if err != nil {
		t.Fatalf("insert task %s: %v", taskID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("insert task id: %v", err)
	}
	return id
}

func (e *projectTestEnv) createProject(t *testing.T, productID, title string) string {
	t.Helper()
	detail, err := e.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     productID,
		Title:         title,
		RequirementMD: "# 需求\n\n正文\n",
		UploadedBy:    "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", title, err)
	}
	return detail.Project.ProjectID
}

func expectInvalidState(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want invalid_state")
	}
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("error = %v, want invalid_state", err)
	}
}

func expectValidationField(t *testing.T, err error, field string) {
	t.Helper()
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	for _, item := range validation.Fields {
		if item.Field == field {
			return
		}
	}
	t.Fatalf("fields = %+v, want %q", validation.Fields, field)
}

func TestSubmitProject(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "登录页改版")
	oldReason := "历史理由"
	env.setProjectState(t, projectID, domain.StatusRequirementDraft, 1, &oldReason)

	result, err := env.service.SubmitProject(context.Background(), projectID, "alice")
	if err != nil {
		t.Fatalf("SubmitProject: %v", err)
	}
	if result.FromStatus != domain.StatusRequirementDraft || result.ToStatus != domain.StatusRequirementReview {
		t.Errorf("edge = %d→%d, want 0→1", result.FromStatus, result.ToStatus)
	}
	if result.Project.Status != domain.StatusRequirementReview {
		t.Errorf("status = %d, want requirement_review", result.Project.Status)
	}
	if result.Project.Reason != "" {
		t.Errorf("reason = %q, want cleared", result.Project.Reason)
	}
	if result.Project.Version != 2 {
		t.Errorf("version = %d, want 2", result.Project.Version)
	}
	if result.Project.LastActor != "alice" {
		t.Errorf("last_actor = %q, want alice", result.Project.LastActor)
	}

	state := env.projectState(t, projectID)
	if state.Status != domain.StatusRequirementReview || state.Version != 2 || state.Reason != nil {
		t.Errorf("persisted state = %+v", state)
	}
	if state.LastActor == nil || *state.LastActor != "alice" {
		t.Errorf("persisted last_actor = %v, want alice", state.LastActor)
	}
}

func TestSubmitProjectRequiresRequirement(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "无需求项目")

	if _, err := env.store.Write().ExecContext(context.Background(),
		`UPDATE projects SET requirement_material_id = NULL WHERE project_id = ?`, projectID); err != nil {
		t.Fatalf("null requirement: %v", err)
	}

	_, err := env.service.SubmitProject(context.Background(), projectID, "alice")
	expectInvalidState(t, err)
	if err.Error() != "请先上传需求文档" {
		t.Errorf("error = %q, want 请先上传需求文档", err)
	}

	state := env.projectState(t, projectID)
	if state.Status != domain.StatusRequirementDraft || state.Version != 1 {
		t.Errorf("state = %+v, want unchanged requirement_draft/1", state)
	}
}

func TestSubmitProjectRequiresSpec(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "无方案项目")
	env.setProjectState(t, projectID, domain.StatusSpecDraft, 3, nil)

	_, err := env.service.SubmitProject(context.Background(), projectID, "alice")
	expectInvalidState(t, err)
	if !strings.Contains(err.Error(), "spec") {
		t.Errorf("error = %q, want it to mention the missing spec", err)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusSpecDraft || state.Version != 3 {
		t.Errorf("state = %+v, want unchanged spec_draft/3", state)
	}

	if _, err := env.service.SubmitSpec(context.Background(), projectID, "cto", nil, "# 方案\n\n正文\n"); err != nil {
		t.Fatalf("SubmitSpec: %v", err)
	}
	result, err := env.service.SubmitProject(context.Background(), projectID, "alice")
	if err != nil {
		t.Fatalf("SubmitProject with a spec: %v", err)
	}
	if result.ToStatus != domain.StatusSpecReview {
		t.Errorf("to = %d, want spec_review", result.ToStatus)
	}
}

func TestDecideProjectStateMachine(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")

	tests := []struct {
		name     string
		status   int
		decision domain.Decision
		reason   string
		want     int
	}{
		{"requirement approve", domain.StatusRequirementReview, domain.DecisionApprove, "", domain.StatusSpecDraft},
		{"requirement reject", domain.StatusRequirementReview, domain.DecisionReject, "缺少验收标准", domain.StatusRequirementDraft},
		{"spec approve", domain.StatusSpecReview, domain.DecisionApprove, "", domain.StatusTasksDraft},
		{"spec reject", domain.StatusSpecReview, domain.DecisionReject, "方案未覆盖会话续期", domain.StatusSpecDraft},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projectID := env.createProject(t, "APP", tc.name)
			oldReason := "旧理由"
			env.setProjectState(t, projectID, tc.status, 7, &oldReason)

			result, err := env.service.Decide(context.Background(), DecideInput{
				Kind:          DecideKindProject,
				ID:            projectID,
				Decision:      tc.decision,
				Reason:        tc.reason,
				ExpectVersion: intPtr(7),
				Actor:         "carol",
			})
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if result.Project.Status != tc.want {
				t.Errorf("status = %s, want %s",
					domain.StatusName(result.Project.Status), domain.StatusName(tc.want))
			}
			if result.Project.Version != 8 {
				t.Errorf("version = %d, want 8", result.Project.Version)
			}
			if result.Project.LastActor != "carol" {
				t.Errorf("last_actor = %q", result.Project.LastActor)
			}
			if tc.decision == domain.DecisionApprove {
				if result.Project.Reason != "" {
					t.Errorf("approved reason = %q, want cleared", result.Project.Reason)
				}
			} else if result.Project.Reason != tc.reason {
				t.Errorf("rejected reason = %q, want %q", result.Project.Reason, tc.reason)
			}
		})
	}
}

func TestDecideProjectGuards(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")

	t.Run("draft cannot decide", func(t *testing.T) {
		projectID := env.createProject(t, "APP", "草稿直接批准")
		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(1), Actor: "x",
		})
		expectInvalidState(t, err)
	})

	t.Run("reject needs reason", func(t *testing.T) {
		projectID := env.createProject(t, "APP", "驳回无理由")
		env.setProjectState(t, projectID, domain.StatusRequirementReview, 1, nil)
		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionReject,
			ExpectVersion: intPtr(1), Actor: "x",
		})
		expectValidationField(t, err, "reason")
	})

	t.Run("invalid decision", func(t *testing.T) {
		projectID := env.createProject(t, "APP", "非法决策")
		env.setProjectState(t, projectID, domain.StatusRequirementReview, 1, nil)
		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindProject, ID: projectID, Decision: "maybe",
			ExpectVersion: intPtr(1), Actor: "x",
		})
		expectValidationField(t, err, "decision")
	})

	t.Run("version mismatch", func(t *testing.T) {
		projectID := env.createProject(t, "APP", "版本不符")
		env.setProjectState(t, projectID, domain.StatusRequirementReview, 3, nil)
		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(9), Actor: "x",
		})
		var versionErr *domain.InvalidVersionError
		if !errors.As(err, &versionErr) {
			t.Fatalf("error = %v, want InvalidVersionError", err)
		}
		if versionErr.Current != 3 || versionErr.Expected != 9 {
			t.Errorf("versions = %d/%d, want 3/9", versionErr.Current, versionErr.Expected)
		}
		if !strings.Contains(err.Error(), "3") {
			t.Errorf("message = %q, want the current version", err)
		}
	})

	t.Run("missing version", func(t *testing.T) {
		projectID := env.createProject(t, "APP", "缺版本")
		env.setProjectState(t, projectID, domain.StatusRequirementReview, 1, nil)
		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionApprove,
			Actor: "x",
		})
		expectValidationField(t, err, "If-Match")
	})
}

func TestTasksReviewApproveReadiesTasks(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "任务就绪")
	env.setProjectState(t, projectID, domain.StatusTasksReview, 4, nil)

	env.insertTaskRow(t, projectID, projectID+"-T0001", 1, domain.TaskBacklog, 1)
	env.insertTaskRow(t, projectID, projectID+"-T0002", 2, domain.TaskBacklog, 1)
	env.insertTaskRow(t, projectID, projectID+"-T0003", 3, domain.TaskDone, 2)

	tasks := NewTaskService(env.store)
	result, err := tasks.ApproveTasks(context.Background(), projectID, "cto")
	if err != nil {
		t.Fatalf("ApproveTasks: %v", err)
	}
	if result.ToStatus != domain.StatusPendingDone {
		t.Errorf("to status = %d, want pending_done", result.ToStatus)
	}
	if result.ReadyTasks != 2 {
		t.Errorf("ready tasks = %d, want 2", result.ReadyTasks)
	}

	var ready, done int64
	for _, query := range []struct {
		status int
		target *int64
	}{
		{domain.TaskReady, &ready},
		{domain.TaskDone, &done},
	} {
		if err := env.store.Read().QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM tasks WHERE project_id = (SELECT id FROM projects WHERE project_id = ?) AND status = ?`,
			projectID, query.status).Scan(query.target); err != nil {
			t.Fatalf("count status %d: %v", query.status, err)
		}
	}
	if ready != 2 {
		t.Errorf("ready tasks = %d, want 2", ready)
	}
	if done != 1 {
		t.Errorf("done tasks = %d, want 1 (untouched)", done)
	}
	if got := env.count(t, `SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE p.project_id = ? AND t.last_actor = 'cto'`, projectID); got != 2 {
		t.Errorf("tasks stamped with the actor = %d, want 2", got)
	}
}

func TestTasksReviewWithoutBacklogIsInvalid(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "空批次")
	env.setProjectState(t, projectID, domain.StatusTasksReview, 2, nil)

	tasks := NewTaskService(env.store)
	_, err := tasks.ApproveTasks(context.Background(), projectID, "cto")
	expectInvalidState(t, err)
	if state := env.projectState(t, projectID); state.Status != domain.StatusTasksReview || state.Version != 2 {
		t.Errorf("state = %+v, want unchanged", state)
	}
}

func TestCancelProjectCancelsUnfinishedTasks(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "取消项目")
	env.setProjectState(t, projectID, domain.StatusTasksDraft, 1, nil)

	env.insertTaskRow(t, projectID, projectID+"-T0001", 1, domain.TaskBacklog, 1)
	env.insertTaskRow(t, projectID, projectID+"-T0002", 2, domain.TaskInProgress, 2)
	env.insertTaskRow(t, projectID, projectID+"-T0003", 3, domain.TaskDone, 3)
	env.insertTaskRow(t, projectID, projectID+"-T0004", 4, domain.TaskCancelled, 2)

	result, err := env.service.CancelProject(context.Background(), projectID, "pm", "")
	if err != nil {
		t.Fatalf("CancelProject: %v", err)
	}
	if result.Project.Status != domain.StatusCancelled {
		t.Errorf("status = %d, want cancelled", result.Project.Status)
	}
	if result.Project.LastActor != "pm" {
		t.Errorf("last_actor = %q", result.Project.LastActor)
	}

	var cancelled, done int64
	if err := env.store.Read().QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ? AND t.status = ?`, projectID, domain.TaskCancelled).Scan(&cancelled); err != nil {
		t.Fatalf("count cancelled: %v", err)
	}
	if err := env.store.Read().QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ? AND t.status = ?`, projectID, domain.TaskDone).Scan(&done); err != nil {
		t.Fatalf("count done: %v", err)
	}
	if cancelled != 3 {
		t.Errorf("cancelled tasks = %d, want 3", cancelled)
	}
	if done != 1 {
		t.Errorf("done tasks = %d, want 1 (untouched)", done)
	}
}

func TestTerminalProjectRejectsEveryAction(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")

	for _, terminal := range []int{domain.StatusCompleted, domain.StatusCancelled} {
		projectID := env.createProject(t, "APP", "终态")
		env.setProjectState(t, projectID, terminal, 2, nil)

		if _, err := env.service.SubmitProject(context.Background(), projectID, "x"); !errors.Is(err, domain.ErrInvalidState) {
			t.Errorf("submit on %s: error = %v", domain.StatusName(terminal), err)
		}
		if _, err := env.service.CancelProject(context.Background(), projectID, "x", ""); !errors.Is(err, domain.ErrInvalidState) {
			t.Errorf("cancel on %s: error = %v", domain.StatusName(terminal), err)
		}
		if _, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(2), Actor: "x",
		}); !errors.Is(err, domain.ErrInvalidState) {
			t.Errorf("decide on %s: error = %v", domain.StatusName(terminal), err)
		}
	}
}

// TestConcurrentProjectDecision races ten approves against the same expected
// version: exactly one must win, the rest must report invalid_version.
func TestConcurrentProjectDecision(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "并发决策")
	env.setProjectState(t, projectID, domain.StatusRequirementReview, 1, nil)

	const workers = 10
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		conflicts int
		other     []error
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := env.service.Decide(context.Background(), DecideInput{
				Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionApprove,
				ExpectVersion: intPtr(1), Actor: "racer",
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrInvalidVersion):
				conflicts++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("successes = %d, want 1", succeeded)
	}
	if conflicts != workers-1 {
		t.Errorf("invalid_version = %d, want %d", conflicts, workers-1)
	}
	if len(other) > 0 {
		t.Errorf("unexpected errors: %v", other)
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusSpecDraft || state.Version != 2 {
		t.Errorf("final state = %+v, want spec_draft/2", state)
	}
}

// --- task half of Decide (HTTP lands in Task-11) ---

func TestDecideTask(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	projectID := env.createProject(t, "APP", "任务验收")

	t.Run("approve", func(t *testing.T) {
		taskID := projectID + "-T0001"
		env.insertTaskRow(t, projectID, taskID, 1, domain.TaskPendingAcceptance, 4)

		result, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindTask, ID: taskID, Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(4), Actor: "qa",
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if result.Task == nil || result.Task.Status != domain.TaskDone {
			t.Fatalf("task = %+v, want done", result.Task)
		}
		if result.Task.Version != 5 || result.Task.LastActor != "qa" {
			t.Errorf("version/actor = %d/%q", result.Task.Version, result.Task.LastActor)
		}
		if result.FromStatus != domain.TaskPendingAcceptance || result.ToStatus != domain.TaskDone {
			t.Errorf("edge = %d→%d", result.FromStatus, result.ToStatus)
		}
	})

	t.Run("reject returns to ready", func(t *testing.T) {
		taskID := projectID + "-T0002"
		env.insertTaskRow(t, projectID, taskID, 2, domain.TaskPendingAcceptance, 1)

		result, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindTask, ID: taskID, Decision: domain.DecisionReject,
			Reason: "报告缺少测试结果", ExpectVersion: intPtr(1), Actor: "qa",
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if result.Task.Status != domain.TaskReady {
			t.Errorf("status = %d, want ready", result.Task.Status)
		}
		if result.Task.Reason != "报告缺少测试结果" {
			t.Errorf("reason = %q", result.Task.Reason)
		}
	})

	t.Run("non-review task is invalid state", func(t *testing.T) {
		taskID := projectID + "-T0003"
		env.insertTaskRow(t, projectID, taskID, 3, domain.TaskInProgress, 1)

		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindTask, ID: taskID, Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(1), Actor: "qa",
		})
		expectInvalidState(t, err)
	})

	t.Run("version mismatch", func(t *testing.T) {
		taskID := projectID + "-T0004"
		env.insertTaskRow(t, projectID, taskID, 4, domain.TaskPendingAcceptance, 2)

		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindTask, ID: taskID, Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(7), Actor: "qa",
		})
		var versionErr *domain.InvalidVersionError
		if !errors.As(err, &versionErr) || versionErr.Current != 2 {
			t.Fatalf("error = %v, want InvalidVersionError with current 2", err)
		}
	})

	t.Run("reject needs reason", func(t *testing.T) {
		taskID := projectID + "-T0005"
		env.insertTaskRow(t, projectID, taskID, 5, domain.TaskPendingAcceptance, 1)

		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindTask, ID: taskID, Decision: domain.DecisionReject,
			ExpectVersion: intPtr(1), Actor: "qa",
		})
		expectValidationField(t, err, "reason")
	})

	t.Run("missing task", func(t *testing.T) {
		_, err := env.service.Decide(context.Background(), DecideInput{
			Kind: DecideKindTask, ID: projectID + "-T9999", Decision: domain.DecisionApprove,
			ExpectVersion: intPtr(1), Actor: "qa",
		})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("error = %v, want not_found", err)
		}
	})
}
