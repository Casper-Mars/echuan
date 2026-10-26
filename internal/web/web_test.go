package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/api"
)

func TestHandlerServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "echuan") {
		t.Errorf("body does not look like the placeholder page: %s", rec.Body.String())
	}
}

func TestHandlerFallbackToIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tasks/42", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 fallback", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "echuan") {
		t.Error("fallback did not serve index.html")
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

// TestRouterWithSPAFallback exercises the real composition: exact API paths
// work, typo variants 404, and frontend deep links still serve index.html.
func TestRouterWithSPAFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := api.NewRouter(logger, Handler(), nil)

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
		{path: "/tasks/42", wantCode: http.StatusOK, wantHTML: true},
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
