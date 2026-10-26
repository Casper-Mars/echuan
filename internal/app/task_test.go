package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// newTaskEnv wires the task service onto the existing project test environment
// (real store, real migrations, real state machine).
func newTaskEnv(t *testing.T) (*projectTestEnv, *TaskService) {
	t.Helper()
	e := newProjectTestEnv(t)
	return e, NewTaskService(e.store)
}

func mustCreateTask(t *testing.T, svc *TaskService, projectID, title, body string, priority int, dependsOn ...string) TaskDetail {
	t.Helper()
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		ProjectID: projectID,
		Title:     title,
		BodyMD:    body,
		Priority:  priority,
		DependsOn: dependsOn,
	}, "agent:cto")
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", title, err)
	}
	return detail
}

// forceTaskState writes status/attempt/lease columns directly so the guards can
// be tested without walking the state machine (Task-10 owns claiming).
func forceTaskState(t *testing.T, e *projectTestEnv, taskID string, status, attempt int, leaseID, leaseUntil, reason *string) {
	t.Helper()
	if _, err := e.store.Write().ExecContext(context.Background(), `
		UPDATE tasks SET status = ?, attempt_count = ?, lease_id = ?, lease_until = ?, reason = ?
		 WHERE task_id = ?`, status, attempt, leaseID, leaseUntil, reason, taskID); err != nil {
		t.Fatalf("force task state: %v", err)
	}
}

func TestCreateTaskPersistsBacklogCard(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "任务 CRUD")

	first := mustCreateTask(t, svc, projectID, "实现状态机", "# 实现状态机\n", 10)
	if first.Task.ID != "APP-1-T0001" || first.Task.TaskID != "APP-1-T0001" {
		t.Fatalf("ids = %q/%q, want APP-1-T0001", first.Task.ID, first.Task.TaskID)
	}
	if first.Task.ProjectID != projectID || first.Task.SeqNo != 1 {
		t.Errorf("project/seq = %q/%d", first.Task.ProjectID, first.Task.SeqNo)
	}
	if first.Task.Status != domain.TaskBacklog || first.Task.Version != 1 {
		t.Errorf("status/version = %d/%d, want backlog/1", first.Task.Status, first.Task.Version)
	}
	if first.Task.AttemptCount != 0 || first.Task.MaxAttempts != defaultTaskMaxAttempts {
		t.Errorf("attempts = %d/%d", first.Task.AttemptCount, first.Task.MaxAttempts)
	}
	if first.Task.LastActor != "agent:cto" {
		t.Errorf("last_actor = %q", first.Task.LastActor)
	}
	if first.Task.DependsOn == nil || len(first.Task.DependsOn) != 0 {
		t.Errorf("depends_on = %v, want []", first.Task.DependsOn)
	}
	if first.Report != nil {
		t.Errorf("report = %+v, want nil", first.Report)
	}
	if first.Task.Lease != nil {
		t.Errorf("lease = %+v, want nil", first.Task.Lease)
	}

	second := mustCreateTask(t, svc, projectID, "补测试", "# 补测试\n", 5, "APP-1-T0001")
	if second.Task.ID != "APP-1-T0002" || second.Task.SeqNo != 2 {
		t.Errorf("second = %s/seq %d, want APP-1-T0002/2", second.Task.ID, second.Task.SeqNo)
	}
	if len(second.Task.DependsOn) != 1 || second.Task.DependsOn[0] != "APP-1-T0001" {
		t.Errorf("depends_on = %v, want [APP-1-T0001]", second.Task.DependsOn)
	}
	if edges := e.planDepEdges(t, projectID); len(edges["APP-1-T0002"]) != 1 {
		t.Errorf("persisted edges = %v", edges)
	}

	// The attempt budget comes from configuration.
	svc.SetMaxAttempts(5)
	third := mustCreateTask(t, svc, projectID, "配置上限", "b", 0)
	if third.Task.MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", third.Task.MaxAttempts)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "校验")

	cases := []struct {
		name  string
		input CreateTaskInput
		field string
	}{
		{"blank title", CreateTaskInput{Title: "  ", BodyMD: "b"}, "title"},
		{"long title", CreateTaskInput{Title: strings.Repeat("字", MaxTaskTitleLen+1), BodyMD: "b"}, "title"},
		{"blank body", CreateTaskInput{Title: "t", BodyMD: " \n\t"}, "body_md"},
		{"long body", CreateTaskInput{Title: "t", BodyMD: strings.Repeat("b", MaxTaskBodyBytes+1)}, "body_md"},
		{"priority high", CreateTaskInput{Title: "t", BodyMD: "b", Priority: MaxTaskPriority + 1}, "priority"},
		{"priority low", CreateTaskInput{Title: "t", BodyMD: "b", Priority: MinTaskPriority - 1}, "priority"},
		{"empty dependency", CreateTaskInput{Title: "t", BodyMD: "b", DependsOn: []string{" "}}, "depends_on"},
		{"duplicate dependency", CreateTaskInput{Title: "t", BodyMD: "b", DependsOn: []string{"APP-1-T0001", "APP-1-T0001"}}, "depends_on"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.input.ProjectID = projectID
			_, err := svc.CreateTask(context.Background(), tc.input, "alice")
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			found := false
			for _, field := range validation.Fields {
				if field.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Errorf("fields = %+v, want %q", validation.Fields, tc.field)
			}
		})
	}

	if rows := e.planTaskRows(t, projectID); len(rows) != 0 {
		t.Errorf("persisted tasks = %d, want 0", len(rows))
	}
}

func TestCreateTaskRequiresPlanningProject(t *testing.T) {
	e, svc := newTaskEnv(t)
	e.createProduct(t, "APP")
	projectID := e.createProject(t, "APP", "草稿项目")

	_, err := svc.CreateTask(context.Background(), CreateTaskInput{
		ProjectID: projectID, Title: "t", BodyMD: "b",
	}, "alice")
	var invalidState *domain.InvalidStateError
	if !errors.As(err, &invalidState) {
		t.Fatalf("error = %v, want InvalidStateError", err)
	}

	_, err = svc.CreateTask(context.Background(), CreateTaskInput{
		ProjectID: "APP-404", Title: "t", BodyMD: "b",
	}, "alice")
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want NotFoundError", err)
	}
}

func TestCreateTaskDependencyRules(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "依赖")
	mustCreateTask(t, svc, projectID, "一", "b", 0)
	mustCreateTask(t, svc, projectID, "二", "b", 0)

	// A dependency outside the project is a field error naming depends_on.
	_, err := svc.CreateTask(context.Background(), CreateTaskInput{
		ProjectID: projectID, Title: "三", BodyMD: "b", DependsOn: []string{"APP-1-T0404"},
	}, "alice")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Fields[0].Field != "depends_on" {
		t.Fatalf("missing dependency error = %v, want depends_on field error", err)
	}
	if !strings.Contains(validation.Fields[0].Reason, "does not exist") {
		t.Errorf("reason = %q", validation.Fields[0].Reason)
	}

	// The next card would be T0003; depending on it is a self reference.
	_, err = svc.CreateTask(context.Background(), CreateTaskInput{
		ProjectID: projectID, Title: "三", BodyMD: "b", DependsOn: []string{"APP-1-T0003"},
	}, "alice")
	if !errors.As(err, &validation) || validation.Fields[0].Reason != "must not depend on itself" {
		t.Fatalf("self reference error = %v", err)
	}

	if rows := e.planTaskRows(t, projectID); len(rows) != 2 {
		t.Errorf("persisted tasks = %d, want 2", len(rows))
	}
}

func TestFindTaskCycle(t *testing.T) {
	acyclic := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": nil,
	}
	if cycle := findTaskCycle(acyclic, "A"); cycle != nil {
		t.Errorf("cycle = %v, want nil", cycle)
	}

	cyclic := map[string][]string{
		"A": {"B"},
		"B": {"C"},
		"C": {"A"},
	}
	cycle := findTaskCycle(cyclic, "A")
	if strings.Join(cycle, "→") != "A→B→C→A" {
		t.Errorf("cycle = %v, want A→B→C→A", cycle)
	}

	self := map[string][]string{"A": {"A"}}
	if cycle := findTaskCycle(self, "A"); strings.Join(cycle, "→") != "A→A" {
		t.Errorf("self cycle = %v, want A→A", cycle)
	}
}

func TestGetTaskDetailAndLeaseVisibility(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "详情")
	mustCreateTask(t, svc, projectID, "一", "b", 0)
	mustCreateTask(t, svc, projectID, "二", "# 二\n", 0, "APP-1-T0001")

	detail, err := svc.GetTask(context.Background(), "APP-1-T0002")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.BodyMD != "# 二\n" {
		t.Errorf("body_md = %q", detail.Task.BodyMD)
	}
	if len(detail.Task.DependsOn) != 1 || detail.Task.DependsOn[0] != "APP-1-T0001" {
		t.Errorf("depends_on = %v", detail.Task.DependsOn)
	}
	if detail.Report != nil || detail.Task.Lease != nil {
		t.Errorf("report/lease = %v/%v, want nil/nil", detail.Report, detail.Task.Lease)
	}

	// An unexpired in_progress lease is visible.
	future := "2999-01-01T00:00:00Z"
	leaseID := "lease-1"
	forceTaskState(t, e, "APP-1-T0002", domain.TaskInProgress, 1, &leaseID, &future, nil)
	detail, err = svc.GetTask(context.Background(), "APP-1-T0002")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Lease == nil || detail.Task.Lease.LeaseID != "lease-1" {
		t.Fatalf("lease = %+v, want lease-1", detail.Task.Lease)
	}

	// An expired lease renders as null and is not reclaimed on read.
	past := "2000-01-01T00:00:00Z"
	forceTaskState(t, e, "APP-1-T0002", domain.TaskInProgress, 1, &leaseID, &past, nil)
	detail, err = svc.GetTask(context.Background(), "APP-1-T0002")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Lease != nil {
		t.Errorf("lease = %+v, want nil (expired)", detail.Task.Lease)
	}
	// A lease row on a non-in_progress task is not rendered either.
	forceTaskState(t, e, "APP-1-T0002", domain.TaskReady, 0, &leaseID, &future, nil)
	detail, err = svc.GetTask(context.Background(), "APP-1-T0002")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Lease != nil {
		t.Errorf("lease = %+v, want nil (not in_progress)", detail.Task.Lease)
	}

	if _, err := svc.GetTask(context.Background(), "APP-1-T0404"); err == nil {
		t.Fatal("GetTask(missing) error = nil")
	} else {
		var notFound *domain.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("error = %v, want NotFoundError", err)
		}
	}
}

func TestListTasksPaginationAndFilters(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "列表")
	for i := 0; i < 12; i++ {
		mustCreateTask(t, svc, projectID, "任务", "b", i%3)
	}

	var seen []string
	var after *TaskCursor
	pages := 0
	for {
		page, err := svc.ListTasks(context.Background(), ListTasksInput{
			ProjectID: projectID,
			After:     after,
			Limit:     5,
		})
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		pages++
		for _, item := range page.Items {
			seen = append(seen, item.ID)
		}
		if page.Next == nil {
			break
		}
		after = page.Next
	}
	if pages != 3 || len(seen) != 12 {
		t.Fatalf("pages/items = %d/%d, want 3/12", pages, len(seen))
	}
	unique := map[string]struct{}{}
	for _, id := range seen {
		unique[id] = struct{}{}
	}
	if len(unique) != 12 {
		t.Errorf("distinct tasks = %d, want 12", len(unique))
	}

	// Ordering: priority DESC then seq ASC.
	page, err := svc.ListTasks(context.Background(), ListTasksInput{ProjectID: projectID, Limit: 200})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for i := 1; i < len(page.Items); i++ {
		previous, current := page.Items[i-1], page.Items[i]
		ordered := previous.Priority > current.Priority ||
			(previous.Priority == current.Priority && previous.SeqNo < current.SeqNo)
		if !ordered {
			t.Errorf("items[%d] = p%d/seq%d before p%d/seq%d", i,
				previous.Priority, previous.SeqNo, current.Priority, current.SeqNo)
		}
	}

	// Status filter: force one task to in_progress.
	forceTaskState(t, e, "APP-1-T0001", domain.TaskInProgress, 1, nil, nil, nil)
	filtered, err := svc.ListTasks(context.Background(), ListTasksInput{
		ProjectID: projectID, Statuses: []int{domain.TaskInProgress}, Limit: 200,
	})
	if err != nil {
		t.Fatalf("ListTasks(status): %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].ID != "APP-1-T0001" {
		t.Errorf("filtered = %+v", filtered.Items)
	}

	if _, err := svc.ListTasks(context.Background(), ListTasksInput{ProjectID: "APP-404", Limit: 10}); err == nil {
		t.Fatal("ListTasks(missing project) error = nil")
	} else {
		var notFound *domain.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("error = %v, want NotFoundError", err)
		}
	}

	// Limit and status bounds are field errors.
	if _, err := svc.ListTasks(context.Background(), ListTasksInput{Limit: 0}); err == nil {
		t.Error("ListTasks(limit=0) error = nil")
	}
	if _, err := svc.ListTasks(context.Background(), ListTasksInput{Limit: 10, Statuses: []int{9}}); err == nil {
		t.Error("ListTasks(status=9) error = nil")
	}
}

func TestUpdateTaskPartialAndVersion(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "编辑")
	created := mustCreateTask(t, svc, projectID, "原标题", "原正文", 1)

	reason := "历史理由"
	forceTaskState(t, e, created.Task.ID, domain.TaskBlocked, 3, nil, nil, &reason)

	title := "新标题"
	updated, err := svc.UpdateTask(context.Background(), created.Task.ID, UpdateTaskInput{Title: &title}, "human:pm")
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Task.Title != title || updated.Task.BodyMD != "原正文" || updated.Task.Priority != 1 {
		t.Errorf("fields = %q/%q/%d", updated.Task.Title, updated.Task.BodyMD, updated.Task.Priority)
	}
	if updated.Task.Version != created.Task.Version+1 {
		t.Errorf("version = %d, want %d", updated.Task.Version, created.Task.Version+1)
	}
	if !updated.Task.UpdateTime.After(created.Task.UpdateTime) {
		t.Errorf("update_time did not advance: %v -> %v", created.Task.UpdateTime, updated.Task.UpdateTime)
	}
	if updated.Task.Status != domain.TaskBlocked {
		t.Errorf("status = %d, want blocked (unchanged)", updated.Task.Status)
	}
	if updated.Task.Reason != reason {
		t.Errorf("reason = %q, want %q (unchanged)", updated.Task.Reason, reason)
	}
	if updated.Task.LastActor != "human:pm" {
		t.Errorf("last_actor = %q", updated.Task.LastActor)
	}

	body := "新正文"
	priority := 1000
	updated, err = svc.UpdateTask(context.Background(), created.Task.ID,
		UpdateTaskInput{BodyMD: &body, Priority: &priority}, "human:pm")
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Task.BodyMD != body || updated.Task.Priority != priority {
		t.Errorf("fields = %q/%d", updated.Task.BodyMD, updated.Task.Priority)
	}
	if updated.Task.Version != created.Task.Version+2 {
		t.Errorf("version = %d, want %d", updated.Task.Version, created.Task.Version+2)
	}

	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, UpdateTaskInput{}, "pm"); err == nil {
		t.Error("empty patch error = nil")
	} else {
		var validation *domain.ValidationError
		if !errors.As(err, &validation) || validation.Fields[0].Field != "body" {
			t.Errorf("error = %v, want body field error", err)
		}
	}

	bad := "  "
	if _, err := svc.UpdateTask(context.Background(), created.Task.ID, UpdateTaskInput{Title: &bad}, "pm"); err == nil {
		t.Error("blank title patch error = nil")
	}

	if _, err := svc.UpdateTask(context.Background(), "APP-1-T0404", UpdateTaskInput{Title: &title}, "pm"); err == nil {
		t.Error("missing task patch error = nil")
	} else {
		var notFound *domain.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("error = %v, want NotFoundError", err)
		}
	}
}

func TestDeleteTaskBranches(t *testing.T) {
	e, svc := newTaskEnv(t)
	projectID := e.newPlanningProject(t, "删除")
	first := mustCreateTask(t, svc, projectID, "一", "b", 0)
	mustCreateTask(t, svc, projectID, "二", "b", 0, "APP-1-T0001")
	third := mustCreateTask(t, svc, projectID, "三", "b", 0)

	// backlog + attempt 0: removable, and the dependency rows that touch it go
	// with it in the same transaction.
	if err := svc.DeleteTask(context.Background(), "APP-1-T0002"); err != nil {
		t.Fatalf("DeleteTask(T0002): %v", err)
	}
	if _, err := svc.GetTask(context.Background(), "APP-1-T0002"); err == nil {
		t.Error("deleted task is still readable")
	}
	if edges := e.planDepEdges(t, projectID); len(edges) != 0 {
		t.Errorf("edges = %v, want none", edges)
	}
	if _, err := svc.GetTask(context.Background(), first.Task.ID); err != nil {
		t.Errorf("first task: %v", err)
	}

	// in_progress: 409 conflict.
	leaseID := "lease-1"
	future := "2999-01-01T00:00:00Z"
	forceTaskState(t, e, first.Task.ID, domain.TaskInProgress, 1, &leaseID, &future, nil)
	err := svc.DeleteTask(context.Background(), first.Task.ID)
	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) || !strings.Contains(conflict.Error(), "in_progress") {
		t.Errorf("in_progress delete error = %v", err)
	}

	// ready but claimed at least once: also 409, with the attempt history named.
	forceTaskState(t, e, third.Task.ID, domain.TaskReady, 2, nil, nil, nil)
	err = svc.DeleteTask(context.Background(), third.Task.ID)
	if !errors.As(err, &conflict) || !strings.Contains(conflict.Error(), "claimed") {
		t.Errorf("claimed delete error = %v", err)
	}

	// ready with attempt_count 0 is still not backlog: 409.
	forced := mustCreateTask(t, svc, projectID, "四", "b", 0)
	forceTaskState(t, e, forced.Task.ID, domain.TaskReady, 0, nil, nil, nil)
	err = svc.DeleteTask(context.Background(), forced.Task.ID)
	if !errors.As(err, &conflict) {
		t.Errorf("ready delete error = %v, want conflict", err)
	}

	if err := svc.DeleteTask(context.Background(), "APP-1-T0404"); err == nil {
		t.Error("missing task delete error = nil")
	} else {
		var notFound *domain.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("error = %v, want NotFoundError", err)
		}
	}
}
