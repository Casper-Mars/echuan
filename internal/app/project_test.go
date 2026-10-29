package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// projectTestEnv wires a real store, a real material writer and the project
// service on a temporary directory, so the use cases run against the same code
// path as production.
type projectTestEnv struct {
	dataDir string
	store   *store.Store
	writer  *material.Writer
	service *ProjectService
}

func newProjectTestEnv(t *testing.T) *projectTestEnv {
	t.Helper()
	return newProjectTestEnvWithLimit(t, 1<<20)
}

// newProjectTestEnvWithLimit builds the same environment with an explicit
// material_max_bytes, so the 413 path (Task-05, reused by Task-07) can be
// driven without building a megabyte body.
func newProjectTestEnvWithLimit(t *testing.T, maxBytes int64) *projectTestEnv {
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

	dataDir := filepath.Join(dir, "data")
	writer, err := material.NewWriter(dataDir, maxBytes)
	if err != nil {
		t.Fatalf("material.NewWriter: %v", err)
	}

	return &projectTestEnv{
		dataDir: dataDir,
		store:   st,
		writer:  writer,
		service: NewProjectService(st, writer),
	}
}

func (e *projectTestEnv) createProduct(t *testing.T, productID string) {
	t.Helper()
	_, err := NewProductService(e.store).CreateProduct(context.Background(), CreateProductInput{
		ProductID: productID,
		Name:      productID + " 产品",
		GitURL:    "git@example.com:" + productID + ".git",
	})
	if err != nil {
		t.Fatalf("CreateProduct(%s): %v", productID, err)
	}
}

func (e *projectTestEnv) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := e.store.Read().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (e *projectTestEnv) physicalProjectID(t *testing.T, projectID string) int64 {
	t.Helper()
	var id int64
	if err := e.store.Read().QueryRowContext(context.Background(),
		"SELECT id FROM projects WHERE project_id = ?", projectID).Scan(&id); err != nil {
		t.Fatalf("lookup physical id of %s: %v", projectID, err)
	}
	return id
}

// insertTask attaches one task row so the delete guard can be exercised before
// Task-06 ships the task endpoints.
func (e *projectTestEnv) insertTask(t *testing.T, projectID string, seq int) {
	t.Helper()
	id := e.physicalProjectID(t, projectID)
	_, err := e.store.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
		VALUES (?, ?, ?, '任务', '正文', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		projectID+"-T000"+string(rune('0'+seq)), id, seq)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

func countObjectFiles(t *testing.T, dataDir string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(filepath.Join(dataDir, "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects: %v", err)
	}
	return count
}

const sampleRequirement = "# 需求：登录页改版\n\n- 支持手机号登录\n- 保留验证码登录 🚀\n"

func TestCreateProjectStoresRequirement(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")

	detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "APP",
		Title:         "登录页改版",
		RequirementMD: sampleRequirement,
		UploadedBy:    "human:liu",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if detail.Project.ProjectID != "APP-1" || detail.Project.SeqNo != 1 {
		t.Errorf("project = %s (seq %d), want APP-1 (seq 1)", detail.Project.ProjectID, detail.Project.SeqNo)
	}
	if detail.Project.Status != domain.StatusRequirementDraft {
		t.Errorf("status = %d, want %d (requirement_draft)", detail.Project.Status, domain.StatusRequirementDraft)
	}
	if detail.Project.Version != 1 {
		t.Errorf("version = %d, want 1", detail.Project.Version)
	}
	if detail.Project.RequirementMaterialID == 0 {
		t.Error("requirement_material_id was not backfilled")
	}

	if detail.Requirement == nil {
		t.Fatal("detail.Requirement = nil, want the requirement summary")
	}
	if detail.Requirement.Version != 1 || detail.Requirement.Kind != domain.MaterialKindRequirement {
		t.Errorf("requirement = %+v, want kind 1 version 1", *detail.Requirement)
	}
	if detail.Requirement.UploadedBy != "human:liu" {
		t.Errorf("uploaded_by = %q, want human:liu", detail.Requirement.UploadedBy)
	}
	if detail.Requirement.MaterialID == "" {
		t.Error("requirement material_id is empty, want the loading handle")
	}

	// sha256 of the record must match both the body and the object on disk.
	sum := sha256.Sum256([]byte(sampleRequirement))
	wantSum := hex.EncodeToString(sum[:])
	if detail.Requirement.SHA256 != wantSum {
		t.Errorf("sha256 = %s, want %s", detail.Requirement.SHA256, wantSum)
	}
	if detail.Requirement.Size != int64(len(sampleRequirement)) {
		t.Errorf("size = %d, want %d", detail.Requirement.Size, len(sampleRequirement))
	}
	onDisk, err := os.ReadFile(filepath.Join(env.dataDir, "objects", wantSum[:2], wantSum))
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if string(onDisk) != sampleRequirement {
		t.Errorf("object content = %q, want the requirement body", onDisk)
	}

	// The pointer on the row matches the material the detail reports.
	var pointer int64
	if err := env.store.Read().QueryRowContext(context.Background(),
		"SELECT requirement_material_id FROM projects WHERE project_id = 'APP-1'").Scan(&pointer); err != nil {
		t.Fatalf("read pointer: %v", err)
	}
	if pointer != detail.Project.RequirementMaterialID {
		t.Errorf("requirement_material_id = %d, want %d", pointer, detail.Project.RequirementMaterialID)
	}
}

func TestCreateProjectNumbering(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	env.createProduct(t, "SHOP")

	for i, want := range []string{"APP-1", "APP-2", "APP-3"} {
		detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
			ProductID:     "APP",
			Title:         "需求 " + string(rune('A'+i)),
			RequirementMD: "正文 " + want,
		})
		if err != nil {
			t.Fatalf("CreateProject #%d: %v", i+1, err)
		}
		if detail.Project.ProjectID != want {
			t.Errorf("project_id = %s, want %s", detail.Project.ProjectID, want)
		}
	}

	// A different product starts its own sequence.
	detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "SHOP",
		Title:         "另一个产品的需求",
		RequirementMD: "正文",
	})
	if err != nil {
		t.Fatalf("CreateProject(SHOP): %v", err)
	}
	if detail.Project.ProjectID != "SHOP-1" {
		t.Errorf("project_id = %s, want SHOP-1", detail.Project.ProjectID)
	}
}

func TestWriteRequirementAppendsVersion(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()

	first, err := env.service.CreateProject(ctx, CreateProjectInput{
		ProductID: "APP", Title: "登录页改版", RequirementMD: "第一版需求",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	firstMaterialID := first.Requirement.MaterialID
	firstPhysicalID := first.Project.RequirementMaterialID

	second, err := env.service.WriteRequirement(ctx, "APP-1", WriteRequirementInput{
		BodyMD: "第二版需求", UploadedBy: "human:liu",
	})
	if err != nil {
		t.Fatalf("WriteRequirement: %v", err)
	}

	if second.Requirement.Version != 2 {
		t.Errorf("new version = %d, want 2", second.Requirement.Version)
	}
	if second.Requirement.MaterialID == firstMaterialID {
		t.Error("material_id did not change")
	}
	if second.Project.RequirementMaterialID == firstPhysicalID {
		t.Error("requirement_material_id did not move")
	}
	if second.Project.Version != 2 {
		t.Errorf("project version = %d, want 2", second.Project.Version)
	}

	// The new row points at the old one, and the old file is still readable.
	old, err := store.GetMaterialByMaterialID(ctx, env.store.Read(), firstMaterialID)
	if err != nil {
		t.Fatalf("GetMaterialByMaterialID(v1): %v", err)
	}
	fresh, err := store.GetMaterialByID(ctx, env.store.Read(), second.Project.RequirementMaterialID)
	if err != nil {
		t.Fatalf("GetMaterialByID(v2): %v", err)
	}
	if fresh.PreviousMaterialID == nil || *fresh.PreviousMaterialID != old.ID {
		t.Errorf("previous_material_id = %v, want %d", fresh.PreviousMaterialID, old.ID)
	}
	body, err := env.writer.Read(old)
	if err != nil {
		t.Fatalf("Read(v1): %v", err)
	}
	if string(body) != "第一版需求" {
		t.Errorf("v1 body = %q, want 第一版需求", body)
	}
	newBody, err := env.writer.Read(fresh)
	if err != nil {
		t.Fatalf("Read(v2): %v", err)
	}
	if string(newBody) != "第二版需求" {
		t.Errorf("v2 body = %q, want 第二版需求", newBody)
	}
}

func TestCreateProjectDeduplicatesContent(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()
	body := "完全相同的一段需求正文"

	first, err := env.service.CreateProject(ctx, CreateProjectInput{ProductID: "APP", Title: "一", RequirementMD: body})
	if err != nil {
		t.Fatalf("CreateProject #1: %v", err)
	}
	second, err := env.service.CreateProject(ctx, CreateProjectInput{ProductID: "APP", Title: "二", RequirementMD: body})
	if err != nil {
		t.Fatalf("CreateProject #2: %v", err)
	}

	if first.Requirement.SHA256 != second.Requirement.SHA256 {
		t.Fatal("sha256 differs for identical content")
	}
	if got := countObjectFiles(t, env.dataDir); got != 1 {
		t.Errorf("object files = %d, want 1", got)
	}
	if got := env.count(t, "SELECT COUNT(*) FROM materials"); got != 2 {
		t.Errorf("materials rows = %d, want 2", got)
	}
}

func TestWriteRequirementRejectsInvalidState(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()

	if _, err := env.service.CreateProject(ctx, CreateProjectInput{
		ProductID: "APP", Title: "改版", RequirementMD: "需求",
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Simulate the project having moved to developing (Task-06 owns the
	// transitions; the requirement endpoint only reads the status).
	if _, err := env.store.Write().ExecContext(ctx,
		"UPDATE projects SET status = 4 WHERE project_id = 'APP-1'"); err != nil {
		t.Fatalf("set status: %v", err)
	}

	_, err := env.service.WriteRequirement(ctx, "APP-1", WriteRequirementInput{BodyMD: "再改一版"})
	var invalidState *domain.InvalidStateError
	if !errors.As(err, &invalidState) {
		t.Fatalf("err = %v, want InvalidStateError", err)
	}
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Errorf("errors.Is(err, ErrInvalidState) = false")
	}

	// The rejected write left no material behind.
	if got := env.count(t, "SELECT COUNT(*) FROM materials"); got != 1 {
		t.Errorf("materials rows = %d, want 1", got)
	}
	if got := countObjectFiles(t, env.dataDir); got != 1 {
		t.Errorf("object files = %d, want 1", got)
	}
}

func TestDeleteProjectBranches(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()

	mustCreate := func(title string) domain.ProjectDetail {
		t.Helper()
		detail, err := env.service.CreateProject(ctx, CreateProjectInput{
			ProductID: "APP", Title: title, RequirementMD: "需求：" + title,
		})
		if err != nil {
			t.Fatalf("CreateProject(%s): %v", title, err)
		}
		return detail
	}
	wantConflict := func(t *testing.T, err error) {
		t.Helper()
		var conflict *domain.ConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("err = %v, want ConflictError", err)
		}
	}

	t.Run("draft without children", func(t *testing.T) {
		detail := mustCreate("可删")
		if err := env.service.DeleteProject(ctx, detail.Project.ProjectID); err != nil {
			t.Fatalf("DeleteProject: %v", err)
		}
		if _, err := env.service.GetProject(ctx, detail.Project.ProjectID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("GetProject after delete: err = %v, want not found", err)
		}
	})

	t.Run("with tasks", func(t *testing.T) {
		detail := mustCreate("有任务")
		env.insertTask(t, detail.Project.ProjectID, 1)
		wantConflict(t, env.service.DeleteProject(ctx, detail.Project.ProjectID))
	})

	t.Run("with spec", func(t *testing.T) {
		detail := mustCreate("有方案")
		if _, err := env.store.Write().ExecContext(ctx,
			"UPDATE projects SET spec_material_id = ? WHERE id = ?",
			detail.Project.RequirementMaterialID, env.physicalProjectID(t, detail.Project.ProjectID)); err != nil {
			t.Fatalf("set spec: %v", err)
		}
		wantConflict(t, env.service.DeleteProject(ctx, detail.Project.ProjectID))
	})

	t.Run("status does not allow it", func(t *testing.T) {
		detail := mustCreate("开发中")
		if _, err := env.store.Write().ExecContext(ctx,
			"UPDATE projects SET status = 4 WHERE id = ?", env.physicalProjectID(t, detail.Project.ProjectID)); err != nil {
			t.Fatalf("set status: %v", err)
		}
		wantConflict(t, env.service.DeleteProject(ctx, detail.Project.ProjectID))
	})

	t.Run("cancelled without children", func(t *testing.T) {
		detail := mustCreate("已取消")
		if _, err := env.store.Write().ExecContext(ctx,
			"UPDATE projects SET status = 8 WHERE id = ?", env.physicalProjectID(t, detail.Project.ProjectID)); err != nil {
			t.Fatalf("set status: %v", err)
		}
		if err := env.service.DeleteProject(ctx, detail.Project.ProjectID); err != nil {
			t.Fatalf("DeleteProject(cancelled): %v", err)
		}
	})

	t.Run("missing project", func(t *testing.T) {
		if err := env.service.DeleteProject(ctx, "APP-404"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v, want not found", err)
		}
	})
}

// TestCreateProjectRollsBackWhenMaterialWriteFails breaks the scratch
// directory so the project row is inserted and the material write then fails:
// the transaction must leave no project row behind.
func TestCreateProjectRollsBackWhenMaterialWriteFails(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")

	tmpPath := filepath.Join(env.dataDir, "tmp")
	if err := os.RemoveAll(tmpPath); err != nil {
		t.Fatalf("remove tmp dir: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("block tmp dir: %v", err)
	}

	if _, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID: "APP", Title: "写盘失败", RequirementMD: "需求",
	}); err == nil {
		t.Fatal("CreateProject succeeded, want a material write failure")
	}

	if got := env.count(t, "SELECT COUNT(*) FROM projects"); got != 0 {
		t.Errorf("projects = %d, want 0 (no half-built project)", got)
	}
	if got := env.count(t, "SELECT COUNT(*) FROM materials"); got != 0 {
		t.Errorf("materials = %d, want 0", got)
	}
	if got := countObjectFiles(t, env.dataDir); got != 0 {
		t.Errorf("object files = %d, want 0", got)
	}
}

// TestCreateProjectCleansUpObjectWhenBackfillFails injects a failure after the
// material was written: the materials row rolls back, and the object file this
// request created must be removed too.
func TestCreateProjectCleansUpObjectWhenBackfillFails(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()

	if _, err := env.store.Write().ExecContext(ctx, `
		CREATE TRIGGER fail_backfill BEFORE UPDATE ON projects
		BEGIN SELECT RAISE(ABORT, 'injected backfill failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if _, err := env.service.CreateProject(ctx, CreateProjectInput{
		ProductID: "APP", Title: "回填失败", RequirementMD: "需求正文",
	}); err == nil {
		t.Fatal("CreateProject succeeded, want an injected failure")
	}

	if got := env.count(t, "SELECT COUNT(*) FROM projects"); got != 0 {
		t.Errorf("projects = %d, want 0", got)
	}
	if got := env.count(t, "SELECT COUNT(*) FROM materials"); got != 0 {
		t.Errorf("materials = %d, want 0 (no orphan row)", got)
	}
	if got := countObjectFiles(t, env.dataDir); got != 0 {
		t.Errorf("object files = %d, want 0 (the object created by this request must be removed)", got)
	}
}

func TestCreateProjectRequiresExistingProduct(t *testing.T) {
	env := newProjectTestEnv(t)
	_, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID: "NOPE", Title: "标题", RequirementMD: "需求",
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestListProjectsFiltersAndPagination(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	env.createProduct(t, "SHOP")
	ctx := context.Background()

	for _, title := range []string{"一", "二", "三"} {
		if _, err := env.service.CreateProject(ctx, CreateProjectInput{
			ProductID: "APP", Title: title, RequirementMD: "正文" + title,
		}); err != nil {
			t.Fatalf("CreateProject(%s): %v", title, err)
		}
	}
	if _, err := env.service.CreateProject(ctx, CreateProjectInput{
		ProductID: "SHOP", Title: "别家", RequirementMD: "正文",
	}); err != nil {
		t.Fatalf("CreateProject(SHOP): %v", err)
	}

	all, err := env.service.ListProjects(ctx, "", nil, 0, 2)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(all.Items) != 2 || all.NextID == 0 {
		t.Fatalf("page = %d items, next=%d; want 2 items and a cursor", len(all.Items), all.NextID)
	}
	// Newest first: the SHOP project was created last, so it heads the list.
	if all.Items[0].Title != "别家" || all.Items[1].Title != "三" {
		t.Errorf("page 1 titles = %q, %q; want 别家, 三", all.Items[0].Title, all.Items[1].Title)
	}
	if all.Items[0].ID <= all.Items[1].ID {
		t.Errorf("page 1 ids not descending: %d, %d", all.Items[0].ID, all.Items[1].ID)
	}

	next, err := env.service.ListProjects(ctx, "", nil, all.NextID, 2)
	if err != nil {
		t.Fatalf("ListProjects(page 2): %v", err)
	}
	if len(next.Items) != 2 || next.NextID != 0 {
		t.Errorf("page 2 = %d items, next=%d; want 2 items and no cursor", len(next.Items), next.NextID)
	}
	// Page 2 continues strictly older than page 1.
	for _, item := range next.Items {
		if item.ID >= all.Items[len(all.Items)-1].ID {
			t.Errorf("page 2 item id %d not older than page 1 tail %d", item.ID, all.Items[len(all.Items)-1].ID)
		}
	}

	app, err := env.service.ListProjects(ctx, "APP", nil, 0, 50)
	if err != nil {
		t.Fatalf("ListProjects(APP): %v", err)
	}
	if len(app.Items) != 3 {
		t.Errorf("APP projects = %d, want 3", len(app.Items))
	}
	for i := 1; i < len(app.Items); i++ {
		if app.Items[i-1].ID <= app.Items[i].ID {
			t.Errorf("APP projects not newest first at %d: %d, %d", i, app.Items[i-1].ID, app.Items[i].ID)
		}
	}

	draft := domain.StatusRequirementDraft
	drafts, err := env.service.ListProjects(ctx, "APP", &draft, 0, 50)
	if err != nil {
		t.Fatalf("ListProjects(status=0): %v", err)
	}
	if len(drafts.Items) != 3 {
		t.Errorf("draft projects = %d, want 3", len(drafts.Items))
	}

	developing := domain.StatusTasksDraft
	none, err := env.service.ListProjects(ctx, "APP", &developing, 0, 50)
	if err != nil {
		t.Fatalf("ListProjects(status=4): %v", err)
	}
	if len(none.Items) != 0 {
		t.Errorf("tasks_draft projects = %d, want 0", len(none.Items))
	}
}

// TestListProjectsPagesNewestFirst walks the whole list page by page: every page
// continues strictly older than the previous one, pages cover every project
// exactly once, and the cursor chain terminates.
func TestListProjectsPagesNewestFirst(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()

	const total = 25
	for i := 0; i < total; i++ {
		if _, err := env.service.CreateProject(ctx, CreateProjectInput{
			ProductID: "APP", Title: fmt.Sprintf("项目-%02d", i), RequirementMD: "正文",
		}); err != nil {
			t.Fatalf("CreateProject(%d): %v", i, err)
		}
	}

	var ids []int64
	var afterID int64
	pages := 0
	for {
		page, err := env.service.ListProjects(ctx, "APP", nil, afterID, 5)
		if err != nil {
			t.Fatalf("ListProjects(page %d): %v", pages+1, err)
		}
		pages++
		if len(page.Items) == 0 {
			break
		}
		for _, item := range page.Items {
			if len(ids) > 0 && item.ID >= ids[len(ids)-1] {
				t.Fatalf("ids not strictly descending: %d then %d", ids[len(ids)-1], item.ID)
			}
			ids = append(ids, item.ID)
		}
		if page.NextID == 0 {
			break
		}
		afterID = page.NextID
	}

	if len(ids) != total {
		t.Errorf("walked %d projects, want %d", len(ids), total)
	}
	if pages != total/5 {
		t.Errorf("pages = %d, want %d", pages, total/5)
	}
}

func TestListProjectsRejectsBadParameters(t *testing.T) {
	env := newProjectTestEnv(t)
	ctx := context.Background()

	bad := func(t *testing.T, status *int, limit int) {
		t.Helper()
		var validation *domain.ValidationError
		if _, err := env.service.ListProjects(ctx, "", status, 0, limit); !errors.As(err, &validation) {
			t.Fatalf("err = %v, want ValidationError", err)
		}
	}
	bad(t, nil, 0)
	bad(t, nil, 201)
	bad(t, intPtr(9), 10)
	bad(t, intPtr(-1), 10)

	var validation *domain.ValidationError
	if _, err := env.service.ListProjects(ctx, "", nil, -1, 10); !errors.As(err, &validation) {
		t.Errorf("negative cursor: err = %v, want ValidationError", err)
	}
}

func TestUpdateProjectOnlyChangesTitle(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	ctx := context.Background()

	detail, err := env.service.CreateProject(ctx, CreateProjectInput{
		ProductID: "APP", Title: "旧标题", RequirementMD: "需求",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	title := "  新标题  "
	updated, err := env.service.UpdateProject(ctx, detail.Project.ProjectID, UpdateProjectInput{Title: &title})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if updated.Title != "新标题" {
		t.Errorf("title = %q, want trimmed 新标题", updated.Title)
	}
	if updated.Version != 2 {
		t.Errorf("version = %d, want 2", updated.Version)
	}
	if !updated.UpdateTime.After(detail.Project.UpdateTime) {
		t.Errorf("update_time did not advance: %s <= %s", updated.UpdateTime, detail.Project.UpdateTime)
	}
	if updated.CreateTime != detail.Project.CreateTime {
		t.Error("create_time changed")
	}

	status := domain.StatusSpecDraft
	var validation *domain.ValidationError
	if _, err := env.service.UpdateProject(ctx, detail.Project.ProjectID, UpdateProjectInput{Status: &status}); !errors.As(err, &validation) {
		t.Errorf("status patch: err = %v, want ValidationError", err)
	}
	productID := "OTHER"
	if _, err := env.service.UpdateProject(ctx, detail.Project.ProjectID, UpdateProjectInput{ProductID: &productID}); !errors.As(err, &validation) {
		t.Errorf("product_id patch: err = %v, want ValidationError", err)
	}
	if _, err := env.service.UpdateProject(ctx, detail.Project.ProjectID, UpdateProjectInput{}); !errors.As(err, &validation) {
		t.Errorf("empty patch: err = %v, want ValidationError", err)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	env := newProjectTestEnv(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		in    CreateProjectInput
		field string
	}{
		{name: "missing product", in: CreateProjectInput{Title: "t", RequirementMD: "b"}, field: "product_id"},
		{name: "missing title", in: CreateProjectInput{ProductID: "APP", RequirementMD: "b"}, field: "title"},
		{name: "blank title", in: CreateProjectInput{ProductID: "APP", Title: "  ", RequirementMD: "b"}, field: "title"},
		{name: "long title", in: CreateProjectInput{ProductID: "APP", Title: strings.Repeat("标", 201), RequirementMD: "b"}, field: "title"},
		{name: "missing body", in: CreateProjectInput{ProductID: "APP", Title: "t", RequirementMD: "  \n"}, field: "requirement_md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.service.CreateProject(ctx, tc.in)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			found := false
			for _, field := range validation.Fields {
				if field.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Errorf("details = %+v, want a detail for %q", validation.Fields, tc.field)
			}
		})
	}
}

func TestGetProjectMissing(t *testing.T) {
	env := newProjectTestEnv(t)
	if _, err := env.service.GetProject(context.Background(), "APP-404"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func intPtr(v int) *int { return &v }
