package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"

	_ "modernc.org/sqlite"
)

// materialServerEnv is the real middleware stack plus the data directory, so a
// test can assert on the object store and on leftover scratch files.
type materialServerEnv struct {
	handler  http.Handler
	store    *store.Store
	dataDir  string
	maxBytes int64
}

func newMaterialServerEnv(t *testing.T, maxBytes int64, logWriter io.Writer) *materialServerEnv {
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

	logger := testLogger(logWriter)
	h := Chain(
		NewRouter(logger, nil, nil,
			app.NewProductService(st),
			app.NewProjectService(st, writer),
			app.NewTaskService(st),
			nil,
			app.NewMaterialService(st, writer)),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)
	return &materialServerEnv{handler: h, store: st, dataDir: dataDir, maxBytes: maxBytes}
}

func newMaterialTestServer(t *testing.T, maxBytes int64) *materialServerEnv {
	t.Helper()
	return newMaterialServerEnv(t, maxBytes, io.Discard)
}

// createProjectWithRequirement runs POST /v1/projects and returns the detail.
func createProjectWithRequirement(t *testing.T, env *materialServerEnv, requirement string) projectDetailResponse {
	t.Helper()

	seedProduct(t, env.store, "APP")
	rec := doJSON(t, env.handler, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "登录页改版",
		"requirement_md": requirement,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	return decodeProjectDetail(t, rec)
}

func countRows(t *testing.T, st *store.Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := st.Read().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func dirEntryCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return len(entries)
}

func countObjectFiles(t *testing.T, dataDir string) int {
	t.Helper()
	var count int
	err := filepath.WalkDir(filepath.Join(dataDir, "objects"), func(_ string, d os.DirEntry, err error) error {
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

func requirementPointer(t *testing.T, st *store.Store, projectID string) *int64 {
	t.Helper()
	var pointer *int64
	if err := st.Read().QueryRowContext(context.Background(),
		"SELECT requirement_material_id FROM projects WHERE project_id = ?", projectID).Scan(&pointer); err != nil {
		t.Fatalf("read requirement pointer of %s: %v", projectID, err)
	}
	return pointer
}

func decodeChain(t *testing.T, rec *httptest.ResponseRecorder) materialChainResponse {
	t.Helper()
	var chain materialChainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &chain); err != nil {
		t.Fatalf("decode chain %q: %v", rec.Body.String(), err)
	}
	return chain
}

func TestDownloadMaterialHTTP(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	const body = "# 需求：登录页改版\n\n- 中文与 emoji 🚀 原样返回\n- trailing spaces matter  \n"
	detail := createProjectWithRequirement(t, env, body)
	materialID := detail.Requirement.MaterialID

	sum := sha256.Sum256([]byte(body))
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/materials?id="+materialID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != body {
		t.Errorf("body = %q, want %q", got, body)
	}
	headers := rec.Header()
	if got := headers.Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got, want := headers.Get("Content-Length"), fmt.Sprint(len(body)); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got := headers.Get("ETag"); got != wantETag {
		t.Errorf("ETag = %q, want %q", got, wantETag)
	}
	if got := headers.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := headers.Get("X-Material-Id"); got != materialID {
		t.Errorf("X-Material-Id = %q, want %q", got, materialID)
	}
	if got := headers.Get("X-Material-Version"); got != "1" {
		t.Errorf("X-Material-Version = %q, want 1", got)
	}
	if got := headers.Get("Content-Disposition"); !strings.HasPrefix(got, "inline;") {
		t.Errorf("Content-Disposition = %q, want inline", got)
	}
}

func TestDownloadMaterialHEAD(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	const body = "# 只有头\n"
	detail := createProjectWithRequirement(t, env, body)
	materialID := detail.Requirement.MaterialID

	rec := doJSON(t, env.handler, http.MethodHead, "/v1/materials?id="+materialID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
	if got, want := rec.Header().Get("Content-Length"), fmt.Sprint(len(body)); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Error("ETag missing on HEAD")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestDownloadMaterialErrors(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")

	cases := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantCode   string
	}{
		{"missing id", http.MethodGet, "/v1/materials", http.StatusBadRequest, "invalid_argument"},
		{"unknown id", http.MethodGet, "/v1/materials?id=NOPE", http.StatusNotFound, "not_found"},
		{"post", http.MethodPost, "/v1/materials?id=NOPE", http.StatusMethodNotAllowed, "method_not_allowed"},
		{"delete", http.MethodDelete, "/v1/materials?id=NOPE", http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, env.handler, tc.method, tc.target, nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
			}
			if body.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Code, tc.wantCode)
			}
			if tc.wantStatus == http.StatusMethodNotAllowed {
				if got := rec.Header().Get("Allow"); !strings.Contains(got, "GET") {
					t.Errorf("Allow = %q, want GET", got)
				}
			}
		})
	}
}

func TestProjectMaterialsChainHTTP(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	first := createProjectWithRequirement(t, env, "# 第一版\n")

	for _, body := range []string{"# 第二版\n", "# 第三版\n"} {
		rec := doJSON(t, env.handler, http.MethodPost, "/v1/projects/requirement?id=APP-1", map[string]any{
			"body_md": body,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("write requirement status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
	}

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	chain := decodeChain(t, rec)
	if chain.Count != 3 || len(chain.Items) != 3 {
		t.Fatalf("count = %d, items = %d, want 3", chain.Count, len(chain.Items))
	}
	if chain.Items[0].MaterialID != first.Requirement.MaterialID {
		t.Errorf("items[0] = %q, want %q", chain.Items[0].MaterialID, first.Requirement.MaterialID)
	}
	if chain.Items[0].PreviousMaterialID != nil {
		t.Errorf("items[0].previous = %v, want null", *chain.Items[0].PreviousMaterialID)
	}
	for i := 1; i < len(chain.Items); i++ {
		if chain.Items[i].Version != i+1 {
			t.Errorf("items[%d].version = %d, want %d", i, chain.Items[i].Version, i+1)
		}
		previous := chain.Items[i-1].MaterialID
		if chain.Items[i].PreviousMaterialID == nil || *chain.Items[i].PreviousMaterialID != previous {
			t.Errorf("items[%d].previous = %v, want %q", i, chain.Items[i].PreviousMaterialID, previous)
		}
	}
	if chain.CurrentMaterialID == nil || *chain.CurrentMaterialID != chain.Items[2].MaterialID {
		t.Errorf("current = %v, want %q", chain.CurrentMaterialID, chain.Items[2].MaterialID)
	}
	for _, item := range chain.Items {
		if item.Kind != 1 {
			t.Errorf("kind = %d, want 1", item.Kind)
		}
		if item.SHA256 == "" || item.Size == 0 || item.Title == "" || item.CreateTime == "" {
			t.Errorf("incomplete item: %+v", item)
		}
	}
}

func TestProjectMaterialsChainEmptyAndErrors(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	createProjectWithRequirement(t, env, "# 需求\n")

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"count":0`) {
		t.Errorf("body = %s, want empty items and count 0", body)
	}
	if strings.Contains(rec.Body.String(), "current_material_id") {
		t.Errorf("empty chain must omit current_material_id: %s", rec.Body.String())
	}

	bad := []string{
		"/v1/projects/materials?id=APP-1",
		"/v1/projects/materials?id=APP-1&kind=",
		"/v1/projects/materials?id=APP-1&kind=3",
		"/v1/projects/materials?id=APP-1&kind=abc",
		"/v1/projects/materials?kind=1",
	}
	for _, target := range bad {
		rec := doJSON(t, env.handler, http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", target, rec.Code, rec.Body.String())
		}
	}

	rec = doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=NOPE-1&kind=1", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing project: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, env.handler, http.MethodPost, "/v1/projects/materials?id=APP-1&kind=1", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("post chain: status = %d, want 405", rec.Code)
	}
}

func TestMaterialDeduplicatesContentAcrossVersions(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	const body = "# 同样的内容\n"
	detail := createProjectWithRequirement(t, env, body)

	rec := doJSON(t, env.handler, http.MethodPost, "/v1/projects/requirement?id=APP-1", map[string]any{
		"body_md": body,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	if got := countObjectFiles(t, env.dataDir); got != 1 {
		t.Errorf("object files = %d, want 1", got)
	}
	if got := countRows(t, env.store, "SELECT COUNT(*) FROM materials"); got != 2 {
		t.Errorf("materials rows = %d, want 2", got)
	}

	chain := decodeChain(t, doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=1", nil))
	if len(chain.Items) != 2 {
		t.Fatalf("chain length = %d, want 2", len(chain.Items))
	}
	if chain.Items[0].MaterialID != detail.Requirement.MaterialID ||
		chain.Items[0].Version != 1 || chain.Items[1].Version != 2 {
		t.Errorf("chain = %+v, want v1 then v2 of the same object", chain.Items)
	}
	if chain.Items[0].SHA256 != chain.Items[1].SHA256 {
		t.Errorf("sha256 differs between versions: %q vs %q", chain.Items[0].SHA256, chain.Items[1].SHA256)
	}
}

// TestMaterialTooLargeBusinessPath covers the writer-side limit: the JSON body
// fits the HTTP cap, but the markdown exceeds material_max_bytes.
func TestMaterialTooLargeBusinessPath(t *testing.T) {
	var logBuf bytes.Buffer
	env := newMaterialServerEnv(t, 1024, &logBuf)
	createProjectWithRequirement(t, env, "# 小需求\n")

	rowsBefore := countRows(t, env.store, "SELECT COUNT(*) FROM materials")
	pointerBefore := requirementPointer(t, env.store, "APP-1")

	rec := doJSON(t, env.handler, http.MethodPost, "/v1/projects/requirement?id=APP-1", map[string]any{
		"body_md": strings.Repeat("a", 2048),
	})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Code != "too_large" {
		t.Errorf("code = %q, want too_large", body.Code)
	}
	if len(body.Details) == 0 || body.Details[0].Field != "body_md" {
		t.Errorf("details = %+v, want field body_md", body.Details)
	}

	if got := countRows(t, env.store, "SELECT COUNT(*) FROM materials"); got != rowsBefore {
		t.Errorf("materials rows = %d, want %d (no new version)", got, rowsBefore)
	}
	if got := dirEntryCount(t, filepath.Join(env.dataDir, "tmp")); got != 0 {
		t.Errorf("tmp leftovers = %d, want 0", got)
	}
	pointerAfter := requirementPointer(t, env.store, "APP-1")
	if pointerBefore == nil || pointerAfter == nil || *pointerBefore != *pointerAfter {
		t.Errorf("requirement pointer moved: %v -> %v", pointerBefore, pointerAfter)
	}

	// The 413 must be logged as a warning carrying the size that was seen.
	// The writer reads limit+1 bytes before it aborts.
	logs := logBuf.String()
	if !strings.Contains(logs, `"level":"WARN"`) || !strings.Contains(logs, `"msg":"request body too large"`) {
		t.Errorf("no warn log for the oversized body: %s", logs)
	}
	if !strings.Contains(logs, `"actual_bytes":1025`) {
		t.Errorf("log does not carry the actual size: %s", logs)
	}

	// The same limit applies at creation: an oversized first requirement must
	// not leave a project row behind either.
	projectsBefore := countRows(t, env.store, "SELECT COUNT(*) FROM projects")
	rec = doJSON(t, env.handler, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "超大需求",
		"requirement_md": strings.Repeat("b", 2048),
	})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("create project status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if got := countRows(t, env.store, "SELECT COUNT(*) FROM projects"); got != projectsBefore {
		t.Errorf("projects rows = %d, want %d (create must roll back)", got, projectsBefore)
	}
	if got := countRows(t, env.store, "SELECT COUNT(*) FROM materials"); got != rowsBefore {
		t.Errorf("materials rows after failed create = %d, want %d", got, rowsBefore)
	}
	if got := dirEntryCount(t, filepath.Join(env.dataDir, "tmp")); got != 0 {
		t.Errorf("tmp leftovers after failed create = %d, want 0", got)
	}
}

// TestMaterialTooLargeHTTPPath covers the reader-side limit: a body far above
// the cap must be cut off without buffering it or reaching the database.
func TestMaterialTooLargeHTTPPath(t *testing.T) {
	env := newMaterialTestServer(t, 1<<20)
	seedProduct(t, env.store, "APP")

	rec := doJSON(t, env.handler, http.MethodPost, "/v1/projects", hugeJSONBody(30<<20))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %.200s)", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %.200q: %v", rec.Body.String(), err)
	}
	if body.Code != "too_large" {
		t.Errorf("code = %q, want too_large", body.Code)
	}
	if got := countRows(t, env.store, "SELECT COUNT(*) FROM projects"); got != 0 {
		t.Errorf("projects rows = %d, want 0", got)
	}
	if got := countRows(t, env.store, "SELECT COUNT(*) FROM materials"); got != 0 {
		t.Errorf("materials rows = %d, want 0", got)
	}
	if got := dirEntryCount(t, filepath.Join(env.dataDir, "tmp")); got != 0 {
		t.Errorf("tmp leftovers = %d, want 0", got)
	}
}

// hugeJSONBody streams a syntactically valid create-project body of about size
// bytes without allocating it, so the test measures the server limit rather
// than the test process memory.
func hugeJSONBody(size int) io.Reader {
	prefix := `{"product_id":"APP","title":"大文件","requirement_md":"`
	suffix := `"}`
	chunk := strings.Repeat("a", 1<<20)

	readers := []io.Reader{strings.NewReader(prefix)}
	remaining := size - len(prefix) - len(suffix)
	for remaining > 0 {
		n := remaining
		if n > len(chunk) {
			n = len(chunk)
		}
		readers = append(readers, strings.NewReader(chunk[:n]))
		remaining -= n
	}
	readers = append(readers, strings.NewReader(suffix))
	return io.MultiReader(readers...)
}

// TestProjectMaterialsChainBrokenData covers the 500 path: a chain link that
// points at a row which does not exist is data corruption, not a 404.
func TestProjectMaterialsChainBrokenData(t *testing.T) {
	var logBuf bytes.Buffer
	env := newMaterialServerEnv(t, 1<<20, &logBuf)
	createProjectWithRequirement(t, env, "# 需求\n")

	db, err := sql.Open(store.DriverName, env.store.Path()+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("open corrupting connection: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE materials SET previous_material_id = 999999 WHERE version = 1`); err != nil {
		t.Fatalf("corrupt chain: %v", err)
	}

	rec := doJSON(t, env.handler, http.MethodGet, "/v1/projects/materials?id=APP-1&kind=1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %s)", rec.Code, rec.Body.String())
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Code != "internal" {
		t.Errorf("code = %q, want internal", body.Code)
	}
	if !strings.Contains(logBuf.String(), "material chain") {
		t.Errorf("log does not mention the broken chain: %s", logBuf.String())
	}
}
