package web

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/api"
)

// indexHTML returns the embedded index.html so tests can compare bodies
// against the real fallback payload instead of guessing at its content.
func indexHTML(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	return string(b)
}

// findAsset returns the name (relative to dist) of the first embedded file with
// the given extension, so the tests do not hard-code build hashes.
func findAsset(t *testing.T, ext string) string {
	t.Helper()
	var found string
	err := fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return err
		}
		if strings.HasSuffix(p, ext) {
			found = p
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
	if found == "" {
		t.Fatalf("no embedded asset with extension %q", ext)
	}
	return found
}

func TestHandlerServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if got, want := rec.Body.String(), indexHTML(t); got != want {
		t.Errorf("body does not match index.html (len %d vs %d)", len(got), len(want))
	}
}

// TestHandlerServesExistingAssets checks that files present in the bundle are
// still served with a sensible Content-Type and are never mistaken for the SPA.
func TestHandlerServesExistingAssets(t *testing.T) {
	cases := []struct {
		ext      string
		wantType string
	}{
		{ext: ".js", wantType: "javascript"},
		{ext: ".css", wantType: "text/css"},
	}
	for _, c := range cases {
		name := findAsset(t, c.ext)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET /%s status = %d, want 200", name, rec.Code)
			continue
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.Contains(ct, c.wantType) {
			t.Errorf("GET /%s Content-Type = %q, want it to contain %q", name, ct, c.wantType)
		}
		if got := rec.Body.String(); got == indexHTML(t) || got == "" {
			t.Errorf("GET /%s served the SPA body instead of the asset", name)
		}
	}
}

// TestHandlerFallbackToIndex covers client-side deep links: extension-less
// paths outside assets/ keep returning index.html.
func TestHandlerFallbackToIndex(t *testing.T) {
	for _, p := range []string{"/", "/products", "/tasks/42", "/products/42/edit"} {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200 fallback", p, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", p, ct)
		}
		if got, want := rec.Body.String(), indexHTML(t); got != want {
			t.Errorf("GET %s fallback did not serve index.html", p)
		}
		// "/" is served straight from the bundle by the file server, so it
		// keeps its historical headers; only deep-link fallbacks are marked
		// no-cache.
		if p != "/" {
			if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
				t.Errorf("GET %s Cache-Control = %q, want no-cache", p, cc)
			}
		}
	}
}

// TestHandlerMissingStaticReturns404 is the D1 regression test: a missing
// static file must be a 404, never a 200 HTML page.
func TestHandlerMissingStaticReturns404(t *testing.T) {
	paths := []string{
		"/assets/index-00000000.js", // stale hash from a previous release
		"/assets/does-not-exist.js",
		"/assets/does-not-exist.css",
		"/missing.css",
		"/favicon.ico",
		"/img/logo.png",
		"/assets/",        // directory, not a file
		"/assets",         // directory, not a file
		"/assets/nested/", // missing nested directory
	}
	index := indexHTML(t)
	for _, p := range paths {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", p, rec.Code)
		}
		if body := rec.Body.String(); body == index {
			t.Errorf("GET %s returned the SPA body, want a 404 body", p)
		}
		if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want non-HTML for a 404", p, ct)
		}
		// A cached 404 would keep serving the error after the asset reappears.
		if cc := rec.Header().Get("Cache-Control"); cc != "" {
			t.Errorf("GET %s Cache-Control = %q, want empty on 404", p, cc)
		}
	}
}

func TestHandlerRejectsAPIPaths(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/projects", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for /v1 path", rec.Code)
	}
}

// TestHandlerReservedPathVariantsReturn404 covers client typos on API paths:
// they must never fall back to the SPA (200 HTML would make a broken health
// probe look healthy).
func TestHandlerReservedPathVariantsReturn404(t *testing.T) {
	paths := []string{
		"/healthz/", "/healthz//", "/HEALTHZ", "/Healthz",
		"/readyz/", "/READYZ",
		"/version/", "/VERSION",
		"/v1", "/v1/", "/v1/nope", "/V1/nope",
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
			t.Errorf("GET %s returned HTML (%s), want 404 without SPA fallback", p, ct)
		}
	}
}

// TestHandlerRejectsNonReadMethods keeps the 405 + Allow contract intact.
func TestHandlerRejectsNonReadMethods(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, p := range []string{"/", "/products", "/assets/does-not-exist.js"} {
			rec := httptest.NewRecorder()
			Handler().ServeHTTP(rec, httptest.NewRequest(m, p, nil))

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s status = %d, want 405", m, p, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s Allow = %q, want %q", m, p, allow, "GET, HEAD")
			}
		}
	}
}

// TestRouterWithSPAFallback exercises the real composition: exact API paths
// work, typo variants 404, frontend deep links still serve index.html, and
// missing static assets 404 instead of falling back.
func TestRouterWithSPAFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := api.NewRouter(logger, Handler(), nil, nil, nil, nil, nil, nil, nil, nil)

	cases := []struct {
		path     string
		wantCode int
		wantHTML bool
		wantJSON bool
	}{
		{path: "/healthz", wantCode: http.StatusOK, wantJSON: true},
		{path: "/healthz/", wantCode: http.StatusNotFound},
		{path: "/HEALTHZ", wantCode: http.StatusNotFound},
		{path: "/version/", wantCode: http.StatusNotFound},
		{path: "/v1", wantCode: http.StatusNotFound},
		{path: "/v1/nope", wantCode: http.StatusNotFound},
		{path: "/", wantCode: http.StatusOK, wantHTML: true},
		{path: "/products", wantCode: http.StatusOK, wantHTML: true},
		{path: "/tasks/42", wantCode: http.StatusOK, wantHTML: true},
		{path: "/assets/does-not-exist.js", wantCode: http.StatusNotFound},
		{path: "/missing.css", wantCode: http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.wantCode {
			t.Errorf("GET %s status = %d, want %d", c.path, rec.Code, c.wantCode)
		}
		ct := rec.Header().Get("Content-Type")
		if c.wantHTML && !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", c.path, ct)
		}
		if c.wantJSON && !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s Content-Type = %q, want application/json", c.path, ct)
		}
	}
}
