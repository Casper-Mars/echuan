package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// newPlanningProject walks the real state machine to planning:
// create → accept (draft → spec_review) → approve (spec_review → planning).
func (e *projectTestEnv) newPlanningProject(t *testing.T, title string) string {
	t.Helper()
	e.createProduct(t, "APP")
	projectID := e.createProject(t, "APP", title)

	ctx := context.Background()
	if _, err := e.service.AcceptProject(ctx, projectID, "pm"); err != nil {
		t.Fatalf("AcceptProject(%s): %v", projectID, err)
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
	if state := e.projectState(t, projectID); state.Status != domain.StatusPlanning {
		t.Fatalf("status = %d, want planning", state.Status)
	}
	return projectID
}

// planTaskRow is the subset of tasks the plan tests assert on.
type planTaskRow struct {
	ID           int64
	TaskID       string
	SeqNo        int
	Title        string
	BodyMD       string
	Priority     int
	Status       int
	AttemptCount int
	MaxAttempts  int
	Version      int
	LastActor    *string
}

func (e *projectTestEnv) planTaskRows(t *testing.T, projectID string) []planTaskRow {
	t.Helper()
	rows, err := e.store.Read().QueryContext(context.Background(), `
		SELECT t.id, t.task_id, t.seq_no, t.title, t.body_md, t.priority, t.status,
		       t.attempt_count, t.max_attempts, t.version, t.last_actor
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ?
		 ORDER BY t.seq_no`, projectID)
	if err != nil {
		t.Fatalf("query tasks of %s: %v", projectID, err)
	}
	defer rows.Close()

	var out []planTaskRow
	for rows.Next() {
		var row planTaskRow
		if err := rows.Scan(&row.ID, &row.TaskID, &row.SeqNo, &row.Title, &row.BodyMD,
			&row.Priority, &row.Status, &row.AttemptCount, &row.MaxAttempts,
			&row.Version, &row.LastActor); err != nil {
			t.Fatalf("scan task: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tasks: %v", err)
	}
	return out
}

// planDepEdges returns task_deps as business ids: owner task id → dependency ids.
func (e *projectTestEnv) planDepEdges(t *testing.T, projectID string) map[string][]string {
	t.Helper()
	rows, err := e.store.Read().QueryContext(context.Background(), `
		SELECT owner.task_id, dep.task_id
		  FROM task_deps td
		  JOIN tasks owner ON owner.id = td.task_id
		  JOIN tasks dep ON dep.id = td.depends_on
		  JOIN projects p ON p.id = owner.project_id
		 WHERE p.project_id = ?
		 ORDER BY owner.seq_no, dep.seq_no`, projectID)
	if err != nil {
		t.Fatalf("query task deps of %s: %v", projectID, err)
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var owner, dep string
		if err := rows.Scan(&owner, &dep); err != nil {
			t.Fatalf("scan task dep: %v", err)
		}
		out[owner] = append(out[owner], dep)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task deps: %v", err)
	}
	return out
}

func (e *projectTestEnv) countTaskDeps(t *testing.T, projectID string) int64 {
	t.Helper()
	return e.count(t, `
		SELECT COUNT(*)
		  FROM task_deps td
		  JOIN tasks t ON t.id = td.task_id
		  JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ?`, projectID)
}

func fivePlanTasks() []PlanTask {
	return []PlanTask{
		{Title: "实现状态机", BodyMD: "# 实现状态机\n\n落地 0–7 状态表。", Priority: 10},
		{Title: "补测试", BodyMD: "# 补测试\n\n覆盖状态迁移。", Priority: 5, DependsOn: []int{1}},
		{Title: "接前端", BodyMD: "# 接前端\n\n暴露接口。", DependsOn: []int{1, 2}},
		{Title: "写文档", BodyMD: "# 文档\n\n更新 README。", Priority: -1000, DependsOn: []int{2}},
		{Title: "上线", BodyMD: "# 上线\n\n发布单文件二进制。", Priority: 1000, DependsOn: []int{3, 4}},
	}
}

func TestSubmitPlanCreatesTasksAndDeps(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "登录页改版")
	before := env.projectState(t, projectID)

	result, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks())
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}

	if result.Project.ProjectID != projectID {
		t.Errorf("project id = %q, want %q", result.Project.ProjectID, projectID)
	}
	if result.Project.Status != domain.StatusPlanReview {
		t.Errorf("status = %d, want plan_review", result.Project.Status)
	}
	if result.Project.Version != before.Version+1 {
		t.Errorf("version = %d, want %d", result.Project.Version, before.Version+1)
	}
	if result.Project.LastActor != "agent:cto" {
		t.Errorf("last_actor = %q, want agent:cto", result.Project.LastActor)
	}
	if result.Project.TaskStats.Total != 5 {
		t.Errorf("task total = %d, want 5", result.Project.TaskStats.Total)
	}

	if len(result.Tasks) != 5 {
		t.Fatalf("tasks = %d, want 5", len(result.Tasks))
	}
	wantDeps := [][]string{
		{},
		{"APP-1-T0001"},
		{"APP-1-T0001", "APP-1-T0002"},
		{"APP-1-T0002"},
		{"APP-1-T0003", "APP-1-T0004"},
	}
	for i, task := range result.Tasks {
		wantID := fmt.Sprintf("APP-1-T%04d", i+1)
		if task.ID != wantID {
			t.Errorf("tasks[%d].id = %q, want %q", i, task.ID, wantID)
		}
		if task.SeqNo != i+1 {
			t.Errorf("tasks[%d].seq_no = %d, want %d", i, task.SeqNo, i+1)
		}
		if task.Status != domain.TaskBacklog {
			t.Errorf("tasks[%d].status = %d, want backlog", i, task.Status)
		}
		if len(task.DependsOn) != len(wantDeps[i]) {
			t.Errorf("tasks[%d].depends_on = %v, want %v", i, task.DependsOn, wantDeps[i])
			continue
		}
		for j, dep := range task.DependsOn {
			if dep != wantDeps[i][j] {
				t.Errorf("tasks[%d].depends_on = %v, want %v", i, task.DependsOn, wantDeps[i])
				break
			}
		}
	}

	rows := env.planTaskRows(t, projectID)
	if len(rows) != 5 {
		t.Fatalf("persisted tasks = %d, want 5", len(rows))
	}
	for i, row := range rows {
		if row.TaskID != fmt.Sprintf("APP-1-T%04d", i+1) || row.SeqNo != i+1 {
			t.Errorf("row %d = %s/seq %d, want T%04d/seq %d", i, row.TaskID, row.SeqNo, i+1, i+1)
		}
		if row.Status != domain.TaskBacklog {
			t.Errorf("%s status = %d, want backlog", row.TaskID, row.Status)
		}
		if row.AttemptCount != 0 {
			t.Errorf("%s attempt_count = %d, want 0", row.TaskID, row.AttemptCount)
		}
		if row.MaxAttempts != defaultTaskMaxAttempts {
			t.Errorf("%s max_attempts = %d, want %d", row.TaskID, row.MaxAttempts, defaultTaskMaxAttempts)
		}
		if row.Version != 1 {
			t.Errorf("%s version = %d, want 1", row.TaskID, row.Version)
		}
		if row.LastActor == nil || *row.LastActor != "agent:cto" {
			t.Errorf("%s last_actor = %v, want agent:cto", row.TaskID, row.LastActor)
		}
	}
	if rows[0].BodyMD != fivePlanTasks()[0].BodyMD {
		t.Errorf("body_md = %q, want the request body", rows[0].BodyMD)
	}
	if rows[0].Priority != 10 || rows[3].Priority != -1000 || rows[4].Priority != 1000 {
		t.Errorf("priorities = %d/%d/%d, want 10/-1000/1000",
			rows[0].Priority, rows[3].Priority, rows[4].Priority)
	}

	if got := env.countTaskDeps(t, projectID); got != 6 {
		t.Errorf("task_deps rows = %d, want 6", got)
	}
	edges := env.planDepEdges(t, projectID)
	if got := strings.Join(edges["APP-1-T0002"], ","); got != "APP-1-T0001" {
		t.Errorf("T0002 deps = %q", got)
	}
	if got := strings.Join(edges["APP-1-T0005"], ","); got != "APP-1-T0003,APP-1-T0004" {
		t.Errorf("T0005 deps = %q", got)
	}

	state := env.projectState(t, projectID)
	if state.Status != domain.StatusPlanReview || state.Version != before.Version+1 {
		t.Errorf("project state = %d/v%d, want plan_review/v%d", state.Status, state.Version, before.Version+1)
	}
	if state.LastActor == nil || *state.LastActor != "agent:cto" {
		t.Errorf("project last_actor = %v, want agent:cto", state.LastActor)
	}
}

func TestSubmitPlanBoundaries(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "边界")

	tasks := make([]PlanTask, MaxPlanTasks)
	body := strings.Repeat("a", MaxTaskBodyBytes)
	tasks[0] = PlanTask{Title: "上限正文", BodyMD: body}
	for i := 1; i < len(tasks); i++ {
		tasks[i] = PlanTask{Title: fmt.Sprintf("任务 %d", i+1), BodyMD: "b"}
	}

	result, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", tasks)
	if err != nil {
		t.Fatalf("SubmitPlan(200 tasks): %v", err)
	}
	if len(result.Tasks) != MaxPlanTasks {
		t.Fatalf("tasks = %d, want %d", len(result.Tasks), MaxPlanTasks)
	}
	last := result.Tasks[len(result.Tasks)-1]
	if last.ID != "APP-1-T0200" || last.SeqNo != MaxPlanTasks {
		t.Errorf("last task = %s/seq %d, want APP-1-T0200/seq 200", last.ID, last.SeqNo)
	}
	rows := env.planTaskRows(t, projectID)
	if len(rows[0].BodyMD) != MaxTaskBodyBytes {
		t.Errorf("body length = %d, want %d", len(rows[0].BodyMD), MaxTaskBodyBytes)
	}
}

func TestSubmitPlanValidationMatrix(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "校验")
	before := env.projectState(t, projectID)

	tooMany := make([]PlanTask, MaxPlanTasks+1)
	for i := range tooMany {
		tooMany[i] = PlanTask{Title: "t", BodyMD: "b"}
	}
	tooLongTitle := strings.Repeat("字", MaxTaskTitleLen+1)
	tooLongBody := strings.Repeat("b", MaxTaskBodyBytes+1)
	five := func(deps []int) []PlanTask {
		tasks := make([]PlanTask, 5)
		for i := range tasks {
			tasks[i] = PlanTask{Title: "t", BodyMD: "b"}
		}
		tasks[0].DependsOn = deps
		return tasks
	}

	cases := []struct {
		name   string
		tasks  []PlanTask
		field  string
		reason string
	}{
		{"empty", []PlanTask{}, "tasks", "at least 1"},
		{"missing", nil, "tasks", "at least 1"},
		{"too many", tooMany, "tasks", "at most 200"},
		{"blank title", []PlanTask{{Title: "   ", BodyMD: "b"}}, "tasks[0].title", "required"},
		{"long title", []PlanTask{{Title: tooLongTitle, BodyMD: "b"}}, "tasks[0].title", "200"},
		{"blank body", []PlanTask{{Title: "t", BodyMD: " \n\t "}}, "tasks[0].body_md", "required"},
		{"long body", []PlanTask{{Title: "t", BodyMD: tooLongBody}}, "tasks[0].body_md", "65536"},
		{"priority high", []PlanTask{{Title: "t", BodyMD: "b", Priority: MaxTaskPriority + 1}}, "tasks[0].priority", "between"},
		{"priority low", []PlanTask{{Title: "t", BodyMD: "b", Priority: MinTaskPriority - 1}}, "tasks[0].priority", "between"},
		{"depends zero", five([]int{0}), "tasks[0].depends_on", "between 1 and 5"},
		{"depends negative", five([]int{-1}), "tasks[0].depends_on", "between 1 and 5"},
		{"depends out of range", five([]int{6}), "tasks[0].depends_on", "between 1 and 5"},
		{"depends self", five([]int{1}), "tasks[0].depends_on", "itself"},
		{"depends duplicate", five([]int{2, 2}), "tasks[0].depends_on", "duplicate"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", tc.tasks)
			if err == nil {
				t.Fatal("error = nil, want validation error")
			}
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			found := false
			for _, field := range validation.Fields {
				if field.Field == tc.field {
					found = true
					if !strings.Contains(field.Reason, tc.reason) {
						t.Errorf("reason = %q, want substring %q", field.Reason, tc.reason)
					}
				}
			}
			if !found {
				t.Errorf("fields = %+v, want %q", validation.Fields, tc.field)
			}
		})
	}

	// Every rejection above happened before the transaction.
	if rows := env.planTaskRows(t, projectID); len(rows) != 0 {
		t.Errorf("persisted tasks = %d, want 0", len(rows))
	}
	state := env.projectState(t, projectID)
	if state.Status != domain.StatusPlanning || state.Version != before.Version {
		t.Errorf("project = %d/v%d, want planning/v%d", state.Status, state.Version, before.Version)
	}
}

func TestSubmitPlanCycleDetection(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "环检测")

	cases := []struct {
		name    string
		tasks   []PlanTask
		message string
		field   string
	}{
		{
			name: "two node cycle",
			tasks: []PlanTask{
				{Title: "a", BodyMD: "b", DependsOn: []int{2}},
				{Title: "b", BodyMD: "b", DependsOn: []int{1}},
			},
			message: "dependency cycle: 1 → 2 → 1",
			field:   "tasks[0].depends_on",
		},
		{
			name: "three node cycle",
			tasks: []PlanTask{
				{Title: "a", BodyMD: "b", DependsOn: []int{2}},
				{Title: "b", BodyMD: "b", DependsOn: []int{3}},
				{Title: "c", BodyMD: "b", DependsOn: []int{1}},
			},
			message: "dependency cycle: 1 → 2 → 3 → 1",
			field:   "tasks[0].depends_on",
		},
		{
			name: "self reference",
			tasks: []PlanTask{
				{Title: "a", BodyMD: "b", DependsOn: []int{1}},
			},
			message: "must not depend on itself",
			field:   "tasks[0].depends_on",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", tc.tasks)
			if err == nil {
				t.Fatal("error = nil, want 400 class")
			}
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if tc.name == "self reference" {
				// A self-edge is a field-level check, not a graph cycle, so it
				// carries no ValidationError.Message.
				if len(validation.Fields) != 1 || validation.Fields[0].Field != tc.field {
					t.Errorf("fields = %+v, want %q", validation.Fields, tc.field)
				}
				if !strings.Contains(validation.Fields[0].Reason, tc.message) {
					t.Errorf("detail reason = %q, want substring %q", validation.Fields[0].Reason, tc.message)
				}
				return
			}
			if !strings.Contains(validation.Message, tc.message) {
				t.Errorf("message = %q, want substring %q", validation.Message, tc.message)
			}
			if len(validation.Fields) != 1 || validation.Fields[0].Field != tc.field {
				t.Errorf("fields = %+v, want %q", validation.Fields, tc.field)
			}
			if !strings.Contains(validation.Fields[0].Reason, tc.message) {
				t.Errorf("detail reason = %q, want substring %q", validation.Fields[0].Reason, tc.message)
			}
		})
	}

	if rows := env.planTaskRows(t, projectID); len(rows) != 0 {
		t.Errorf("persisted tasks = %d, want 0", len(rows))
	}
	if state := env.projectState(t, projectID); state.Status != domain.StatusPlanning {
		t.Errorf("status = %d, want planning", state.Status)
	}
}

func TestSubmitPlanForwardReference(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "前向引用")

	result, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", []PlanTask{
		{Title: "先做", BodyMD: "b", DependsOn: []int{2}},
		{Title: "后做", BodyMD: "b"},
	})
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	if len(result.Tasks[0].DependsOn) != 1 || result.Tasks[0].DependsOn[0] != "APP-1-T0002" {
		t.Errorf("deps = %v, want [APP-1-T0002]", result.Tasks[0].DependsOn)
	}
	edges := env.planDepEdges(t, projectID)
	if got := strings.Join(edges["APP-1-T0001"], ","); got != "APP-1-T0002" {
		t.Errorf("persisted deps = %q, want APP-1-T0002", got)
	}
}

func TestSubmitPlanInvalidStateZeroSideEffects(t *testing.T) {
	statuses := []int{
		domain.StatusDraft,
		domain.StatusSpecReview,
		domain.StatusPlanReview,
		domain.StatusDeveloping,
		domain.StatusDeliveryReview,
		domain.StatusAccepted,
		domain.StatusCancelled,
	}

	for _, status := range statuses {
		t.Run(domain.StatusName(status), func(t *testing.T) {
			env := newProjectTestEnv(t)
			projectID := env.newPlanningProject(t, "状态")
			reason := "历史理由"
			env.setProjectState(t, projectID, status, 7, &reason)

			_, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks())
			if err == nil {
				t.Fatal("error = nil, want invalid_state")
			}
			if !errors.Is(err, domain.ErrInvalidState) {
				t.Fatalf("error = %v, want invalid_state", err)
			}

			if rows := env.planTaskRows(t, projectID); len(rows) != 0 {
				t.Errorf("persisted tasks = %d, want 0", len(rows))
			}
			if got := env.countTaskDeps(t, projectID); got != 0 {
				t.Errorf("task_deps = %d, want 0", got)
			}
			state := env.projectState(t, projectID)
			if state.Status != status || state.Version != 7 {
				t.Errorf("project = %d/v%d, want %d/v7", state.Status, state.Version, status)
			}
			if state.Reason == nil || *state.Reason != reason {
				t.Errorf("reason = %v, want %q", state.Reason, reason)
			}
		})
	}
}

func TestSubmitPlanNotFound(t *testing.T) {
	env := newProjectTestEnv(t)
	_, err := env.service.SubmitPlan(context.Background(), "APP-404", "agent:cto", fivePlanTasks())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("error = %v, want not_found", err)
	}
}

// TestSubmitPlanRollback injects a failure at the third insert: a pre-existing
// row already owns APP-1-T0006, and with the pre-existing seq 3 the batch takes
// seq 4..8, so the third generated id is APP-1-T0006 and hits UNIQUE(task_id).
// The transaction must roll back the two tasks inserted before it.
func TestSubmitPlanRollback(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "回滚")
	physical := env.physicalProjectID(t, projectID)
	before := env.projectState(t, projectID)

	if _, err := env.store.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
		VALUES ('APP-1-T0006', ?, 3, '注入冲突行', 'b', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		physical); err != nil {
		t.Fatalf("inject conflicting task: %v", err)
	}

	_, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks())
	if err == nil {
		t.Fatal("error = nil, want conflict")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("error = %v, want conflict", err)
	}

	rows := env.planTaskRows(t, projectID)
	if len(rows) != 1 || rows[0].TaskID != "APP-1-T0006" {
		t.Errorf("persisted tasks = %+v, want only the injected row", rows)
	}
	if got := env.countTaskDeps(t, projectID); got != 0 {
		t.Errorf("task_deps = %d, want 0", got)
	}
	state := env.projectState(t, projectID)
	if state.Status != domain.StatusPlanning || state.Version != before.Version {
		t.Errorf("project = %d/v%d, want planning/v%d", state.Status, state.Version, before.Version)
	}
}

func TestSubmitPlanReasonPreserved(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "保留理由")
	reason := "方案未覆盖会话续期"
	env.setProjectState(t, projectID, domain.StatusPlanning, 4, &reason)

	result, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks())
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	if result.Project.Reason != reason {
		t.Errorf("reason = %q, want %q", result.Project.Reason, reason)
	}
	if result.Project.Version != 5 {
		t.Errorf("version = %d, want 5", result.Project.Version)
	}
}

func TestSubmitPlanMaxAttemptsFromConfig(t *testing.T) {
	t.Setenv("ECHUAN_MAX_ATTEMPTS", "5")
	cfg, err := platform.Load("")
	if err != nil {
		t.Fatalf("platform.Load: %v", err)
	}
	if cfg.MaxAttempts != 5 {
		t.Fatalf("cfg.MaxAttempts = %d, want 5", cfg.MaxAttempts)
	}

	env := newProjectTestEnv(t)
	env.service.SetMaxAttempts(cfg.MaxAttempts)
	projectID := env.newPlanningProject(t, "配置")

	if _, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks()); err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	for _, row := range env.planTaskRows(t, projectID) {
		if row.MaxAttempts != 5 {
			t.Errorf("%s max_attempts = %d, want 5 (from env)", row.TaskID, row.MaxAttempts)
		}
	}
}

func TestSubmitPlanSequenceContinuesWithoutGaps(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "续号")
	physical := env.physicalProjectID(t, projectID)

	// A historical row at seq 9999 proves both continuation and the 5-digit id
	// after 9999.
	if _, err := env.store.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
		VALUES ('APP-1-T9999', ?, 9999, '历史任务', 'b', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		physical); err != nil {
		t.Fatalf("seed task 9999: %v", err)
	}

	result, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", []PlanTask{
		{Title: "第一张", BodyMD: "b"},
		{Title: "第二张", BodyMD: "b"},
	})
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}
	if result.Tasks[0].SeqNo != 10000 || result.Tasks[0].ID != "APP-1-T10000" {
		t.Errorf("first = %s/seq %d, want APP-1-T10000/seq 10000", result.Tasks[0].ID, result.Tasks[0].SeqNo)
	}
	if result.Tasks[1].SeqNo != 10001 || result.Tasks[1].ID != "APP-1-T10001" {
		t.Errorf("second = %s/seq %d, want APP-1-T10001/seq 10001", result.Tasks[1].ID, result.Tasks[1].SeqNo)
	}
}

// TestSubmitPlanThenApproveIsReady is the Task-06 linkage: approving the plan
// review (plan_review → developing) puts every backlog task into ready.
func TestSubmitPlanThenApproveIsReady(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "批计划")

	plan, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks())
	if err != nil {
		t.Fatalf("SubmitPlan: %v", err)
	}

	approve, err := env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: intPtr(plan.Project.Version),
		Actor:         "human:cto",
	})
	if err != nil {
		t.Fatalf("Decide(approve plan): %v", err)
	}
	if approve.ToStatus != domain.StatusDeveloping {
		t.Errorf("to status = %d, want developing", approve.ToStatus)
	}
	for _, row := range env.planTaskRows(t, projectID) {
		if row.Status != domain.TaskReady {
			t.Errorf("%s status = %d, want ready", row.TaskID, row.Status)
		}
		if row.Version != 2 {
			t.Errorf("%s version = %d, want 2", row.TaskID, row.Version)
		}
	}
}

func TestSubmitPlanConcurrentOnlyOneWins(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newPlanningProject(t, "并发")

	const attempts = 10
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.service.SubmitPlan(context.Background(), projectID, "agent:cto", fivePlanTasks())
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	success, invalidState := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, domain.ErrInvalidState):
			invalidState++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if success != 1 {
		t.Errorf("successes = %d, want exactly 1", success)
	}
	if invalidState != attempts-1 {
		t.Errorf("invalid_state = %d, want %d", invalidState, attempts-1)
	}

	rows := env.planTaskRows(t, projectID)
	if len(rows) != 5 {
		t.Fatalf("persisted tasks = %d, want 5", len(rows))
	}
	for i, row := range rows {
		if row.SeqNo != i+1 || row.TaskID != fmt.Sprintf("APP-1-T%04d", i+1) {
			t.Errorf("row %d = %s/seq %d, want contiguous seq", i, row.TaskID, row.SeqNo)
		}
	}
	if got := env.countTaskDeps(t, projectID); got != 6 {
		t.Errorf("task_deps = %d, want 6", got)
	}
}
