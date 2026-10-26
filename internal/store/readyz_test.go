package store_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Casper-Mars/echuan/internal/api"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// readyzBody mirrors the API readiness response for assertions.
type readyzBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// openReadyzStore opens a migrated store with a read pool. The test lives in
// the external store_test package because the API router now depends on the
// application layer, which in turn depends on store: importing api from
// package store would be an import cycle.
func openReadyzStore(t *testing.T, readPoolSize int) *store.Store {
	t.Helper()

	st, err := store.Open(store.Config{
		Path:         filepath.Join(t.TempDir(), "state.db"),
		ReadPoolSize: readPoolSize,
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
	return st
}

func readyzServer(st *store.Store) http.Handler {
	return api.NewRouter(
		platform.NewLogger("error", io.Discard),
		nil,
		[]api.HealthChecker{store.NewDBChecker(st), store.NewMigrationChecker(st)},
		nil,
		nil,
		nil,
		nil,
		nil,
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
	st := openReadyzStore(t, 2)

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
	st := openReadyzStore(t, 1)
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
	st := openReadyzStore(t, 1)

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
