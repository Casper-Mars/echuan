package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// setWorkerLastSeen rewrites workers.last_seen so the TTL boundary can be probed
// without waiting: HTTP cannot verify the exact 45s edge (a round trip turns
// 45.0s into 45.1s), but the two sides are stable at 40s and 50s.
func (a *apiServer) setWorkerLastSeen(t *testing.T, workerID string, seen time.Time) {
	t.Helper()
	if _, err := a.store.Write().ExecContext(context.Background(),
		`UPDATE workers SET last_seen = ? WHERE worker_id = ?`,
		seen.UTC().Format("2006-01-02T15:04:05.000Z"), workerID); err != nil {
		t.Fatalf("update last_seen %s: %v", workerID, err)
	}
}

// workerDetailKeys is AC1's field set for the bare detail object.
var workerDetailKeys = []string{
	"worker_id", "kinds", "state", "current_task_id", "current_work_item_id",
	"current_lease_id", "first_seen", "last_seen", "presence",
	"heartbeat_interval_ms", "lease_ttl_seconds", "metadata",
}

func decodeRaw(t *testing.T, payload []byte) map[string]json.RawMessage {
	t.Helper()
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("decode raw body: %v (%s)", err, payload)
	}
	return raw
}

// TestWorkerDetailBareObject is AC1: GET /v1/workers?id=<registered> answers 200
// with the bare worker object (no items envelope), every listed field present,
// presence in {active,offline}, the configured rhythm (default 15s / 90s) and
// the held card as a `t_...` business id.
func TestWorkerDetailBareObject(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_cur", "kind": "dev", "title": "执行中",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, body)
	}

	a.registerWorker(t, "w_busy")
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_busy", "kind": "dev",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, body)
	}
	a.registerWorker(t, "w_idle")

	resp, payload := a.do(t, http.MethodGet, "/v1/workers?id=w_busy", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("worker detail = %d (%s)", resp.StatusCode, payload)
	}
	raw := decodeRaw(t, payload)
	for _, key := range workerDetailKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("detail missing field %q: %s", key, payload)
		}
	}
	detail := decodeBody[v3WorkerDetailResponse](t, payload)
	if detail.WorkerID != "w_busy" || detail.State != enum.WorkerBusy {
		t.Errorf("detail = %+v, want w_busy/busy", detail)
	}
	if len(detail.Kinds) == 0 {
		t.Errorf("detail kinds empty, want the heartbeated kinds")
	}
	if detail.Presence != "active" && detail.Presence != "offline" {
		t.Errorf("presence = %q, want active|offline", detail.Presence)
	}
	if detail.Presence != "active" {
		t.Errorf("presence of a just-claimed worker = %q, want active", detail.Presence)
	}
	if detail.HeartbeatIntervalMS != 15000 || detail.LeaseTTLSeconds != 90 {
		t.Errorf("rhythm = %dms/%ds, want 15000ms/90s (default config)", detail.HeartbeatIntervalMS, detail.LeaseTTLSeconds)
	}
	if detail.CurrentWorkItemID == nil || *detail.CurrentWorkItemID != "t_cur" {
		t.Errorf("current_work_item_id = %v, want t_cur (same source as the listing)", detail.CurrentWorkItemID)
	}
	if detail.CurrentTaskID == nil || detail.CurrentLeaseID == nil {
		t.Errorf("detail pointer fields = %v/%v, want both set for a busy worker", detail.CurrentTaskID, detail.CurrentLeaseID)
	}
	if detail.FirstSeen == "" || detail.LastSeen == "" {
		t.Errorf("first_seen/last_seen = %q/%q, want both set", detail.FirstSeen, detail.LastSeen)
	}
	if string(detail.Metadata) != "{}" {
		t.Errorf("metadata = %s, want the stored {}", detail.Metadata)
	}

	// An idle worker keeps every key too: the pointer fields are null, not
	// dropped, so a client never has to special-case a missing field.
	resp, payload = a.do(t, http.MethodGet, "/v1/workers?id=w_idle", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("idle worker detail = %d (%s)", resp.StatusCode, payload)
	}
	idle := decodeRaw(t, payload)
	for _, key := range workerDetailKeys {
		if _, ok := idle[key]; !ok {
			t.Errorf("idle detail missing field %q: %s", key, payload)
		}
	}
	if string(idle["current_task_id"]) != "null" || string(idle["current_work_item_id"]) != "null" {
		t.Errorf("idle pointers = %s / %s, want null / null", idle["current_task_id"], idle["current_work_item_id"])
	}
}

// TestWorkerDetailNotFoundAndBlankID is AC2+AC3: unknown id → 404 not_found;
// present-but-blank id (empty or whitespace) → 400 invalid_argument naming the
// id field; no id at all → the listing.
func TestWorkerDetailNotFoundAndBlankID(t *testing.T) {
	a := newAPIServer(t)
	a.registerWorker(t, "w_live")

	resp, payload := a.do(t, http.MethodGet, "/v1/workers?id=w_missing", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id = %d (%s), want 404", resp.StatusCode, payload)
	}
	if body := decodeBody[ErrorBody](t, payload); body.Code != "not_found" {
		t.Errorf("unknown id code = %q, want not_found", body.Code)
	}

	for _, query := range []string{"/v1/workers?id=", "/v1/workers?id=%20%20"} {
		resp, payload := a.do(t, http.MethodGet, query, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d (%s), want 400", query, resp.StatusCode, payload)
		}
		body := decodeBody[ErrorBody](t, payload)
		if body.Code != "invalid_argument" {
			t.Errorf("%s code = %q, want invalid_argument", query, body.Code)
		}
		if len(body.Details) == 0 || body.Details[0].Field != "id" {
			t.Errorf("%s details = %+v, want field id", query, body.Details)
		}
	}

	// No id key at all is the collection form, not a 400.
	resp, payload = a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d (%s), want 200", resp.StatusCode, payload)
	}
	list := decodeBody[v3WorkerListResponse](t, payload)
	if len(list.Items) != 1 || list.Items[0].WorkerID != "w_live" {
		t.Fatalf("list = %+v", list.Items)
	}
}

// TestWorkerListCarriesPresence is AC4: every item carries presence and the
// pre-existing fields keep their meaning for a busy node.
func TestWorkerListCarriesPresence(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_l1", "kind": "dev", "title": "执行中",
	})
	a.registerWorker(t, "w_busy")
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_busy", "kind": "dev",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, body)
	}
	a.registerWorker(t, "w_idle")

	resp, payload := a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d (%s)", resp.StatusCode, payload)
	}
	list := decodeBody[v3WorkerListResponse](t, payload)
	if len(list.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(list.Items))
	}
	for _, item := range list.Items {
		if item.Presence != "active" && item.Presence != "offline" {
			t.Errorf("%s presence = %q, want active|offline", item.WorkerID, item.Presence)
		}
	}
	busy := list.Items[0]
	if busy.WorkerID != "w_busy" {
		busy = list.Items[1]
	}
	if busy.State != enum.WorkerBusy || busy.CurrentWorkItemID == nil || *busy.CurrentWorkItemID != "t_l1" {
		t.Errorf("busy item = %+v, want state busy and current_work_item_id t_l1", busy)
	}
	if busy.LastSeen == "" {
		t.Errorf("busy item lost last_seen")
	}

	// The list shape carries presence as a key on every item, including the idle
	// one whose pointer fields stay omitted (unchanged pre-existing contract).
	var envelope struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode raw list: %v", err)
	}
	for i, item := range envelope.Items {
		if _, ok := item["presence"]; !ok {
			t.Errorf("item %d missing presence: %v", i, item)
		}
	}
}

// TestWorkerPresenceFollowsTTLAndUnknownHeartbeatRegisters is AC5+AC6 over HTTP:
// one id answers the same presence in list and detail, 40s → active and
// 50s → offline, and an unknown id heartbeat still answers 204 (empty body)
// after which the same id has a detail row. No heartbeat input answers 409.
func TestWorkerPresenceFollowsTTLAndUnknownHeartbeatRegisters(t *testing.T) {
	a := newAPIServer(t)

	// AC6: an unknown id heartbeat registers (204 + empty body), never 409.
	resp, payload := a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": "w_new", "kinds": []string{"dev"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unknown id heartbeat = %d (%s), want 204", resp.StatusCode, payload)
	}
	if len(payload) != 0 {
		t.Errorf("heartbeat body = %q, want empty", payload)
	}
	resp, payload = a.do(t, http.MethodGet, "/v1/workers?id=w_new", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail after heartbeat = %d (%s), want 200", resp.StatusCode, payload)
	}

	// A second heartbeat with an empty kind list and empty metadata is also 204.
	resp, payload = a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": "w_new", "kinds": []string{},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second heartbeat = %d (%s), want 204", resp.StatusCode, payload)
	}

	now := time.Now()
	a.setWorkerLastSeen(t, "w_new", now.Add(-40*time.Second))
	if got := a.workerPresence(t, "w_new"); got != "active" {
		t.Errorf("presence 40s after last_seen = %q, want active (ttl 45s)", got)
	}
	a.setWorkerLastSeen(t, "w_new", now.Add(-50*time.Second))
	if got := a.workerPresence(t, "w_new"); got != "offline" {
		t.Errorf("presence 50s after last_seen = %q, want offline (ttl 45s)", got)
	}

	// List and detail agree on the same row.
	detailPresence := a.workerPresence(t, "w_new")
	resp, payload = a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d (%s)", resp.StatusCode, payload)
	}
	for _, item := range decodeBody[v3WorkerListResponse](t, payload).Items {
		if item.WorkerID == "w_new" && item.Presence != detailPresence {
			t.Errorf("list presence %q != detail presence %q", item.Presence, detailPresence)
		}
	}
}

// workerPresence reads one detail and returns its presence.
func (a *apiServer) workerPresence(t *testing.T, workerID string) string {
	t.Helper()
	resp, payload := a.do(t, http.MethodGet, "/v1/workers?id="+workerID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail %s = %d (%s)", workerID, resp.StatusCode, payload)
	}
	return decodeBody[v3WorkerDetailResponse](t, payload).Presence
}

// TestClaimRequiresRegisteredWorkerHTTP is AC7 over the real router: 409 conflict
// with the recovery instruction for an unregistered id — with a card queued and
// with an empty queue — and 200 after one heartbeat.
func TestClaimRequiresRegisteredWorkerHTTP(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_claim", "kind": "dev", "title": "排队的卡",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, body)
	}

	// Unregistered + a card queued.
	resp, payload := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_ghost", "kind": "dev",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("claim(unregistered, queued) = %d (%s), want 409", resp.StatusCode, payload)
	}
	body := decodeBody[ErrorBody](t, payload)
	if body.Code != "conflict" {
		t.Errorf("claim code = %q, want conflict", body.Code)
	}
	if !strings.Contains(body.Message, "未注册") || !strings.Contains(body.Message, "心跳") {
		t.Errorf("claim message = %q, want it to name 未注册 and 心跳", body.Message)
	}

	// Unregistered + empty queue (another kind has no card): still 409, because
	// the registry check runs before the queue.
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_ghost", "kind": "requirement",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("claim(unregistered, empty queue) = %d (%s), want 409", resp.StatusCode, payload)
	}

	// One heartbeat, then the same claim gets the card.
	a.registerWorker(t, "w_ghost")
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_ghost", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim after heartbeat = %d (%s), want 200", resp.StatusCode, payload)
	}
	if claimed := decodeBody[v3ClaimResponse](t, payload); claimed.Task.TaskID != "t_claim" {
		t.Errorf("claimed task = %q, want t_claim", claimed.Task.TaskID)
	}
}
