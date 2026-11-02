package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// packageEntries opens the response body as a zip and returns name → body.
func packageEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	entries := make(map[string]string, len(zr.File))
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		entries[file.Name] = string(body)
	}
	return entries
}

func TestDownloadProjectPackageHTTP(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n\n登录页正文\n")
	acceptHTTPProject(t, env, "APP-1")

	if rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/specs", map[string]any{
		"project_id": "APP-1", "title": "接口设计", "body_md": "# 接口设计\n\n正文 A\n",
	}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create spec status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := doJSONHeaders(t, env.handler, http.MethodPost, "/v1/specs", map[string]any{
		"project_id": "APP-1", "title": "前端改造", "body_md": "# 前端改造\n\n正文 B\n",
	}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create spec status = %d (body %s)", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/package?id=APP-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %q, want %d", got, rec.Body.Len())
	}

	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment; ") {
		t.Errorf("Content-Disposition = %q, want an attachment", disposition)
	}
	if !strings.Contains(disposition, `filename="APP-1-package.zip"`) {
		t.Errorf("ASCII fallback missing: %q", disposition)
	}
	_, encoded, ok := strings.Cut(disposition, "filename*=UTF-8''")
	if !ok {
		t.Fatalf("Content-Disposition = %q, want filename*", disposition)
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil || decoded != "APP-1-资料包.zip" {
		t.Errorf("decoded filename* = %q (%v), want APP-1-资料包.zip", decoded, err)
	}

	entries := packageEntries(t, rec.Body.Bytes())
	want := map[string]string{
		"需求-v1-登录页改版.md":      "# 需求\n\n登录页正文\n",
		"方案-S0001-v1-接口设计.md": "# 接口设计\n\n正文 A\n",
		"方案-S0002-v1-前端改造.md": "# 前端改造\n\n正文 B\n",
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want %d files", entries, len(want))
	}
	for name, body := range want {
		if entries[name] != body {
			t.Errorf("entry %q = %q, want %q", name, entries[name], body)
		}
	}
}

func TestDownloadProjectPackageHeadAndEmpty(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")

	// HEAD carries the same headers and no body.
	rec := doJSON(t, env.handler, http.MethodHead, "/v1/projects/package?id=APP-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body has %d bytes, want 0", rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("HEAD Content-Type = %q, want application/zip", got)
	}
	if got := rec.Header().Get("Content-Length"); got == "" || got == "0" {
		t.Errorf("HEAD Content-Length = %q, want the zip size", got)
	}

	// A project with no requirement and no specs yields a valid empty zip.
	rec = doJSON(t, env.handler, http.MethodPost, "/v1/projects", map[string]any{
		"product_id": "APP", "title": "空项目",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create empty project status = %d (body %s)", rec.Code, rec.Body.String())
	}
	emptyID := decodeProjectDetail(t, rec).ProjectID
	rec = doJSON(t, env.handler, http.MethodGet, "/v1/projects/package?id="+emptyID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty package status = %d, want 200", rec.Code)
	}
	if entries := packageEntries(t, rec.Body.Bytes()); len(entries) != 0 {
		t.Errorf("empty package entries = %v, want none", entries)
	}
}

func TestDownloadProjectPackageErrors(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/package", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id status = %d, want 400", rec.Code)
	}
	if body := decodeError(t, rec); body.Code != "invalid_argument" {
		t.Errorf("missing id code = %q, want invalid_argument", body.Code)
	}

	rec = doJSON(t, env.handler, http.MethodGet, "/v1/projects/package?id=APP-404", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project status = %d, want 404", rec.Code)
	}
	if body := decodeError(t, rec); body.Code != "not_found" {
		t.Errorf("unknown project code = %q, want not_found", body.Code)
	}
}
