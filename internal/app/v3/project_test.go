package v3

import (
	"context"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

func TestCreateProjectWritesOnlyProject(t *testing.T) {
	now := testClock
	_, st := newTestService(t, &now)
	seedProductOnly(t, st, "APP")
	projects := NewProjectService(st).WithClock(func() time.Time { return now })

	project, err := projects.CreateProject(context.Background(), CreateProjectRequest{
		ProductID: "APP",
		Name:      "登录改造",
		ContextMD: "issue #12",
		Actor:     "cto",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.ProjectID != "APP-1" || project.Status != enum.ProjectOpen {
		t.Errorf("project = %+v", project)
	}

	var tasks int
	if err := st.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if tasks != 0 {
		t.Errorf("tasks = %d, want 0 (project creation must not create work)", tasks)
	}

	if _, err := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP"}); StatusOf(err) != 400 {
		t.Fatalf("missing name = %d (%v), want 400", StatusOf(err), err)
	}
	if _, err := projects.CreateProject(context.Background(), CreateProjectRequest{
		ProductID: "NOPE", Name: "x",
	}); StatusOf(err) != 404 {
		t.Fatalf("missing product = %d (%v), want 404", StatusOf(err), err)
	}
}

func TestCloseProjectRequiresNoActiveTasks(t *testing.T) {
	now := testClock
	tasks, st := newTestService(t, &now)
	projects := NewProjectService(st).WithClock(func() time.Time { return now })
	seedProductOnly(t, st, "APP")
	project, err := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP", Name: "关闭"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	createReadyTask(t, tasks, project.ProjectID, "t_req", enum.Requirement)

	if err := projects.CloseProject(context.Background(), project.ProjectID, false, "cto"); StatusOf(err) != 409 {
		t.Fatalf("close with active task = %d (%v), want 409", StatusOf(err), err)
	}

	if err := projects.CloseProject(context.Background(), project.ProjectID, true, "cto"); err != nil {
		t.Fatalf("force close: %v", err)
	}
	closed, err := projects.GetProject(context.Background(), project.ProjectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if closed.Status != enum.ProjectClosed {
		t.Errorf("status = %s, want closed", closed.Status)
	}
	task, err := tasks.GetTask(context.Background(), "t_req")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Cancelled {
		t.Errorf("task status = %s, want cancelled", task.Status)
	}
	events, err := tasks.ListEvents(context.Background(), "t_req", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Event == enum.EventCancelled {
			found = true
		}
	}
	if !found {
		t.Error("force close did not write a cancelled event")
	}
}

func TestCloseAfterAllTasksDone(t *testing.T) {
	now := testClock
	tasks, st := newTestService(t, &now)
	projects := NewProjectService(st).WithClock(func() time.Time { return now })
	seedProductOnly(t, st, "APP")
	project, _ := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP", Name: "完成"})
	createReadyTask(t, tasks, project.ProjectID, "t_dev", enum.Dev)
	materialID := createMaterial(t, tasks, "m_done")

	claimed, err := tasks.Claim(context.Background(), "w1", enum.Dev)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := tasks.Report(context.Background(), "t_dev", *claimed.LeaseID, materialID); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, err := tasks.Decide(context.Background(), DecideRequest{
		TaskID: "t_dev", Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if err := projects.CloseProject(context.Background(), project.ProjectID, false, "cto"); err != nil {
		t.Fatalf("close after done: %v", err)
	}
}

func TestReopenAllowsNewTasks(t *testing.T) {
	now := testClock
	tasks, st := newTestService(t, &now)
	projects := NewProjectService(st).WithClock(func() time.Time { return now })
	seedProductOnly(t, st, "APP")
	project, _ := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP", Name: "重开"})

	if err := projects.CloseProject(context.Background(), project.ProjectID, false, "cto"); err != nil {
		t.Fatalf("close empty project: %v", err)
	}
	if err := projects.ReopenProject(context.Background(), project.ProjectID, "cto"); err != nil {
		t.Fatalf("ReopenProject: %v", err)
	}
	reopened, err := projects.GetProject(context.Background(), project.ProjectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if reopened.Status != enum.ProjectOpen {
		t.Errorf("status = %s, want open", reopened.Status)
	}
	if _, err := tasks.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: project.ProjectID, TaskID: "t_after_reopen", Kind: enum.Dev, Title: "新任务",
	}); err != nil {
		t.Fatalf("create task after reopen: %v", err)
	}

	if err := projects.ReopenProject(context.Background(), "NOPE-1", "cto"); StatusOf(err) != 404 {
		t.Fatalf("reopen missing = %d (%v), want 404", StatusOf(err), err)
	}
}

func TestUpdateProject(t *testing.T) {
	now := testClock
	_, st := newTestService(t, &now)
	projects := NewProjectService(st).WithClock(func() time.Time { return now })
	seedProductOnly(t, st, "APP")
	project, _ := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP", Name: "旧名"})

	updated, err := projects.UpdateProject(context.Background(), UpdateProjectRequest{
		ProjectID: project.ProjectID, Name: "新名", ContextMD: "新上下文", Actor: "cto",
	})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if updated.Name != "新名" || updated.ContextMD != "新上下文" {
		t.Errorf("updated = %+v", updated)
	}
	if _, err := projects.UpdateProject(context.Background(), UpdateProjectRequest{
		ProjectID: "NOPE-1", Name: "x",
	}); StatusOf(err) != 404 {
		t.Fatalf("update missing = %d (%v), want 404", StatusOf(err), err)
	}
}

func TestListProjectsFilter(t *testing.T) {
	now := testClock
	_, st := newTestService(t, &now)
	projects := NewProjectService(st).WithClock(func() time.Time { return now })
	seedProductOnly(t, st, "APP")
	first, _ := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP", Name: "一"})
	if _, err := projects.CreateProject(context.Background(), CreateProjectRequest{ProductID: "APP", Name: "二"}); err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := projects.CloseProject(context.Background(), first.ProjectID, false, "cto"); err != nil {
		t.Fatalf("close first: %v", err)
	}

	open := enum.ProjectOpen
	openList, err := projects.ListProjects(context.Background(), ProjectFilter{Status: &open})
	if err != nil {
		t.Fatalf("ListProjects open: %v", err)
	}
	if len(openList) != 1 {
		t.Errorf("open projects = %d, want 1", len(openList))
	}
	closed := enum.ProjectClosed
	closedList, err := projects.ListProjects(context.Background(), ProjectFilter{Status: &closed})
	if err != nil {
		t.Fatalf("ListProjects closed: %v", err)
	}
	if len(closedList) != 1 {
		t.Errorf("closed projects = %d, want 1", len(closedList))
	}
}
