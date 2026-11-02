package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/store"
)

// newWorkItemTestServer wires the work-item, claim and worker services so the
// heartbeat/claim contract can be exercised over HTTP.
func newWorkItemTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(store.Config{Path: filepath.Join(dir, "state.db"), ReadPoolSize: 2})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.OpenReadPool(context.Background()); err != nil {
		t.Fatalf("OpenReadPool: %v", err)
	}

	logger := testLogger(io.Discard)
	handler := Chain(
		NewRouter(logger, nil, nil,
			app.NewProductService(st),
			app.NewProjectService(st, nil),
			app.NewWorkItemService(st, nil),
			app.NewClaimService(st, logger, 60*time.Second),
			app.NewWorkerService(st, logger, app.WorkerServiceOptions{
				LeaseTTL:          60 * time.Second,
				WorkerTTL:         time.Minute,
				HeartbeatInterval: 15 * time.Second,
				MetadataMaxBytes:  4096,
			}),
			nil,
			nil),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)
	return handler, st
}

func TestRemovedTaskRoutesAreGone(t *testing.T) {
	h, _ := newWorkItemTestServer(t)

	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/v1/tasks"},
		{http.MethodPost, "/v1/tasks"},
		{http.MethodPatch, "/v1/tasks?id=w_1"},
		{http.MethodDelete, "/v1/tasks?id=w_1"},
		{http.MethodPost, "/v1/tasks/claim"},
		{http.MethodPost, "/v1/tasks/heartbeat?id=w_1"},
		{http.MethodPost, "/v1/tasks/report?id=w_1"},
		{http.MethodPost, "/v1/tasks/decide?id=w_1"},
		{http.MethodPost, "/v1/tasks/review?project_id=APP-1"},
	} {
		rec := doJSON(t, h, tc.method, tc.target, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", tc.method, tc.target, rec.Code)
			continue
		}
		if body := decodeError(t, rec); body.Code != "not_found" {
			t.Errorf("%s %s code = %q, want not_found", tc.method, tc.target, body.Code)
		}
	}
}

func TestHeartbeatKindsIsRequiredOverHTTP(t *testing.T) {
	h, _ := newWorkItemTestServer(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing", map[string]any{"state": "idle"}},
		{"empty", map[string]any{"kinds": []string{}}},
		{"illegal", map[string]any{"kinds": []string{"merge"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != "invalid_argument" {
				t.Errorf("code = %q, want invalid_argument", body.Code)
			}
			requireFieldDetail(t, body, "kinds")
		})
	}
}

func TestHeartbeatEchoesKindsAndEmptyCommands(t *testing.T) {
	h, _ := newWorkItemTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", map[string]any{"kinds": []string{"dev"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var response workerHeartbeatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Worker.WorkerID == "" || response.Worker.WorkerID[:2] != "w_" {
		t.Errorf("worker_id = %q, want w_ prefix", response.Worker.WorkerID)
	}
	if len(response.Worker.Kinds) != 1 || response.Worker.Kinds[0] != "dev" {
		t.Errorf("kinds = %v, want [dev]", response.Worker.Kinds)
	}
	if response.Commands == nil {
		t.Fatal("commands = null, want []")
	}
	if len(response.Commands) != 0 {
		t.Errorf("commands = %d, want 0", len(response.Commands))
	}
	if response.Worker.CurrentWorkItemID != nil {
		t.Errorf("current_work_item_id = %v, want null", response.Worker.CurrentWorkItemID)
	}
}

func TestClaimWithoutWorkReturns204(t *testing.T) {
	h, _ := newWorkItemTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", map[string]any{"kinds": []string{"dev"}})
	var heartbeat workerHeartbeatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &heartbeat); err != nil {
		t.Fatalf("decode heartbeat: %v", err)
	}

	rec = doJSON(t, h, http.MethodPost, "/v1/work-items/claim",
		map[string]any{"worker_id": heartbeat.Worker.WorkerID})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 body = %q, want empty", rec.Body.String())
	}
}

func TestClaimRequiresWorkerID(t *testing.T) {
	h, _ := newWorkItemTestServer(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/work-items/claim", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	requireFieldDetail(t, decodeError(t, rec), "worker_id")
}

// TestNewRouterDoesNotServeTaskPaths guards the route table itself: the retired
// prefix must not be reachable even before any handler runs.
func TestNewRouterDoesNotServeTaskPaths(t *testing.T) {
	h, _ := newWorkItemTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
