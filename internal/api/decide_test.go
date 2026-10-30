package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/store"
)

// doJSONHeaders is doJSON with explicit headers on top of the actor default.
func doJSONHeaders(t *testing.T, h http.Handler, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderActor, "alice")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// createHTTPProject creates a draft project through the API and returns its
// business id.
func createHTTPProject(t *testing.T, h http.Handler, title string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          title,
		"requirement_md": "# 需求\n\n正文\n",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	return decodeProjectDetail(t, rec).ProjectID
}

// forceProjectState moves a project to a status/version without walking the
// state machine, so guards can be tested directly.
func forceProjectState(t *testing.T, st *store.Store, projectID string, status, version int, reason *string) {
	t.Helper()
	if _, err := st.Write().ExecContext(context.Background(), `
		UPDATE projects SET status = ?, version = ?, reason = ? WHERE project_id = ?`,
		status, version, reason, projectID); err != nil {
		t.Fatalf("force project state: %v", err)
	}
}

func decodeProjectFromResponse(t *testing.T, rec *httptest.ResponseRecorder) projectResponse {
	t.Helper()
	var project projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project %q: %v", rec.Body.String(), err)
	}
	return project
}

func TestSubmitProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createHTTPProject(t, h, "登录页改版")

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id="+projectID, nil,
		map[string]string{HeaderActor: "alice"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	project := decodeProjectFromResponse(t, rec)
	if project.Status != 1 || project.StatusName != "requirement_review" {
		t.Errorf("status = %d (%s), want 1 (requirement_review)", project.Status, project.StatusName)
	}
	if project.Reason != nil {
		t.Errorf("reason = %v, want null", *project.Reason)
	}
	if project.Version != 2 {
		t.Errorf("version = %d, want 2", project.Version)
	}
	if project.LastActor == nil || *project.LastActor != "alice" {
		t.Errorf("last_actor = %v, want alice", project.LastActor)
	}

	// A second submit is invalid state, not a version problem.
	again := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id="+projectID, nil, nil)
	if again.Code != http.StatusConflict {
		t.Fatalf("second submit status = %d, want 409", again.Code)
	}
	if body := decodeError(t, again); body.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", body.Code)
	}
}

func TestSubmitProjectWithoutRequirementHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createHTTPProject(t, h, "无需求项目")

	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET requirement_material_id = NULL WHERE project_id = ?`, projectID); err != nil {
		t.Fatalf("null requirement: %v", err)
	}

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id="+projectID, nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", body.Code)
	}
	if body.Message != "请先上传需求文档" {
		t.Errorf("message = %q, want 请先上传需求文档", body.Message)
	}
}

func TestDecideProjectHTTPValidation(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createHTTPProject(t, h, "校验")
	forceProjectState(t, st, projectID, 1, 1, nil)
	target := "/v1/projects/decide?id=" + projectID

	t.Run("missing If-Match", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, target,
			map[string]any{"decision": "approve"}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_argument" || len(body.Details) != 1 || body.Details[0].Field != "If-Match" {
			t.Errorf("body = %+v, want invalid_argument on If-Match", body)
		}
	})

	t.Run("malformed If-Match", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, target,
			map[string]any{"decision": "approve"}, map[string]string{"If-Match": "abc"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if body := decodeError(t, rec); body.Details[0].Field != "If-Match" {
			t.Errorf("details = %+v", body.Details)
		}
	})

	t.Run("reject without reason", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, target,
			map[string]any{"decision": "reject"}, map[string]string{"If-Match": "1"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_argument" || body.Details[0].Field != "reason" {
			t.Errorf("body = %+v, want invalid_argument on reason", body)
		}
	})

	t.Run("invalid decision", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, target,
			map[string]any{"decision": "maybe"}, map[string]string{"If-Match": "1"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if body := decodeError(t, rec); body.Details[0].Field != "decision" {
			t.Errorf("details = %+v", body.Details)
		}
	})

	t.Run("stale version", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, target,
			map[string]any{"decision": "approve"}, map[string]string{"If-Match": "999"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_version" {
			t.Errorf("code = %q, want invalid_version", body.Code)
		}
		if !strings.Contains(body.Message, "current version is 1") {
			t.Errorf("message = %q, want the current version", body.Message)
		}
	})
}

func TestDecideProjectHTTPFlow(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	t.Run("draft cannot approve", func(t *testing.T) {
		projectID := createHTTPProject(t, h, "草稿直接批准")
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
			map[string]any{"decision": "approve"}, map[string]string{"If-Match": "1"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "invalid_state" {
			t.Errorf("code = %q, want invalid_state", body.Code)
		}
	})

	t.Run("requirement approve clears reason", func(t *testing.T) {
		projectID := createHTTPProject(t, h, "需求批准")
		old := "旧理由"
		forceProjectState(t, st, projectID, 1, 3, &old)

		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
			map[string]any{"decision": "approve"}, map[string]string{"If-Match": "3"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		project := decodeProjectFromResponse(t, rec)
		if project.Status != 2 || project.StatusName != "spec_draft" {
			t.Errorf("status = %d (%s), want spec_draft", project.Status, project.StatusName)
		}
		if project.Reason != nil {
			t.Errorf("reason = %v, want null", *project.Reason)
		}
		if project.Version != 4 {
			t.Errorf("version = %d, want 4", project.Version)
		}
	})

	t.Run("requirement reject keeps the reason", func(t *testing.T) {
		projectID := createHTTPProject(t, h, "需求驳回")
		forceProjectState(t, st, projectID, 1, 1, nil)

		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
			map[string]any{"decision": "reject", "reason": "缺少验收标准"},
			map[string]string{"If-Match": "1", HeaderActor: "reviewer"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		project := decodeProjectFromResponse(t, rec)
		if project.Status != 0 {
			t.Errorf("status = %d, want requirement_draft", project.Status)
		}
		if project.Reason == nil || *project.Reason != "缺少验收标准" {
			t.Errorf("reason = %v, want the rejection reason", project.Reason)
		}
		if project.Version != 2 {
			t.Errorf("version = %d, want 2", project.Version)
		}
		if project.LastActor == nil || *project.LastActor != "reviewer" {
			t.Errorf("last_actor = %v, want reviewer", project.LastActor)
		}
	})

	t.Run("spec approve moves to tasks_draft", func(t *testing.T) {
		projectID := createHTTPProject(t, h, "方案批准")
		forceProjectState(t, st, projectID, 3, 1, nil)

		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
			map[string]any{"decision": "approve"}, map[string]string{"If-Match": "1"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		project := decodeProjectFromResponse(t, rec)
		if project.Status != 4 || project.StatusName != "tasks_draft" {
			t.Errorf("status = %d (%s), want tasks_draft", project.Status, project.StatusName)
		}
	})
}

func TestCancelProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	t.Run("developing cancels unfinished tasks", func(t *testing.T) {
		projectID := createHTTPProject(t, h, "取消项目")
		forceProjectState(t, st, projectID, 4, 1, nil)
		if _, err := st.Write().ExecContext(context.Background(), `
			INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
			SELECT 'T1', id, 1, '任务1', '正文', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z' FROM projects WHERE project_id = ?`,
			projectID); err != nil {
			t.Fatalf("insert task: %v", err)
		}

		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/cancel?id="+projectID, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		project := decodeProjectFromResponse(t, rec)
		if project.Status != 8 || project.StatusName != "cancelled" {
			t.Errorf("status = %d (%s), want cancelled(8)", project.Status, project.StatusName)
		}

		var cancelled int64
		if err := st.Read().QueryRowContext(context.Background(), `
			SELECT COUNT(*) FROM tasks t JOIN projects p ON p.id = t.project_id
			 WHERE p.project_id = ? AND t.status = 7`, projectID).Scan(&cancelled); err != nil {
			t.Fatalf("count cancelled tasks: %v", err)
		}
		if cancelled != 1 {
			t.Errorf("cancelled tasks = %d, want 1", cancelled)
		}
	})

	t.Run("completed cannot be cancelled", func(t *testing.T) {
		projectID := createHTTPProject(t, h, "已完成")
		forceProjectState(t, st, projectID, 7, 2, nil)
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/cancel?id="+projectID, nil, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "invalid_state" {
			t.Errorf("code = %q, want invalid_state", body.Code)
		}
	})
}

func TestTerminalProjectRejectsHTTPActions(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	for _, terminal := range []int{7, 8} {
		projectID := createHTTPProject(t, h, "终态")
		forceProjectState(t, st, projectID, terminal, 1, nil)

		requests := []struct {
			method string
			target string
			body   any
			header map[string]string
		}{
			{http.MethodPost, "/v1/projects/submit?id=" + projectID, nil, nil},
			{http.MethodPost, "/v1/projects/decide?id=" + projectID, map[string]any{"decision": "approve"}, map[string]string{"If-Match": "1"}},
			{http.MethodPost, "/v1/projects/cancel?id=" + projectID, nil, nil},
		}
		for _, req := range requests {
			rec := doJSONHeaders(t, h, req.method, req.target, req.body, req.header)
			if rec.Code != http.StatusConflict {
				t.Errorf("terminal %d %s: status = %d, want 409", terminal, req.target, rec.Code)
				continue
			}
			if body := decodeError(t, rec); body.Code != "invalid_state" {
				t.Errorf("terminal %d %s: code = %q, want invalid_state", terminal, req.target, body.Code)
			}
		}
	}
}

// TestConcurrentDecideHTTP races ten approves with the same If-Match; the HTTP
// contract says exactly one 200 and nine 409 invalid_version.
func TestConcurrentDecideHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createHTTPProject(t, h, "并发审批")
	forceProjectState(t, st, projectID, 1, 1, nil)

	const workers = 10
	payload, err := json.Marshal(map[string]any{"decision": "approve"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok      int
		invalid int
		others  []int
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost,
				"/v1/projects/decide?id="+projectID, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(HeaderActor, "racer")
			req.Header.Set("If-Match", "1")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				var body ErrorBody
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err == nil && body.Code == "invalid_version" {
					invalid++
					return
				}
				others = append(others, rec.Code)
			default:
				others = append(others, rec.Code)
			}
		}()
	}
	close(start)
	wg.Wait()

	if ok != 1 {
		t.Errorf("200 responses = %d, want 1", ok)
	}
	if invalid != workers-1 {
		t.Errorf("invalid_version responses = %d, want %d", invalid, workers-1)
	}
	if len(others) > 0 {
		t.Errorf("unexpected statuses: %v", others)
	}
}

func TestDecideUnknownProject(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-999",
		map[string]any{"decision": "approve"}, map[string]string{"If-Match": "1"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Code)
	}
}
