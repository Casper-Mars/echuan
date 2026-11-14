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
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// newProjectTestServer builds the real middleware stack for the project
// endpoints on a temporary migrated database with a real material writer.
func newProjectTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	return newProjectTestServerWithMaxAttempts(t, 0)
}

// newProjectTestServerWithMaxAttempts is newProjectTestServer with an explicit
// task attempt budget (0 keeps the service default). Plan tests use it to prove
// max_attempts comes from configuration.
func newProjectTestServerWithMaxAttempts(t *testing.T, maxAttempts int) (http.Handler, *store.Store) {
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
	if maxAttempts > 0 {
		projects.SetMaxAttempts(maxAttempts)
	}
	workItems := app.NewWorkItemService(st, writer)
	if maxAttempts > 0 {
		workItems.SetMaxAttempts(maxAttempts)
	}
	h := Chain(
		NewRouter(logger, nil, nil, app.NewProductService(st), projects,
			workItems, app.NewClaimService(st, logger, 90*time.Second),
			nil, app.NewMaterialService(st, writer), nil),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)
	return h, st
}

// seedProduct creates a product straight through the service, so project tests
// do not depend on the product HTTP endpoints.
func seedProduct(t *testing.T, st *store.Store, productID string) {
	t.Helper()
	if _, err := app.NewProductService(st).CreateProduct(context.Background(), app.CreateProductInput{
		ProductID: productID,
		Name:      productID + " 产品",
		GitURL:    "git@example.com:" + productID + ".git",
	}); err != nil {
		t.Fatalf("seed product %s: %v", productID, err)
	}
}

func decodeProjectDetail(t *testing.T, rec *httptest.ResponseRecorder) projectDetailResponse {
	t.Helper()
	var detail projectDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode project detail %q: %v", rec.Body.String(), err)
	}
	return detail
}

func decodeProjectList(t *testing.T, rec *httptest.ResponseRecorder) projectListResponse {
	t.Helper()
	var page projectListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode project list %q: %v", rec.Body.String(), err)
	}
	return page
}

// fieldReason returns the reason recorded for one field, or "" when the error
// body carries no detail for it.
func fieldReason(body ErrorBody, field string) string {
	for _, detail := range body.Details {
		if detail.Field == field {
			return detail.Reason
		}
	}
	return ""
}

const httpRequirement = "# 需求\n\n- 中文与 emoji 🚀 都要原样保存\n"

func TestCreateProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "登录页改版",
		"requirement_md": httpRequirement,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/projects?id=APP-1" {
		t.Errorf("Location = %q, want /v1/projects?id=APP-1", loc)
	}

	detail := decodeProjectDetail(t, rec)
	if detail.ID != "APP-1" || detail.ProjectID != "APP-1" || detail.ProductID != "APP" {
		t.Errorf("ids = %q/%q/%q, want APP-1/APP-1/APP", detail.ID, detail.ProjectID, detail.ProductID)
	}
	if detail.Status != 1 || detail.StatusName != "requirement_review" {
		t.Errorf("status = %d (%s), want 1 (requirement_review)", detail.Status, detail.StatusName)
	}
	if detail.Version != 1 {
		t.Errorf("version = %d, want 1", detail.Version)
	}
	if detail.Reason != nil {
		t.Errorf("reason = %v, want null", *detail.Reason)
	}
	if detail.Requirement == nil {
		t.Fatal("requirement = null, want the requirement summary")
	}
	if detail.Requirement.Version != 1 || detail.Requirement.Kind != 1 {
		t.Errorf("requirement = %+v, want kind 1 version 1", *detail.Requirement)
	}
	if detail.Requirement.Size != int64(len(httpRequirement)) {
		t.Errorf("requirement size = %d, want %d", detail.Requirement.Size, len(httpRequirement))
	}
	if detail.Requirement.MaterialID == "" {
		t.Error("requirement material_id is empty, want the loading handle")
	}
	if detail.Spec != nil {
		t.Errorf("spec = %+v, want null", *detail.Spec)
	}
	// The project enters the review already carrying its requirement, so no
	// stage card is enqueued: nothing is pending and nothing is done.
	if detail.WorkItemTotal != 0 || detail.WorkItemDone != 0 {
		t.Errorf("work_item stats = %d/%d, want 0/0", detail.WorkItemTotal, detail.WorkItemDone)
	}
}

// TestCreateProjectRequirementNoWorkItemNoExtraVersion is the #18 regression
// guard at the HTTP boundary: a created project owns zero requirement work
// items (nothing left to write), and its requirement chain holds exactly the v1
// the request carried.
func TestCreateProjectRequirementNoWorkItemNoExtraVersion(t *testing.T) {
	const title = "登录页改版"
	const body = "# 需求\n\n保留验证码登录\n"

	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": title, "requirement_md": body,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	created := decodeProjectDetail(t, rec)
	if created.Requirement == nil {
		t.Fatal("requirement = null, want the v1 summary")
	}
	if created.Status != 1 || created.StatusName != "requirement_review" || created.Version != 1 {
		t.Errorf("project = status %d (%s) version %d, want 1 (requirement_review) version 1",
			created.Status, created.StatusName, created.Version)
	}

	// AC4: no requirement card is dispatched for the new project.
	items := doJSON(t, h, http.MethodGet, "/v1/work-items?project_id="+created.ProjectID+"&kind=requirement", nil)
	if items.Code != http.StatusOK {
		t.Fatalf("list requirement work items: status = %d (body %s)", items.Code, items.Body.String())
	}
	var list workItemListResponse
	if err := json.Unmarshal(items.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode work items %q: %v", items.Body.String(), err)
	}
	if list.Items == nil || len(list.Items) != 0 {
		t.Errorf("requirement work items = %+v, want an empty array", list.Items)
	}
	var cards int64
	if err := st.Read().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM work_items WHERE kind = 'requirement'").Scan(&cards); err != nil {
		t.Fatalf("count requirement work items: %v", err)
	}
	if cards != 0 {
		t.Errorf("requirement work items in the store = %d, want 0", cards)
	}
	var all int64
	if err := st.Read().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM work_items").Scan(&all); err != nil {
		t.Fatalf("count work items: %v", err)
	}
	if all != 0 {
		t.Errorf("work items after create = %d, want 0", all)
	}

	// AC5: the requirement chain is exactly one version, titled and bodied the
	// way the request asked.
	chainRec := doJSON(t, h, http.MethodGet, "/v1/projects/materials?id="+created.ProjectID+"&kind=1", nil)
	if chainRec.Code != http.StatusOK {
		t.Fatalf("requirement chain: status = %d (body %s)", chainRec.Code, chainRec.Body.String())
	}
	chain := decodeChain(t, chainRec)
	if len(chain.Items) != 1 || chain.Items[0].Version != 1 {
		t.Fatalf("requirement chain = %+v, want exactly v1", chain.Items)
	}
	if chain.Items[0].MaterialID != created.Requirement.MaterialID || chain.Count != 1 {
		t.Errorf("chain head = %+v (count %d), want %s and count 1",
			chain.Items[0], chain.Count, created.Requirement.MaterialID)
	}
	if chain.Items[0].Title != title {
		t.Errorf("requirement title = %q, want %q", chain.Items[0].Title, title)
	}
	if chain.Items[0].PreviousMaterialID != nil {
		t.Errorf("previous_material_id = %v, want null for v1", *chain.Items[0].PreviousMaterialID)
	}
	download := doJSON(t, h, http.MethodGet, "/v1/materials?id="+created.Requirement.MaterialID, nil)
	if download.Code != http.StatusOK {
		t.Fatalf("download requirement: status = %d (body %s)", download.Code, download.Body.String())
	}
	if got := download.Body.String(); got != body {
		t.Errorf("requirement body = %q, want %q", got, body)
	}
	var materials int64
	if err := st.Read().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM materials").Scan(&materials); err != nil {
		t.Fatalf("count materials: %v", err)
	}
	if materials != 1 {
		t.Errorf("materials = %d, want 1 (only the first requirement version)", materials)
	}
}

func TestCreateProjectHTTPValidation(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	t.Run("unknown product", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
			"product_id": "NOPE", "title": "t", "requirement_md": "b",
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if body := decodeError(t, rec); body.Code != "not_found" {
			t.Errorf("code = %q, want not_found", body.Code)
		}
	})

	t.Run("missing requirement", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
			"product_id": "APP", "title": "无需求项目",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_argument" {
			t.Errorf("code = %q, want invalid_argument", body.Code)
		}
		requireFieldDetail(t, body, "requirement_md")
		if reason := fieldReason(body, "requirement_md"); reason != "is required" {
			t.Errorf("requirement_md reason = %q, want is required", reason)
		}
	})

	t.Run("blank requirement", func(t *testing.T) {
		for name, blank := range map[string]string{
			"spaces":   "   ",
			"newlines": "\n\n",
			"tabs":     "\t\t",
		} {
			t.Run(name, func(t *testing.T) {
				rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
					"product_id": "APP", "title": "空白需求", "requirement_md": blank,
				})
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
				}
				body := decodeError(t, rec)
				if body.Code != "invalid_argument" {
					t.Errorf("code = %q, want invalid_argument", body.Code)
				}
				requireFieldDetail(t, body, "requirement_md")
			})
		}
		// No request above may leave a project behind.
		var projects int64
		if err := st.Read().QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM projects").Scan(&projects); err != nil {
			t.Fatalf("count projects: %v", err)
		}
		if projects != 0 {
			t.Errorf("projects = %d, want 0", projects)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
			"product_id": "APP", "title": "t", "requirement_md": "b", "status": 3,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		requireFieldDetail(t, decodeError(t, rec), "status")
	})

	t.Run("invalid json", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPost, "/v1/projects", "{")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})
}

func TestGetProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	if rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "登录页改版", "requirement_md": httpRequirement,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}

	rec := doJSON(t, h, http.MethodGet, "/v1/projects?id=APP-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	detail := decodeProjectDetail(t, rec)
	if detail.Requirement == nil || detail.Requirement.SHA256 == "" || detail.Requirement.MaterialID == "" {
		t.Errorf("requirement = %+v, want metadata with sha256 and material_id", detail.Requirement)
	}

	missing := doJSON(t, h, http.MethodGet, "/v1/projects?id=APP-404", nil)
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing project: status = %d, want 404", missing.Code)
	}

	blank := doJSON(t, h, http.MethodGet, "/v1/projects?id=", nil)
	if blank.Code != http.StatusBadRequest {
		t.Errorf("blank id: status = %d, want 400", blank.Code)
	}
}

func TestListProjectsHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	seedProduct(t, st, "SHOP")

	for _, body := range []map[string]any{
		{"product_id": "APP", "title": "一", "requirement_md": "正文一"},
		{"product_id": "APP", "title": "二", "requirement_md": "正文二"},
		{"product_id": "APP", "title": "三", "requirement_md": "正文三"},
		{"product_id": "SHOP", "title": "别家", "requirement_md": "正文"},
	} {
		if rec := doJSON(t, h, http.MethodPost, "/v1/projects", body); rec.Code != http.StatusCreated {
			t.Fatalf("create %v: status = %d (body %s)", body["title"], rec.Code, rec.Body.String())
		}
	}

	t.Run("all", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodGet, "/v1/projects", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if page := decodeProjectList(t, rec); len(page.Items) != 4 {
			t.Errorf("items = %d, want 4", len(page.Items))
		}
	})

	t.Run("product filter", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodGet, "/v1/projects?product_id=APP", nil)
		page := decodeProjectList(t, rec)
		if len(page.Items) != 3 {
			t.Fatalf("items = %d, want 3", len(page.Items))
		}
		for _, item := range page.Items {
			if item.ProductID != "APP" {
				t.Errorf("product_id = %q, want APP", item.ProductID)
			}
		}
	})

	t.Run("status filter", func(t *testing.T) {
		// Created projects sit in requirement_review now; the filter is the
		// stored status, so 1 returns them and 0 returns none of them.
		rec := doJSON(t, h, http.MethodGet, "/v1/projects?product_id=APP&status=1", nil)
		if page := decodeProjectList(t, rec); len(page.Items) != 3 {
			t.Errorf("requirement_review items = %d, want 3", len(page.Items))
		}
		rec = doJSON(t, h, http.MethodGet, "/v1/projects?product_id=APP&status=0", nil)
		if page := decodeProjectList(t, rec); len(page.Items) != 0 {
			t.Errorf("draft items = %d, want 0", len(page.Items))
		}
		rec = doJSON(t, h, http.MethodGet, "/v1/projects?product_id=APP&status=4", nil)
		if page := decodeProjectList(t, rec); len(page.Items) != 0 {
			t.Errorf("developing items = %d, want 0", len(page.Items))
		}
	})

	t.Run("pagination", func(t *testing.T) {
		first := decodeProjectList(t, doJSON(t, h, http.MethodGet, "/v1/projects?limit=2", nil))
		if len(first.Items) != 2 || first.NextCursor == nil {
			t.Fatalf("first page = %d items, cursor=%v", len(first.Items), first.NextCursor)
		}
		second := decodeProjectList(t, doJSON(t, h, http.MethodGet, "/v1/projects?limit=2&cursor="+*first.NextCursor, nil))
		if len(second.Items) != 2 || second.NextCursor != nil {
			t.Errorf("second page = %d items, cursor=%v; want 2 items and no cursor", len(second.Items), second.NextCursor)
		}
		if first.Items[1].ID == second.Items[0].ID {
			t.Error("pages overlap")
		}
	})

	t.Run("invalid parameters", func(t *testing.T) {
		for _, target := range []string{
			"/v1/projects?limit=abc",
			"/v1/projects?limit=0",
			"/v1/projects?limit=201",
			"/v1/projects?status=9",
			"/v1/projects?status=x",
			"/v1/projects?cursor=not-base64!",
		} {
			rec := doJSON(t, h, http.MethodGet, target, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", target, rec.Code)
				continue
			}
			if body := decodeError(t, rec); body.Code != "invalid_argument" {
				t.Errorf("%s: code = %q, want invalid_argument", target, body.Code)
			}
		}
	})
}

func TestUpdateProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	if rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "旧标题", "requirement_md": "需求",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}

	rec := doJSON(t, h, http.MethodPatch, "/v1/projects?id=APP-1", map[string]any{"title": "新标题"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var project projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if project.Title != "新标题" || project.Version != 2 {
		t.Errorf("project = %+v, want 新标题 at version 2", project)
	}

	t.Run("status is not patchable", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPatch, "/v1/projects?id=APP-1", map[string]any{"status": 2})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		requireFieldDetail(t, decodeError(t, rec), "status")
	})

	t.Run("product_id is immutable", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPatch, "/v1/projects?id=APP-1", map[string]any{"product_id": "OTHER"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		requireFieldDetail(t, decodeError(t, rec), "product_id")
	})

	t.Run("unknown field", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPatch, "/v1/projects?id=APP-1", map[string]any{"requirement_md": "x"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		requireFieldDetail(t, decodeError(t, rec), "requirement_md")
	})

	t.Run("empty patch", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPatch, "/v1/projects?id=APP-1", map[string]any{})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing project", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPatch, "/v1/projects?id=APP-404", map[string]any{"title": "x"})
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

func TestDeleteProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	if rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "可删", "requirement_md": "需求",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}

	// The delete whitelist is unchanged (requirement_draft/cancelled only), so a
	// project created by mistake goes through cancel first.
	if rec := doJSON(t, h, http.MethodDelete, "/v1/projects?id=APP-1", nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete while in review: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, h, http.MethodPost, "/v1/projects/cancel?id=APP-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("cancel: status = %d (body %s)", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, h, http.MethodDelete, "/v1/projects?id=APP-1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty", body)
	}

	if rec := doJSON(t, h, http.MethodGet, "/v1/projects?id=APP-1", nil); rec.Code != http.StatusNotFound {
		t.Errorf("after delete: status = %d, want 404", rec.Code)
	}

	// The requirement materials survive the project: they are versions, not
	// children (no physical deletion).
	var materials int64
	if err := st.Read().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM materials").Scan(&materials); err != nil {
		t.Fatalf("count materials: %v", err)
	}
	if materials != 1 {
		t.Errorf("materials = %d, want 1 (materials outlive the project)", materials)
	}
}

func TestDeleteProjectHTTPConflict(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	if rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "有任务", "requirement_md": "需求",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}

	var projectID int64
	if err := st.Read().QueryRowContext(context.Background(),
		"SELECT id FROM projects WHERE project_id = 'APP-1'").Scan(&projectID); err != nil {
		t.Fatalf("lookup project: %v", err)
	}
	if _, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO work_items (work_id, project_id, seq_no, kind, title, body_md, state,
		                        max_attempts, version, create_time, update_time)
		VALUES ('w_APP1T0001', ?, 1, 'dev', '任务', '正文', 'blocked', 3, 1,
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		projectID); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	rec := doJSON(t, h, http.MethodDelete, "/v1/projects?id=APP-1", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if body := decodeError(t, rec); body.Code != "conflict" {
		t.Errorf("code = %q, want conflict", body.Code)
	}
}

func TestWriteRequirementHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	if rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "登录页改版", "requirement_md": "第一版",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}

	rec := doJSON(t, h, http.MethodPost, "/v1/projects/requirement?id=APP-1", map[string]any{
		"body_md": "第二版需求",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	detail := decodeProjectDetail(t, rec)
	if detail.Requirement == nil || detail.Requirement.Version != 2 {
		t.Fatalf("requirement = %+v, want version 2", detail.Requirement)
	}
	if detail.Requirement.MaterialID == "" {
		t.Error("requirement material_id is empty, want the new version's handle")
	}

	// Reading the project back returns the new version.
	got := decodeProjectDetail(t, doJSON(t, h, http.MethodGet, "/v1/projects?id=APP-1", nil))
	if got.Requirement == nil || got.Requirement.Version != 2 {
		t.Errorf("detail requirement = %+v, want version 2", got.Requirement)
	}
	if got.Requirement.MaterialID != detail.Requirement.MaterialID {
		t.Error("detail and write reported different chain heads")
	}

	t.Run("empty body", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPost, "/v1/projects/requirement?id=APP-1", map[string]any{"body_md": "  "})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		requireFieldDetail(t, decodeError(t, rec), "body_md")
	})

	t.Run("missing id", func(t *testing.T) {
		rec := doJSON(t, h, http.MethodPost, "/v1/projects/requirement", map[string]any{"body_md": "x"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("status does not allow it", func(t *testing.T) {
		if _, err := st.Write().ExecContext(context.Background(),
			"UPDATE projects SET status = 4 WHERE project_id = 'APP-1'"); err != nil {
			t.Fatalf("set status: %v", err)
		}
		rec := doJSON(t, h, http.MethodPost, "/v1/projects/requirement?id=APP-1", map[string]any{"body_md": "第三版"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if body := decodeError(t, rec); body.Code != "invalid_state" {
			t.Errorf("code = %q, want invalid_state", body.Code)
		}
	})
}

// TestProjectDetailOmitsBodies is the regression guard for the detail
// contract: neither the requirement nor the spec body may be serialised into
// the response, no matter how large the documents are. It also proves the
// bytes stay reachable through the content-addressed materials endpoint.
func TestProjectDetailOmitsBodies(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	// 200k CJK characters: ~600KB of body, far past the 2KB response bound.
	requirement := strings.Repeat("需", 200_000)
	spec := strings.Repeat("方", 200_000)

	if rec := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "长正文项目", "requirement_md": requirement,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create project: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id=APP-1",
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": "1"}); rec.Code != http.StatusOK {
		t.Fatalf("approve requirement: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/specs?id=APP-1",
		map[string]any{"body_md": spec}, map[string]string{HeaderActor: "agent:cto"}); rec.Code != http.StatusOK {
		t.Fatalf("submit spec: status = %d (body %s)", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, h, http.MethodGet, "/v1/projects?id=APP-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if size := rec.Body.Len(); size >= 2048 {
		t.Errorf("detail body = %d bytes, want < 2048", size)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	for _, key := range []string{"requirement_md", "spec_excerpt"} {
		if _, ok := raw[key]; ok {
			t.Errorf("detail response still carries %q", key)
		}
	}

	detail := decodeProjectDetail(t, rec)
	if detail.Requirement == nil || detail.Spec == nil {
		t.Fatalf("requirement/spec metadata = %+v/%+v, want both present", detail.Requirement, detail.Spec)
	}

	// The bodies remain downloadable in full from the materials endpoint.
	for name, materialID := range map[string]string{
		"requirement": detail.Requirement.MaterialID,
		"spec":        detail.Spec.MaterialID,
	} {
		got := doJSON(t, h, http.MethodGet, "/v1/materials?id="+materialID, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("%s material: status = %d (body %s)", name, got.Code, got.Body.String())
		}
		if got.Body.Len() != 200_000*3 {
			t.Errorf("%s material = %d bytes, want %d", name, got.Body.Len(), 200_000*3)
		}
	}
}

// forceProjectStatus puts a project into a state/version pair the HTTP API would
// take many requests to reach, so the completion endpoint can be driven from
// pending_done directly.
func forceProjectStatus(t *testing.T, st *store.Store, projectID string, status, version int) {
	t.Helper()
	if _, err := st.Write().ExecContext(context.Background(),
		`UPDATE projects SET status = ?, version = ? WHERE project_id = ?`,
		status, version, projectID); err != nil {
		t.Fatalf("force project %s to %d/v%d: %v", projectID, status, version, err)
	}
}

// completionGateCount counts the project-level gate=complete rows of one project.
func completionGateCount(t *testing.T, st *store.Store, projectID string) int64 {
	t.Helper()
	var n int64
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM gate_decisions WHERE subject_type = 'project' AND subject_id = ? AND gate = 'complete'`,
		projectID).Scan(&n); err != nil {
		t.Fatalf("count completion records: %v", err)
	}
	return n
}

// TestCompleteProjectHTTP covers the success path of POST
// /v1/projects/complete: 200 with status completed, version + 1, the human
// actor in last_actor, and one gate_decisions record sharing the project's
// update_time.
func TestCompleteProjectHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	created := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "登录页改版",
		"requirement_md": httpRequirement,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body %s)", created.Code, created.Body.String())
	}
	projectID := decodeProjectDetail(t, created).ProjectID
	forceProjectStatus(t, st, projectID, 6, 5)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
		map[string]string{"If-Match": "5", HeaderActor: "human:pm"})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	completed := decodeProjectFromResponse(t, rec)
	if completed.Status != 7 || completed.StatusName != "completed" {
		t.Errorf("status = %d (%s), want 7 (completed)", completed.Status, completed.StatusName)
	}
	if completed.Version != 6 {
		t.Errorf("version = %d, want 6 (one bump)", completed.Version)
	}
	if completed.LastActor == nil || *completed.LastActor != "human:pm" {
		t.Errorf("last_actor = %v, want human:pm", completed.LastActor)
	}

	// The read model agrees with the response, and the completion record shares
	// the project's update_time.
	detailRec := doJSON(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("get: status = %d", detailRec.Code)
	}
	detail := decodeProjectDetail(t, detailRec)
	if detail.Status != 7 || detail.Version != 6 {
		t.Errorf("persisted state = %d/v%d, want 7/v6", detail.Status, detail.Version)
	}

	historyRec := doJSON(t, h, http.MethodGet, "/v1/gate-decisions?project_id="+projectID, nil)
	if historyRec.Code != http.StatusOK {
		t.Fatalf("history: status = %d (body %s)", historyRec.Code, historyRec.Body.String())
	}
	var history gateDecisionListResponse
	if err := json.Unmarshal(historyRec.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(history.Items) != 1 {
		t.Fatalf("history items = %d, want 1 (only the completion)", len(history.Items))
	}
	row := history.Items[0]
	if row.SubjectType != "project" || row.SubjectID != projectID || row.Gate != "complete" {
		t.Errorf("record = %s/%s/%s, want project/%s/complete", row.SubjectType, row.SubjectID, row.Gate, projectID)
	}
	if row.Decision != "approve" || row.DecidedBy != "human:pm" {
		t.Errorf("record decision = %s/%s, want approve/human:pm", row.Decision, row.DecidedBy)
	}
	if row.MaterialID != nil || row.MaterialVersion != nil {
		t.Errorf("record material = %v/%v, want null/null", row.MaterialID, row.MaterialVersion)
	}
	if row.CreateTime != detail.UpdateTime {
		t.Errorf("record create_time = %q, project update_time = %q, want the same value", row.CreateTime, detail.UpdateTime)
	}

	// A replay is an invalid state and adds no second record.
	replay := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
		map[string]string{"If-Match": "6", HeaderActor: "human:pm"})
	if replay.Code != http.StatusConflict {
		t.Fatalf("replay: status = %d, want 409 (body %s)", replay.Code, replay.Body.String())
	}
	if body := decodeError(t, replay); body.Code != "invalid_state" {
		t.Errorf("replay code = %q, want invalid_state", body.Code)
	}
	if got := completionGateCount(t, st, projectID); got != 1 {
		t.Errorf("completion records after replay = %d, want 1", got)
	}
}

// TestCompleteProjectHTTPRejectsAgents pins the actor gate of the completion
// endpoint: an agent may not close a project.
func TestCompleteProjectHTTPRejectsAgents(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	created := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "门禁", "requirement_md": httpRequirement,
	})
	projectID := decodeProjectDetail(t, created).ProjectID
	forceProjectStatus(t, st, projectID, 6, 2)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
		map[string]string{"If-Match": "2", HeaderActor: "agent:echuan"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "agent_forbidden" {
		t.Errorf("code = %q, want agent_forbidden", body.Code)
	}
	if got := completionGateCount(t, st, projectID); got != 0 {
		t.Errorf("completion records = %d, want 0", got)
	}
}

// TestCompleteProjectHTTPUnsettledCards keeps the "still has open work" answer at
// the HTTP boundary: 409 invalid_state, the message carries the count, the
// project stays pending_done and no record is written.
func TestCompleteProjectHTTPUnsettledCards(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	created := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "未结清", "requirement_md": httpRequirement,
	})
	projectID := decodeProjectDetail(t, created).ProjectID
	forceProjectStatus(t, st, projectID, 6, 3)
	insertCard(t, st, physicalProjectID(t, st, projectID), projectID+"-T0001", "dev",
		store.WorkItemStateBlocked, 1, nil)
	insertCard(t, st, physicalProjectID(t, st, projectID), projectID+"-T0002", "dev",
		store.WorkItemStateQueued, 2, nil)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
		map[string]string{"If-Match": "3", HeaderActor: "human:pm"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", body.Code)
	}
	if !strings.Contains(body.Message, "2") {
		t.Errorf("message = %q, want the unsettled count", body.Message)
	}
	detail := decodeProjectDetail(t, doJSON(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil))
	if detail.Status != 6 || detail.Version != 3 {
		t.Errorf("state = %d/v%d, want 6/v3", detail.Status, detail.Version)
	}
	if got := completionGateCount(t, st, projectID); got != 0 {
		t.Errorf("completion records = %d, want 0", got)
	}
}

// TestCompleteProjectHTTPPreconditions walks the remaining error contract of the
// completion endpoint: version header (400), stale version (409), illegal state
// (409) and unknown project (404).
func TestCompleteProjectHTTPPreconditions(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")
	created := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "前置条件", "requirement_md": httpRequirement,
	})
	projectID := decodeProjectDetail(t, created).ProjectID
	forceProjectStatus(t, st, projectID, 6, 4)

	t.Run("missing If-Match", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
			map[string]string{HeaderActor: "human:pm"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeError(t, rec)
		if body.Code != "invalid_version" {
			t.Errorf("code = %q, want invalid_version", body.Code)
		}
		if detail := fieldReason(body, "If-Match"); detail == "" {
			t.Errorf("details = %+v, want a field entry for If-Match", body.Details)
		}
	})

	t.Run("malformed If-Match", func(t *testing.T) {
		for _, raw := range []string{"abc", "0", "-3"} {
			rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
				map[string]string{"If-Match": raw, HeaderActor: "human:pm"})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("If-Match %q: status = %d, want 400 (body %s)", raw, rec.Code, rec.Body.String())
			}
			if body := decodeError(t, rec); body.Code != "invalid_version" {
				t.Errorf("If-Match %q: code = %q, want invalid_version", raw, body.Code)
			}
		}
	})

	t.Run("stale version", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
			map[string]string{"If-Match": "3", HeaderActor: "human:pm"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "invalid_version" {
			t.Errorf("code = %q, want invalid_version", body.Code)
		}
	})

	t.Run("illegal state", func(t *testing.T) {
		for _, status := range []int{0, 1, 2, 3, 4, 5, 7, 8} {
			forceProjectStatus(t, st, projectID, status, 9)
			rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id="+projectID, nil,
				map[string]string{"If-Match": "9", HeaderActor: "human:pm"})
			if rec.Code != http.StatusConflict {
				t.Fatalf("status %d: rec = %d, want 409 (body %s)", status, rec.Code, rec.Body.String())
			}
			if body := decodeError(t, rec); body.Code != "invalid_state" {
				t.Errorf("status %d: code = %q, want invalid_state", status, body.Code)
			}
		}
	})

	t.Run("unknown project", func(t *testing.T) {
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/complete?id=APP-404", nil,
			map[string]string{"If-Match": "1", HeaderActor: "human:pm"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "not_found" {
			t.Errorf("code = %q, want not_found", body.Code)
		}
	})

	if got := completionGateCount(t, st, projectID); got != 0 {
		t.Errorf("completion records after every rejection = %d, want 0", got)
	}
}

// TestProjectResponseDerivedFields pins repo_url and the status-derived
// dev_branch on both the list and the detail response (same DTO).
func TestProjectResponseDerivedFields(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	// The product git_url is the repo_url the project response must echo.
	productRec := doJSON(t, h, http.MethodGet, "/v1/products?id=APP", nil)
	if productRec.Code != http.StatusOK {
		t.Fatalf("product: status = %d", productRec.Code)
	}
	var product struct {
		GitURL string `json:"git_url"`
	}
	if err := json.Unmarshal(productRec.Body.Bytes(), &product); err != nil {
		t.Fatalf("decode product: %v", err)
	}
	if product.GitURL == "" {
		t.Fatal("seeded product has no git_url")
	}

	created := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "分支与仓库", "requirement_md": httpRequirement,
	})
	projectID := decodeProjectDetail(t, created).ProjectID

	detail := func() projectResponse {
		t.Helper()
		rec := doJSON(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get: status = %d (body %s)", rec.Code, rec.Body.String())
		}
		return decodeProjectDetail(t, rec).projectResponse
	}
	list := func() projectResponse {
		t.Helper()
		rec := doJSON(t, h, http.MethodGet, "/v1/projects?product_id=APP", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: status = %d (body %s)", rec.Code, rec.Body.String())
		}
		page := decodeProjectList(t, rec)
		if len(page.Items) != 1 {
			t.Fatalf("list items = %d, want 1", len(page.Items))
		}
		return page.Items[0]
	}

	// 0–3: no branch yet, but the repository is already known.
	for _, status := range []int{0, 1, 2, 3} {
		forceProjectStatus(t, st, projectID, status, 1)
		for name, got := range map[string]projectResponse{"detail": detail(), "list": list()} {
			if got.Status != status {
				t.Fatalf("%s: status = %d, want %d", name, got.Status, status)
			}
			if got.DevBranch != nil {
				t.Errorf("%s status %d: dev_branch = %q, want null", name, status, *got.DevBranch)
			}
			if got.RepoURL == nil || *got.RepoURL != product.GitURL {
				t.Errorf("%s status %d: repo_url = %v, want %q", name, status, got.RepoURL, product.GitURL)
			}
		}
	}

	// 4–7: the recorded branch is returned.
	for _, status := range []int{4, 5, 6, 7} {
		forceProjectStatus(t, st, projectID, status, 1)
		for name, got := range map[string]projectResponse{"detail": detail(), "list": list()} {
			if got.DevBranch == nil || *got.DevBranch == "" {
				t.Errorf("%s status %d: dev_branch = %v, want the recorded branch", name, status, got.DevBranch)
			}
			if got.RepoURL == nil || *got.RepoURL != product.GitURL {
				t.Errorf("%s status %d: repo_url = %v, want %q", name, status, got.RepoURL, product.GitURL)
			}
		}
	}

	// 8 (cancelled): no branch is claimed.
	forceProjectStatus(t, st, projectID, 8, 1)
	for name, got := range map[string]projectResponse{"detail": detail(), "list": list()} {
		if got.DevBranch != nil {
			t.Errorf("%s status 8: dev_branch = %q, want null", name, *got.DevBranch)
		}
	}
}
