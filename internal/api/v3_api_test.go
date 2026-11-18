package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

type apiServer struct {
	server   *httptest.Server
	store    *storedb.Store
	projects *appv3.ProjectService
	tasks    *appv3.Service
	workers  *appv3.WorkerService
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()

	st, err := storedb.Open(storedb.Config{
		Path:         filepath.Join(t.TempDir(), "state.db"),
		ReadPoolSize: 4,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.OpenReadPool(context.Background()); err != nil {
		t.Fatalf("open read pool: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	projects := appv3.NewProjectService(st)
	tasks := appv3.New(st)
	workers := appv3.NewWorkerService(st)

	handler := NewRouterV3(slog.New(slog.DiscardHandler), nil, nil, V3Services{
		Projects: projects,
		Tasks:    tasks,
		Workers:  workers,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &apiServer{server: server, store: st, projects: projects, tasks: tasks, workers: workers}
}

func (a *apiServer) seedProduct(t *testing.T, code string) {
	t.Helper()
	ctx := context.Background()
	if err := a.store.Tx(ctx, func(tx *sql.Tx) error {
		_, err := storedb.CreateProduct(ctx, tx, storedb.NewProduct{
			ProductID: code, Name: code, GitURL: "https://example.com/" + code + ".git",
			CreateTime: "2026-11-18T00:00:00.000Z", UpdateTime: "2026-11-18T00:00:00.000Z",
		})
		return err
	}); err != nil {
		t.Fatalf("seed product: %v", err)
	}
}

func (a *apiServer) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body.Close()
	return resp, payload
}

func decodeBody[T any](t *testing.T, payload []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return out
}

func TestRoutesV3Contract(t *testing.T) {
	got := make([]string, 0)
	for _, route := range RoutesV3() {
		got = append(got, route.Method+" "+route.Path)
	}
	sort.Strings(got)

	want := []string{
		"DELETE /v1/products", "DELETE /v1/tasks",
		"GET /healthz", "GET /readyz", "GET /version",
		"GET /v1/decisions", "GET /v1/inbox", "GET /v1/materials",
		"GET /v1/products", "GET /v1/projects", "GET /v1/tasks",
		"GET /v1/tasks/events", "GET /v1/workers",
		"PATCH /v1/products", "PATCH /v1/projects", "PATCH /v1/tasks",
		"POST /v1/products", "POST /v1/projects", "POST /v1/projects/close",
		"POST /v1/projects/reopen", "POST /v1/tasks", "POST /v1/tasks/attach",
		"POST /v1/tasks/claim", "POST /v1/tasks/decide", "POST /v1/tasks/lease",
		"POST /v1/tasks/report", "POST /v1/tasks/reset", "POST /v1/workers/heartbeat",
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("routes = %d, want %d\n got: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("route[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, gone := range []string{
		"POST /v1/work-items", "POST /v1/projects/requirement",
		"POST /v1/projects/specs", "GET /v1/specs", "POST /v1/projects/plans",
		"POST /v1/projects/submit", "POST /v1/projects/decide",
	} {
		if containsRoute(got, gone) {
			t.Errorf("legacy route %q must be gone", gone)
		}
	}
}

func containsRoute(routes []string, want string) bool {
	for _, route := range routes {
		if route == want {
			return true
		}
	}
	return false
}

func TestLegacyRoutesReturn404(t *testing.T) {
	a := newAPIServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/work-items"},
		{http.MethodGet, "/v1/work-items"},
		{http.MethodPost, "/v1/projects/requirement"},
		{http.MethodPost, "/v1/projects/specs"},
		{http.MethodGet, "/v1/specs"},
		{http.MethodPost, "/v1/projects/plans"},
		{http.MethodPost, "/v1/projects/submit"},
	} {
		resp, _ := a.do(t, tc.method, tc.path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestCreateProjectAndTask(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")

	resp, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "name": "登录改造", "context_md": "issue #12",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project = %d (%s)", resp.StatusCode, payload)
	}
	project := decodeBody[v3ProjectResponse](t, payload)
	if project.ProjectID != "APP-1" || project.Status != enum.ProjectOpen {
		t.Errorf("project = %+v", project)
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "dev", "title": "接口", "description_md": "实现接口",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, payload)
	}
	task := decodeBody[v3TaskResponse](t, payload)
	if task.TaskID == "" || task.Status != enum.Ready || task.Kind != enum.Dev {
		t.Errorf("task = %+v", task)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/tasks?id="+task.TaskID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get task = %d (%s)", resp.StatusCode, payload)
	}
}

func TestInvalidEnumReturns400(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "nope", "title": "x",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid kind = %d (%s)", resp.StatusCode, payload)
	}
	body := decodeBody[ErrorBody](t, payload)
	if len(body.Details) == 0 || body.Details[0].Field != "kind" {
		t.Fatalf("details = %+v, want field kind", body.Details)
	}
	if !bytes.Contains(payload, []byte("nope")) {
		t.Errorf("error body does not mention the bad value: %s", payload)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/tasks?status=nope", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid status = %d (%s)", resp.StatusCode, payload)
	}
}

func TestClaimConcurrentReturns204(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "dev", "title": "only one",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, body)
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes []int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, _ := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
				"worker_id": fmt.Sprintf("w_%d", i), "kind": "dev",
			})
			mu.Lock()
			codes = append(codes, resp.StatusCode)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	sort.Ints(codes)
	if len(codes) != 2 || codes[0] != http.StatusOK || codes[1] != http.StatusNoContent {
		t.Fatalf("claim codes = %v, want [200 204]", codes)
	}
}

func TestReportDecideAndFlow(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	_, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_req", "kind": "requirement", "title": "需求",
	})
	if resp, body := a.do(t, http.MethodGet, "/v1/tasks?kind=requirement", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("list requirement = %d (%s)", resp.StatusCode, body)
	}
	_ = payload

	materialID, err := a.tasks.CreateMaterial(context.Background(), appv3.CreateMaterialRequest{
		MaterialID: "m_req", Kind: enum.MaterialRequirement, Version: 1, Title: "需求",
		Sha256: "sha", Size: 1, Path: "/tmp/m_req", UploadedBy: "pm",
	})
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w1", "kind": "requirement",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}
	claimed := decodeBody[v3TaskResponse](t, payload)
	if claimed.LeaseID == nil {
		t.Fatal("claim returned no lease")
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/report", map[string]any{
		"task_id": "t_req", "lease_id": *claimed.LeaseID, "material_id": materialID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report = %d (%s)", resp.StatusCode, payload)
	}
	reported := decodeBody[v3TaskResponse](t, payload)
	if reported.Status != enum.InReview {
		t.Errorf("report status = %s, want in_review", reported.Status)
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/decide", map[string]any{
		"task_id": "t_req", "decision": "approve", "material_id": materialID, "decided_by": "cto",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decide = %d (%s)", resp.StatusCode, payload)
	}
	decided := decodeBody[v3TaskResponse](t, payload)
	if decided.Status != enum.Done {
		t.Errorf("decide status = %s, want done", decided.Status)
	}

	// The flow rule created exactly one design task.
	resp, payload = a.do(t, http.MethodGet, "/v1/tasks?kind=design", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list design = %d (%s)", resp.StatusCode, payload)
	}
	designs := decodeBody[v3TaskListResponse](t, payload)
	if len(designs.Items) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs.Items))
	}

	// The decision endpoint reports the reviewed material version.
	resp, payload = a.do(t, http.MethodGet, "/v1/decisions?task_id=t_req", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decisions = %d (%s)", resp.StatusCode, payload)
	}
	decisions := decodeBody[v3DecisionListResponse](t, payload)
	if len(decisions.Items) != 1 || decisions.Items[0].MaterialID == nil {
		t.Fatalf("decisions = %+v", decisions.Items)
	}

	// Events include the reported and approved rows.
	resp, payload = a.do(t, http.MethodGet, "/v1/tasks/events?task_id=t_req", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events = %d (%s)", resp.StatusCode, payload)
	}
	events := decodeBody[v3EventListResponse](t, payload)
	if len(events.Items) < 3 {
		t.Errorf("events = %d, want at least 3", len(events.Items))
	}
}

func TestCloseProjectRoute(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "dev", "title": "open",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, body)
	}

	resp, _ := a.do(t, http.MethodPost, "/v1/projects/close?id="+project.ProjectID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("close with active task = %d, want 409", resp.StatusCode)
	}
	resp, _ = a.do(t, http.MethodPost, "/v1/projects/close?id="+project.ProjectID+"&force=1", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("force close = %d, want 204", resp.StatusCode)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/projects?id="+project.ProjectID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get project = %d", resp.StatusCode)
	}
	closed := decodeBody[v3ProjectResponse](t, payload)
	if closed.Status != enum.ProjectClosed {
		t.Errorf("status = %s, want closed", closed.Status)
	}
}

func TestHeartbeatAndWorkers(t *testing.T) {
	a := newAPIServer(t)
	resp, payload := a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": "w1", "metadata": `{"host":"mac"}`, "kinds": []string{"dev", "design"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat = %d (%s)", resp.StatusCode, payload)
	}
	resp, payload = a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list workers = %d (%s)", resp.StatusCode, payload)
	}
	workers := decodeBody[v3WorkerListResponse](t, payload)
	if len(workers.Items) != 1 || workers.Items[0].WorkerID != "w1" {
		t.Errorf("workers = %+v", workers.Items)
	}
}

func TestInboxListsReadyAndReview(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	_, _ = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "dev", "title": "ready",
	})

	resp, payload := a.do(t, http.MethodGet, "/v1/inbox", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inbox = %d (%s)", resp.StatusCode, payload)
	}
	inbox := decodeBody[v3TaskListResponse](t, payload)
	if len(inbox.Items) != 1 || inbox.Items[0].Status != enum.Ready {
		t.Errorf("inbox = %+v", inbox.Items)
	}
}
