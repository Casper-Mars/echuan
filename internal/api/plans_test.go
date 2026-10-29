package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// createTasksDraftHTTPProject drives
// create → submit → approve → spec → submit → approve so the project sits in
// tasks_draft (status 4) with version 6.
func createTasksDraftHTTPProject(t *testing.T, h http.Handler) string {
	t.Helper()
	projectID := createHTTPProject(t, h, "登录页改版")

	post := func(target string, body any, version int) projectResponse {
		headers := map[string]string{}
		if version > 0 {
			headers["If-Match"] = strconv.Itoa(version)
		}
		rec := doJSONHeaders(t, h, http.MethodPost, target, body, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (body %s)", target, rec.Code, rec.Body.String())
		}
		return decodeProjectFromResponse(t, rec)
	}

	project := post("/v1/projects/submit?id="+projectID, nil, 0)
	project = post("/v1/projects/decide?id="+projectID, map[string]any{"decision": "approve"}, project.Version)
	specRec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/specs?id="+projectID,
		map[string]any{"body_md": "# 方案\n\n正文\n"}, nil)
	if specRec.Code != http.StatusOK {
		t.Fatalf("spec: status = %d (body %s)", specRec.Code, specRec.Body.String())
	}
	project = post("/v1/projects/submit?id="+projectID, nil, 0)
	project = post("/v1/projects/decide?id="+projectID, map[string]any{"decision": "approve"}, project.Version)
	if project.Status != domain.StatusTasksDraft {
		t.Fatalf("status = %d, want tasks_draft", project.Status)
	}
	return projectID
}

func planBody(tasks ...map[string]any) map[string]any {
	return map[string]any{"tasks": tasks}
}

func planTask(title, body string, extra ...map[string]any) map[string]any {
	task := map[string]any{"title": title, "body_md": body}
	for _, values := range extra {
		for key, value := range values {
			task[key] = value
		}
	}
	return task
}

func decodeSubmitPlan(t *testing.T, rec *httptest.ResponseRecorder) submitPlanResponse {
	t.Helper()
	var response submitPlanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode plan response %q: %v", rec.Body.String(), err)
	}
	return response
}

func taskRowsByProject(t *testing.T, st *store.Store, projectID string) []struct {
	TaskID      string
	SeqNo       int
	Status      int
	MaxAttempts int
} {
	t.Helper()
	rows, err := st.Read().QueryContext(context.Background(), `
		SELECT t.task_id, t.seq_no, t.status, t.max_attempts
		  FROM tasks t JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ?
		 ORDER BY t.seq_no`, projectID)
	if err != nil {
		t.Fatalf("query tasks: %v", err)
	}
	defer rows.Close()

	var out []struct {
		TaskID      string
		SeqNo       int
		Status      int
		MaxAttempts int
	}
	for rows.Next() {
		var row struct {
			TaskID      string
			SeqNo       int
			Status      int
			MaxAttempts int
		}
		if err := rows.Scan(&row.TaskID, &row.SeqNo, &row.Status, &row.MaxAttempts); err != nil {
			t.Fatalf("scan task: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tasks: %v", err)
	}
	return out
}

func countDeps(t *testing.T, st *store.Store, projectID string) int64 {
	t.Helper()
	var n int64
	if err := st.Read().QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		  FROM task_deps td
		  JOIN tasks t ON t.id = td.task_id
		  JOIN projects p ON p.id = t.project_id
		 WHERE p.project_id = ?`, projectID).Scan(&n); err != nil {
		t.Fatalf("count task deps: %v", err)
	}
	return n
}

func fiveHTTPPlanTasks() []map[string]any {
	return []map[string]any{
		planTask("实现状态机", "# 实现状态机\n", map[string]any{"priority": 10}),
		planTask("补测试", "# 补测试\n", map[string]any{"priority": 5, "depends_on": []int{1}}),
		planTask("接前端", "# 接前端\n", map[string]any{"depends_on": []int{1, 2}}),
		planTask("写文档", "# 文档\n", map[string]any{"priority": -1000, "depends_on": []int{2}}),
		planTask("上线", "# 上线\n", map[string]any{"priority": 1000, "depends_on": []int{3, 4}}),
	}
}

func TestSubmitPlanHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(fiveHTTPPlanTasks()...), map[string]string{HeaderActor: "agent:cto"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/projects?id=APP-1" {
		t.Errorf("Location = %q, want /v1/projects?id=APP-1", loc)
	}

	response := decodeSubmitPlan(t, rec)
	if response.ProjectID != "APP-1" {
		t.Errorf("project_id = %q, want APP-1", response.ProjectID)
	}
	if response.Status != domain.StatusTasksReview || response.StatusName != "tasks_review" {
		t.Errorf("status = %d (%s), want 5 (tasks_review)", response.Status, response.StatusName)
	}
	if response.ProjectVersion != 7 {
		t.Errorf("project_version = %d, want 7", response.ProjectVersion)
	}
	if len(response.Tasks) != 5 {
		t.Fatalf("tasks = %d, want 5", len(response.Tasks))
	}

	wantDeps := [][]string{
		{},
		{"APP-1-T0001"},
		{"APP-1-T0001", "APP-1-T0002"},
		{"APP-1-T0002"},
		{"APP-1-T0003", "APP-1-T0004"},
	}
	for i, task := range response.Tasks {
		wantID := fmt.Sprintf("APP-1-T%04d", i+1)
		if task.ID != wantID || task.SeqNo != i+1 {
			t.Errorf("tasks[%d] = %s/seq %d, want %s/seq %d", i, task.ID, task.SeqNo, wantID, i+1)
		}
		if task.Status != domain.TaskBacklog || task.StatusName != "backlog" {
			t.Errorf("tasks[%d] status = %d (%s), want backlog", i, task.Status, task.StatusName)
		}
		if strings.Join(task.DependsOn, ",") != strings.Join(wantDeps[i], ",") {
			t.Errorf("tasks[%d].depends_on = %v, want %v", i, task.DependsOn, wantDeps[i])
		}
		if task.DependsOn == nil {
			t.Errorf("tasks[%d].depends_on = null, want []", i)
		}
	}
	if response.Tasks[0].Priority != 10 || response.Tasks[3].Priority != -1000 || response.Tasks[4].Priority != 1000 {
		t.Errorf("priorities = %d/%d/%d, want 10/-1000/1000",
			response.Tasks[0].Priority, response.Tasks[3].Priority, response.Tasks[4].Priority)
	}

	// The project endpoint reflects the transition and the counters.
	projectRec := doJSON(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil)
	if projectRec.Code != http.StatusOK {
		t.Fatalf("get project: status = %d", projectRec.Code)
	}
	project := decodeProjectDetail(t, projectRec)
	if project.Status != domain.StatusTasksReview || project.Version != 7 {
		t.Errorf("project = %d/v%d, want tasks_review/v7", project.Status, project.Version)
	}
	if project.TaskStats.Total != 5 {
		t.Errorf("task_stats.total = %d, want 5", project.TaskStats.Total)
	}
	if project.LastActor == nil || *project.LastActor != "agent:cto" {
		t.Errorf("last_actor = %v, want agent:cto", project.LastActor)
	}
	if got := countDeps(t, st, projectID); got != 6 {
		t.Errorf("task_deps = %d, want 6", got)
	}
}

func TestSubmitPlanHTTPValidation(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	tooMany := make([]map[string]any, 201)
	for i := range tooMany {
		tooMany[i] = planTask("t", "b")
	}

	cases := []struct {
		name  string
		body  any
		field string
	}{
		{"missing id", planBody(planTask("t", "b")), "id"},
		{"unknown top level", map[string]any{"tasks": []any{planTask("t", "b")}, "bogus": 1}, "bogus"},
		{"unknown nested", planBody(planTask("t", "b", map[string]any{"bogus": true})), "tasks[0].bogus"},
		{"missing tasks", map[string]any{}, "tasks"},
		{"null tasks", map[string]any{"tasks": nil}, "tasks"},
		{"empty tasks", planBody(), "tasks"},
		{"too many tasks", planBody(tooMany...), "tasks"},
		{"blank title", planBody(planTask("  ", "b")), "tasks[0].title"},
		{"long title", planBody(planTask(strings.Repeat("字", 201), "b")), "tasks[0].title"},
		{"blank body", planBody(planTask("t", "  \n")), "tasks[0].body_md"},
		{"long body", planBody(planTask("t", strings.Repeat("b", 64*1024+1))), "tasks[0].body_md"},
		{"priority high", planBody(planTask("t", "b", map[string]any{"priority": 1001})), "tasks[0].priority"},
		{"priority type", planBody(planTask("t", "b", map[string]any{"priority": "high"})), "tasks[0].priority"},
		{"depends zero", planBody(planTask("t", "b", map[string]any{"depends_on": []int{0}})), "tasks[0].depends_on"},
		{"depends out of range", planBody(planTask("t", "b", map[string]any{"depends_on": []int{2}})), "tasks[0].depends_on"},
		{"depends duplicate", planBody(
			planTask("a", "b", map[string]any{"depends_on": []int{2, 2}}),
			planTask("b", "b"),
		), "tasks[0].depends_on"},
		{"depends type", planBody(planTask("t", "b", map[string]any{"depends_on": []any{"1"}})), "tasks[0].depends_on"},
		{"self reference", planBody(planTask("t", "b", map[string]any{"depends_on": []int{1}})), "tasks[0].depends_on"},
		{"invalid json", "{", "body"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := "/v1/projects/plans?id=" + projectID
			if tc.name == "missing id" {
				target = "/v1/projects/plans"
			}
			var rec *httptest.ResponseRecorder
			if tc.name == "invalid json" {
				// doJSON writes a string body verbatim; doJSONHeaders would
				// JSON-encode the malformed payload into a valid string.
				rec = doJSON(t, h, http.MethodPost, target, tc.body)
			} else {
				rec = doJSONHeaders(t, h, http.MethodPost, target, tc.body, nil)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != "invalid_argument" {
				t.Errorf("code = %q, want invalid_argument", body.Code)
			}
			requireFieldDetail(t, body, tc.field)
		})
	}

	if rows := taskRowsByProject(t, st, projectID); len(rows) != 0 {
		t.Errorf("persisted tasks = %d, want 0", len(rows))
	}
	state := mustProjectState(t, st, projectID)
	if state.Status != domain.StatusTasksDraft || state.Version != 6 {
		t.Errorf("project = %d/v%d, want tasks_draft/v6", state.Status, state.Version)
	}
}

func TestSubmitPlanHTTPCycle(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID, planBody(
		planTask("a", "b", map[string]any{"depends_on": []int{2}}),
		planTask("b", "b", map[string]any{"depends_on": []int{3}}),
		planTask("c", "b", map[string]any{"depends_on": []int{1}}),
	), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "invalid_argument" {
		t.Errorf("code = %q, want invalid_argument", body.Code)
	}
	if !strings.Contains(body.Message, "dependency cycle: 1 → 2 → 3 → 1") {
		t.Errorf("message = %q, want the cycle path", body.Message)
	}
	requireFieldDetail(t, body, "tasks[0].depends_on")
	if len(body.Details) == 0 || !strings.Contains(body.Details[0].Reason, "1 → 2 → 3 → 1") {
		t.Errorf("details = %+v, want the cycle path", body.Details)
	}
}

func TestSubmitPlanHTTPInvalidStateZeroSideEffects(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	statuses := []int{
		domain.StatusRequirementDraft,
		domain.StatusRequirementReview,
		domain.StatusSpecDraft,
		domain.StatusSpecReview,
		domain.StatusCompleted,
		domain.StatusCancelled,
	}
	reason := "历史理由"
	for _, status := range statuses {
		t.Run(domain.StatusName(status), func(t *testing.T) {
			forceProjectState(t, st, projectID, status, 9, &reason)

			rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
				planBody(fiveHTTPPlanTasks()...), nil)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
			}
			if body := decodeError(t, rec); body.Code != "invalid_state" {
				t.Errorf("code = %q, want invalid_state", body.Code)
			}
			if rows := taskRowsByProject(t, st, projectID); len(rows) != 0 {
				t.Errorf("persisted tasks = %d, want 0", len(rows))
			}
			if got := countDeps(t, st, projectID); got != 0 {
				t.Errorf("task_deps = %d, want 0", got)
			}
			state := mustProjectState(t, st, projectID)
			if state.Status != status || state.Version != 9 {
				t.Errorf("project = %d/v%d, want %d/v9", state.Status, state.Version, status)
			}
			if state.Reason == nil || *state.Reason != reason {
				t.Errorf("reason = %v, want %q", state.Reason, reason)
			}
		})
	}
}

func TestSubmitPlanHTTPNotFound(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id=APP-404",
		planBody(fiveHTTPPlanTasks()...), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if body := decodeError(t, rec); body.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Code)
	}
}

func TestSubmitPlanHTTPSecondBatchAppends(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	first := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(fiveHTTPPlanTasks()...), nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}

	second := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(planTask("追加", "b")), nil)
	if second.Code != http.StatusCreated {
		t.Fatalf("second status = %d, want 201 (body %s)", second.Code, second.Body.String())
	}
	appended := decodeSubmitPlan(t, second)
	if len(appended.Tasks) != 1 || appended.Tasks[0].ID != "APP-1-T0006" {
		t.Errorf("appended tasks = %+v, want APP-1-T0006", appended.Tasks)
	}
	if appended.Status != domain.StatusTasksReview || appended.ProjectVersion != 7 {
		t.Errorf("project = %d/v%d, want tasks_review/v7 (no second bump)",
			appended.Status, appended.ProjectVersion)
	}

	rows := taskRowsByProject(t, st, projectID)
	if len(rows) != 6 {
		t.Errorf("persisted tasks = %d, want 6", len(rows))
	}
}

func TestSubmitPlanHTTPCompletedConflicts(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)
	forceProjectState(t, st, projectID, domain.StatusCompleted, 9, nil)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(planTask("迟到", "b")), nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", body.Code)
	}
}

func TestSubmitPlanHTTPMaxAttemptsFromConfig(t *testing.T) {
	// The HTTP test server takes the resolved attempt budget explicitly; the
	// app test proves the value comes from ECHUAN_MAX_ATTEMPTS via platform.Load.
	h, st := newProjectTestServerWithMaxAttempts(t, 5)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(planTask("t", "b")), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	for _, row := range taskRowsByProject(t, st, projectID) {
		if row.MaxAttempts != 5 {
			t.Errorf("%s max_attempts = %d, want 5", row.TaskID, row.MaxAttempts)
		}
	}
}

// mustProjectState reads status/version/reason directly so a guard failure can
// be proven to leave the row untouched.
func mustProjectState(t *testing.T, st *store.Store, projectID string) struct {
	Status  int
	Version int
	Reason  *string
} {
	t.Helper()
	var state struct {
		Status  int
		Version int
		Reason  *string
	}
	if err := st.Read().QueryRowContext(context.Background(), `
		SELECT status, version, reason FROM projects WHERE project_id = ?`, projectID).
		Scan(&state.Status, &state.Version, &state.Reason); err != nil {
		t.Fatalf("read project state: %v", err)
	}
	return state
}
