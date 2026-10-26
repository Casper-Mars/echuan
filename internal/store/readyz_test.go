package store

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Casper-Mars/echuan/internal/api"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// readyzBody mirrors the API readiness response for assertions.
type readyzBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func readyzServer(st *Store) http.Handler {
	return api.NewRouter(
		platform.NewLogger("error", io.Discard),
		nil,
		[]api.HealthChecker{NewDBChecker(st), NewMigrationChecker(st)},
	)
}

func getReadyz(t *testing.T, h http.Handler) (int, readyzBody) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body readyzBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readyz body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func TestReadyzHealthy(t *testing.T) {
	st := openTestStore(t, 2)

	status, body := getReadyz(t, readyzServer(st))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %+v", status, body)
	}
	if body.Status != "ok" {
		t.Errorf("status field = %q, want ok", body.Status)
	}
	if body.Checks["db"] != "ok" || body.Checks["migration"] != "ok" {
		t.Errorf("checks = %v, want db/migration ok", body.Checks)
	}
}

func TestReadyzClosedStore(t *testing.T) {
	st := openTestStore(t, 1)
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	status, body := getReadyz(t, readyzServer(st))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if body.Status != "unavailable" {
		t.Errorf("status field = %q, want unavailable", body.Status)
	}
	if body.Checks["db"] != "error" {
		t.Errorf("checks = %v, want db error", body.Checks)
	}
}

func TestReadyzMigrationMismatch(t *testing.T) {
	st := openTestStore(t, 1)

	// Simulate a schema left behind by another binary version.
	if _, err := st.Write().ExecContext(context.Background(), "PRAGMA user_version = 0"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	status, body := getReadyz(t, readyzServer(st))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %+v", status, body)
	}
	if body.Checks["db"] != "ok" {
		t.Errorf("db check = %q, want ok (the database itself works)", body.Checks["db"])
	}
	if body.Checks["migration"] != "error" {
		t.Errorf("migration check = %q, want error", body.Checks["migration"])
	}
}
