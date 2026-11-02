package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// workerHTTPEnv builds the real middleware stack with the worker service wired
// onto a temporary migrated database. Product APP is seeded so a planning
// project can hold dispatchable cards.
func workerHTTPEnv(t *testing.T) (http.Handler, *store.Store) {
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

	writer, err := material.NewWriter(filepath.Join(dir, "data"), 1<<20)
	if err != nil {
		t.Fatalf("material.NewWriter: %v", err)
	}

	logger := testLogger(io.Discard)
	projects := app.NewProjectService(st, writer)
	tasks := app.NewTaskService(st)
	tasks.SetMaterialWriter(writer)
	claims := app.NewClaimService(st, logger, 90*time.Second)
	workers := app.NewWorkerService(st, logger, app.WorkerServiceOptions{
		LeaseTTL:          90 * time.Second,
		WorkerTTL:         45 * time.Second,
		HeartbeatInterval: 15 * time.Second,
		MetadataMaxBytes:  8 << 10,
	})

	h := Chain(
		NewRouter(logger, nil, nil, app.NewProductService(st), projects, tasks, claims, workers,
			app.NewMaterialService(st, writer), nil),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)
	seedProduct(t, st, "APP")
	return h, st
}

// workerDTO is the worker object shared by the heartbeat body, the list items
// and the detail response.
type workerDTO struct {
	WorkerID            string          `json:"worker_id"`
	Presence            string          `json:"presence"`
	State               string          `json:"state"`
	CurrentTaskID       *string         `json:"current_task_id"`
	Metadata            json.RawMessage `json:"metadata"`
	FirstSeen           string          `json:"first_seen"`
	LastSeen            string          `json:"last_seen"`
	HeartbeatIntervalMS int             `json:"heartbeat_interval_ms"`
	LeaseTTLSeconds     int             `json:"lease_ttl_seconds"`
}

// workerHTTPResponse is the 200 body of POST /v1/workers/heartbeat.
type workerHTTPResponse struct {
	Worker   workerDTO `json:"worker"`
	Commands []struct {
		Kind string `json:"kind"`
		Task *struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
			Title     string `json:"title"`
			BodyMD    string `json:"body_md"`
			Attempt   int    `json:"attempt"`
			Priority  int    `json:"priority"`
		} `json:"task"`
		Lease *struct {
			LeaseID    string `json:"lease_id"`
			Until      string `json:"until"`
			TTLSeconds int    `json:"ttl_seconds"`
		} `json:"lease"`
		Context *struct {
			SpecVersion int    `json:"spec_version"`
			SpecDigest  string `json:"spec_digest"`
			Product     struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				GitURL string `json:"git_url"`
			} `json:"product"`
		} `json:"context"`
	} `json:"commands"`
}

func decodeWorkerHeartbeat(t *testing.T, rec *httptest.ResponseRecorder) workerHTTPResponse {
	t.Helper()
	var body workerHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode heartbeat %q: %v", rec.Body.String(), err)
	}
	return body
}

func heartbeatWorker(t *testing.T, h http.Handler, payload map[string]any) workerHTTPResponse {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/workers/heartbeat: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	return decodeWorkerHeartbeat(t, rec)
}

func TestWorkerHeartbeatRegisters(t *testing.T) {
	h, _ := workerHTTPEnv(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// "nothing to do" must be an empty array, never null: a worker parses the
	// same shape on every heartbeat.
	if raw := rec.Body.String(); !strings.Contains(raw, `"commands":[]`) {
		t.Errorf("body = %s, want commands encoded as []", raw)
	}

	body := decodeWorkerHeartbeat(t, rec)
	if !strings.HasPrefix(body.Worker.WorkerID, "w_") || len(body.Worker.WorkerID) != 28 {
		t.Errorf("worker_id = %q, want w_ + 26-char ULID", body.Worker.WorkerID)
	}
	if body.Worker.Presence != domain.PresenceActive {
		t.Errorf("presence = %q, want active", body.Worker.Presence)
	}
	if body.Worker.State != domain.WorkerStateIdle {
		t.Errorf("state = %q, want idle", body.Worker.State)
	}
	if body.Worker.CurrentTaskID != nil {
		t.Errorf("current_task_id = %v, want null", *body.Worker.CurrentTaskID)
	}
	if string(body.Worker.Metadata) != "{}" {
		t.Errorf("metadata = %s, want {}", body.Worker.Metadata)
	}
	if body.Worker.HeartbeatIntervalMS != 15000 {
		t.Errorf("heartbeat_interval_ms = %d, want 15000", body.Worker.HeartbeatIntervalMS)
	}
	if body.Worker.LeaseTTLSeconds != 90 {
		t.Errorf("lease_ttl_seconds = %d, want 90", body.Worker.LeaseTTLSeconds)
	}
	if _, err := time.Parse(time.RFC3339Nano, body.Worker.FirstSeen); err != nil {
		t.Errorf("first_seen = %q: %v", body.Worker.FirstSeen, err)
	}
	if body.Worker.FirstSeen != body.Worker.LastSeen {
		t.Errorf("first_seen = %q, last_seen = %q; want equal on registration",
			body.Worker.FirstSeen, body.Worker.LastSeen)
	}
}

func TestWorkerHeartbeatDispatchesAndKeepsMetadata(t *testing.T) {
	h, st := workerHTTPEnv(t)
	projectID := createTasksDraftHTTPProject(t, h)
	detail := postTask(t, h, projectID, "派单卡", "# 正文\n", 7)
	forceTaskColumns(t, st, detail.ID, domain.TaskReady, 0, nil, nil, nil)

	dispatched := heartbeatWorker(t, h, map[string]any{})
	workerID := dispatched.Worker.WorkerID
	if len(dispatched.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(dispatched.Commands))
	}
	command := dispatched.Commands[0]
	if command.Kind != app.CommandRunTask {
		t.Errorf("command kind = %q, want %q", command.Kind, app.CommandRunTask)
	}
	if command.Task == nil || command.Lease == nil || command.Context == nil {
		t.Fatalf("command payload incomplete: %+v", command)
	}
	if command.Task.ID != detail.ID || command.Task.ProjectID != projectID {
		t.Errorf("task = %s/%s, want %s/%s", command.Task.ID, command.Task.ProjectID, detail.ID, projectID)
	}
	if command.Task.Title != "派单卡" || command.Task.Priority != 7 || command.Task.Attempt != 1 {
		t.Errorf("task payload = %+v", command.Task)
	}
	if !strings.HasPrefix(command.Lease.LeaseID, "l_") || command.Lease.TTLSeconds != 90 {
		t.Errorf("lease = %+v", command.Lease)
	}
	if command.Context.Product.ID != "APP" {
		t.Errorf("context = %+v", command.Context)
	}
	// The pointer, not the declared state, is the occupancy signal.
	if dispatched.Worker.CurrentTaskID == nil || *dispatched.Worker.CurrentTaskID != detail.ID {
		t.Errorf("current_task_id = %v, want %s", dispatched.Worker.CurrentTaskID, detail.ID)
	}
	if dispatched.Worker.State != domain.WorkerStateBusy {
		t.Errorf("state = %q, want busy", dispatched.Worker.State)
	}

	// A second heartbeat with metadata stores the document and returns no card.
	busy := heartbeatWorker(t, h, map[string]any{
		"worker_id": workerID,
		"state":     "busy",
		"metadata":  map[string]any{"host": "mac-mini", "slots": 2},
	})
	if busy.Worker.WorkerID != workerID {
		t.Errorf("worker_id = %q, want %q", busy.Worker.WorkerID, workerID)
	}
	if len(busy.Commands) != 0 {
		t.Errorf("commands = %d, want 0 for a busy worker", len(busy.Commands))
	}
	var metadata map[string]any
	if err := json.Unmarshal(busy.Worker.Metadata, &metadata); err != nil {
		t.Fatalf("metadata = %s: %v", busy.Worker.Metadata, err)
	}
	if metadata["host"] != "mac-mini" || metadata["slots"] != float64(2) {
		t.Errorf("metadata = %v", metadata)
	}
	if busy.Worker.FirstSeen != dispatched.Worker.FirstSeen {
		t.Errorf("first_seen moved: %q → %q", dispatched.Worker.FirstSeen, busy.Worker.FirstSeen)
	}

	// A heartbeat without metadata must not wipe the stored document, and an
	// idle declaration while still holding the card keeps the pointer (§4.1).
	idle := heartbeatWorker(t, h, map[string]any{"worker_id": workerID, "state": "idle"})
	if string(idle.Worker.Metadata) == "{}" || !strings.Contains(string(idle.Worker.Metadata), "mac-mini") {
		t.Errorf("metadata = %s, want the previously stored document", idle.Worker.Metadata)
	}
	if idle.Worker.CurrentTaskID == nil || *idle.Worker.CurrentTaskID != detail.ID {
		t.Errorf("current_task_id = %v, want the held card %s", idle.Worker.CurrentTaskID, detail.ID)
	}
	if idle.Worker.State != domain.WorkerStateIdle {
		t.Errorf("state = %q, want the declared idle", idle.Worker.State)
	}
	if len(idle.Commands) != 0 {
		t.Errorf("commands = %d, want 0 while holding a card", len(idle.Commands))
	}
}

func TestWorkerHeartbeatUnknownWorker(t *testing.T) {
	h, _ := workerHTTPEnv(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat",
		map[string]any{"worker_id": "w_00000000000000ff"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "worker_unknown" {
		t.Errorf("code = %q, want worker_unknown", body.Code)
	}
	if len(body.Details) != 0 {
		t.Errorf("details = %v, want []", body.Details)
	}
}

func TestWorkerHeartbeatValidation(t *testing.T) {
	h, _ := workerHTTPEnv(t)

	cases := []struct {
		name       string
		payload    map[string]any
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name:       "invalid state",
			payload:    map[string]any{"state": "sleeping"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
			wantField:  "state",
		},
		{
			name:       "metadata array",
			payload:    map[string]any{"metadata": []any{"a", "b"}},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
			wantField:  "metadata",
		},
		{
			name:       "metadata string",
			payload:    map[string]any{"metadata": "host=mac"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
			wantField:  "metadata",
		},
		{
			name:       "metadata too large",
			payload:    map[string]any{"metadata": map[string]any{"blob": strings.Repeat("x", 9000)}},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "too_large",
			wantField:  "metadata",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, h, http.MethodPost, "/v1/workers/heartbeat", tc.payload)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Code, tc.wantCode)
			}
			if len(body.Details) != 1 || body.Details[0].Field != tc.wantField {
				t.Errorf("details = %+v, want one detail for field %q", body.Details, tc.wantField)
			}
		})
	}
}

// workerListHTTPResponse is the GET /v1/workers body.
type workerListHTTPResponse struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func decodeWorkerList(t *testing.T, rec *httptest.ResponseRecorder) workerListHTTPResponse {
	t.Helper()
	var page workerListHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode worker list %q: %v", rec.Body.String(), err)
	}
	return page
}

func workerIDOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var worker struct {
		WorkerID string `json:"worker_id"`
	}
	if err := json.Unmarshal(raw, &worker); err != nil {
		t.Fatalf("decode worker item %s: %v", raw, err)
	}
	return worker.WorkerID
}

func TestWorkerListPagesAndDetail(t *testing.T) {
	h, _ := workerHTTPEnv(t)

	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, heartbeatWorker(t, h, map[string]any{}).Worker.WorkerID)
	}

	rec := doJSON(t, h, http.MethodGet, "/v1/workers?limit=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	first := decodeWorkerList(t, rec)
	if len(first.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(first.Items))
	}
	if first.NextCursor == nil {
		t.Fatal("next_cursor is absent on a page with more rows")
	}
	// last_seen DESC, id DESC: the newest registration comes first.
	if got := workerIDOf(t, first.Items[0]); got != ids[2] {
		t.Errorf("first item = %q, want newest %q", got, ids[2])
	}

	rec = doJSON(t, h, http.MethodGet, "/v1/workers?limit=2&cursor="+*first.NextCursor, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second page: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	second := decodeWorkerList(t, rec)
	if len(second.Items) != 1 {
		t.Fatalf("second page items = %d, want 1", len(second.Items))
	}
	if second.NextCursor != nil {
		t.Errorf("next_cursor = %q, want absent on the last page", *second.NextCursor)
	}
	if got := workerIDOf(t, second.Items[0]); got != ids[0] {
		t.Errorf("last item = %q, want oldest %q", got, ids[0])
	}

	// Detail by id.
	rec = doJSON(t, h, http.MethodGet, "/v1/workers?id="+ids[1], nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var detail workerDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.WorkerID != ids[1] {
		t.Errorf("detail worker_id = %q, want %q", detail.WorkerID, ids[1])
	}
	if detail.HeartbeatIntervalMS != 15000 || detail.LeaseTTLSeconds != 90 {
		t.Errorf("detail timing = %d/%d", detail.HeartbeatIntervalMS, detail.LeaseTTLSeconds)
	}

	// An unknown id is a plain not_found; a blank id is a client mistake.
	rec = doJSON(t, h, http.MethodGet, "/v1/workers?id=w_00000000000000ff", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown detail: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	var notFound ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &notFound); err != nil {
		t.Fatalf("decode 404: %v", err)
	}
	if notFound.Code != "not_found" {
		t.Errorf("code = %q, want not_found", notFound.Code)
	}

	rec = doJSON(t, h, http.MethodGet, "/v1/workers?id=", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank id: status = %d, want 400", rec.Code)
	}

	// Cursor and limit validation.
	for _, target := range []string{
		"/v1/workers?cursor=not-a-cursor",
		"/v1/workers?limit=0",
		"/v1/workers?limit=201",
		"/v1/workers?limit=abc",
	} {
		rec = doJSON(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", target, rec.Code, rec.Body.String())
			continue
		}
		var body ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: decode body: %v", target, err)
			continue
		}
		if body.Code != "invalid_argument" || len(body.Details) != 1 {
			t.Errorf("%s: code = %q details = %+v", target, body.Code, body.Details)
		}
	}
}

func TestWorkerCursorRoundTrip(t *testing.T) {
	for _, id := range []int64{0, 1, 42, 1 << 40} {
		encoded := encodeWorkerCursor(id)
		decoded, err := decodeWorkerCursor(encoded)
		if err != nil {
			t.Fatalf("decodeWorkerCursor(%q): %v", encoded, err)
		}
		if decoded != id {
			t.Errorf("decoded = %d, want %d", decoded, id)
		}
	}
	for _, invalid := range []string{"", "!!!", encodeWorkerCursor(0)[:2], "aWQ6MQ", "aTo"} {
		if _, err := decodeWorkerCursor(invalid); err == nil {
			t.Errorf("decodeWorkerCursor(%q) error = nil", invalid)
		}
	}
}

func TestWorkersMethodNotAllowed(t *testing.T) {
	h, _ := workerHTTPEnv(t)

	cases := []struct {
		method    string
		target    string
		wantAllow string
	}{
		{http.MethodPost, "/v1/workers", "GET, HEAD"},
		{http.MethodDelete, "/v1/workers", "GET, HEAD"},
		{http.MethodGet, "/v1/workers/heartbeat", "POST"},
		{http.MethodPut, "/v1/workers/heartbeat", "POST"},
	}
	for _, tc := range cases {
		rec := doJSON(t, h, tc.method, tc.target, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tc.method, tc.target, rec.Code)
			continue
		}
		if allow := rec.Header().Get("Allow"); allow != tc.wantAllow {
			t.Errorf("%s %s: Allow = %q, want %q", tc.method, tc.target, allow, tc.wantAllow)
		}
		var body ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s %s: decode body: %v", tc.method, tc.target, err)
			continue
		}
		if body.Code != "method_not_allowed" {
			t.Errorf("%s %s: code = %q", tc.method, tc.target, body.Code)
		}
	}
}
