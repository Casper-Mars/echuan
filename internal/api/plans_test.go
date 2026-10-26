package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// createPlanningHTTPProject drives create → accept → approve so the project
// sits in planning (status 2) with version 3.
func createPlanningHTTPProject(t *testing.T, h http.Handler) string {
	t.Helper()
	projectID := createHTTPProject(t, h, "登录页改版")

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/accept?id="+projectID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	project := decodeProjectFromResponse(t, rec)

	rec = doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(project.Version)})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve spec: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if project := decodeProjectFromResponse(t, rec); project.Status != domain.StatusPlanning {
		t.Fatalf("status = %d, want planning", project.Status)
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
	projectID := createPlanningHTTPProject(t, h)

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
	if response.Status != domain.StatusPlanReview || response.StatusName != "plan_review" {
		t.Errorf("status = %d (%s), want 3 (plan_review)", response.Status, response.StatusName)
	}
	if response.ProjectVersion != 4 {
		t.Errorf("project_version = %d, want 4", response.ProjectVersion)
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
	if project.Status != domain.StatusPlanReview || project.Version != 4 {
		t.Errorf("project = %d/v%d, want plan_review/v4", project.Status, project.Version)
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
	projectID := createPlanningHTTPProject(t, h)

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
	if state.Status != domain.StatusPlanning || state.Version != 3 {
		t.Errorf("project = %d/v%d, want planning/v3", state.Status, state.Version)
	}
}

func TestSubmitPlanHTTPCycle(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createPlanningHTTPProject(t, h)

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
	projectID := createPlanningHTTPProject(t, h)

	statuses := []int{
		domain.StatusDraft,
		domain.StatusSpecReview,
		domain.StatusPlanReview,
		domain.StatusDeveloping,
		domain.StatusDeliveryReview,
		domain.StatusAccepted,
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

func TestSubmitPlanHTTPIdempotentSecondSubmitConflicts(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createPlanningHTTPProject(t, h)

	first := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(fiveHTTPPlanTasks()...), nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}

	second := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/plans?id="+projectID,
		planBody(planTask("重复", "b")), nil)
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, want 409 (body %s)", second.Code, second.Body.String())
	}
	if body := decodeError(t, second); body.Code != "invalid_state" {
		t.Errorf("second code = %q, want invalid_state", body.Code)
	}

	rows := taskRowsByProject(t, st, projectID)
	if len(rows) != 5 {
		t.Errorf("persisted tasks = %d, want 5", len(rows))
	}
	state := mustProjectState(t, st, projectID)
	if state.Status != domain.StatusPlanReview || state.Version != 4 {
		t.Errorf("project = %d/v%d, want plan_review/v4", state.Status, state.Version)
	}
}

func TestSubmitPlanHTTPConcurrent(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createPlanningHTTPProject(t, h)

	payload, err := json.Marshal(planBody(fiveHTTPPlanTasks()...))
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}

	const attempts = 10
	var wg sync.WaitGroup
	statuses := make(chan int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/projects/plans?id="+projectID, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(HeaderActor, "test-suite")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			statuses <- rec.Code
		}()
	}
	wg.Wait()
	close(statuses)

	created, conflicts := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if created != 1 {
		t.Errorf("201 count = %d, want exactly 1", created)
	}
	if conflicts != attempts-1 {
		t.Errorf("409 count = %d, want %d", conflicts, attempts-1)
	}

	rows := taskRowsByProject(t, st, projectID)
	if len(rows) != 5 {
		t.Fatalf("persisted tasks = %d, want 5", len(rows))
	}
	for i, row := range rows {
		if row.SeqNo != i+1 || row.TaskID != fmt.Sprintf("APP-1-T%04d", i+1) {
			t.Errorf("row %d = %s/seq %d, want contiguous", i, row.TaskID, row.SeqNo)
		}
	}
	if got := countDeps(t, st, projectID); got != 6 {
		t.Errorf("task_deps = %d, want 6", got)
	}
}

func TestSubmitPlanHTTPMaxAttemptsFromConfig(t *testing.T) {
	// The HTTP test server takes the resolved attempt budget explicitly; the
	// app test proves the value comes from ECHUAN_MAX_ATTEMPTS via platform.Load.
	h, st := newProjectTestServerWithMaxAttempts(t, 5)
	seedProduct(t, st, "APP")
	projectID := createPlanningHTTPProject(t, h)

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
