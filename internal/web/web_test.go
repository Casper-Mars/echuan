package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
