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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/platform"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

type apiServer struct {
	server    *httptest.Server
	store     *storedb.Store
	projects  *appv3.ProjectService
	tasks     *appv3.Service
	workers   *appv3.WorkerService
	materials *app.MaterialService
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	return newAPIServerWithConfig(t, platform.DefaultConfig())
}

// newAPIServerWithConfig builds the same router from an explicit
// platform.Config, so a test can prove a configured value reaches the HTTP
// surface instead of falling back to the built-in defaults (lease_ttl,
// max_attempts, the worker rhythm). Callers that do not care use newAPIServer.
func newAPIServerWithConfig(t *testing.T, cfg platform.Config) *apiServer {
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
	tasks := appv3.New(st).WithMaxAttempts(cfg.MaxAttempts).WithLeaseTTL(cfg.LeaseTTL)
	workers := appv3.NewWorkerService(st).
		WithRhythm(cfg.WorkerTTL, cfg.HeartbeatInterval, cfg.LeaseTTL)
	writer, err := material.NewWriter(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("material writer: %v", err)
	}
	materials := app.NewMaterialService(st, writer)

	handler := NewRouterV3(slog.New(slog.DiscardHandler), nil, nil, V3Services{
		Projects:  projects,
		Tasks:     tasks,
		Workers:   workers,
		Materials: materials,
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &apiServer{server: server, store: st, projects: projects, tasks: tasks, workers: workers, materials: materials}
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

// registerWorker sends one heartbeat, which is how a worker enters the registry
// and how a client recovers from the claim 409 (issue #165 §2).
func (a *apiServer) registerWorker(t *testing.T, workerID string) {
	t.Helper()
	resp, payload := a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": workerID, "kinds": []string{"dev", "requirement", "design"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat %s = %d (%s)", workerID, resp.StatusCode, payload)
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
		"GET /v1/products", "GET /v1/projects", "GET /v1/projects/materials", "GET /v1/tasks",
		"GET /v1/tasks/events", "GET /v1/tasks/dependencies", "GET /v1/tasks/attachments", "GET /v1/workers",
		"PATCH /v1/products", "PATCH /v1/projects", "PATCH /v1/tasks",
		"POST /v1/products", "POST /v1/projects", "POST /v1/projects/close",
		"POST /v1/projects/reopen", "POST /v1/tasks", "POST /v1/tasks/attach",
		"POST /v1/tasks/claim", "POST /v1/tasks/decide", "POST /v1/tasks/lease",
		"POST /v1/tasks/report", "POST /v1/tasks/reset", "POST /v1/workers/heartbeat", "POST /v1/materials",
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

	// Both contenders must be registered: claim answers 409 for an unregistered
	// worker_id, and the point of this case is still the 200/204 race over one
	// card (issue #165 §2 regression point 1).
	for i := 0; i < 2; i++ {
		a.registerWorker(t, fmt.Sprintf("w_%d", i))
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
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p", "context_md": "项目背景"})
	project := decodeBody[v3ProjectResponse](t, payload)
	_, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_req", "kind": "requirement", "title": "需求",
	})
	_ = payload
	_ = payload

	materialID, err := a.tasks.CreateMaterial(context.Background(), appv3.CreateMaterialRequest{
		MaterialID: "m_req", Kind: enum.MaterialRequirement, Version: 1, Title: "需求",
		Sha256: "sha", Size: 1, Path: "/tmp/m_req", UploadedBy: "pm",
	})
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}

	a.registerWorker(t, "w1")
	resp, payload := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w1", "kind": "requirement",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}
	claimed := decodeBody[v3ClaimResponse](t, payload)
	if claimed.Task.LeaseID == nil {
		t.Fatal("claim returned no lease")
	}
	if claimed.Project.ContextMD != "项目背景" || claimed.Product.GitURL == "" {
		t.Fatalf("claim envelope = %+v", claimed)
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/report", map[string]any{
		"task_id": "t_req", "lease_id": *claimed.Task.LeaseID, "material_id": materialID,
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
		"worker_id": "w1", "metadata": map[string]any{"host": "mac"}, "kinds": []string{"dev", "design"},
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

func TestCreateMaterialAndReport(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_req", "kind": "requirement", "title": "需求",
	})

	resp, payload := a.do(t, http.MethodPost, "/v1/materials", map[string]any{
		"kind": "requirement", "title": "需求正文", "body_md": "# 需求\n\n正文",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create material = %d (%s)", resp.StatusCode, payload)
	}
	materialRow := decodeBody[v3MaterialResponse](t, payload)
	if materialRow.MaterialID == "" || materialRow.Kind != "requirement" {
		t.Fatalf("material = %+v", materialRow)
	}

	a.registerWorker(t, "w1")
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w1", "kind": "requirement",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}
	claimed := decodeBody[v3ClaimResponse](t, payload)
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/report", map[string]any{
		"task_id": "t_req", "lease_id": *claimed.Task.LeaseID, "material_id": materialRow.MaterialID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report = %d (%s)", resp.StatusCode, payload)
	}
}

// TestWorkersExposeCurrentWorkItemID 锁住 /v1/workers 的当前任务口径（issue #89 C1/E4，返工）。
//
// 数据边界：workers.current_task_id 存的是 tasks 的自增行 id，而对外契约（旧版 /v1/workers、
// 任务响应、claim 信封）一律是任务业务标识 `t_...`；两者混用会让前端把行 id 当字符串显示、
// 或者干脆显示不出来。
// 回归保护：上一版只改前端类型 → mock 通过、真后端下忙碌节点显示「—」。所以断言直接打
// 真正挂载的 V3 路由（NewRouterV3），而不是 store 层或旧版 handler。
func TestWorkersExposeCurrentWorkItemID(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_cur_item", "kind": "dev", "title": "执行中",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, payload)
	}
	for _, workerID := range []string{"w_busy", "w_idle"} {
		resp, payload = a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
			"worker_id": workerID, "kinds": []string{"dev"},
		})
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("heartbeat %s = %d (%s)", workerID, resp.StatusCode, payload)
		}
	}
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_busy", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}
	if claimed := decodeBody[v3ClaimResponse](t, payload); claimed.Task.TaskID != "t_cur_item" {
		t.Fatalf("claimed task = %q, want t_cur_item", claimed.Task.TaskID)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list workers = %d (%s)", resp.StatusCode, payload)
	}
	list := decodeBody[v3WorkerListResponse](t, payload)
	byID := make(map[string]v3WorkerResponse, len(list.Items))
	for _, worker := range list.Items {
		byID[worker.WorkerID] = worker
	}

	busy, ok := byID["w_busy"]
	if !ok {
		t.Fatalf("w_busy missing: %+v", list.Items)
	}
	if busy.CurrentWorkItemID == nil {
		t.Fatalf("w_busy current_work_item_id = null, want t_cur_item（行 id 没映射成业务标识）")
	}
	if got := *busy.CurrentWorkItemID; got != "t_cur_item" || !strings.HasPrefix(got, "t_") {
		t.Errorf("w_busy current_work_item_id = %q, want t_cur_item（t_ 前缀）", got)
	}
	// 旧字段保留：仍是行 id（潜在消费者依赖它），不能把业务标识塞进去。
	if busy.CurrentTaskID == nil || *busy.CurrentTaskID <= 0 {
		t.Errorf("w_busy current_task_id = %v, want 行 id 保留", busy.CurrentTaskID)
	}

	idle, ok := byID["w_idle"]
	if !ok {
		t.Fatalf("w_idle missing: %+v", list.Items)
	}
	if idle.CurrentWorkItemID != nil {
		t.Errorf("w_idle current_work_item_id = %q, want null", *idle.CurrentWorkItemID)
	}

	// 线上 JSON 再确认一次：该键必须存在且空闲节点显式为 null（前端契约是 `string | null`，
	// 缺省键与 null 在类型层不同，别让 omitempty 把契约改成可选）。
	var raw struct {
		Items []struct {
			WorkerID          string          `json:"worker_id"`
			CurrentWorkItemID json.RawMessage `json:"current_work_item_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("decode raw workers: %v", err)
	}
	for _, item := range raw.Items {
		if len(item.CurrentWorkItemID) == 0 {
			t.Fatalf("%s: current_work_item_id 键缺失（契约要求显式 null / 字符串）", item.WorkerID)
		}
		switch item.WorkerID {
		case "w_busy":
			if got := string(item.CurrentWorkItemID); got != `"t_cur_item"` {
				t.Errorf("w_busy raw current_work_item_id = %s, want \"t_cur_item\"", got)
			}
		case "w_idle":
			if got := string(item.CurrentWorkItemID); got != "null" {
				t.Errorf("w_idle raw current_work_item_id = %s, want null", got)
			}
		}
	}
}

// countingQuerier 是 storedb.Querier 的计数包装器（测试专用）：记录每条经过读接缝的 SQL，
// 用来证明「任务业务标识解析」不随节点数增长（review：N+1）。
//
// 接缝说明：store/v3 的函数都吃 storedb.Querier，所以在 store 接缝上计数就能覆盖
// ListWorkers 的全部读；app/v3.WorkerService 依赖具体 *storedb.Store（Read() 返回 *sql.DB），
// 没有可注入点，端到端行为另由 TestWorkersExposeCurrentWorkItemID /
// TestWorkersCurrentWorkItemIDDanglingTask 覆盖。
type countingQuerier struct {
	inner *sql.DB
	mu    sync.Mutex
	sqls  []string
}

func (c *countingQuerier) record(query string) {
	c.mu.Lock()
	c.sqls = append(c.sqls, query)
	c.mu.Unlock()
}

func (c *countingQuerier) reset() {
	c.mu.Lock()
	c.sqls = nil
	c.mu.Unlock()
}

func (c *countingQuerier) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sqls...)
}

func (c *countingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.record(query)
	return c.inner.QueryContext(ctx, query, args...)
}

func (c *countingQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	c.record(query)
	return c.inner.Query(query, args...)
}

func (c *countingQuerier) QueryRow(query string, args ...any) *sql.Row {
	c.record(query)
	return c.inner.QueryRow(query, args...)
}

func (c *countingQuerier) Exec(query string, args ...any) (sql.Result, error) {
	c.record(query)
	return c.inner.Exec(query, args...)
}

// seedBusyWorker 造一个「忙碌节点」：建一张 dev 卡 + 注册 worker，并把 workers.current_task_id
// 直接绑到该卡上（绕开 claim 的排队语义，只为了造这一状态）。
func (a *apiServer) seedBusyWorker(t *testing.T, projectID, workerID, taskID, now string) {
	t.Helper()
	ctx := context.Background()
	task, err := storev3.CreateTask(ctx, a.store, storev3.CreateTaskInput{
		ProjectID: projectID, TaskID: taskID, Kind: enum.Dev, Title: "N+1 取证", Now: now,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", taskID, err)
	}
	if err := storev3.UpsertWorker(ctx, a.store, workerID, "{}", now); err != nil {
		t.Fatalf("upsert worker %s: %v", workerID, err)
	}
	if _, err := a.store.Write().ExecContext(ctx,
		`UPDATE workers SET state = ?, current_task_id = ? WHERE worker_id = ?`,
		enum.WorkerBusy, task.ID, workerID); err != nil {
		t.Fatalf("bind worker %s: %v", workerID, err)
	}
}

// TestListWorkersTaskMappingDoesNotScale 锁住「一次列表请求里任务标识解析不随节点数增长」。
//
// 上一版在 handler 里逐节点调 CurrentWorkItemID（每次一条 `SELECT task_id FROM tasks WHERE id = ?`）：
// 默认 100 个忙碌节点就是 100 次串行查询（review 的 N+1）。现在映射并进 ListWorkers 的
// LEFT JOIN，整页列表 1 条 SQL，任务标识也在这一条里取到 → 解析相关的额外查询 0 条。
func TestListWorkersTaskMappingDoesNotScale(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	ctx := context.Background()
	now := "2026-11-18T00:00:00.000Z"
	cq := &countingQuerier{inner: a.store.Read()}
	counts := make(map[int][]string, 2)
	bound := 0
	for _, n := range []int{3, 30} {
		for i := 0; i < n; i++ {
			bound++
			a.seedBusyWorker(t,
				project.ProjectID,
				fmt.Sprintf("w_n1_%02d", bound),
				fmt.Sprintf("t_n1_%02d", bound),
				now)
		}

		cq.reset()
		workers, err := storev3.ListWorkers(ctx, cq, 1000)
		if err != nil {
			t.Fatalf("N=%d list workers: %v", n, err)
		}
		sqls := cq.snapshot()
		counts[n] = sqls
		if len(sqls) != 1 {
			t.Fatalf("N=%d 的列表查询数 = %d，want 1（SQL：%v）", n, len(sqls), sqls)
		}
		if !strings.Contains(strings.ToLower(sqls[0]), "left join tasks") {
			t.Errorf("N=%d 的列表 SQL 没有 LEFT JOIN tasks：%s", n, sqls[0])
		}
		if len(workers) != bound {
			t.Fatalf("N=%d 的列表条数 = %d，want %d", n, len(workers), bound)
		}
		for _, worker := range workers {
			if worker.CurrentTaskID == nil {
				continue
			}
			if worker.CurrentTaskCode == nil {
				t.Fatalf("%s（行 id %d）的任务业务标识没在列表查询里取到", worker.WorkerID, *worker.CurrentTaskID)
			}
			if !strings.HasPrefix(*worker.CurrentTaskCode, "t_n1_") {
				t.Errorf("%s current_task_code = %q, want t_n1_ 前缀", worker.WorkerID, *worker.CurrentTaskCode)
			}
		}
	}
	if len(counts[3]) != len(counts[30]) {
		t.Fatalf("任务标识解析随节点数变化：N=3 → %d 条查询，N=30 → %d 条查询", len(counts[3]), len(counts[30]))
	}
	for _, n := range []int{3, 30} {
		workers, tasks, other := 0, 0, 0
		for _, statement := range counts[n] {
			lower := strings.ToLower(statement)
			switch {
			case strings.Contains(lower, "from tasks") && !strings.Contains(lower, "join tasks"):
				tasks++
			case strings.Contains(lower, "from workers"):
				workers++
			default:
				other++
			}
		}
		// 原始计数（按 SQL 前缀分类）：改前是 workers=1、tasks=N（逐节点解析任务标识）。
		t.Logf("N=%d 原始计数：SELECT … FROM workers = %d 条（含 LEFT JOIN tasks）；单独 FROM tasks（逐节点解析任务标识）= %d 条；其它 = %d 条",
			n, workers, tasks, other)
	}
}

// TestWorkersCurrentWorkItemIDDanglingTask 钉住悬空指针的完整契约（issue #165 §1）：
// worker 指向已删任务行时列表仍 200、该节点 current_work_item_id 为 null（不能 500，
// 也不能把行 id 当标识吐出去）；维护循环跑过一轮 sweep 后指针被清掉、行本身保留到
// 超过 retention 才删。改动理由：旧断言「悬空行 id 仍在」正是 §1 要修的永久残留，
// 清指针后该行才能按保留期正常退休。
func TestWorkersCurrentWorkItemIDDanglingTask(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_dangling", "kind": "dev", "title": "会被硬删的任务",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, payload)
	}
	resp, payload = a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": "w_dangling", "kinds": []string{"dev"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat = %d (%s)", resp.StatusCode, payload)
	}
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_dangling", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}

	// 造「任务行被硬删、worker 指针还留着」：workers.current_task_id REFERENCES tasks(id)，
	// 写连接开了 foreign_keys，删除前临时关掉（写池 MaxOpenConns=1，PRAGMA 作用在同一连接上）。
	ctx := context.Background()
	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`DELETE FROM tasks WHERE task_id = 't_dangling'`,
		`PRAGMA foreign_keys = ON`,
	} {
		if _, err := a.store.Write().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	// 硬删之后指针确实还悬着：不先钉住这一点，下面的「sweep 后为 nil」也可能是
	// claim 根本没绑上而假绿。
	var dangling sql.NullInt64
	if err := a.store.Read().QueryRowContext(ctx,
		`SELECT current_task_id FROM workers WHERE worker_id = 'w_dangling'`).Scan(&dangling); err != nil {
		t.Fatalf("read dangling pointer: %v", err)
	}
	if !dangling.Valid {
		t.Fatalf("硬删后 w_dangling.current_task_id 已为 NULL，悬空指针场景没造出来")
	}

	// 「任务行被硬删、worker 指针还留着」只能由维护循环的一轮 sweep 收敛
	// （issue #165 §1）：先释放不再指向 in_progress 任务的指针，再按 retention 删行。
	// 该节点刚心跳过，未过 retention，所以这一轮清指针、留行。
	if _, err := a.workers.ReleaseStale(ctx); err != nil {
		t.Fatalf("ReleaseStale: %v", err)
	}
	if _, err := a.workers.PruneStale(ctx, time.Hour); err != nil {
		t.Fatalf("PruneStale: %v", err)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("悬空指针下的列表 = %d (%s)，want 200", resp.StatusCode, payload)
	}
	list := decodeBody[v3WorkerListResponse](t, payload)
	var found bool
	for _, worker := range list.Items {
		if worker.WorkerID != "w_dangling" {
			continue
		}
		found = true
		// 一轮 sweep 之后悬空指针必须被清掉：留着它就再也没人能把该行退休
		// （PruneStaleWorkers 不删带指针的行）。
		if worker.CurrentTaskID != nil {
			t.Errorf("w_dangling current_task_id = %d，want nil（sweep 后悬空指针应被清掉）", *worker.CurrentTaskID)
		}
		if worker.CurrentWorkItemID != nil {
			t.Errorf("w_dangling current_work_item_id = %q，want null", *worker.CurrentWorkItemID)
		}
	}
	if !found {
		t.Fatalf("w_dangling 不在列表里：%+v", list.Items)
	}

	var raw struct {
		Items []struct {
			WorkerID          string          `json:"worker_id"`
			CurrentWorkItemID json.RawMessage `json:"current_work_item_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("decode raw workers: %v", err)
	}
	for _, item := range raw.Items {
		if item.WorkerID != "w_dangling" {
			continue
		}
		if len(item.CurrentWorkItemID) == 0 {
			t.Fatalf("w_dangling: current_work_item_id 键缺失（契约要求显式 null）")
		}
		if got := string(item.CurrentWorkItemID); got != "null" {
			t.Errorf("w_dangling raw current_work_item_id = %s, want null", got)
		}
	}
}

// TestWorkersProjectForceCloseReleasesHolder 覆盖 AC1/AC2/AC4 的 HTTP 面：
// 项目 force 关闭批量取消持卡任务后，GET /v1/workers 里该节点的
// current_task_id / current_lease_id 立刻为空；超过 worker_retention 再跑一轮 sweep，
// 列表不再含该节点（旧实现在这里永久残留，issue #165 §1）。
func TestWorkersProjectForceCloseReleasesHolder(t *testing.T) {
	a := newAPIServer(t)
	ctx := context.Background()
	a.seedProduct(t, "APP")

	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "关闭"})
	project := decodeBody[v3ProjectResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "task_id": "t_close", "kind": "dev", "title": "会被 force 取消的任务",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task = %d (%s)", resp.StatusCode, payload)
	}
	resp, payload = a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": "w_close", "kinds": []string{"dev"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat = %d (%s)", resp.StatusCode, payload)
	}
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_close", "kind": "dev",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}

	// helper: GET /v1/workers 并按 worker_id 取一项。
	workerOf := func(workerID string) (v3WorkerResponse, bool) {
		t.Helper()
		resp, payload := a.do(t, http.MethodGet, "/v1/workers", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/workers = %d (%s)", resp.StatusCode, payload)
		}
		for _, worker := range decodeBody[v3WorkerListResponse](t, payload).Items {
			if worker.WorkerID == workerID {
				return worker, true
			}
		}
		return v3WorkerResponse{}, false
	}

	busy, found := workerOf("w_close")
	if !found {
		t.Fatalf("w_close 领卡后不在列表里")
	}
	if busy.State != enum.WorkerBusy || busy.CurrentTaskID == nil || busy.CurrentLeaseID == nil {
		t.Fatalf("w_close 领卡后 = %+v，want busy 且带指针", busy)
	}
	if busy.CurrentWorkItemID == nil || *busy.CurrentWorkItemID != "t_close" {
		t.Fatalf("w_close current_work_item_id = %v, want t_close", busy.CurrentWorkItemID)
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/projects/close?id="+project.ProjectID+"&force=1", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("force close = %d (%s)", resp.StatusCode, payload)
	}

	// AC1 / AC4：批量取消必须当场归还占用。
	idle, found := workerOf("w_close")
	if !found {
		t.Fatalf("w_close 在 force 关闭后从列表消失（未过 retention，行应保留）")
	}
	if idle.State != enum.WorkerIdle {
		t.Errorf("w_close state = %s, want idle", idle.State)
	}
	if idle.CurrentTaskID != nil {
		t.Errorf("w_close current_task_id = %d, want nil", *idle.CurrentTaskID)
	}
	if idle.CurrentLeaseID != nil {
		t.Errorf("w_close current_lease_id = %q, want nil", *idle.CurrentLeaseID)
	}
	if idle.CurrentWorkItemID != nil {
		t.Errorf("w_close current_work_item_id = %q, want null", *idle.CurrentWorkItemID)
	}

	// AC2：超过 retention 后一轮 sweep，列表不再含该节点。
	if _, err := a.store.Write().ExecContext(ctx,
		`UPDATE workers SET last_seen = '2020-01-01T00:00:00.000Z' WHERE worker_id = 'w_close'`); err != nil {
		t.Fatalf("age worker: %v", err)
	}
	if _, err := a.workers.ReleaseStale(ctx); err != nil {
		t.Fatalf("ReleaseStale: %v", err)
	}
	pruned, err := a.workers.PruneStale(ctx, time.Hour)
	if err != nil {
		t.Fatalf("PruneStale: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d, want 1", pruned)
	}
	if _, found := workerOf("w_close"); found {
		t.Errorf("w_close 超过 retention 且 sweep 一轮后仍在列表里")
	}
}

// TestProjectMaterialsList pins the project material view: materials are owned by
// tasks, and the project view is derived by joining the project's tasks.
func TestProjectMaterialsList(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")

	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "name": "p",
	})
	project := decodeBody[v3ProjectResponse](t, payload)

	_, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "requirement", "title": "需求",
	})
	task := decodeBody[v3TaskResponse](t, payload)

	_, payload = a.do(t, http.MethodPost, "/v1/materials", map[string]any{
		"kind": "requirement", "title": "需求", "body_md": "# 需求\n正文",
	})
	material := decodeBody[v3MaterialResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks/attach", map[string]any{
		"task_id": task.TaskID, "material_id": material.MaterialID, "role": "output",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("attach = %d (%s)", resp.StatusCode, payload)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/projects/materials?id="+project.ProjectID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list project materials = %d (%s)", resp.StatusCode, payload)
	}
	list := decodeBody[v3ProjectMaterialList](t, payload)
	if len(list.Items) != 1 {
		t.Fatalf("items = %+v, want 1", list.Items)
	}
	item := list.Items[0]
	if item.MaterialID != material.MaterialID || item.Kind != "requirement" || item.Role != "output" {
		t.Errorf("item = %+v, want material %s requirement/output", item, material.MaterialID)
	}
	if item.TaskID != task.TaskID {
		t.Errorf("item task_id = %q, want the producing task %q", item.TaskID, task.TaskID)
	}

	resp, payload = a.do(t, http.MethodGet, "/v1/projects/materials?id="+project.ProjectID+"&role=input", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list by role = %d (%s)", resp.StatusCode, payload)
	}
	if got := decodeBody[v3ProjectMaterialList](t, payload); len(got.Items) != 0 {
		t.Errorf("role=input items = %+v, want none", got.Items)
	}
}

// TestProjectMaterialsPairBriefs pins the rework case: a rejected requirement
// resubmitted as v2 must keep each brief attached to the version it explains,
// so "the brief of the latest requirement" is unambiguous.
func TestProjectMaterialsPairBriefs(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")

	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "name": "p",
	})
	project := decodeBody[v3ProjectResponse](t, payload)

	_, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "requirement", "title": "需求",
	})
	task := decodeBody[v3TaskResponse](t, payload)

	createAndAttach := func(kind, title, body, source string) string {
		t.Helper()
		req := map[string]any{"kind": kind, "title": title, "body_md": body}
		if source != "" {
			req["source_material_id"] = source
		}
		_, payload := a.do(t, http.MethodPost, "/v1/materials", req)
		material := decodeBody[v3MaterialResponse](t, payload)
		resp, payload := a.do(t, http.MethodPost, "/v1/tasks/attach", map[string]any{
			"task_id": task.TaskID, "material_id": material.MaterialID, "role": "output",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("attach %s: %d (%s)", title, resp.StatusCode, payload)
		}
		return material.MaterialID
	}

	reqV1 := createAndAttach("requirement", "需求 v1", "# v1", "")
	briefV1 := createAndAttach("context", "需求 v1 简报", "## 结论\nv1", reqV1)
	reqV2 := createAndAttach("requirement", "需求 v2", "# v2", "")
	briefV2 := createAndAttach("context", "需求 v2 简报", "## 结论\nv2", reqV2)

	resp, payload := a.do(t, http.MethodGet, "/v1/projects/materials?id="+project.ProjectID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list project materials = %d (%s)", resp.StatusCode, payload)
	}
	items := decodeBody[v3ProjectMaterialList](t, payload).Items
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4 (%+v)", len(items), items)
	}

	pairs := map[string]string{}
	for _, item := range items {
		if item.TaskID != task.TaskID {
			t.Errorf("item %s task_id = %q, want the producing task %q", item.MaterialID, item.TaskID, task.TaskID)
		}
		if item.Kind == "context" {
			if item.SourceMaterialID == nil {
				t.Fatalf("brief %s has no source", item.MaterialID)
			}
			pairs[*item.SourceMaterialID] = item.MaterialID
		}
	}
	if pairs[reqV1] != briefV1 || pairs[reqV2] != briefV2 {
		t.Errorf("pairs = %+v, want %s->%s and %s->%s", pairs, reqV1, briefV1, reqV2, briefV2)
	}
	if len(pairs) != 2 {
		t.Errorf("distinct brief sources = %d, want 2", len(pairs))
	}
}

// TestProjectMaterialsListTwoDevTasks pins the grouping precondition of the
// project view: two dev cards of the same project each carry their own report,
// so both rows come back and each one points at its own task. An implementation
// that deduplicates by task or drops the task_id would break that grouping.
func TestProjectMaterialsListTwoDevTasks(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")

	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "name": "p",
	})
	project := decodeBody[v3ProjectResponse](t, payload)

	createDevTask := func(title string) string {
		t.Helper()
		_, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
			"project_id": project.ProjectID, "kind": "dev", "title": title,
		})
		return decodeBody[v3TaskResponse](t, payload).TaskID
	}
	attachReport := func(taskID, title string) string {
		t.Helper()
		_, payload := a.do(t, http.MethodPost, "/v1/materials", map[string]any{
			"kind": "report", "title": title, "body_md": "# " + title,
		})
		material := decodeBody[v3MaterialResponse](t, payload)
		resp, payload := a.do(t, http.MethodPost, "/v1/tasks/attach", map[string]any{
			"task_id": taskID, "material_id": material.MaterialID, "role": "output",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("attach %s: %d (%s)", title, resp.StatusCode, payload)
		}
		return material.MaterialID
	}

	taskA := createDevTask("卡片 A")
	reportA := attachReport(taskA, "A 报告")
	taskB := createDevTask("卡片 B")
	reportB := attachReport(taskB, "B 报告")

	resp, payload := a.do(t, http.MethodGet, "/v1/projects/materials?id="+project.ProjectID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list project materials = %d (%s)", resp.StatusCode, payload)
	}
	items := decodeBody[v3ProjectMaterialList](t, payload).Items
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (%+v)", len(items), items)
	}

	byTask := map[string]string{}
	for _, item := range items {
		if item.TaskID == "" {
			t.Fatalf("item %s has no task_id", item.MaterialID)
		}
		byTask[item.TaskID] = item.MaterialID
	}
	if len(byTask) != 2 {
		t.Fatalf("distinct task_ids = %d, want 2 (%+v)", len(byTask), items)
	}
	if byTask[taskA] != reportA || byTask[taskB] != reportB {
		t.Errorf("mapping = %+v, want %s->%s and %s->%s", byTask, taskA, reportA, taskB, reportB)
	}
}
