package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// TestClaimWhileHoldingIsConflict is D-1 (issue #165) over the real router: a
// worker that still holds an in_progress card and claims again — with another
// card queued — must answer 409 conflict naming the held card, not the 500 the
// unique index ux_tasks_worker_lease used to produce
// ("UNIQUE constraint failed: tasks.worker_id").
//
// The assertions after the 409 are what prove the transaction really rolled back:
// the queued card is still ready and unowned, the holder still owns its card,
// and another registered worker can still claim it.
func TestClaimWhileHoldingIsConflict(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "name": "p",
	})
	project := decodeBody[v3ProjectResponse](t, payload)

	for _, taskID := range []string{"t_first", "t_second"} {
		if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
			"project_id": project.ProjectID, "task_id": taskID, "kind": "dev", "title": taskID,
		}); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s = %d (%s)", taskID, resp.StatusCode, body)
		}
	}

	a.registerWorker(t, "w_hold")
	resp, payload := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_hold", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first claim = %d (%s), want 200", resp.StatusCode, payload)
	}
	if claimed := decodeBody[v3ClaimResponse](t, payload); claimed.Task.TaskID != "t_first" {
		t.Fatalf("first claim = %q, want t_first", claimed.Task.TaskID)
	}

	// The regression: t_second is queued, so the claim reaches the lease write
	// and hits the one-lease-per-worker index. It must be a conflict.
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_hold", "kind": "dev",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("claim while holding = %d (%s), want 409", resp.StatusCode, payload)
	}
	body := decodeBody[ErrorBody](t, payload)
	if body.Code != "conflict" {
		t.Errorf("claim while holding code = %q, want conflict", body.Code)
	}
	if !strings.Contains(body.Message, "已持卡") {
		t.Errorf("claim while holding message = %q, want it to say 已持卡", body.Message)
	}

	// The queued card is untouched: the transaction rolled back, so no lease,
	// no owner and no attempt bump landed on it.
	resp, payload = a.do(t, http.MethodGet, "/v1/tasks?id=t_second", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get t_second = %d (%s)", resp.StatusCode, payload)
	}
	queued := decodeBody[v3TaskResponse](t, payload)
	if queued.Status != enum.Ready || queued.Attempt != 0 {
		t.Errorf("t_second after 409 = status %v attempt %d, want ready/0", queued.Status, queued.Attempt)
	}
	if queued.WorkerID != nil || queued.LeaseID != nil {
		t.Errorf("t_second after 409 = worker %v lease %v, want both null", queued.WorkerID, queued.LeaseID)
	}

	// The holder keeps its own card (the 409 did not reconcile anything away).
	resp, payload = a.do(t, http.MethodGet, "/v1/tasks?id=t_first", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get t_first = %d (%s)", resp.StatusCode, payload)
	}
	held := decodeBody[v3TaskResponse](t, payload)
	if held.Status != enum.InProgress || held.WorkerID == nil || *held.WorkerID != "w_hold" {
		t.Errorf("t_first after 409 = status %v worker %v, want in_progress/w_hold", held.Status, held.WorkerID)
	}

	// The queue is not poisoned: a second worker gets the card.
	a.registerWorker(t, "w_free")
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_free", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second worker claim = %d (%s), want 200", resp.StatusCode, payload)
	}
	if claimed := decodeBody[v3ClaimResponse](t, payload); claimed.Task.TaskID != "t_second" {
		t.Errorf("second worker claimed %q, want t_second", claimed.Task.TaskID)
	}

	// A busy worker facing an empty queue keeps the ordinary 204: "no work" is
	// not a conflict, and the e2e contract depends on it.
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_hold", "kind": "requirement",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("busy worker with empty queue = %d (%s), want 204", resp.StatusCode, payload)
	}
	if len(payload) != 0 {
		t.Errorf("204 body = %q, want empty", payload)
	}
}

// TestClaimUnregisteredStillConflict is AC2's non-regression half: the D-1 fix
// must not reshuffle the registry check. An id that never sent a heartbeat is a
// 409 even with work queued, one heartbeat later the same id claims for real.
func TestClaimUnregisteredStillConflict(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "name": "p",
	})
	project := decodeBody[v3ProjectResponse](t, payload)
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_reg", "kind": "dev", "title": "queued",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, body)
	}

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_ghost", "kind": "dev",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("claim(unregistered) = %d (%s), want 409", resp.StatusCode, payload)
	}
	if body := decodeBody[ErrorBody](t, payload); !strings.Contains(body.Message, "未注册") {
		t.Errorf("claim(unregistered) message = %q, want it to say 未注册", body.Message)
	}

	a.registerWorker(t, "w_ghost")
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_ghost", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim after heartbeat = %d (%s), want 200", resp.StatusCode, payload)
	}
}
