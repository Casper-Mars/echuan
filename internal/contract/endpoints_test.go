package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/api"
	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
	"github.com/Casper-Mars/echuan/internal/web"
)

const contractDocPath = "../../api/openapi.yaml"

// lockedBuffer makes the access-log output readable from the test goroutine
// without racing the server's handlers.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type endpointEnv struct {
	t       *testing.T
	server  *httptest.Server
	store   *store.Store
	doc     *Document
	logs    *lockedBuffer
	covered map[string]bool
	codes   map[string]int
}

func newEndpointEnv(t *testing.T) *endpointEnv {
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

	writer, err := material.NewWriter(filepath.Join(dir, "data"), 8<<10)
	if err != nil {
		t.Fatalf("material.NewWriter: %v", err)
	}

	logs := &lockedBuffer{}
	logger := platform.NewLogger("info", logs)
	products := app.NewProductService(st)
	projects := app.NewProjectService(st, writer)
	tasks := app.NewTaskService(st)
	tasks.SetMaterialWriter(writer)
	claims := app.NewClaimService(st, logger, 90*time.Second, filepath.Join(dir, "workspaces"))
	workers := app.NewWorkerService(st, logger, app.WorkerServiceOptions{
		LeaseTTL:          90 * time.Second,
		WorkerTTL:         45 * time.Second,
		HeartbeatInterval: 15 * time.Second,
		MetadataMaxBytes:  8 << 10,
		WorkspaceDir:      filepath.Join(dir, "workspaces"),
	})
	materials := app.NewMaterialService(st, writer)
	inbox := app.NewInboxService(st, logger, 72)

	// The production SPA fallback must take part in the contract: with a nil
	// fallback the router's /v1 semantics hide behind ServeMux defaults and the
	// 404/405 behaviour silently diverges from what users see.
	handler := api.Chain(
		api.NewRouter(logger, web.Handler(),
			[]api.HealthChecker{store.NewDBChecker(st), store.NewMigrationChecker(st)},
			products, projects, tasks, claims, workers, materials, inbox),
		api.Recover(logger),
		api.RequestID(),
		api.UnifiedErrors(),
		api.AccessLog(logger),
		api.Timeout(api.DefaultRequestTimeout),
	)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	doc, err := Load(contractDocPath)
	if err != nil {
		t.Fatalf("load %s: %v", contractDocPath, err)
	}
	return &endpointEnv{
		t:       t,
		server:  server,
		store:   st,
		doc:     doc,
		logs:    logs,
		covered: map[string]bool{},
		codes:   map[string]int{},
	}
}

// call performs one HTTP request, asserts the status, validates the JSON body
// against the OpenAPI schema of the operation and records coverage.
func (e *endpointEnv) call(method, target string, body any, headers map[string]string, wantStatus int) ([]byte, http.Header) {
	e.t.Helper()

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal %s %s body: %v", method, target, err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, e.server.URL+target, reader)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, target, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(api.HeaderActor, "contract")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("%s %s: read body: %v", method, target, err)
	}
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s: status = %d, want %d (body %s)", method, target, resp.StatusCode, wantStatus, raw)
	}

	requestID := resp.Header.Get(api.HeaderRequestID)
	if requestID == "" {
		e.t.Fatalf("%s %s: missing %s response header", method, target, api.HeaderRequestID)
	}

	path := req.URL.Path
	operation, documented := e.doc.Operation(method, path)
	if documented {
		e.covered[method+" "+path] = true
	}

	contentType := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		value := decodeValue(e.t, raw)
		if documented {
			if err := e.doc.Validate(method, path, resp.StatusCode, value); err != nil {
				e.t.Errorf("%s %s: response violates contract: %v", method, target, err)
			}
		} else if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			if err := e.doc.ValidateComponent("Error", value); err != nil {
				e.t.Errorf("%s %s: 404/405 envelope violates Error schema: %v", method, target, err)
			}
		} else {
			e.t.Errorf("%s %s: response is not a documented operation", method, target)
		}
		if object, ok := value.(map[string]any); ok {
			if code, ok := object["code"].(string); ok {
				e.codes[code]++
				if got, _ := object["request_id"].(string); got != requestID {
					e.t.Errorf("%s %s: body.request_id = %q, header = %q", method, target, got, requestID)
				}
			}
			if got, ok := object["request_id"].(string); ok {
				if got != requestID {
					e.t.Errorf("%s %s: body.request_id = %q, header = %q", method, target, got, requestID)
				}
			}
		}
	case documented && operationHasJSON(operation, resp.StatusCode):
		e.t.Errorf("%s %s: Content-Type = %q, want application/json", method, target, contentType)
	}

	return raw, resp.Header
}

func operationHasJSON(operation *Operation, status int) bool {
	response, ok := operation.Responses[strconv.Itoa(status)]
	if !ok {
		return false
	}
	_, ok = response.Content["application/json"]
	return ok
}

func decodeValue(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON %q: %v", raw, err)
	}
	return value
}

func object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	value, ok := decodeValue(t, raw).(map[string]any)
	if !ok {
		t.Fatalf("body is not a JSON object: %s", raw)
	}
	return value
}

func stringField(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	if !ok {
		t.Fatalf("field %q = %v, want string", key, object[key])
	}
	return value
}

func intField(t *testing.T, object map[string]any, key string) int {
	t.Helper()
	value, ok := object[key].(json.Number)
	if !ok {
		t.Fatalf("field %q = %v, want number", key, object[key])
	}
	parsed, err := strconv.Atoi(value.String())
	if err != nil {
		t.Fatalf("field %q = %v: %v", key, value, err)
	}
	return parsed
}

func objectField(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("field %q = %v, want object", key, object[key])
	}
	return value
}

func arrayField(t *testing.T, object map[string]any, key string) []any {
	t.Helper()
	value, ok := object[key].([]any)
	if !ok {
		t.Fatalf("field %q = %v, want array", key, object[key])
	}
	return value
}

// createDraftProject creates a draft project through the API and returns its
// business id plus the ProjectDetail body.
func (e *endpointEnv) createDraftProject(productID, title, requirement string) map[string]any {
	e.t.Helper()
	raw, _ := e.call(http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     productID,
		"title":          title,
		"requirement_md": requirement,
	}, nil, http.StatusCreated)
	return object(e.t, raw)
}

// decideProject calls POST /v1/projects/decide with an explicit If-Match.
func (e *endpointEnv) decideProject(projectID string, version int, decision, reason string) map[string]any {
	e.t.Helper()
	payload := map[string]any{"decision": decision}
	if reason != "" {
		payload["reason"] = reason
	}
	raw, _ := e.call(http.MethodPost, "/v1/projects/decide?id="+projectID, payload,
		map[string]string{"If-Match": strconv.Itoa(version)}, http.StatusOK)
	return object(e.t, raw)
}

// planningProject creates a draft, accepts it and approves the spec, leaving
// the project in planning with the returned version.
func (e *endpointEnv) planningProject(productID, title string) (string, int) {
	e.t.Helper()
	created := e.createDraftProject(productID, title, "# 需求\n")
	projectID := stringField(e.t, created, "project_id")

	raw, _ := e.call(http.MethodPost, "/v1/projects/accept?id="+projectID, nil, nil, http.StatusOK)
	accepted := object(e.t, raw)

	approved := e.decideProject(projectID, intField(e.t, accepted, "version"), "approve", "")
	if name := stringField(e.t, approved, "status_name"); name != "planning" {
		e.t.Fatalf("project %s status = %q, want planning", projectID, name)
	}
	return projectID, intField(e.t, approved, "version")
}

func TestContractEndpoints(t *testing.T) {
	e := newEndpointEnv(t)

	// The operation set is pinned: adding or dropping one is a contract change.
	operations := e.doc.Operations()
	if len(operations) != 33 {
		t.Fatalf("openapi operations = %d, want exactly 33", len(operations))
	}

	// ---- health & version -------------------------------------------------
	const fixedID = "contract-fixed-id"
	raw, headers := e.call(http.MethodGet, "/healthz", nil,
		map[string]string{api.HeaderRequestID: fixedID}, http.StatusOK)
	if got := headers.Get(api.HeaderRequestID); got != fixedID {
		t.Fatalf("inbound X-Request-Id echo = %q, want %q", got, fixedID)
	}
	if status := stringField(t, object(t, raw), "status"); status != "ok" {
		t.Errorf("healthz status = %q", status)
	}

	raw, headers = e.call(http.MethodGet, "/readyz", nil, nil, http.StatusOK)
	readiness := object(t, raw)
	if checks := objectField(t, readiness, "checks"); checks["db"] != "ok" || checks["migration"] != "ok" {
		t.Errorf("readyz checks = %v", checks)
	}
	if got := stringField(t, readiness, "request_id"); got != headers.Get(api.HeaderRequestID) {
		t.Errorf("readyz request_id = %q, header = %q", got, headers.Get(api.HeaderRequestID))
	}
	// The 503 form keeps checks and gains request_id.
	failingHandler := api.Chain(
		api.NewRouter(platform.NewLogger("error", io.Discard), nil,
			[]api.HealthChecker{failingChecker{}}, nil, nil, nil, nil, nil, nil, nil),
		api.Recover(platform.NewLogger("error", io.Discard)),
		api.RequestID(),
		api.UnifiedErrors(),
		api.AccessLog(platform.NewLogger("error", io.Discard)),
		api.Timeout(api.DefaultRequestTimeout),
	)
	failing := httptest.NewServer(failingHandler)
	failResp, err := failing.Client().Get(failing.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET failing /readyz: %v", err)
	}
	failRaw, _ := io.ReadAll(failResp.Body)
	failResp.Body.Close()
	failing.Close()
	if failResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failing readyz status = %d, want 503", failResp.StatusCode)
	}
	failBody := object(t, failRaw)
	if checks := objectField(t, failBody, "checks"); checks["failing"] != "error" {
		t.Errorf("failing readyz checks = %v", checks)
	}
	if rid := failResp.Header.Get(api.HeaderRequestID); rid == "" || stringField(t, failBody, "request_id") != rid {
		t.Errorf("failing readyz request_id mismatch: header=%q body=%v", rid, failBody["request_id"])
	}
	if err := e.doc.Validate(http.MethodGet, "/readyz", http.StatusServiceUnavailable, failBody); err != nil {
		t.Errorf("readyz 503 body violates contract: %v", err)
	}

	e.call(http.MethodGet, "/version", nil, nil, http.StatusOK)

	// ---- products ---------------------------------------------------------
	raw, _ = e.call(http.MethodPost, "/v1/products", map[string]any{
		"product_id": "APP",
		"name":       "契约产品",
		"git_url":    "git@example.com:APP.git",
	}, nil, http.StatusCreated)
	product := object(t, raw)
	if stringField(t, product, "product_id") != "APP" {
		t.Fatalf("created product = %v", product)
	}

	e.call(http.MethodPost, "/v1/products", map[string]any{
		"product_id": "VICTIM", "name": "待删除", "git_url": "git@example.com:V.git",
	}, nil, http.StatusCreated)
	e.call(http.MethodDelete, "/v1/products?id=VICTIM", nil, nil, http.StatusNoContent)

	e.call(http.MethodGet, "/v1/products?id=APP", nil, nil, http.StatusOK)
	e.call(http.MethodPatch, "/v1/products?id=APP", map[string]any{"description": "契约产品描述"}, nil, http.StatusOK)
	e.call(http.MethodGet, "/v1/products", nil, nil, http.StatusOK)
	e.call(http.MethodPost, "/v1/products", map[string]any{
		"product_id": "APP", "name": "重复", "git_url": "git@example.com:APP.git",
	}, nil, http.StatusConflict)
	e.call(http.MethodGet, "/v1/products?id=", nil, nil, http.StatusBadRequest)
	e.call(http.MethodGet, "/v1/products?id=NOPE", nil, nil, http.StatusNotFound)

	// ---- projects: requirement, spec and plan -----------------------------
	main := e.createDraftProject("APP", "契约项目", "# 需求 v1\n")
	mainID := stringField(t, main, "project_id")
	if _, ok := main["requirement"].(map[string]any); !ok {
		t.Fatal("project detail has no requirement object")
	}

	e.call(http.MethodGet, "/v1/projects?id="+mainID, nil, nil, http.StatusOK)
	e.call(http.MethodGet, "/v1/projects?product_id=APP", nil, nil, http.StatusOK)
	e.call(http.MethodPatch, "/v1/projects?id="+mainID, map[string]any{"title": "契约项目-改"}, nil, http.StatusOK)

	disposable := e.createDraftProject("APP", "待删除项目", "# 临时\n")
	e.call(http.MethodDelete, "/v1/projects?id="+stringField(t, disposable, "project_id"), nil, nil, http.StatusNoContent)

	raw, _ = e.call(http.MethodPost, "/v1/projects/requirement?id="+mainID,
		map[string]any{"body_md": "# 需求 v2\n"}, nil, http.StatusOK)
	if version := intField(t, objectField(t, object(t, raw), "requirement"), "version"); version != 2 {
		t.Errorf("requirement version = %d, want 2", version)
	}

	raw, _ = e.call(http.MethodPost, "/v1/projects/accept?id="+mainID, nil, nil, http.StatusOK)
	accepted := object(t, raw)
	if name := stringField(t, accepted, "status_name"); name != "spec_review" {
		t.Fatalf("after accept status = %q", name)
	}

	raw, _ = e.call(http.MethodPost, "/v1/projects/specs?id="+mainID,
		map[string]any{"title": "契约规格", "body_md": "# 规格\n"}, nil, http.StatusOK)
	specResponse := object(t, raw)
	specVersion := intField(t, specResponse, "project_version")
	if id := stringField(t, objectField(t, specResponse, "spec"), "material_id"); id == "" {
		t.Fatal("spec response has an empty material_id")
	}

	e.decideProject(mainID, specVersion, "approve", "")

	raw, _ = e.call(http.MethodPost, "/v1/projects/plans?id="+mainID, map[string]any{
		"tasks": []any{
			map[string]any{"title": "任务一", "body_md": "# 任务一\n", "priority": 10},
			map[string]any{"title": "任务二", "body_md": "# 任务二\n", "priority": 5},
		},
	}, nil, http.StatusCreated)
	plan := object(t, raw)
	tasks := arrayField(t, plan, "tasks")
	if len(tasks) != 2 {
		t.Fatalf("plan tasks = %d, want 2", len(tasks))
	}
	firstID := stringField(t, tasks[0].(map[string]any), "id")
	secondID := stringField(t, tasks[1].(map[string]any), "id")

	raw, _ = e.decideProjectRaw(mainID, intField(t, plan, "project_version"), "approve", "")
	if name := stringField(t, object(t, raw), "status_name"); name != "developing" {
		t.Fatalf("after plan approval status = %q", name)
	}

	e.call(http.MethodGet, "/v1/projects/materials?id="+mainID+"&kind=1", nil, nil, http.StatusOK)
	e.call(http.MethodGet, "/v1/projects/materials?id="+mainID+"&kind=2", nil, nil, http.StatusOK)

	// ---- task lifecycle ---------------------------------------------------
	raw, _ = e.call(http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"}, nil, http.StatusOK)
	claim := object(t, raw)
	claimed := objectField(t, claim, "task")
	if got := stringField(t, claimed, "id"); got != firstID {
		t.Fatalf("claim returned %s, want %s", got, firstID)
	}
	firstLease := stringField(t, objectField(t, claim, "lease"), "lease_id")

	e.call(http.MethodPost, "/v1/tasks/heartbeat?id="+firstID,
		map[string]any{"lease_id": "l_ffffffffffffffff"}, nil, http.StatusConflict)
	raw, _ = e.call(http.MethodPost, "/v1/tasks/heartbeat?id="+firstID,
		map[string]any{"lease_id": firstLease}, nil, http.StatusOK)
	if stringField(t, object(t, raw), "lease_id") != firstLease {
		t.Error("heartbeat returned a different lease")
	}

	e.call(http.MethodPost, "/v1/tasks/report?id="+firstID,
		map[string]any{"lease_id": firstLease, "summary_md": "   "}, nil, http.StatusUnprocessableEntity)
	summary1 := "任务一报告\n"
	raw, _ = e.call(http.MethodPost, "/v1/tasks/report?id="+firstID,
		map[string]any{"lease_id": firstLease, "summary_md": summary1}, nil, http.StatusOK)
	report1 := object(t, raw)
	reportMaterial := stringField(t, objectField(t, report1, "report"), "material_id")

	raw, _ = e.call(http.MethodPost, "/v1/tasks/decide?id="+firstID,
		map[string]any{"decision": "accept"},
		map[string]string{"If-Match": strconv.Itoa(intField(t, report1, "version"))}, http.StatusOK)
	if name := stringField(t, object(t, raw), "status_name"); name != "done" {
		t.Errorf("task one status = %q, want done", name)
	}

	raw, _ = e.call(http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"}, nil, http.StatusOK)
	claim2 := object(t, raw)
	if got := stringField(t, objectField(t, claim2, "task"), "id"); got != secondID {
		t.Fatalf("second claim returned %s, want %s", got, secondID)
	}
	secondLease := stringField(t, objectField(t, claim2, "lease"), "lease_id")
	raw, _ = e.call(http.MethodPost, "/v1/tasks/report?id="+secondID,
		map[string]any{"lease_id": secondLease, "summary_md": "任务二报告\n"}, nil, http.StatusOK)
	report2 := object(t, raw)
	e.call(http.MethodPost, "/v1/tasks/decide?id="+secondID,
		map[string]any{"decision": "accept"},
		map[string]string{"If-Match": strconv.Itoa(intField(t, report2, "version"))}, http.StatusOK)

	// All work done: the queue is empty and the project waits for acceptance.
	e.call(http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"}, nil, http.StatusNoContent)

	// ---- worker registry: heartbeat, list, detail -------------------------
	// The queue is empty here, so a heartbeat exercises the "no work" shape:
	// commands must be [] and the worker must come back idle.
	raw, _ = e.call(http.MethodPost, "/v1/workers/heartbeat", map[string]any{}, nil, http.StatusOK)
	workerBody := object(t, raw)
	worker := objectField(t, workerBody, "worker")
	workerID := stringField(t, worker, "worker_id")
	if !strings.HasPrefix(workerID, "w_") {
		t.Errorf("worker_id = %q, want a w_ id", workerID)
	}
	if presence := stringField(t, worker, "presence"); presence != "active" {
		t.Errorf("worker presence = %q, want active", presence)
	}
	if commands := arrayField(t, workerBody, "commands"); len(commands) != 0 {
		t.Errorf("heartbeat commands = %d, want 0 with an empty queue", len(commands))
	}
	e.call(http.MethodGet, "/v1/workers", nil, nil, http.StatusOK)
	e.call(http.MethodGet, "/v1/workers?id="+workerID, nil, nil, http.StatusOK)
	// An id the registry never minted is worker_unknown, not not_found: the
	// worker must register again instead of retrying the heartbeat.
	e.call(http.MethodPost, "/v1/workers/heartbeat",
		map[string]any{"worker_id": "w_00000000000000ff"}, nil, http.StatusConflict)

	// The pull-mode claim carries the same worker semantics: an unknown id is
	// 409 worker_unknown, and a registered worker with an empty queue is 204
	// that must not leave a pointer behind (a client would otherwise read the
	// next heartbeat as "busy").
	e.call(http.MethodPost, "/v1/tasks/claim",
		map[string]any{"product": "APP", "worker_id": "w_00000000000000ff"}, nil, http.StatusConflict)
	e.call(http.MethodPost, "/v1/tasks/claim",
		map[string]any{"product": "APP", "worker_id": workerID}, nil, http.StatusNoContent)
	raw, _ = e.call(http.MethodGet, "/v1/workers?id="+workerID, nil, nil, http.StatusOK)
	if pointed := object(t, raw)["current_task_id"]; pointed != nil {
		t.Errorf("claim 204 set the worker pointer to %v, want null", pointed)
	}

	raw, _ = e.call(http.MethodGet, "/v1/projects?id="+mainID, nil, nil, http.StatusOK)
	mainDetail := object(t, raw)
	e.decideProject(mainID, intField(t, mainDetail, "version"), "approve", "")
	e.call(http.MethodPost, "/v1/projects/accept?id="+mainID, nil, nil, http.StatusConflict)

	// ---- task CRUD on a second planning project ---------------------------
	secondProject, _ := e.planningProject("APP", "契约任务项目")
	raw, _ = e.call(http.MethodPost, "/v1/tasks?project_id="+secondProject, map[string]any{
		"title": "可修改任务", "body_md": "# 正文\n", "priority": 1,
	}, nil, http.StatusCreated)
	taskA := stringField(t, object(t, raw), "id")
	e.call(http.MethodGet, "/v1/tasks?id="+taskA, nil, nil, http.StatusOK)
	e.call(http.MethodGet, "/v1/tasks?project_id="+secondProject, nil, nil, http.StatusOK)
	e.call(http.MethodPatch, "/v1/tasks?id="+taskA, map[string]any{"title": "可修改任务-改"}, nil, http.StatusOK)
	raw, _ = e.call(http.MethodPost, "/v1/tasks?project_id="+secondProject, map[string]any{
		"title": "待删除任务", "body_md": "# 正文\n",
	}, nil, http.StatusCreated)
	taskB := stringField(t, object(t, raw), "id")
	e.call(http.MethodDelete, "/v1/tasks?id="+taskB, nil, nil, http.StatusNoContent)

	// ---- cancel, invalid version, inbox -----------------------------------
	cancelable := e.createDraftProject("APP", "待取消项目", "# 取消\n")
	e.call(http.MethodPost, "/v1/projects/cancel?id="+stringField(t, cancelable, "project_id"), nil, nil, http.StatusOK)

	pending := e.createDraftProject("APP", "待评审项目", "# 待评审\n")
	pendingID := stringField(t, pending, "project_id")
	raw, _ = e.call(http.MethodPost, "/v1/projects/accept?id="+pendingID, nil, nil, http.StatusOK)
	pendingAccepted := object(t, raw)
	e.call(http.MethodPost, "/v1/projects/decide?id="+pendingID,
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(intField(t, pendingAccepted, "version") + 99)},
		http.StatusConflict)

	e.call(http.MethodGet, "/v1/inbox", nil, nil, http.StatusOK)

	// ---- binary download (no JSON contract, header contract instead) -------
	raw, binaryHeaders := e.call(http.MethodGet, "/v1/materials?id="+reportMaterial, nil, nil, http.StatusOK)
	if got := string(raw); got != summary1 {
		t.Errorf("material bytes = %q, want %q", got, summary1)
	}
	if ct := binaryHeaders.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
	if length := binaryHeaders.Get("Content-Length"); length != strconv.Itoa(len(summary1)) {
		t.Errorf("Content-Length = %q, want %d", length, len(summary1))
	}
	if etag := binaryHeaders.Get("ETag"); !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Errorf("ETag = %q", etag)
	}
	if id := binaryHeaders.Get("X-Material-Id"); id != reportMaterial {
		t.Errorf("X-Material-Id = %q, want %s", id, reportMaterial)
	}
	if version := binaryHeaders.Get("X-Material-Version"); version == "" {
		t.Error("X-Material-Version is empty")
	}

	// HEAD and Range must keep working under the error middleware.
	head := rawRequest(t, e, http.MethodHead, "/v1/materials?id="+reportMaterial, "")
	if head.StatusCode != http.StatusOK || len(head.Body) != 0 {
		t.Errorf("HEAD material: status=%d body=%d", head.StatusCode, len(head.Body))
	}
	if head.Header.Get("Content-Length") != strconv.Itoa(len(summary1)) {
		t.Errorf("HEAD Content-Length = %q", head.Header.Get("Content-Length"))
	}
	rangeResp := rawRequest(t, e, http.MethodGet, "/v1/materials?id="+reportMaterial, "bytes=0-3")
	if rangeResp.StatusCode != http.StatusPartialContent || string(rangeResp.Body) != summary1[:4] {
		t.Errorf("Range: status=%d body=%q", rangeResp.StatusCode, rangeResp.Body)
	}
	if rangeResp.Header.Get(api.HeaderRequestID) == "" {
		t.Error("Range response missing X-Request-Id")
	}

	// An unsatisfiable range must keep the unified error envelope and the
	// Content-Range header that tells the client the real size.
	raw, rangeHeaders := e.call(http.MethodGet, "/v1/materials?id="+reportMaterial, nil,
		map[string]string{"Range": "bytes=999999999-"}, http.StatusRequestedRangeNotSatisfiable)
	if ct := rangeHeaders.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("unsatisfiable Range Content-Type = %q, want application/json", ct)
	}
	if cr := rangeHeaders.Get("Content-Range"); !strings.HasPrefix(cr, "bytes */") {
		t.Errorf("unsatisfiable Range Content-Range = %q, want bytes */<size>", cr)
	}
	rangeBody := object(t, raw)
	if code := stringField(t, rangeBody, "code"); code != "range_not_satisfiable" {
		t.Errorf("unsatisfiable Range code = %q", code)
	}
	if details, ok := rangeBody["details"].([]any); !ok || len(details) != 0 {
		t.Errorf("unsatisfiable Range details = %v, want []", rangeBody["details"])
	}

	// ---- unified 404 / 405 under the production fallback ------------------
	// Each case pins the full Allow header, not just its presence: a truncated
	// list is exactly the bug the SPA fallback used to produce.
	fallbackCases := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{"unknown path PUT", http.MethodPut, "/v1/nope", http.StatusNotFound, "not_found", ""},
		{"unknown path PATCH", http.MethodPatch, "/v1/nope", http.StatusNotFound, "not_found", ""},
		{"unknown path GET", http.MethodGet, "/v1/nope", http.StatusNotFound, "not_found", ""},
		{"unknown nested path", http.MethodGet, "/v1/nope/deeper", http.StatusNotFound, "not_found", ""},
		{"products wrong method", http.MethodPut, "/v1/products", http.StatusMethodNotAllowed, "method_not_allowed", "DELETE, GET, HEAD, PATCH, POST"},
		{"plans wrong method", http.MethodGet, "/v1/projects/plans", http.StatusMethodNotAllowed, "method_not_allowed", "POST"},
		{"claim wrong method", http.MethodGet, "/v1/tasks/claim", http.StatusMethodNotAllowed, "method_not_allowed", "POST"},
		{"healthz wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD"},
	}
	for _, tc := range fallbackCases {
		raw, headers = e.call(tc.method, tc.target, nil, nil, tc.wantStatus)
		body := object(t, raw)
		if code := stringField(t, body, "code"); code != tc.wantCode {
			t.Errorf("%s: code = %q, want %q", tc.name, code, tc.wantCode)
		}
		if details, ok := body["details"].([]any); !ok || len(details) != 0 {
			t.Errorf("%s: details = %v, want []", tc.name, body["details"])
		}
		if got := stringField(t, body, "request_id"); got != headers.Get(api.HeaderRequestID) {
			t.Errorf("%s: request_id = %q, header = %q", tc.name, got, headers.Get(api.HeaderRequestID))
		}
		if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type = %q, want application/json", tc.name, ct)
		}
		if allow := headers.Get("Allow"); allow != tc.wantAllow {
			t.Errorf("%s: Allow = %q, want %q", tc.name, allow, tc.wantAllow)
		}
	}

	// A business error must keep its own envelope rather than being replaced.
	raw, _ = e.call(http.MethodPost, "/v1/projects/accept?id="+mainID, nil, nil, http.StatusConflict)
	business := object(t, raw)
	if code := stringField(t, business, "code"); code != "invalid_state" {
		t.Errorf("business error code = %q, want invalid_state", code)
	}
	if message := stringField(t, business, "message"); message == "method not allowed" || message == "not found" {
		t.Errorf("business error message was replaced: %q", message)
	}

	// ---- too_large (writer enforces material_max_bytes) --------------------
	e.call(http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "超大需求",
		"requirement_md": strings.Repeat("x", 9<<10),
	}, nil, http.StatusRequestEntityTooLarge)

	// Independent documents retain their own histories while old readers see all specs.
	multi := e.createDraftProject("APP", "多方案项目", "# 多方案需求\n")
	multiID := stringField(t, multi, "project_id")
	e.call(http.MethodPost, "/v1/projects/accept?id="+multiID, nil, nil, http.StatusOK)
	raw, _ = e.call(http.MethodPost, "/v1/specs", map[string]any{
		"project_id": multiID, "title": "前端方案", "body_md": "前端第一版",
	}, nil, http.StatusCreated)
	firstSpec := object(t, raw)
	firstSpecID := stringField(t, firstSpec, "spec_id")
	firstMaterial := stringField(t, objectField(t, firstSpec, "material"), "material_id")
	raw, _ = e.call(http.MethodPost, "/v1/specs", map[string]any{
		"project_id": multiID, "title": "后端方案", "body_md": "后端独立正文",
	}, nil, http.StatusCreated)
	secondSpecID := stringField(t, object(t, raw), "spec_id")
	e.call(http.MethodGet, "/v1/specs?project_id="+multiID, nil, nil, http.StatusOK)
	raw, _ = e.call(http.MethodPatch, "/v1/specs?id="+firstSpecID, map[string]any{
		"title": "前端修订方案", "body_md": "前端第二版",
	}, map[string]string{"If-Match": "1"}, http.StatusOK)
	if got := intField(t, object(t, raw), "version"); got != 2 {
		t.Errorf("revised document version = %d, want 2", got)
	}
	e.call(http.MethodPatch, "/v1/specs?id="+firstSpecID, map[string]any{
		"title": "过期修改", "body_md": "不可覆盖",
	}, map[string]string{"If-Match": "1"}, http.StatusConflict)
	raw, _ = e.call(http.MethodGet, "/v1/specs?id="+firstSpecID+"&material_id="+firstMaterial, nil, nil, http.StatusOK)
	if stringField(t, object(t, raw), "body_md") != "前端第一版" {
		t.Error("independent historical document was overwritten")
	}
	raw, _ = e.call(http.MethodGet, "/v1/specs?id="+secondSpecID, nil, nil, http.StatusOK)
	if intField(t, object(t, raw), "version") != 1 || stringField(t, object(t, raw), "body_md") != "后端独立正文" {
		t.Error("revising the first document changed the second")
	}
	e.call(http.MethodPost, "/v1/projects/specs?id="+multiID,
		map[string]any{"body_md": "旧客户端修订第一份"}, nil, http.StatusOK)
	raw, _ = e.call(http.MethodGet, "/v1/projects?id="+multiID, nil, nil, http.StatusOK)
	if excerpt := stringField(t, object(t, raw), "spec_excerpt"); !strings.Contains(excerpt, "旧客户端修订第一份") || !strings.Contains(excerpt, "后端独立正文") {
		t.Errorf("legacy execution snapshot omitted a document: %s", excerpt)
	}
	large := e.createDraftProject("APP", "方案包限额", "需求")
	largeID := stringField(t, large, "project_id")
	e.call(http.MethodPost, "/v1/projects/accept?id="+largeID, nil, nil, http.StatusOK)
	e.call(http.MethodPost, "/v1/specs", map[string]any{
		"project_id": largeID, "title": "第一份", "body_md": strings.Repeat("a", 5<<10),
	}, nil, http.StatusCreated)
	e.call(http.MethodPost, "/v1/specs", map[string]any{
		"project_id": largeID, "title": "第二份", "body_md": strings.Repeat("b", 5<<10),
	}, nil, http.StatusRequestEntityTooLarge)
	raw, _ = e.call(http.MethodGet, "/v1/specs?project_id="+largeID, nil, nil, http.StatusOK)
	if items := object(t, raw)["items"].([]any); len(items) != 1 {
		t.Errorf("failed oversized bundle left %d documents, want 1", len(items))
	}

	// ---- coverage: every documented operation -----------------------------
	want := map[string]bool{}
	for _, op := range operations {
		want[op.Method+" "+op.Path] = true
	}
	for key := range want {
		if !e.covered[key] {
			t.Errorf("operation not exercised: %s", key)
		}
	}
	for key := range e.covered {
		if !want[key] {
			t.Errorf("coverage contains unknown operation: %s", key)
		}
	}

	// ---- every observed error code is inside the documented enum -----------
	documentedCodes := map[string]bool{}
	for _, schema := range e.doc.Components.Schemas["Error"].Properties["code"].Enum {
		documentedCodes[fmt.Sprint(schema)] = true
	}
	for code, count := range e.codes {
		if !documentedCodes[code] {
			t.Errorf("observed code %q (%d times) is not in Error.code enum", code, count)
		}
	}
	for _, code := range []string{"invalid_argument", "not_found", "invalid_state", "invalid_version", "conflict", "lease_lost", "material_required", "too_large", "method_not_allowed", "range_not_satisfiable", "worker_unknown"} {
		if e.codes[code] == 0 {
			t.Errorf("error code %q was never exercised", code)
		}
	}

	// ---- access log carries the same correlation id -----------------------
	e.server.Close()
	if line := findLogLine(t, e.logs.String(), fixedID); line == nil {
		t.Fatalf("access log has no entry with request_id %q", fixedID)
	} else {
		if line["method"] != http.MethodGet || line["path"] != "/healthz" || line["status"] != json.Number("200") {
			t.Errorf("access log entry = %v", line)
		}
	}

	// ---- non-vacuous: broken copies of the real schema must fail -----------
	mutated, err := Load(contractDocPath)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	healthSchema := mutated.Components.Schemas["Health"]
	healthSchema.Properties["status"] = &Schema{Type: "integer"}
	healthBody := decodeValue(t, []byte(`{"status":"ok"}`))
	if err := mutated.Validate(http.MethodGet, "/healthz", 200, healthBody); err == nil {
		t.Error("validator accepted a response against a schema with the wrong property type")
	}

	required, err := Load(contractDocPath)
	if err != nil {
		t.Fatalf("reload contract: %v", err)
	}
	requiredSchema := required.Components.Schemas["Health"]
	requiredSchema.Required = append(requiredSchema.Required, "definitely_missing")
	if err := required.Validate(http.MethodGet, "/healthz", 200, healthBody); err == nil {
		t.Error("validator accepted a response missing a newly required field")
	}
}

// TestRoutesMatchOpenAPI keeps the router's method table and the documented
// operation set in lockstep: a route added on one side alone fails here instead
// of being discovered by a client hitting a 404/405 in production.
func TestRoutesMatchOpenAPI(t *testing.T) {
	doc, err := Load(contractDocPath)
	if err != nil {
		t.Fatalf("load %s: %v", contractDocPath, err)
	}

	want := map[string]bool{}
	for _, op := range doc.Operations() {
		want[op.Method+" "+op.Path] = true
	}
	got := map[string]bool{}
	for _, route := range api.Routes() {
		got[route.Method+" "+route.Path] = true
	}
	if len(want) != 33 {
		t.Fatalf("openapi operations = %d, want 33", len(want))
	}
	for key := range want {
		if !got[key] {
			t.Errorf("operation %s is documented but not routed", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("route %s is served but not documented", key)
		}
	}
}

// decideProjectRaw is decideProject without decoding the response, so a caller
// can assert on the raw body while still exercising the operation.
func (e *endpointEnv) decideProjectRaw(projectID string, version int, decision, reason string) ([]byte, http.Header) {
	e.t.Helper()
	payload := map[string]any{"decision": decision}
	if reason != "" {
		payload["reason"] = reason
	}
	return e.call(http.MethodPost, "/v1/projects/decide?id="+projectID, payload,
		map[string]string{"If-Match": strconv.Itoa(version)}, http.StatusOK)
}

type rawResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func rawRequest(t *testing.T, e *endpointEnv, method, target, rangeHeader string) rawResponse {
	t.Helper()
	req, err := http.NewRequest(method, e.server.URL+target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return rawResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}
}

func findLogLine(t *testing.T, output, requestID string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("access log line is not JSON: %q: %v", line, err)
		}
		if entry["request_id"] == requestID {
			return entry
		}
	}
	return nil
}

type failingChecker struct{}

func (failingChecker) Name() string { return "failing" }
func (failingChecker) Check(context.Context) error {
	return errors.New("contract fixture failure")
}
