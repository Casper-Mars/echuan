package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/store"
)

// newProductTestServer builds the real middleware stack on top of a temporary
// migrated SQLite database, so the product tests are integration tests.
func newProductTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	return newProductTestServerWithLogger(t, testLogger(io.Discard))
}

func newProductTestServerWithLogger(t *testing.T, logger *slog.Logger) (http.Handler, *store.Store) {
	t.Helper()

	st, err := store.Open(store.Config{
		Path:         filepath.Join(t.TempDir(), "state.db"),
		ReadPoolSize: 2,
	})
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

	service := app.NewProductService(st)
	h := Chain(
		NewRouter(logger, nil, nil, service, nil, nil),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)
	return h, st
}

// doJSON issues a request; body is marshalled unless it is nil, a string (sent
// verbatim so invalid JSON can be exercised) or an io.Reader.
func doJSON(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(value)
	case io.Reader:
		reader = value
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderActor, "test-suite")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeProduct(t *testing.T, rec *httptest.ResponseRecorder) productResponse {
	t.Helper()
	var product productResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &product); err != nil {
		t.Fatalf("decode product %q: %v", rec.Body.String(), err)
	}
	return product
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error %q: %v", rec.Body.String(), err)
	}
	return body
}

// requireFieldDetail asserts the error body names the given field.
func requireFieldDetail(t *testing.T, body ErrorBody, field string) {
	t.Helper()
	for _, detail := range body.Details {
		if detail.Field == field {
			return
		}
	}
	t.Fatalf("details = %+v, want a detail for field %q", body.Details, field)
}

func validCreateBody(productID string) map[string]any {
	return map[string]any{
		"product_id":  productID,
		"name":        "示例产品",
		"git_url":     "git@github.com:Casper-Mars/app.git",
		"description": "示范产品",
		"license":     "MIT",
	}
}

func TestProductCRUDLifecycle(t *testing.T) {
	h, _ := newProductTestServer(t)

	// POST → 201 with the full object.
	rec := doJSON(t, h, http.MethodPost, "/v1/products", map[string]any{
		"product_id":  "APP",
		"name":        "  示例产品  ",
		"git_url":     "git@github.com:Casper-Mars/app.git",
		"description": "示范产品",
		"license":     "MIT",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(HeaderRequestID) == "" {
		t.Error("POST response is missing X-Request-Id")
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/products?id=APP" {
		t.Errorf("Location = %q", loc)
	}
	created := decodeProduct(t, rec)
	if created.ID != "APP" || created.ProductID != "APP" {
		t.Errorf("id/product_id = %q/%q, want APP", created.ID, created.ProductID)
	}
	if created.Name != "示例产品" {
		t.Errorf("name = %q, want trimmed name", created.Name)
	}
	if created.GitURL != "git@github.com:Casper-Mars/app.git" || created.Description != "示范产品" || created.License != "MIT" {
		t.Errorf("unexpected fields: %+v", created)
	}
	if created.ProjectCount != 0 {
		t.Errorf("project_count = %d, want 0", created.ProjectCount)
	}
	createTime, err := time.Parse(time.RFC3339, created.CreateTime)
	if err != nil {
		t.Fatalf("create_time %q is not RFC3339: %v", created.CreateTime, err)
	}
	if created.CreateTime != created.UpdateTime {
		t.Errorf("create_time = %q, update_time = %q, want equal on insert", created.CreateTime, created.UpdateTime)
	}

	// GET detail.
	rec = doJSON(t, h, http.MethodGet, "/v1/products?id=APP", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET detail status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if got := decodeProduct(t, rec); got.ProductID != "APP" {
		t.Errorf("GET detail product_id = %q", got.ProductID)
	}

	// PATCH bumps update_time and leaves create_time alone.
	rec = doJSON(t, h, http.MethodPatch, "/v1/products?id=APP", map[string]any{
		"name":        "改后的产品",
		"description": "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	patched := decodeProduct(t, rec)
	if patched.Name != "改后的产品" {
		t.Errorf("patched name = %q", patched.Name)
	}
	if patched.Description != "" {
		t.Errorf("description = %q, want cleared", patched.Description)
	}
	updateTime, err := time.Parse(time.RFC3339, patched.UpdateTime)
	if err != nil {
		t.Fatalf("update_time %q is not RFC3339: %v", patched.UpdateTime, err)
	}
	if !updateTime.After(createTime) {
		t.Errorf("update_time %v is not after create_time %v", updateTime, createTime)
	}
	if patched.CreateTime != created.CreateTime {
		t.Errorf("create_time changed: %q → %q", created.CreateTime, patched.CreateTime)
	}

	// List.
	rec = doJSON(t, h, http.MethodGet, "/v1/products", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var list productListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ProductID != "APP" || list.Items[0].ProjectCount != 0 {
		t.Errorf("list = %+v", list)
	}
	if list.NextCursor != nil {
		t.Errorf("next_cursor = %q, want omitted on last page", *list.NextCursor)
	}
	if raw := rec.Body.String(); strings.Contains(raw, "next_cursor") {
		t.Errorf("last page body = %s, want no next_cursor key", raw)
	}

	// DELETE → 204 with an empty body, then detail is gone.
	rec = doJSON(t, h, http.MethodDelete, "/v1/products?id=APP", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("DELETE body = %q, want empty", rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodGet, "/v1/products?id=APP", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want 404", rec.Code)
	}
	body := decodeError(t, rec)
	if body.Code != "not_found" || body.Details == nil || body.RequestID == "" {
		t.Errorf("error body = %+v", body)
	}
	if rec.Header().Get(HeaderRequestID) != body.RequestID {
		t.Errorf("header request id %q != body %q", rec.Header().Get(HeaderRequestID), body.RequestID)
	}
}

func TestProductCreateValidation(t *testing.T) {
	longName := strings.Repeat("名", 201)
	longURL := strings.Repeat("u", 501)
	longDescription := strings.Repeat("d", 2001)
	longLicense := strings.Repeat("l", 65)

	cases := []struct {
		name  string
		body  any
		field string
	}{
		{
			name:  "missing product_id",
			body:  map[string]any{"name": "n", "git_url": "g"},
			field: "product_id",
		},
		{
			name:  "invalid product_id leading digit",
			body:  validCreateBody("9APP"),
			field: "product_id",
		},
		{
			name:  "invalid product_id space",
			body:  validCreateBody("AP P"),
			field: "product_id",
		},
		{
			name:  "product_id too long",
			body:  validCreateBody("A" + strings.Repeat("b", 32)),
			field: "product_id",
		},
		{
			name:  "blank name",
			body:  map[string]any{"product_id": "APP", "name": "   ", "git_url": "g"},
			field: "name",
		},
		{
			name:  "name too long",
			body:  map[string]any{"product_id": "APP", "name": longName, "git_url": "g"},
			field: "name",
		},
		{
			name:  "missing git_url",
			body:  map[string]any{"product_id": "APP", "name": "n"},
			field: "git_url",
		},
		{
			name:  "git_url too long",
			body:  map[string]any{"product_id": "APP", "name": "n", "git_url": longURL},
			field: "git_url",
		},
		{
			name:  "description too long",
			body:  map[string]any{"product_id": "APP", "name": "n", "git_url": "g", "description": longDescription},
			field: "description",
		},
		{
			name:  "license too long",
			body:  map[string]any{"product_id": "APP", "name": "n", "git_url": "g", "license": longLicense},
			field: "license",
		},
		{
			name:  "unknown field",
			body:  map[string]any{"product_id": "APP", "name": "n", "git_url": "g", "unexpected": true},
			field: "unexpected",
		},
		{
			name:  "non-string field",
			body:  map[string]any{"product_id": "APP", "name": 42, "git_url": "g"},
			field: "name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newProductTestServer(t)
			rec := doJSON(t, h, http.MethodPost, "/v1/products", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != "invalid_argument" {
				t.Errorf("code = %q, want invalid_argument", body.Code)
			}
			requireFieldDetail(t, body, tc.field)
		})
	}

	t.Run("invalid JSON", func(t *testing.T) {
		h, _ := newProductTestServer(t)
		rec := doJSON(t, h, http.MethodPost, "/v1/products", `{"product_id":`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		body := decodeError(t, rec)
		requireFieldDetail(t, body, "body")
	})
}

func TestProductDuplicateConflict(t *testing.T) {
	h, _ := newProductTestServer(t)

	if rec := doJSON(t, h, http.MethodPost, "/v1/products", validCreateBody("APP")); rec.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	rec := doJSON(t, h, http.MethodPost, "/v1/products", validCreateBody("APP"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate POST status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "conflict" || !strings.Contains(body.Message, "already exists") {
		t.Errorf("body = %+v", body)
	}
	if body.Details == nil || len(body.Details) != 0 {
		t.Errorf("details = %v, want []", body.Details)
	}

	list := doJSON(t, h, http.MethodGet, "/v1/products", nil)
	var page productListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("items = %d, want exactly one product", len(page.Items))
	}
}

func TestProductDeleteProtectedByProjects(t *testing.T) {
	h, st := newProductTestServer(t)

	if rec := doJSON(t, h, http.MethodPost, "/v1/products", validCreateBody("APP")); rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var productRowID int64
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT id FROM products WHERE product_id = ?`, "APP").Scan(&productRowID); err != nil {
		t.Fatalf("lookup product id: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, version, create_time, update_time)
		VALUES ('PRJ-1', ?, 1, '示范项目', 0, 1, ?, ?)`,
		productRowID, now, now); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	rec := doJSON(t, h, http.MethodDelete, "/v1/products?id=APP", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	body := decodeError(t, rec)
	if body.Code != "conflict" || !strings.Contains(body.Message, "still has") {
		t.Errorf("body = %+v", body)
	}

	// The product and its project count survive the refused delete.
	rec = doJSON(t, h, http.MethodGet, "/v1/products?id=APP", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if product := decodeProduct(t, rec); product.ProjectCount != 1 {
		t.Errorf("project_count = %d, want 1", product.ProjectCount)
	}

	if _, err := st.Write().ExecContext(context.Background(),
		`DELETE FROM projects WHERE project_id = 'PRJ-1'`); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if rec := doJSON(t, h, http.MethodDelete, "/v1/products?id=APP", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE after project removal status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductPagination(t *testing.T) {
	h, _ := newProductTestServer(t)

	want := []string{"P1", "P2", "P3", "P4", "P5"}
	for _, id := range want {
		if rec := doJSON(t, h, http.MethodPost, "/v1/products", validCreateBody(id)); rec.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d; body = %s", id, rec.Code, rec.Body.String())
		}
	}

	var got []string
	cursor := ""
	for page := 0; page < 5; page++ {
		target := "/v1/products?limit=2"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		rec := doJSON(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d status = %d; body = %s", page+1, rec.Code, rec.Body.String())
		}
		var body productListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode page %d: %v", page+1, err)
		}
		for _, item := range body.Items {
			got = append(got, item.ProductID)
		}
		if body.NextCursor == nil {
			break
		}
		cursor = *body.NextCursor
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paged ids = %v, want %v (no duplicates, no gaps)", got, want)
	}

	// Boundary limits.
	for _, tc := range []struct {
		query string
		field string
	}{
		{query: "limit=0", field: "limit"},
		{query: "limit=201", field: "limit"},
		{query: "limit=abc", field: "limit"},
		{query: "cursor=not-base64!", field: "cursor"},
		{query: "cursor=" + base64.RawURLEncoding.EncodeToString([]byte("v1:1")), field: "cursor"},
	} {
		rec := doJSON(t, h, http.MethodGet, "/v1/products?"+tc.query, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400; body = %s", tc.query, rec.Code, rec.Body.String())
		}
		requireFieldDetail(t, decodeError(t, rec), tc.field)
	}

	// A cursor past the last row is valid and returns an empty last page.
	far := base64.RawURLEncoding.EncodeToString([]byte("id:999999"))
	rec := doJSON(t, h, http.MethodGet, "/v1/products?cursor="+far, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("far cursor status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var farPage productListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &farPage); err != nil {
		t.Fatalf("decode far page: %v", err)
	}
	if len(farPage.Items) != 0 || farPage.NextCursor != nil {
		t.Errorf("far page = %+v, want empty without next_cursor", farPage)
	}

	// limit=200 (the maximum) is accepted.
	if rec := doJSON(t, h, http.MethodGet, "/v1/products?limit=200", nil); rec.Code != http.StatusOK {
		t.Fatalf("limit=200 status = %d, want 200", rec.Code)
	}
}

func TestProductListDefaultLimit(t *testing.T) {
	h, _ := newProductTestServer(t)

	for i := 0; i < 55; i++ {
		body := validCreateBody(fmt.Sprintf("P%02d", i))
		if rec := doJSON(t, h, http.MethodPost, "/v1/products", body); rec.Code != http.StatusCreated {
			t.Fatalf("POST %d status = %d; body = %s", i, rec.Code, rec.Body.String())
		}
	}

	rec := doJSON(t, h, http.MethodGet, "/v1/products", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("default list status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var first productListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Items) != 50 {
		t.Errorf("default limit items = %d, want 50", len(first.Items))
	}
	if first.NextCursor == nil {
		t.Fatal("default list must expose next_cursor")
	}

	rec = doJSON(t, h, http.MethodGet, "/v1/products?cursor="+*first.NextCursor, nil)
	var second productListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(second.Items) != 5 || second.NextCursor != nil {
		t.Errorf("second page = %d items, next=%v; want 5 and no cursor", len(second.Items), second.NextCursor)
	}
}

func TestProductPatchRules(t *testing.T) {
	h, _ := newProductTestServer(t)
	if rec := doJSON(t, h, http.MethodPost, "/v1/products", validCreateBody("APP")); rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d; body = %s", rec.Code, rec.Body.String())
	}

	cases := []struct {
		name  string
		path  string
		body  any
		field string
	}{
		{name: "immutable product_id", path: "/v1/products?id=APP", body: map[string]any{"product_id": "NEW"}, field: "product_id"},
		{name: "unknown field", path: "/v1/products?id=APP", body: map[string]any{"unknown": 1}, field: "unknown"},
		{name: "empty patch", path: "/v1/products?id=APP", body: map[string]any{}, field: "body"},
		{name: "blank name", path: "/v1/products?id=APP", body: map[string]any{"name": "  "}, field: "name"},
		{name: "license too long", path: "/v1/products?id=APP", body: map[string]any{"license": strings.Repeat("l", 65)}, field: "license"},
		{name: "missing id", path: "/v1/products", body: map[string]any{"name": "x"}, field: "id"},
		{name: "invalid JSON", path: "/v1/products?id=APP", body: `{"name":`, field: "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, h, http.MethodPatch, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != "invalid_argument" {
				t.Errorf("code = %q", body.Code)
			}
			requireFieldDetail(t, body, tc.field)
		})
	}

	if rec := doJSON(t, h, http.MethodPatch, "/v1/products?id=NOPE", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}

	// Clearing an optional field is allowed and stored as empty.
	rec := doJSON(t, h, http.MethodPatch, "/v1/products?id=APP", map[string]any{"license": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear license status = %d; body = %s", rec.Code, rec.Body.String())
	}
	if product := decodeProduct(t, rec); product.License != "" {
		t.Errorf("license = %q, want cleared", product.License)
	}
}

func TestProductConcurrentCreateIsSerialisedByUnique(t *testing.T) {
	h, _ := newProductTestServer(t)

	const workers = 20
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		created  int
		conflict int
		other    []int
	)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			rec := doJSON(t, h, http.MethodPost, "/v1/products", map[string]any{
				"product_id": "RACE",
				"name":       fmt.Sprintf("race-%d", i),
				"git_url":    "git@example.com:race/app.git",
			})
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				conflict++
			default:
				other = append(other, rec.Code)
			}
		}(i)
	}
	wg.Wait()

	if created != 1 || conflict != workers-1 || len(other) != 0 {
		t.Fatalf("created=%d conflict=%d other=%v, want 1/%d/[]", created, conflict, workers-1, other)
	}
}

func TestProductAccessLogIncludesSubject(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)
	h, _ := newProductTestServerWithLogger(t, logger)

	if rec := doJSON(t, h, http.MethodPost, "/v1/products", validCreateBody("APP")); rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d; body = %s", rec.Code, rec.Body.String())
	}

	var entry map[string]any
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var candidate map[string]any
		if err := json.Unmarshal([]byte(line), &candidate); err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		if candidate["msg"] == "request" && candidate["path"] == "/v1/products" {
			entry = candidate
			found = true
		}
	}
	if !found {
		t.Fatalf("no access log for POST /v1/products in %q", buf.String())
	}
	if entry["subject"] != "APP" {
		t.Errorf("subject = %v, want APP; entry = %v", entry["subject"], entry)
	}
	if entry["actor"] != "test-suite" {
		t.Errorf("actor = %v, want test-suite", entry["actor"])
	}
}
