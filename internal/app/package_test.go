package app

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// readPackageEntries opens the zip and returns file name → body.
func readPackageEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	entries := make(map[string]string, len(zr.File))
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}
		body, err := io.ReadAll(rc)
		if cerr := rc.Close(); cerr != nil {
			t.Errorf("close zip entry %s: %v", file.Name, cerr)
		}
		if err != nil {
			t.Fatalf("read zip entry %s: %v", file.Name, err)
		}
		entries[file.Name] = string(body)
	}
	return entries
}

// saveIndependentSpec writes one independent spec document and returns the
// submission.
func saveIndependentSpec(t *testing.T, env *projectTestEnv, projectID, title, body string) SpecSubmission {
	t.Helper()
	result, _, err := env.service.SaveSpec(context.Background(), projectID, "", "agent:cto", &title, body, 0, false)
	if err != nil {
		t.Fatalf("SaveSpec(%s, %s): %v", projectID, title, err)
	}
	return result
}

func TestProjectPackageContents(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "登录页改版")

	if got := saveIndependentSpec(t, env, projectID, "接口设计", "# 接口设计\n\n第一份正文\n"); got.Spec.Title != "接口设计" {
		t.Fatalf("first spec title = %q", got.Spec.Title)
	}
	saveIndependentSpec(t, env, projectID, "前端改造", "# 前端改造\n\n第二份正文\n")

	pkg, err := env.service.ProjectPackage(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ProjectPackage: %v", err)
	}
	if pkg.Name != projectID+"-资料包.zip" {
		t.Errorf("package name = %q, want %q", pkg.Name, projectID+"-资料包.zip")
	}
	if pkg.Entries != 3 {
		t.Errorf("entries = %d, want 3", pkg.Entries)
	}

	entries := readPackageEntries(t, pkg.Data)
	want := map[string]string{
		"需求-v1-登录页改版.md":      "# 需求\n\n正文\n",
		"方案-S0001-v1-接口设计.md": "# 接口设计\n\n第一份正文\n",
		"方案-S0002-v1-前端改造.md": "# 前端改造\n\n第二份正文\n",
	}
	if len(entries) != len(want) {
		t.Fatalf("zip entries = %d (%v), want %d", len(entries), entries, len(want))
	}
	for name, body := range want {
		got, ok := entries[name]
		if !ok {
			t.Errorf("zip is missing %q; has %v", name, keysOf(entries))
			continue
		}
		if got != body {
			t.Errorf("entry %q body = %q, want %q", name, got, body)
		}
	}
}

// keysOf keeps failure messages readable without depending on map order.
func keysOf(entries map[string]string) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	return names
}

func TestProjectPackageWithoutSpecs(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "仅需求项目")

	pkg, err := env.service.ProjectPackage(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ProjectPackage: %v", err)
	}
	if pkg.Entries != 1 {
		t.Errorf("entries = %d, want 1 (requirement only)", pkg.Entries)
	}
	entries := readPackageEntries(t, pkg.Data)
	if len(entries) != 1 {
		t.Fatalf("zip entries = %v, want only the requirement", keysOf(entries))
	}
	if _, ok := entries["需求-v1-仅需求项目.md"]; !ok {
		t.Errorf("zip names = %v, want 需求-v1-仅需求项目.md", keysOf(entries))
	}
}

func TestProjectPackageWithoutRequirement(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "APP",
		Title:         "无需求项目",
		RequirementMD: "# 需求\n\n稍后删除\n",
		UploadedBy:    "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	projectID := detail.Project.ProjectID
	if _, err := env.service.SubmitProject(context.Background(), projectID, "pm"); err != nil {
		t.Fatalf("SubmitProject: %v", err)
	}
	version := env.projectState(t, projectID).Version
	if _, err := env.service.Decide(context.Background(), DecideInput{
		Kind: DecideKindProject, ID: projectID, Decision: domain.DecisionApprove,
		ExpectVersion: &version, Actor: "human:pm",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	saveIndependentSpec(t, env, projectID, "接口设计", "# 接口设计\n")

	// Simulate a project whose requirement pointer was cleared: the package
	// must still work and contain only the spec documents.
	if err := env.store.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`UPDATE projects SET requirement_material_id = NULL WHERE project_id = ?`, projectID)
		return err
	}); err != nil {
		t.Fatalf("clear requirement pointer: %v", err)
	}

	pkg, err := env.service.ProjectPackage(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ProjectPackage: %v", err)
	}
	if pkg.Entries != 1 {
		t.Errorf("entries = %d, want 1 (spec only)", pkg.Entries)
	}
	entries := readPackageEntries(t, pkg.Data)
	if _, ok := entries["方案-S0001-v1-接口设计.md"]; !ok {
		t.Errorf("zip names = %v, want 方案-S0001-v1-接口设计.md", keysOf(entries))
	}
}

func TestProjectPackageEmpty(t *testing.T) {
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:  "APP",
		Title:      "空项目",
		UploadedBy: "pm",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	pkg, err := env.service.ProjectPackage(context.Background(), detail.Project.ProjectID)
	if err != nil {
		t.Fatalf("ProjectPackage: %v", err)
	}
	if pkg.Entries != 0 {
		t.Errorf("entries = %d, want 0", pkg.Entries)
	}
	if entries := readPackageEntries(t, pkg.Data); len(entries) != 0 {
		t.Errorf("zip entries = %v, want none", keysOf(entries))
	}
}

func TestProjectPackageLookupErrors(t *testing.T) {
	env := newProjectTestEnv(t)

	if _, err := env.service.ProjectPackage(context.Background(), "  "); err == nil {
		t.Fatal("empty id: want a validation error")
	} else {
		var verr *domain.ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("empty id error = %T, want *domain.ValidationError", err)
		}
	}

	if _, err := env.service.ProjectPackage(context.Background(), "APP-404"); err == nil {
		t.Fatal("unknown project: want a not-found error")
	} else {
		var nerr *domain.NotFoundError
		if !errors.As(err, &nerr) {
			t.Errorf("unknown project error = %T, want *domain.NotFoundError", err)
		}
	}
}

func TestZipEntryNameSanitization(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		fallback string
		want     string
	}{
		{"slash becomes dash", "方案-S0001-v1-a/b", "方案-S0001", "方案-S0001-v1-a-b.md"},
		{"control and reserved", "需求-v1-a\x01b<c>d|e", "需求", "需求-v1-a-b-c-d-e.md"},
		{"leading and trailing dots", "需求-v1-..标题.. ", "需求", "需求-v1-..标题.md"},
		{"only dots and spaces falls back", " . . ", "需求", "需求.md"},
		{"chinese preserved", "需求-v2-登录页改版", "需求", "需求-v2-登录页改版.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := zipEntryName(tc.raw, tc.fallback); got != tc.want {
				t.Errorf("zipEntryName(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	long := strings.Repeat("中", 100)
	got := zipEntryName("需求-v1-"+long, "需求")
	if len(got) > maxPackageEntryBytes+len(".md") {
		t.Errorf("entry name is %d bytes, want at most %d", len(got), maxPackageEntryBytes+len(".md"))
	}
	if !strings.HasSuffix(got, ".md") {
		t.Errorf("long entry name = %q, want .md suffix", got)
	}
	if strings.Contains(got, "\xef\xbf\xbd") {
		t.Errorf("long entry name contains a replacement character: %q", got)
	}
}

// TestSaveSpecPointerNamesSubmittedDocument pins the post-Task-06 contract:
// every submission stores exactly one material, the project spec pointer names
// it, and no aggregated bundle material is created.
func TestSaveSpecPointerNamesSubmittedDocument(t *testing.T) {
	env := newProjectTestEnv(t)
	projectID := env.newSpecReviewProject(t, "多方案项目")

	before := env.count(t, `SELECT COUNT(*) FROM materials`)
	first := saveIndependentSpec(t, env, projectID, "接口设计", "# 接口\n\nv1\n")
	second := saveIndependentSpec(t, env, projectID, "前端改造", "# 前端\n\nv1\n")
	after := env.count(t, `SELECT COUNT(*) FROM materials`)

	if added := after - before; added != 2 {
		t.Errorf("materials added = %d, want 2 (one per submission)", added)
	}
	if got := env.specPointer(t, projectID); got != second.Spec.MaterialID {
		t.Errorf("spec pointer = %q, want the last submitted material %q", got, second.Spec.MaterialID)
	}
	if first.Spec.MaterialID == second.Spec.MaterialID {
		t.Error("both submissions share one material id")
	}
	if bundles := env.count(t, `SELECT COUNT(*) FROM materials WHERE title LIKE '%技术方案包'`); bundles != 0 {
		t.Errorf("bundle materials = %d, want 0", bundles)
	}

	// The legacy entry point revises the first document and moves the pointer
	// to that revision, still without a bundle.
	legacy := mustSubmitSpec(t, env, projectID, nil, "# 接口\n\nv2 旧客户端\n")
	if got := env.specPointer(t, projectID); got != legacy.Spec.MaterialID {
		t.Errorf("pointer after legacy submit = %q, want %q", got, legacy.Spec.MaterialID)
	}
	if added := env.count(t, `SELECT COUNT(*) FROM materials`) - after; added != 1 {
		t.Errorf("materials added by legacy submit = %d, want 1", added)
	}
	if bundles := env.count(t, `SELECT COUNT(*) FROM materials WHERE title LIKE '%技术方案包'`); bundles != 0 {
		t.Errorf("bundle materials after legacy submit = %d, want 0", bundles)
	}
}
