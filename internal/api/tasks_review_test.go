package api

import (
	"net/http"
	"testing"

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

	t.Run("reject is not implemented", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/review?project_id="+projectID,
			map[string]any{"decision": "reject"}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_argument" {
			t.Errorf("code = %q, want invalid_argument", body.Code)
		}
		requireFieldDetail(t, body, "decision")
		if state := mustProjectState(t, st, projectID); state.Status != domain.StatusTasksReview {
			t.Errorf("project status = %d, want it untouched", state.Status)
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
