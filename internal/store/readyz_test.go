package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The readiness probes are tested directly here rather than through the HTTP
// router: the router lives in internal/api, which depends on the application
// layer, and a store test must not drag that in (it would also invert the
// dependency direction).
func openReadyzStore(t *testing.T, readPoolSize int) *Store {
	t.Helper()

	st, err := Open(Config{
		Path:         filepath.Join(t.TempDir(), "state.db"),
		ReadPoolSize: readPoolSize,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
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

func TestReadyzHealthy(t *testing.T) {
	st := openReadyzStore(t, 2)
	ctx := context.Background()

	db := NewDBChecker(st)
	if db.Name() != "db" {
		t.Errorf("DBChecker.Name() = %q, want db", db.Name())
	}
	if err := db.Check(ctx); err != nil {
		t.Errorf("DBChecker.Check = %v, want nil", err)
	}

	migration := NewMigrationChecker(st)
	if migration.Name() != "migration" {
		t.Errorf("MigrationChecker.Name() = %q, want migration", migration.Name())
	}
	if err := migration.Check(ctx); err != nil {
		t.Errorf("MigrationChecker.Check = %v, want nil", err)
	}
}

func TestReadyzClosedStore(t *testing.T) {
	st := openReadyzStore(t, 1)
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := NewDBChecker(st).Check(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("DBChecker.Check on a closed store = %v, want ErrClosed", err)
	}
	if err := NewMigrationChecker(st).Check(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("MigrationChecker.Check on a closed store = %v, want ErrClosed", err)
	}
}

func TestReadyzMigrationMismatch(t *testing.T) {
	st := openReadyzStore(t, 1)
	ctx := context.Background()

	// Simulate a schema left behind by another binary version.
	if _, err := st.Write().ExecContext(ctx, "PRAGMA user_version = 0"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	if err := NewDBChecker(st).Check(ctx); err != nil {
		t.Errorf("DBChecker.Check = %v, want nil (the database itself works)", err)
	}
	if err := NewMigrationChecker(st).Check(ctx); err == nil {
		t.Error("MigrationChecker.Check = nil, want a version mismatch error")
	}
}
