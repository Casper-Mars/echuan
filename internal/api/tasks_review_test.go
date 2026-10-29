package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// TestReviewTasksHTTP covers the approve branch plus the deliberately
// unimplemented reject branch (400 invalid_argument) and the state guard.
func TestReviewTasksHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)
	postTask(t, h, projectID, "第一张", "# 正文\n", 0)

	// The first card already opened the batch gate.
	if state := mustProjectState(t, st, projectID); state.Status != domain.StatusTasksReview {
		t.Fatalf("project status = %d, want tasks_review", state.Status)
	}

	t.Run("reject requires a reason", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID+"&decision=reject",
			map[string]any{}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_argument" {
			t.Errorf("code = %q, want invalid_argument", body.Code)
		}
		requireFieldDetail(t, body, "reason")
		if state := mustProjectState(t, st, projectID); state.Status != domain.StatusTasksReview {
			t.Errorf("project status = %d, want it untouched", state.Status)
		}
	})

	t.Run("reject records the objection", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID+"&decision=reject",
			map[string]any{"reason": "  拆分过粗  "}, map[string]string{HeaderActor: "cto"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeReviewTasks(t, rec.Body)
		if body.FromStatus != domain.StatusTasksReview || body.Status != domain.StatusTasksReview {
			t.Errorf("edge = %d→%d, want 5→5", body.FromStatus, body.Status)
		}
		if body.ReadyTasks != 0 {
			t.Errorf("ready_tasks = %d, want 0", body.ReadyTasks)
		}
		if body.Reason == nil || *body.Reason != "拆分过粗" {
			t.Errorf("reason = %v, want the trimmed text", body.Reason)
		}
		if body.RejectedAt == nil {
			t.Error("rejected_at = null, want a timestamp")
		}
		if _, err := time.Parse(time.RFC3339, *body.RejectedAt); err != nil {
			t.Errorf("rejected_at %q is not RFC3339: %v", *body.RejectedAt, err)
		}
		state := mustProjectState(t, st, projectID)
		if state.Status != domain.StatusTasksReview || state.Version != body.Version {
			t.Errorf("project = status %d version %d, want tasks_review/%d", state.Status, state.Version, body.Version)
		}
		for _, row := range taskRowsByProject(t, st, projectID) {
			if row.Status != domain.TaskBacklog {
				t.Errorf("%s status = %d, want backlog", row.TaskID, row.Status)
			}
		}
	})

	t.Run("invalid decision", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID,
			map[string]any{"decision": "maybe"}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		requireFieldDetail(t, decodeError(t, rec), "decision")
	})

	t.Run("missing project_id", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review",
			map[string]any{"decision": "approve"}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		requireFieldDetail(t, decodeError(t, rec), "project_id")
	})

	t.Run("approve", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID,
			map[string]any{"decision": "approve"}, map[string]string{HeaderActor: "cto"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeReviewTasks(t, rec.Body)
		if body.Status != domain.StatusPendingDone || body.StatusName != "pending_done" {
			t.Errorf("status = %d (%s), want pending_done", body.Status, body.StatusName)
		}
		if body.FromStatus != domain.StatusTasksReview || body.ReadyTasks != 1 {
			t.Errorf("edge/ready = %d→%d/%d", body.FromStatus, body.Status, body.ReadyTasks)
		}
		for _, row := range taskRowsByProject(t, st, projectID) {
			if row.Status != domain.TaskReady {
				t.Errorf("%s status = %d, want ready", row.TaskID, row.Status)
			}
		}
	})
}

// TestReviewTasksHTTPWrongState pins the 409 when the project has no open batch.
func TestReviewTasksHTTPWrongState(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID,
		map[string]any{"decision": "approve"}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", body.Code)
	}
	if state := mustProjectState(t, st, projectID); state.Status != domain.StatusTasksDraft {
		t.Errorf("project status = %d, want tasks_draft", state.Status)
	}
}

// TestReviewTasksHTTPPendingDoneAddCard pins the 6 → 6 self-loop over HTTP: a
// card added after approval lands in backlog, the repeated batch approval
// answers 200 and leaves the project in pending_done.
func TestReviewTasksHTTPPendingDoneAddCard(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)
	postTask(t, h, projectID, "第一张", "# 正文\n", 0)

	approve := func() *httptest.ResponseRecorder {
		return doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID,
			map[string]any{"decision": "approve"}, map[string]string{HeaderActor: "cto"})
	}
	if rec := approve(); rec.Code != http.StatusOK {
		t.Fatalf("first approve: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	before := mustProjectState(t, st, projectID)
	if before.Status != domain.StatusPendingDone {
		t.Fatalf("project status = %d, want pending_done", before.Status)
	}

	postTask(t, h, projectID, "追加卡", "# 追加卡\n", 0)
	if state := mustProjectState(t, st, projectID); state.Status != domain.StatusPendingDone {
		t.Fatalf("project status after add = %d, want pending_done", state.Status)
	}

	rec := approve()
	if rec.Code != http.StatusOK {
		t.Fatalf("second approve: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeReviewTasks(t, rec.Body)
	if body.FromStatus != domain.StatusPendingDone || body.Status != domain.StatusPendingDone {
		t.Errorf("edge = %d→%d, want 6→6", body.FromStatus, body.Status)
	}
	if body.ReadyTasks != 1 {
		t.Errorf("ready_tasks = %d, want 1", body.ReadyTasks)
	}
	after := mustProjectState(t, st, projectID)
	if after.Status != domain.StatusPendingDone || after.Version != before.Version+1 {
		t.Errorf("state = %d/version %d, want pending_done/version %d", after.Status, after.Version, before.Version+1)
	}
	for _, row := range taskRowsByProject(t, st, projectID) {
		if row.Status != domain.TaskReady {
			t.Errorf("%s status = %d, want ready", row.TaskID, row.Status)
		}
	}

	// No backlog left: the same call is a 409 again.
	if rec := approve(); rec.Code != http.StatusConflict {
		t.Fatalf("empty re-approval: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestReviewTasksProjectDTOExposesRejection pins the two new project fields on
// the wire: set after a reject, null again after an approve.
func TestReviewTasksProjectDTOExposesRejection(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	projectID := createTasksDraftHTTPProject(t, h)
	postTask(t, h, projectID, "第一张", "# 正文\n", 0)

	post := func(decision string, body map[string]any) *httptest.ResponseRecorder {
		return doJSONHeaders(t, h, http.MethodPost,
			"/v1/tasks/review?project_id="+projectID+"&decision="+decision, body,
			map[string]string{HeaderActor: "cto"})
	}
	if rec := post("reject", map[string]any{"reason": "需要补充验收标准"}); rec.Code != http.StatusOK {
		t.Fatalf("reject: status = %d (body %s)", rec.Code, rec.Body.String())
	}

	get := func() map[string]any {
		rec := doJSONHeaders(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET project: status = %d (body %s)", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode project: %v", err)
		}
		return body
	}

	body := get()
	if body["tasks_review_reason"] != "需要补充验收标准" {
		t.Errorf("tasks_review_reason = %v, want the rejection text", body["tasks_review_reason"])
	}
	if _, ok := body["tasks_review_rejected_at"].(string); !ok {
		t.Errorf("tasks_review_rejected_at = %v, want a timestamp", body["tasks_review_rejected_at"])
	}

	if rec := post("approve", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("approve: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	body = get()
	if body["tasks_review_reason"] != nil || body["tasks_review_rejected_at"] != nil {
		t.Errorf("rejection = %v/%v, want both null after approve",
			body["tasks_review_reason"], body["tasks_review_rejected_at"])
	}
}
