package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// breakdownHTTPEnv drives the pipeline with the services main.go wires, leaving
// its breakdown card leased so the HTTP report path starts from tasks_draft.
type breakdownHTTPEnv struct {
	handler    http.Handler
	store      *store.Store
	work       *app.WorkItemService
	projects   *app.ProjectService
	workers    *app.WorkerService
	projectID  string
	workerID   string
	breakdown  *app.Dispatch
	materialID string
}

func newBreakdownHTTPEnv(t *testing.T) *breakdownHTTPEnv {
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
	logger := testLogger(io.Discard)
	products := app.NewProductService(st)
	projects := app.NewProjectService(st, writer)
	work := app.NewWorkItemService(st, writer)
	claims := app.NewClaimService(st, logger, 60*time.Second)
	workers := app.NewWorkerService(st, logger, app.WorkerServiceOptions{
		LeaseTTL:          60 * time.Second,
		WorkerTTL:         time.Minute,
		HeartbeatInterval: 15 * time.Second,
		MetadataMaxBytes:  8 << 10,
	})
	handler := Chain(
		NewRouter(logger, nil, nil, products, projects, work, claims, workers,
			app.NewMaterialService(st, writer), app.NewInboxService(st, logger, 72)),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)

	if _, err := products.CreateProduct(context.Background(), app.CreateProductInput{
		ProductID: "APP", Name: "APP", GitURL: "git@github.com:Casper-Mars/app.git",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	created, err := projects.CreateProject(context.Background(), app.CreateProjectInput{
		ProductID: "APP", Title: "HTTP 拆分", RequirementMD: "# 需求\n\n正文\n", UploadedBy: "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	env := &breakdownHTTPEnv{
		handler: handler, store: st, work: work, projects: projects, workers: workers,
		projectID: created.Project.ProjectID,
	}
	if created.Requirement == nil {
		t.Fatal("project has no requirement material")
	}

	dispatch := env.heartbeat(t, "requirement")
	if _, err := work.ReportWorkItem(context.Background(), app.ReportWorkItemInput{
		WorkID: dispatch.WorkID, LeaseID: dispatch.Lease.LeaseID,
		MaterialID: created.Requirement.MaterialID, Actor: "agent:w",
	}); err != nil {
		t.Fatalf("report requirement: %v", err)
	}
	env.approveProject(t)

	dispatch = env.heartbeat(t, "design")
	spec, _, err := projects.SaveSpec(context.Background(), env.projectID, "", "agent:cto",
		strPtr("方案"), "# 方案\n\n正文\n", 0, false)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := work.ReportWorkItem(context.Background(), app.ReportWorkItemInput{
		WorkID: dispatch.WorkID, LeaseID: dispatch.Lease.LeaseID,
		MaterialID: spec.Spec.MaterialID, Actor: "agent:w",
	}); err != nil {
		t.Fatalf("report design: %v", err)
	}
	env.approveProject(t)

	env.breakdown = env.heartbeat(t, "breakdown")
	env.materialID = spec.Spec.MaterialID
	return env
}

func (e *breakdownHTTPEnv) heartbeat(t *testing.T, wantKind string) *app.Dispatch {
	t.Helper()
	result, err := e.workers.Heartbeat(context.Background(), app.HeartbeatInput{
		State: "idle", Kinds: []string{"requirement", "design", "breakdown", "dev"}, Actor: "agent:w",
	})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(result.Commands) == 0 || result.Commands[0].Dispatch == nil {
		t.Fatalf("heartbeat produced no dispatch, want %s", wantKind)
	}
	dispatch := result.Commands[0].Dispatch
	if dispatch.Kind != wantKind {
		t.Fatalf("dispatch kind = %q, want %q", dispatch.Kind, wantKind)
	}
	e.workerID = result.Worker.WorkerID
	return dispatch
}

func (e *breakdownHTTPEnv) approveProject(t *testing.T) {
	t.Helper()
	detail, err := e.projects.GetProject(context.Background(), e.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	version := detail.Project.Version
	if _, err := e.projects.Decide(context.Background(), app.DecideInput{
		Kind: app.DecideKindProject, ID: e.projectID,
		Decision: "approve", ExpectVersion: &version, Actor: "human:cto",
	}); err != nil {
		t.Fatalf("approve project: %v", err)
	}
}

func (e *breakdownHTTPEnv) reportBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"work_id":  e.breakdown.WorkID,
		"lease_id": e.breakdown.Lease.LeaseID,
	}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func (e *breakdownHTTPEnv) projectStatus(t *testing.T) int {
	t.Helper()
	var status int
	if err := e.store.Read().QueryRowContext(context.Background(),
		`SELECT status FROM projects WHERE project_id = ?`, e.projectID).Scan(&status); err != nil {
		t.Fatalf("read project status: %v", err)
	}
	return status
}

// TestBreakdownReportHTTPValidation separates the five rejection classes at the
// HTTP boundary and proves the lease survives every one of them.
func TestBreakdownReportHTTPValidation(t *testing.T) {
	e := newBreakdownHTTPEnv(t)

	cases := []struct {
		name   string
		extra  map[string]any
		field  string
		reason string
	}{
		{"missing plan", map[string]any{}, "plan", "required"},
		{"empty plan", map[string]any{"plan": map[string]any{}}, "plan.items", "required"},
		{"no items", map[string]any{"plan": map[string]any{"items": []any{}}}, "plan.items", "at least 1 item"},
		{"cycle", map[string]any{"plan": map[string]any{"items": []any{
			map[string]any{"seq_no": 1, "title": "一", "body_md": "# 一", "priority": 10, "depends_on": []int{2}},
			map[string]any{"seq_no": 2, "title": "二", "body_md": "# 二", "priority": 10, "depends_on": []int{1}},
		}}}, "plan.items", "dependency cycle: 1 → 2 → 1"},
		{"cross project", map[string]any{"plan": map[string]any{"items": []any{
			map[string]any{"seq_no": 1, "project_id": "OTHER-1", "title": "一", "body_md": "# 一", "priority": 10},
		}}}, "plan.items[0].project_id", "must equal"},
		{"seq gap", map[string]any{"plan": map[string]any{"items": []any{
			map[string]any{"seq_no": 1, "title": "一", "body_md": "# 一", "priority": 10},
			map[string]any{"seq_no": 3, "title": "三", "body_md": "# 三", "priority": 10},
		}}}, "plan.items[1].seq_no", "must be between 1 and 2"},
		{"summary on breakdown", map[string]any{
			"summary_md": "# 报告",
			"plan": map[string]any{"items": []any{
				map[string]any{"seq_no": 1, "title": "一", "body_md": "# 一", "priority": 10},
			}},
		}, "summary_md", "not allowed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report", e.reportBody(tc.extra))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			t.Logf("HTTP %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
			if body.Code != "invalid_argument" {
				t.Errorf("code = %q, want invalid_argument", body.Code)
			}
			requireDetail(t, body, tc.field, tc.reason)

			// Rollback: the card is still leased and the project has not moved.
			var state, leaseID string
			if err := e.store.Read().QueryRowContext(context.Background(), `
				SELECT state, lease_id FROM work_items WHERE work_id = ?`,
				e.breakdown.WorkID).Scan(&state, &leaseID); err != nil {
				t.Fatalf("read breakdown: %v", err)
			}
			if state != store.WorkItemStateLeased || leaseID != e.breakdown.Lease.LeaseID {
				t.Errorf("breakdown = %q/%q, want leased with the original lease", state, leaseID)
			}
			if status := e.projectStatus(t); status != 4 {
				t.Errorf("project status = %d, want tasks_draft", status)
			}
			var children int
			if err := e.store.Read().QueryRowContext(context.Background(), `
				SELECT COUNT(*) FROM work_items WHERE parent_id = (SELECT id FROM work_items WHERE work_id = ?)`,
				e.breakdown.WorkID).Scan(&children); err != nil {
				t.Fatalf("count children: %v", err)
			}
			if children != 0 {
				t.Errorf("children = %d, want 0", children)
			}
		})
	}
}

// TestBreakdownReportHTTPSuccess covers the happy path over HTTP: plan accepted
// without material_id, children blocked under the breakdown, gate unlock, and
// the duplicate 409.
func TestBreakdownReportHTTPSuccess(t *testing.T) {
	e := newBreakdownHTTPEnv(t)

	plan := map[string]any{"plan": map[string]any{"items": []any{
		map[string]any{"seq_no": 1, "project_id": e.projectID, "title": "实现登录",
			"body_md": "# 实现登录\n", "priority": 10},
		map[string]any{"seq_no": 2, "title": "登录测试", "body_md": "# 登录测试\n",
			"priority": 5, "depends_on": []int{1}},
	}}}

	rec := doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report", e.reportBody(plan))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var reported workItemDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reported); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if reported.State != "submitted" {
		t.Errorf("state = %q, want submitted", reported.State)
	}
	if reported.ResultMaterialID == nil {
		t.Fatal("result_material_id = nil, want the kind=4 plan material")
	}
	if reported.Result == nil || reported.Result.MaterialID != *reported.ResultMaterialID {
		t.Errorf("result = %+v, want material %q", reported.Result, *reported.ResultMaterialID)
	}
	if reported.Result.Kind != 4 {
		t.Errorf("result.kind = %d, want 4", reported.Result.Kind)
	}
	if reported.Result.Title != "拆分计划：HTTP 拆分" {
		t.Errorf("result.title = %q", reported.Result.Title)
	}

	rec = doJSON(t, e.handler, http.MethodGet, "/v1/materials?id="+*reported.ResultMaterialID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download plan material = %d (body %s)", rec.Code, rec.Body.String())
	}
	var planDocument struct {
		Items []struct {
			SeqNo     int    `json:"seq_no"`
			ProjectID string `json:"project_id"`
			Title     string `json:"title"`
			BodyMD    string `json:"body_md"`
			Priority  int    `json:"priority"`
			DependsOn []int  `json:"depends_on"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &planDocument); err != nil {
		t.Fatalf("plan material is not JSON: %v (%s)", err, rec.Body.String())
	}
	if len(planDocument.Items) != 2 {
		t.Fatalf("plan items = %d, want 2", len(planDocument.Items))
	}
	if planDocument.Items[0].SeqNo != 1 || planDocument.Items[0].Title != "实现登录" ||
		planDocument.Items[0].ProjectID != e.projectID {
		t.Errorf("plan item[0] = %+v", planDocument.Items[0])
	}
	if deps := planDocument.Items[1].DependsOn; len(deps) != 1 || deps[0] != 1 {
		t.Errorf("plan item[1].depends_on = %v, want [1]", deps)
	}
	if status := e.projectStatus(t); status != 5 {
		t.Errorf("project status = %d, want tasks_review", status)
	}

	rec = doJSON(t, e.handler, http.MethodGet,
		"/v1/work-items?project_id="+e.projectID+"&kind=dev", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list dev: %d (body %s)", rec.Code, rec.Body.String())
	}
	var listed workItemListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Items) != 2 {
		t.Fatalf("dev items = %d, want 2", len(listed.Items))
	}
	for i, item := range listed.Items {
		if item.Kind != "dev" || item.State != "blocked" {
			t.Errorf("dev[%d] = %q/%q, want dev/blocked", i, item.Kind, item.State)
		}
		if item.ParentID == nil || *item.ParentID != e.breakdown.WorkID {
			t.Errorf("dev[%d].parent_id = %v, want %q", i, item.ParentID, e.breakdown.WorkID)
		}
	}
	if deps := listed.Items[1].DependsOn; len(deps) != 1 || deps[0] != listed.Items[0].WorkID {
		t.Errorf("dev[1].depends_on = %v, want [%s]", deps, listed.Items[0].WorkID)
	}

	// Before the gate approves, the queue is empty for this worker.
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/claim",
		map[string]any{"worker_id": e.workerID})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("claim before approve = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/review",
		map[string]any{"project_id": e.projectID, "decision": "approve"})
	if rec.Code != http.StatusOK {
		t.Fatalf("review approve = %d (body %s)", rec.Code, rec.Body.String())
	}
	var review reviewWorkItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &review); err != nil {
		t.Fatalf("decode review: %v", err)
	}
	if review.ReadyWorkItems != 2 || review.Status != 6 {
		t.Errorf("review = %d/%d, want 2 ready / status 6", review.ReadyWorkItems, review.Status)
	}

	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/claim",
		map[string]any{"worker_id": e.workerID})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim after approve = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var claimed claimResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode dispatch: %v", err)
	}
	if claimed.Dispatch.Kind != "dev" {
		t.Errorf("dispatch kind = %q, want dev", claimed.Dispatch.Kind)
	}

	// Replaying the report is a stable 409 and creates no second batch.
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report", e.reportBody(plan))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate report = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "conflict" ||
		!strings.Contains(body.Message, "plan already submitted for breakdown item") {
		t.Errorf("duplicate body = %+v, want conflict/plan already submitted", body)
	}
}

// TestWorkReportWithSummaryMD covers the non-breakdown delivery path: the
// caller sends only summary_md, the platform writes a kind=3 report material in
// the same transaction, and result_material_id points at it.
func TestWorkReportWithSummaryMD(t *testing.T) {
	e := newBreakdownHTTPEnv(t)

	// Drive the project through breakdown so the first dev card is claimable.
	plan := map[string]any{"plan": map[string]any{"items": []any{
		map[string]any{"seq_no": 1, "project_id": e.projectID, "title": "实现登录",
			"body_md": "# 实现登录\n", "priority": 10},
	}}}
	rec := doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report", e.reportBody(plan))
	if rec.Code != http.StatusOK {
		t.Fatalf("report breakdown = %d (body %s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/review",
		map[string]any{"project_id": e.projectID, "decision": "approve"})
	if rec.Code != http.StatusOK {
		t.Fatalf("review approve = %d (body %s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/claim",
		map[string]any{"worker_id": e.workerID})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim dev = %d (body %s)", rec.Code, rec.Body.String())
	}
	var claimed claimResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode dispatch: %v", err)
	}
	workID, leaseID := claimed.Dispatch.WorkID, claimed.Dispatch.Lease.LeaseID

	// Neither material_id nor summary_md is rejected locally by the platform.
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report",
		map[string]any{"work_id": workID, "lease_id": leaseID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty report = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	requireDetail(t, decodeError(t, rec), "material_id", "material_id or summary_md")

	// Both fields are ambiguous and rejected before the material write.
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report",
		map[string]any{"work_id": workID, "lease_id": leaseID,
			"material_id": e.materialID, "summary_md": "# 报告"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("both report fields = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	requireDetail(t, decodeError(t, rec), "summary_md", "mutually exclusive")

	summary := "# 改了什么\n- 实现登录\n\n# 怎么自检\n- go test ./...\n"
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report",
		map[string]any{"work_id": workID, "lease_id": leaseID, "summary_md": summary})
	if rec.Code != http.StatusOK {
		t.Fatalf("report dev = %d (body %s)", rec.Code, rec.Body.String())
	}
	var reported workItemDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reported); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if reported.State != "submitted" {
		t.Errorf("state = %q, want submitted", reported.State)
	}
	if reported.Result == nil || reported.Result.Kind != 3 {
		t.Fatalf("result = %+v, want a kind=3 report material", reported.Result)
	}
	if reported.ResultMaterialID == nil || *reported.ResultMaterialID != reported.Result.MaterialID {
		t.Errorf("result_material_id = %v, result = %+v", reported.ResultMaterialID, reported.Result)
	}
	if reported.Result.Title != "任务报告：实现登录" {
		t.Errorf("result.title = %q", reported.Result.Title)
	}

	rec = doJSON(t, e.handler, http.MethodGet, "/v1/materials?id="+reported.Result.MaterialID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download report material = %d (body %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != summary {
		t.Errorf("report body = %q, want %q", body, summary)
	}
}

// requireDetail asserts one details[] entry names field and contains reason.
func requireDetail(t *testing.T, body ErrorBody, field, reason string) {
	t.Helper()
	for _, detail := range body.Details {
		if detail.Field == field && strings.Contains(detail.Reason, reason) {
			return
		}
	}
	t.Fatalf("details = %+v, want %q containing %q", body.Details, field, reason)
}

func strPtr(value string) *string { return &value }
