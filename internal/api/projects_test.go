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
	tasks := app.NewTaskService(st)
	if maxAttempts > 0 {
		tasks.SetMaxAttempts(maxAttempts)
	}
	h := Chain(
		NewRouter(logger, nil, nil, app.NewProductService(st), projects,
			tasks, app.NewClaimService(st, logger, 90*time.Second, filepath.Join(dir, "workspaces")),
			app.NewMaterialService(st, writer)),
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
	if detail.Status != 0 || detail.StatusName != "draft" {
		t.Errorf("status = %d (%s), want 0 (draft)", detail.Status, detail.StatusName)
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
	if detail.RequirementMD != httpRequirement {
		t.Errorf("requirement_md = %q, want the request body", detail.RequirementMD)
	}
	if detail.Spec != nil {
		t.Errorf("spec = %+v, want null", *detail.Spec)
	}
	if detail.TaskStats.Total != 0 || detail.TaskStats.Done != 0 {
		t.Errorf("task_stats = %+v, want zeros", detail.TaskStats)
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
			"product_id": "APP", "title": "t",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		requireFieldDetail(t, decodeError(t, rec), "requirement_md")
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
	if detail.RequirementMD != httpRequirement {
		t.Errorf("requirement_md = %q, want the stored body", detail.RequirementMD)
	}
	if detail.Requirement == nil || detail.Requirement.SHA256 == "" {
		t.Errorf("requirement = %+v, want a summary with sha256", detail.Requirement)
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
		rec := doJSON(t, h, http.MethodGet, "/v1/projects?product_id=APP&status=0", nil)
		if page := decodeProjectList(t, rec); len(page.Items) != 3 {
			t.Errorf("draft items = %d, want 3", len(page.Items))
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
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
		VALUES ('APP-1-T0001', ?, 1, '任务', '正文', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
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
	if detail.RequirementMD != "第二版需求" {
		t.Errorf("requirement_md = %q, want the new body", detail.RequirementMD)
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
