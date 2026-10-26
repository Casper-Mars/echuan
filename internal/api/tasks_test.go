package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// newTaskHTTPEnv builds the real middleware stack and a planning project.
func newTaskHTTPEnv(t *testing.T) (http.Handler, *store.Store, string) {
	t.Helper()
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	return h, st, createPlanningHTTPProject(t, h)
}

// postTask creates one card through the API and returns its detail.
func postTask(t *testing.T, h http.Handler, projectID, title, body string, priority int, dependsOn ...string) taskDetailResponse {
	t.Helper()
	payload := map[string]any{"title": title, "body_md": body, "priority": priority}
	if len(dependsOn) > 0 {
		payload["depends_on"] = dependsOn
	}
	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks?project_id="+projectID, payload, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST task %q: status = %d (body %s)", title, rec.Code, rec.Body.String())
	}
	return decodeTaskDetail(t, rec)
}

// requirePlatformFieldDetail asserts that body carries a detail for field whose
// reason says the column is maintained by the platform. Asserting only on the
// field name would also pass for a 400 raised by the unknown-field branch, which
// is exactly the regression this guards against.
func requirePlatformFieldDetail(t *testing.T, body ErrorBody, field string) {
	t.Helper()
	for _, detail := range body.Details {
		if detail.Field != field {
			continue
		}
		if !strings.Contains(detail.Reason, "maintained by the platform") {
			t.Fatalf("detail.reason = %q for field %q, want the platform-maintained reason", detail.Reason, field)
		}
		return
	}
	t.Fatalf("details = %+v, want a platform-maintained detail for field %q", body.Details, field)
}

func decodeTaskDetail(t *testing.T, rec *httptest.ResponseRecorder) taskDetailResponse {
	t.Helper()
	var detail taskDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode task detail %q: %v", rec.Body.String(), err)
	}
	return detail
}

func decodeTaskList(t *testing.T, rec *httptest.ResponseRecorder) taskListResponse {
	t.Helper()
	var page taskListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode task list %q: %v", rec.Body.String(), err)
	}
	return page
}

// forceTaskColumns writes platform columns directly so the read/write guards
// can be exercised without Task-10's claim flow.
func forceTaskColumns(t *testing.T, st *store.Store, taskID string, status, attempt int, leaseID, leaseUntil, reason *string) {
	t.Helper()
	if _, err := st.Write().ExecContext(context.Background(), `
		UPDATE tasks SET status = ?, attempt_count = ?, lease_id = ?, lease_until = ?, reason = ?
		 WHERE task_id = ?`, status, attempt, leaseID, leaseUntil, reason, taskID); err != nil {
		t.Fatalf("force task columns: %v", err)
	}
}

func TestCreateTaskHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks?project_id="+projectID,
		map[string]any{"title": "补一张卡", "body_md": "# 正文\n", "priority": 5}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/tasks?id=APP-1-T0001" {
		t.Errorf("Location = %q, want /v1/tasks?id=APP-1-T0001", loc)
	}

	detail := decodeTaskDetail(t, rec)
	if detail.ID != "APP-1-T0001" || detail.TaskID != "APP-1-T0001" {
		t.Errorf("ids = %q/%q", detail.ID, detail.TaskID)
	}
	if detail.ProjectID != projectID || detail.SeqNo != 1 {
		t.Errorf("project/seq = %q/%d", detail.ProjectID, detail.SeqNo)
	}
	if detail.Status != domain.TaskBacklog || detail.StatusName != "backlog" {
		t.Errorf("status = %d (%s), want backlog", detail.Status, detail.StatusName)
	}
	if detail.Priority != 5 || detail.BodyMD != "# 正文\n" {
		t.Errorf("priority/body = %d/%q", detail.Priority, detail.BodyMD)
	}
	if detail.Version != 1 || detail.AttemptCount != 0 || detail.MaxAttempts != 3 {
		t.Errorf("version/attempts = %d/%d/%d", detail.Version, detail.AttemptCount, detail.MaxAttempts)
	}
	if detail.LastActor == nil || *detail.LastActor != "alice" {
		t.Errorf("last_actor = %v, want alice", detail.LastActor)
	}
	if detail.DependsOn == nil || len(detail.DependsOn) != 0 {
		t.Errorf("depends_on = %v, want []", detail.DependsOn)
	}
	if detail.Report != nil || detail.Lease != nil {
		t.Errorf("report/lease = %v/%v, want null/null", detail.Report, detail.Lease)
	}

	// depends_on carries business ids and lands in task_deps.
	second := postTask(t, h, projectID, "补测试", "b", 1, "APP-1-T0001")
	if second.ID != "APP-1-T0002" || len(second.DependsOn) != 1 || second.DependsOn[0] != "APP-1-T0001" {
		t.Errorf("second = %+v", second)
	}
	if got := countDeps(t, st, projectID); got != 1 {
		t.Errorf("task_deps = %d, want 1", got)
	}

	// The sequence number keeps counting and stays zero padded to 4 digits.
	for i := 3; i <= 10; i++ {
		created := postTask(t, h, projectID, "卡片", "b", 0)
		if want := fmt.Sprintf("APP-1-T%04d", i); created.ID != want {
			t.Fatalf("card %d = %s, want %s", i, created.ID, want)
		}
	}
}

func TestCreateTaskHTTPMaxAttemptsFromConfig(t *testing.T) {
	h, st := newProjectTestServerWithMaxAttempts(t, 5)
	seedProduct(t, st, "APP")
	projectID := createPlanningHTTPProject(t, h)

	created := postTask(t, h, projectID, "上限", "b", 0)
	if created.MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", created.MaxAttempts)
	}
}

func TestCreateTaskHTTPErrors(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	draftID := createHTTPProject(t, h, "草稿项目")

	cases := []struct {
		name   string
		target string
		body   any
		status int
		code   string
		field  string
	}{
		{"missing project_id", "/v1/tasks", map[string]any{"title": "t", "body_md": "b"}, 400, "invalid_argument", "project_id"},
		{"missing project", "/v1/tasks?project_id=APP-404", map[string]any{"title": "t", "body_md": "b"}, 404, "not_found", ""},
		{"not planning", "/v1/tasks?project_id=" + draftID, map[string]any{"title": "t", "body_md": "b"}, 409, "invalid_state", ""},
		{"blank title", "/v1/tasks?project_id=" + projectID, map[string]any{"title": "  ", "body_md": "b"}, 400, "invalid_argument", "title"},
		{"blank body", "/v1/tasks?project_id=" + projectID, map[string]any{"title": "t", "body_md": " "}, 400, "invalid_argument", "body_md"},
		{"priority out of range", "/v1/tasks?project_id=" + projectID, map[string]any{"title": "t", "body_md": "b", "priority": 1001}, 400, "invalid_argument", "priority"},
		{"unknown field", "/v1/tasks?project_id=" + projectID, map[string]any{"title": "t", "body_md": "b", "bogus": 1}, 400, "invalid_argument", "bogus"},
		{"dependency missing", "/v1/tasks?project_id=" + projectID, map[string]any{"title": "t", "body_md": "b", "depends_on": []string{"APP-1-T0404"}}, 400, "invalid_argument", "depends_on"},
		{"self reference", "/v1/tasks?project_id=" + projectID, map[string]any{"title": "t", "body_md": "b", "depends_on": []string{"APP-1-T0001"}}, 400, "invalid_argument", "depends_on"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSONHeaders(t, h, http.MethodPost, tc.target, tc.body, nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.status, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Code, tc.code)
			}
			if tc.field != "" {
				requireFieldDetail(t, body, tc.field)
			}
		})
	}

	if rows := taskRowsByProject(t, st, projectID); len(rows) != 0 {
		t.Errorf("persisted tasks = %d, want 0", len(rows))
	}
	if rows := taskRowsByProject(t, st, draftID); len(rows) != 0 {
		t.Errorf("draft project tasks = %d, want 0", len(rows))
	}
}

func TestListTasksHTTPOrderAndFilters(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	priorities := []int{0, 10, 5, 10, -1000}
	for i, priority := range priorities {
		postTask(t, h, projectID, fmt.Sprintf("任务 %d", i+1), "b", priority)
	}

	rec := doJSON(t, h, http.MethodGet, "/v1/tasks?project_id="+projectID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	page := decodeTaskList(t, rec)
	if len(page.Items) != 5 {
		t.Fatalf("items = %d, want 5", len(page.Items))
	}
	if page.NextCursor != nil {
		t.Errorf("next_cursor = %v, want nil", page.NextCursor)
	}
	want := []string{"APP-1-T0002", "APP-1-T0004", "APP-1-T0003", "APP-1-T0001", "APP-1-T0005"}
	for i, item := range page.Items {
		if item.ID != want[i] {
			t.Errorf("items[%d] = %s, want %s", i, item.ID, want[i])
		}
	}

	// Repeated and comma-separated status filters are equivalent.
	forceTaskColumns(t, st, "APP-1-T0001", domain.TaskReady, 0, nil, nil, nil)
	forceTaskColumns(t, st, "APP-1-T0002", domain.TaskInProgress, 1, nil, nil, nil)

	repeated := doJSON(t, h, http.MethodGet, "/v1/tasks?project_id="+projectID+"&status=1&status=2", nil)
	comma := doJSON(t, h, http.MethodGet, "/v1/tasks?project_id="+projectID+"&status=1,2", nil)
	if repeated.Code != http.StatusOK || comma.Code != http.StatusOK {
		t.Fatalf("status codes = %d/%d", repeated.Code, comma.Code)
	}
	repeatedIDs := taskIDs(decodeTaskList(t, repeated))
	commaIDs := taskIDs(decodeTaskList(t, comma))
	if strings.Join(repeatedIDs, ",") != strings.Join(commaIDs, ",") {
		t.Errorf("repeated = %v, comma = %v", repeatedIDs, commaIDs)
	}
	if len(repeatedIDs) != 2 {
		t.Errorf("filtered items = %d, want 2", len(repeatedIDs))
	}

	// Invalid filters.
	for _, target := range []string{
		"/v1/tasks?project_id=",
		"/v1/tasks?project_id=" + projectID + "&status=",
		"/v1/tasks?project_id=" + projectID + "&status=9",
		"/v1/tasks?project_id=" + projectID + "&status=abc",
		"/v1/tasks?project_id=" + projectID + "&limit=abc",
		"/v1/tasks?project_id=" + projectID + "&limit=0",
		"/v1/tasks?project_id=" + projectID + "&limit=201",
		"/v1/tasks?project_id=" + projectID + "&cursor=not-a-cursor",
	} {
		rec := doJSON(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", target, rec.Code, rec.Body.String())
			continue
		}
		if body := decodeError(t, rec); body.Code != "invalid_argument" {
			t.Errorf("%s: code = %q", target, body.Code)
		}
	}

	// A product cursor (base64url "id:1") is not a task cursor.
	productCursor := encodeCursor(1)
	rec = doJSON(t, h, http.MethodGet, "/v1/tasks?project_id="+projectID+"&cursor="+productCursor, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("product cursor: status = %d, want 400", rec.Code)
	}
}

func TestListTasksHTTPProjectNotFound(t *testing.T) {
	h, _, _ := newTaskHTTPEnv(t)

	rec := doJSON(t, h, http.MethodGet, "/v1/tasks?project_id=NOPE", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Code)
	}
}

func taskIDs(page taskListResponse) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestListTasksHTTPPagination(t *testing.T) {
	h, _, projectID := newTaskHTTPEnv(t)
	for i := 0; i < 25; i++ {
		postTask(t, h, projectID, fmt.Sprintf("任务 %02d", i+1), "b", i%4)
	}

	seen := map[string]int{}
	var cursor string
	pages := 0
	for {
		target := "/v1/tasks?project_id=" + projectID + "&limit=10"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		rec := doJSON(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status = %d (body %s)", pages, rec.Code, rec.Body.String())
		}
		page := decodeTaskList(t, rec)
		pages++
		for _, id := range taskIDs(page) {
			seen[id]++
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if pages != 3 || len(seen) != 25 {
		t.Fatalf("pages/distinct = %d/%d, want 3/25", pages, len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("task %s appeared %d times", id, count)
		}
	}
}

func TestGetTaskHTTP(t *testing.T) {
	h, _, projectID := newTaskHTTPEnv(t)
	postTask(t, h, projectID, "一", "# 一\n", 0)
	postTask(t, h, projectID, "二", "# 二\n", 0, "APP-1-T0001")

	rec := doJSON(t, h, http.MethodGet, "/v1/tasks?id=APP-1-T0002", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	detail := decodeTaskDetail(t, rec)
	if detail.BodyMD != "# 二\n" {
		t.Errorf("body_md = %q", detail.BodyMD)
	}
	if len(detail.DependsOn) != 1 || detail.DependsOn[0] != "APP-1-T0001" {
		t.Errorf("depends_on = %v", detail.DependsOn)
	}
	if detail.Report != nil || detail.Lease != nil {
		t.Errorf("report/lease = %v/%v, want null/null", detail.Report, detail.Lease)
	}
	if detail.Status != domain.TaskBacklog || detail.Reason != nil {
		t.Errorf("status/reason = %d/%v", detail.Status, detail.Reason)
	}

	// The raw body carries explicit nulls for report and lease.
	if !strings.Contains(rec.Body.String(), `"report":null`) || !strings.Contains(rec.Body.String(), `"lease":null`) {
		t.Errorf("body = %s, want report/lease null", rec.Body.String())
	}

	if rec := doJSON(t, h, http.MethodGet, "/v1/tasks?id=APP-1-T0404", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing task: status = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, h, http.MethodGet, "/v1/tasks?id=", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("empty id: status = %d, want 400", rec.Code)
	}
}

func TestPatchTaskHTTP(t *testing.T) {
	h, _, projectID := newTaskHTTPEnv(t)
	created := postTask(t, h, projectID, "原标题", "原正文", 1)

	rec := doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id="+created.ID,
		map[string]any{"title": "新标题"}, map[string]string{HeaderActor: "human:pm"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	updated := decodeTaskDetail(t, rec)
	if updated.Title != "新标题" || updated.BodyMD != "原正文" || updated.Priority != 1 {
		t.Errorf("fields = %q/%q/%d", updated.Title, updated.BodyMD, updated.Priority)
	}
	if updated.Version != created.Version+1 {
		t.Errorf("version = %d, want %d", updated.Version, created.Version+1)
	}
	if !parseWireTime(t, updated.UpdateTime).After(parseWireTime(t, created.UpdateTime)) {
		t.Errorf("update_time did not advance: %q -> %q", created.UpdateTime, updated.UpdateTime)
	}
	if updated.LastActor == nil || *updated.LastActor != "human:pm" {
		t.Errorf("last_actor = %v", updated.LastActor)
	}
	if updated.Status != domain.TaskBacklog {
		t.Errorf("status = %d, want backlog", updated.Status)
	}

	rec = doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id="+created.ID,
		map[string]any{"body_md": "# 新正文\n", "priority": 999}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	updated = decodeTaskDetail(t, rec)
	if updated.BodyMD != "# 新正文\n" || updated.Priority != 999 {
		t.Errorf("fields = %q/%d", updated.BodyMD, updated.Priority)
	}

	// Every platform-maintained column is rejected with 400 invalid_argument
	// and a detail naming it with the platform-maintained reason. status in
	// particular is a 400, not a 409.
	//
	// The matrix is hard-coded on purpose instead of ranging over
	// taskPlatformFields: ranging over the list under test makes the matrix
	// shrink with the list, so dropping a column (e.g. status or reason) would
	// leave every remaining case green. With a fixed matrix a dropped column
	// falls through to the "unknown field" branch and the reason assertion
	// below turns red.
	for _, tc := range []struct {
		field string
		value any
	}{
		{"status", 2},
		{"task_id", "APP-1-T9999"},
		{"seq_no", 7},
		{"project_id", "APP-2"},
		{"attempt_count", 1},
		{"max_attempts", 9},
		{"lease_id", "lease-1"},
		{"lease_until", "2026-01-01T00:00:00Z"},
		{"report_material_id", "mat-1"},
		{"last_actor", "human:pm"},
		{"version", 5},
		{"create_time", "2026-01-01T00:00:00Z"},
		{"update_time", "2026-01-01T00:00:00Z"},
		{"reason", "理由"},
	} {
		t.Run("reject "+tc.field, func(t *testing.T) {
			rec := doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id="+created.ID,
				map[string]any{tc.field: tc.value}, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != "invalid_argument" {
				t.Errorf("code = %q, want invalid_argument", body.Code)
			}
			// The reason must come from the platform-maintained branch, not
			// from the generic unknown-field branch: a 400 alone cannot tell
			// the two apart, so a weakened guard would still pass.
			requirePlatformFieldDetail(t, body, tc.field)
		})
	}

	// Anything else is either an unknown field or a malformed value.
	for _, tc := range []struct {
		name  string
		body  any
		field string
	}{
		{"empty object", map[string]any{}, "body"},
		{"unknown field", map[string]any{"bogus": 1}, "bogus"},
		{"wrong type priority", map[string]any{"priority": "high"}, "priority"},
		{"blank title", map[string]any{"title": "  "}, "title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id="+created.ID, tc.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			requireFieldDetail(t, decodeError(t, rec), tc.field)
		})
	}

	// The rejected patches left the row untouched.
	rec = doJSON(t, h, http.MethodGet, "/v1/tasks?id="+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d", rec.Code)
	}
	final := decodeTaskDetail(t, rec)
	if final.Version != created.Version+2 {
		t.Errorf("version = %d, want %d", final.Version, created.Version+2)
	}

	if rec := doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id=APP-1-T0404",
		map[string]any{"title": "x"}, nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing task: status = %d, want 404", rec.Code)
	}
	if rec := doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id=",
		map[string]any{"title": "x"}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing id: status = %d, want 400", rec.Code)
	}

}

func TestPatchTaskHTTPConcurrent(t *testing.T) {
	h, _, projectID := newTaskHTTPEnv(t)
	created := postTask(t, h, projectID, "并发", "b", 0)

	const workers = 10
	var wg sync.WaitGroup
	codes := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			rec := doJSONHeaders(t, h, http.MethodPatch, "/v1/tasks?id="+created.ID,
				map[string]any{"title": fmt.Sprintf("并发-%d", index)}, nil)
			codes[index] = rec.Code
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("worker %d: status = %d, want 200", i, code)
		}
	}

	rec := doJSON(t, h, http.MethodGet, "/v1/tasks?id="+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d", rec.Code)
	}
	final := decodeTaskDetail(t, rec)
	if final.Version != created.Version+workers {
		t.Errorf("version = %d, want %d", final.Version, created.Version+workers)
	}
}

func TestDeleteTaskHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)
	first := postTask(t, h, projectID, "一", "b", 0)
	second := postTask(t, h, projectID, "二", "b", 0, first.ID)
	third := postTask(t, h, projectID, "三", "b", 0)

	if got := countDeps(t, st, projectID); got != 1 {
		t.Fatalf("task_deps = %d, want 1", got)
	}

	rec := doJSON(t, h, http.MethodDelete, "/v1/tasks?id="+second.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
	if got := countDeps(t, st, projectID); got != 0 {
		t.Errorf("task_deps = %d, want 0 (edges removed with the card)", got)
	}
	if got := taskRowsByProject(t, st, projectID); len(got) != 2 {
		t.Errorf("tasks = %d, want 2", len(got))
	}
	if rec := doJSON(t, h, http.MethodGet, "/v1/tasks?id="+second.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("deleted task: status = %d, want 404", rec.Code)
	}

	// in_progress is not deletable.
	leaseID := "lease-1"
	future := "2999-01-01T00:00:00Z"
	forceTaskColumns(t, st, first.ID, domain.TaskInProgress, 1, &leaseID, &future, nil)
	rec = doJSON(t, h, http.MethodDelete, "/v1/tasks?id="+first.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("in_progress delete: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "conflict" {
		t.Errorf("code = %q, want conflict", body.Code)
	}
	if !strings.Contains(body.Message, "in_progress") {
		t.Errorf("message = %q, want the status named", body.Message)
	}

	// A ready card that was already claimed is not deletable either.
	forceTaskColumns(t, st, third.ID, domain.TaskReady, 2, nil, nil, nil)
	rec = doJSON(t, h, http.MethodDelete, "/v1/tasks?id="+third.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("claimed delete: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if message := decodeError(t, rec).Message; !strings.Contains(message, "claimed") {
		t.Errorf("message = %q, want the attempt history named", message)
	}

	if rec := doJSON(t, h, http.MethodDelete, "/v1/tasks?id=APP-1-T0404", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing task: status = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, h, http.MethodDelete, "/v1/tasks?id=", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing id: status = %d, want 400", rec.Code)
	}
}

func TestTaskCursorRoundTrip(t *testing.T) {
	cursor := app.TaskCursor{Priority: -1000, SeqNo: 3, ID: 42}
	encoded := encodeTaskCursor(cursor)
	decoded, err := decodeTaskCursor(encoded)
	if err != nil {
		t.Fatalf("decodeTaskCursor(%q): %v", encoded, err)
	}
	if decoded != cursor {
		t.Errorf("decoded = %+v, want %+v", decoded, cursor)
	}

	for _, invalid := range []string{"", "!!!", encodeCursor(1), rawCursor("p:1:s:2")} {
		if _, err := decodeTaskCursor(invalid); err == nil {
			t.Errorf("decodeTaskCursor(%q) error = nil", invalid)
		}
	}
}

// parseWireTime parses a timestamp rendered by the API. Comparing the strings
// directly is wrong: RFC3339Nano drops trailing zeros, so "…56.92Z" sorts after
// "…56.921Z" as text while being earlier in time.
func parseWireTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse timestamp %q: %v", value, err)
	}
	return parsed
}

// rawCursor base64url-encodes an arbitrary payload, so malformed cursor shapes
// can be exercised without going through encodeTaskCursor.
func rawCursor(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}
