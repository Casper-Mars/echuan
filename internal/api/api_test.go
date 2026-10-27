package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/platform"
)

func testLogger(buf io.Writer) *slog.Logger {
	// Use the production logger so tests assert the documented field names
	// (e.g. "ts" instead of slog's default "time").
	return platform.NewLogger("info", buf)
}

func testServer(t *testing.T, checks ...HealthChecker) http.Handler {
	t.Helper()
	return Chain(
		NewRouter(testLogger(io.Discard), nil, checks, nil, nil, nil, nil, nil, nil),
		Recover(testLogger(io.Discard)),
		RequestID(),
		AccessLog(testLogger(io.Discard)),
		Timeout(DefaultRequestTimeout),
	)
}

func doRequest(t *testing.T, h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	h := testServer(t)

	rec := doRequest(t, h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want ok", body["status"])
	}
}

func TestReadyzPending(t *testing.T) {
	h := testServer(t, NewPendingChecker("db"), NewPendingChecker("migration"))

	rec := doRequest(t, h, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Checks["db"] != "pending" || body.Checks["migration"] != "pending" {
		t.Errorf("checks = %v, want db/migration pending", body.Checks)
	}
}

type okChecker struct{ name string }

func (c okChecker) Name() string                { return c.name }
func (c okChecker) Check(context.Context) error { return nil }

type failingChecker struct{ name string }

func (c failingChecker) Name() string { return c.name }
func (c failingChecker) Check(context.Context) error {
	return errors.New("database is locked")
}

func TestReadyzHealthyCheckers(t *testing.T) {
	h := testServer(t, okChecker{"db"}, okChecker{"migration"})

	rec := doRequest(t, h, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Checks["db"] != "ok" {
		t.Errorf("checks = %v, want db ok", body.Checks)
	}
}

func TestReadyzFailure(t *testing.T) {
	h := testServer(t, okChecker{"db"}, failingChecker{"migration"})

	rec := doRequest(t, h, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "unavailable" {
		t.Errorf("status = %q, want unavailable", body.Status)
	}
	if body.Checks["migration"] != "error" {
		t.Errorf("checks = %v, want migration error", body.Checks)
	}
}

func TestVersion(t *testing.T) {
	h := testServer(t)

	rec := doRequest(t, h, http.MethodGet, "/version", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Version == "" {
		t.Error("version field is empty")
	}
	if body.Commit == "" {
		t.Error("commit field is empty")
	}
	if body.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q, want %q", body.GoVersion, runtime.Version())
	}
}

func TestRequestIDGenerated(t *testing.T) {
	h := testServer(t)

	rec := doRequest(t, h, http.MethodGet, "/healthz", nil)
	if got := rec.Header().Get(HeaderRequestID); got == "" {
		t.Fatal("response is missing X-Request-Id")
	}
}

func TestRequestIDReused(t *testing.T) {
	h := testServer(t)

	rec := doRequest(t, h, http.MethodGet, "/healthz", map[string]string{HeaderRequestID: "req-abc"})
	if got := rec.Header().Get(HeaderRequestID); got != "req-abc" {
		t.Errorf("X-Request-Id = %q, want req-abc", got)
	}
}

func TestPanicRecovered(t *testing.T) {
	var logBuf bytes.Buffer
	logger := testLogger(&logBuf)

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	h := Chain(panicking, Recover(logger), RequestID(), AccessLog(logger))

	rec := doRequest(t, h, http.MethodGet, "/boom", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "internal" {
		t.Errorf("code = %q, want internal", body.Code)
	}
	if body.RequestID == "" {
		t.Error("request_id is empty")
	}
	if body.Details == nil {
		t.Error("details is null, want []")
	}
	if !strings.Contains(logBuf.String(), "panic recovered") {
		t.Errorf("log output missing panic entry: %s", logBuf.String())
	}
}

func TestErrorBodyShape(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusBadRequest, "bad_request", "subject is required", []ErrorDetail{
			{Field: "subject", Reason: "is required"},
		})
	}), RequestID())

	rec := doRequest(t, h, http.MethodGet, "/any", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}

	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "bad_request" {
		t.Errorf("code = %q", body.Code)
	}
	if body.Message != "subject is required" {
		t.Errorf("message = %q", body.Message)
	}
	if len(body.Details) != 1 || body.Details[0].Field != "subject" || body.Details[0].Reason != "is required" {
		t.Errorf("details = %v", body.Details)
	}
	if body.RequestID == "" {
		t.Error("request_id is empty")
	}

	// details must always be encoded as an array, never null.
	if raw := rec.Body.String(); !strings.Contains(raw, `"details":[{"field":"subject","reason":"is required"}]`) {
		t.Errorf("body = %s, want inline details array", raw)
	}
}

func TestNotFoundIsNative(t *testing.T) {
	h := testServer(t)

	rec := doRequest(t, h, http.MethodGet, "/no-such-path", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAccessLogFields(t *testing.T) {
	var logBuf bytes.Buffer
	logger := testLogger(&logBuf)

	h := Chain(
		NewRouter(logger, nil, nil, nil, nil, nil, nil, nil, nil),
		Recover(logger),
		RequestID(),
		AccessLog(logger),
		Timeout(DefaultRequestTimeout),
	)

	doRequest(t, h, http.MethodGet, "/healthz", map[string]string{HeaderActor: "alice"})

	line := strings.TrimSpace(logBuf.String())
	if line == "" {
		t.Fatal("no access log line emitted")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("decode log line: %v", err)
	}
	if entry["ts"] == nil {
		t.Error("log line missing ts field")
	}
	if entry["msg"] != "request" {
		t.Errorf("msg = %v, want request", entry["msg"])
	}
	if entry["actor"] != "alice" {
		t.Errorf("actor = %v, want alice", entry["actor"])
	}
	if entry["method"] != "GET" {
		t.Errorf("method = %v", entry["method"])
	}
	if entry["path"] != "/healthz" {
		t.Errorf("path = %v", entry["path"])
	}
	if entry["request_id"] == "" || entry["request_id"] == nil {
		t.Error("request_id missing from log line")
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Error("duration_ms missing from log line")
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", entry["status"])
	}
}

func TestAccessLogUnknownActor(t *testing.T) {
	var logBuf bytes.Buffer
	logger := testLogger(&logBuf)

	h := Chain(NewRouter(logger, nil, nil, nil, nil, nil, nil, nil, nil), RequestID(), AccessLog(logger))
	doRequest(t, h, http.MethodGet, "/healthz", nil)

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &entry); err != nil {
		t.Fatalf("decode log line: %v", err)
	}
	if entry["actor"] != "unknown" {
		t.Errorf("actor = %v, want unknown", entry["actor"])
	}
}

func TestTimeout(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately ignore the request context: the middleware must cut
		// the request off on its own.
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	h := Chain(slow, RequestID(), Timeout(20*time.Millisecond))

	rec := doRequest(t, h, http.MethodGet, "/slow", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != "timeout" {
		t.Errorf("code = %q, want timeout", body.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := testServer(t)

	rec := doRequest(t, h, http.MethodPost, "/healthz", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestIsReservedPath(t *testing.T) {
	reserved := []string{
		"/v1", "/v1/", "/v1/nope", "/v1/a/b",
		"/healthz", "/healthz/", "/healthz//", "/HEALTHZ", "/Healthz/",
		"/readyz", "/READYZ/",
		"/version", "/version/", "/Version",
	}
	for _, p := range reserved {
		if !IsReservedPath(p) {
			t.Errorf("IsReservedPath(%q) = false, want true", p)
		}
	}

	free := []string{
		"/", "", "/tasks/42", "/index.html", "/health", "/healthzz",
		"/healthz.json", "/v10/x", "/assets/app.js",
	}
	for _, p := range free {
		if IsReservedPath(p) {
			t.Errorf("IsReservedPath(%q) = true, want false", p)
		}
	}
}
