package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// registerWorker posts a heartbeat and returns the resulting worker payload.
func registerWorker(t *testing.T, h http.Handler) workerResponse {
	t.Helper()

	rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"kinds": []string{"dev"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var response workerHeartbeatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}
	return response.Worker
}

// assertWorkerTiming checks the two timing fields are positive and match the
// heartbeat payload, which is the authoritative source.
func assertWorkerTiming(t *testing.T, label string, got workerResponse, want workerResponse) {
	t.Helper()

	if got.HeartbeatIntervalMS <= 0 {
		t.Errorf("%s heartbeat_interval_ms = %d, want > 0", label, got.HeartbeatIntervalMS)
	}
	if got.LeaseTTLSeconds <= 0 {
		t.Errorf("%s lease_ttl_seconds = %d, want > 0", label, got.LeaseTTLSeconds)
	}
	if got.HeartbeatIntervalMS != want.HeartbeatIntervalMS {
		t.Errorf("%s heartbeat_interval_ms = %d, want %d (heartbeat)", label, got.HeartbeatIntervalMS, want.HeartbeatIntervalMS)
	}
	if got.LeaseTTLSeconds != want.LeaseTTLSeconds {
		t.Errorf("%s lease_ttl_seconds = %d, want %d (heartbeat)", label, got.LeaseTTLSeconds, want.LeaseTTLSeconds)
	}
}

func TestGetWorkerReturnsTimingFields(t *testing.T) {
	h, _ := newWorkItemTestServer(t)
	heartbeat := registerWorker(t, h)

	rec := doJSON(t, h, http.MethodGet, "/v1/workers?id="+heartbeat.WorkerID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got workerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode worker: %v", err)
	}
	if got.WorkerID != heartbeat.WorkerID {
		t.Errorf("worker_id = %q, want %q", got.WorkerID, heartbeat.WorkerID)
	}
	assertWorkerTiming(t, "GET /v1/workers?id=", got, heartbeat)
}

func TestListWorkersReturnsTimingFields(t *testing.T) {
	h, _ := newWorkItemTestServer(t)
	first := registerWorker(t, h)
	second := registerWorker(t, h)

	rec := doJSON(t, h, http.MethodGet, "/v1/workers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var list workerListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}

	byID := make(map[string]workerResponse, len(list.Items))
	for _, item := range list.Items {
		byID[item.WorkerID] = item
	}
	for _, want := range []workerResponse{first, second} {
		got, ok := byID[want.WorkerID]
		if !ok {
			t.Fatalf("worker %q missing from list (items %d)", want.WorkerID, len(list.Items))
		}
		assertWorkerTiming(t, "GET /v1/workers "+want.WorkerID, got, want)
	}
}
