package v3

import (
	"context"
	"errors"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

func TestCreateProjectAndLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")

	project, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP",
		Name:      "登录改造",
		ContextMD: "issue #12；仓库画像见附件",
		Actor:     "cto",
		Now:       testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.ProjectID != "APP-1" {
		t.Errorf("project id = %q, want APP-1", project.ProjectID)
	}
	if project.SeqNo != 1 {
		t.Errorf("seq = %d, want 1", project.SeqNo)
	}
	if project.Status != enum.ProjectOpen {
		t.Errorf("status = %s, want open", project.Status)
	}
	if project.ContextMD == "" {
		t.Error("context_md was dropped")
	}

	second, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP", Name: "第二个", Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject second: %v", err)
	}
	if second.ProjectID != "APP-2" {
		t.Errorf("second project id = %q, want APP-2", second.ProjectID)
	}

	open := enum.ProjectOpen
	list, err := ListProjects(ctx, st.Read(), &open, 0, 10)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("open projects = %d, want 2", len(list))
	}

	if err := UpdateProjectContext(ctx, st, "APP-1", "登录改造 v2", "新上下文", "cto", testNow); err != nil {
		t.Fatalf("UpdateProjectContext: %v", err)
	}
	updated, err := GetProject(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.Name != "登录改造 v2" || updated.ContextMD != "新上下文" {
		t.Errorf("updated = %+v", updated)
	}

	if err := CloseProject(ctx, st, "APP-1", false, "cto", testNow); err != nil {
		t.Fatalf("CloseProject: %v", err)
	}
	closed, err := GetProject(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProject closed: %v", err)
	}
	if closed.Status != enum.ProjectClosed || closed.ClosedAt == nil {
		t.Errorf("closed = %+v", closed)
	}

	if err := ReopenProject(ctx, st, "APP-1", "cto", testNow); err != nil {
		t.Fatalf("ReopenProject: %v", err)
	}
	reopened, err := GetProject(ctx, st.Read(), "APP-1")
	if err != nil {
		t.Fatalf("GetProject reopened: %v", err)
	}
	if reopened.Status != enum.ProjectOpen || reopened.ClosedAt != nil {
		t.Errorf("reopened = %+v", reopened)
	}
}

func TestCloseProjectRequiresNoActiveTasks(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{
		ProductID: "APP", Name: "关闭测试", Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, err = CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID,
		TaskID:    "t_req",
		Kind:      enum.Requirement,
		Title:     "需求",
		Now:       testNow,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := CloseProject(ctx, st, project.ProjectID, false, "cto", testNow); !errors.Is(err, ErrProjectNotEmpty) {
		t.Fatalf("CloseProject error = %v, want ErrProjectNotEmpty", err)
	}

	if err := CloseProject(ctx, st, project.ProjectID, true, "cto", testNow); err != nil {
		t.Fatalf("force CloseProject: %v", err)
	}

	task, err := GetTask(ctx, st.Read(), "t_req")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Cancelled {
		t.Errorf("task status = %s, want cancelled", task.Status)
	}

	events, err := ListEvents(ctx, st.Read(), "t_req", 100)
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
		t.Error("cancelled event missing")
	}
}
