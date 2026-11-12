package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestMigrateFSForeignKeyCheck verifies the post-migration integrity guard:
// rows with a dangling reference must surface as a ForeignKeyIntegrityError
// naming the child table, its rowid and the missing parent.
func TestMigrateFSForeignKeyCheck(t *testing.T) {
	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Simulate a migration that rebuilt a table and dropped a child row's
	// parent: enforcement is off for the insert, exactly like during a
	// migration.
	if err := st.setForeignKeys(ctx, false); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO work_items (work_id, project_id, kind, seq_no, title, body_md, state, version, create_time, update_time)
		VALUES ('w_orphan', 999999, 'dev', 1, '孤儿工作项', '', 'queued', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert orphan work item: %v", err)
	}
	if err := st.setForeignKeys(ctx, true); err != nil {
		t.Fatalf("restore foreign_keys: %v", err)
	}

	err = st.checkForeignKeys(ctx)
	if err == nil {
		t.Fatal("checkForeignKeys: error = nil, want a violation report")
	}
	var integrity *ForeignKeyIntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("error = %T (%v), want *ForeignKeyIntegrityError", err, err)
	}
	if len(integrity.Violations) != 1 {
		t.Fatalf("violations = %+v, want exactly 1", integrity.Violations)
	}
	got := integrity.Violations[0]
	if got.Table != "work_items" || got.Parent != "projects" || got.RowID == 0 {
		t.Errorf("violation = %+v, want work_items/rowid/parent=projects", got)
	}
	for _, want := range []string{"work_items", "projects", "rowid="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}

	// Deleting the orphan clears the report, so the check is not sticky.
	if _, err := st.Write().ExecContext(ctx, `DELETE FROM work_items WHERE work_id = 'w_orphan'`); err != nil {
		t.Fatalf("delete orphan: %v", err)
	}
	if err := st.checkForeignKeys(ctx); err != nil {
		t.Errorf("checkForeignKeys after cleanup = %v, want nil", err)
	}
}

// TestMigrateFSRunsForeignKeyCheck pins the wiring: MigrateFS validates the
// schema whenever it applied at least one migration, and leaves an up-to-date
// database (applied == 0) alone.
func TestMigrateFSRunsForeignKeyCheck(t *testing.T) {
	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// A no-op pending migration is enough to exercise the post-migration
	// guard; the orphan row inserted before it must make the run fail.
	if err := st.setForeignKeys(ctx, false); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO work_items (work_id, project_id, kind, seq_no, title, body_md, state, version, create_time, update_time)
		VALUES ('w_orphan', 999999, 'dev', 1, '孤儿工作项', '', 'queued', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert orphan work item: %v", err)
	}
	if err := st.setForeignKeys(ctx, true); err != nil {
		t.Fatalf("restore foreign_keys: %v", err)
	}

	applied, err := st.MigrateFS(ctx, fstest.MapFS{
		"0005_noop.sql": {Data: []byte("CREATE TABLE __noop_0005 (id INTEGER);")},
	})
	var integrity *ForeignKeyIntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("MigrateFS error = %v, want *ForeignKeyIntegrityError", err)
	}
	if applied == 0 {
		t.Errorf("applied = 0, want the pending migration to be applied")
	}

	// Once the row is gone, a further pending migration applies cleanly.
	if _, err := st.Write().ExecContext(ctx, `DELETE FROM work_items WHERE work_id = 'w_orphan'`); err != nil {
		t.Fatalf("delete orphan: %v", err)
	}
	applied, err = st.MigrateFS(ctx, fstest.MapFS{
		"0005_noop.sql": {Data: []byte("CREATE TABLE __noop_0005 (id INTEGER);")},
		"0006_noop.sql": {Data: []byte("CREATE TABLE __noop_0006 (id INTEGER);")},
	})
	if err != nil {
		t.Fatalf("MigrateFS after cleanup: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	// An up-to-date database applies nothing and skips the check.
	applied, err = st.MigrateFS(ctx, fstest.MapFS{
		"0005_noop.sql": {Data: []byte("CREATE TABLE __noop_0005 (id INTEGER);")},
		"0006_noop.sql": {Data: []byte("CREATE TABLE __noop_0006 (id INTEGER);")},
	})
	if err != nil {
		t.Fatalf("MigrateFS up-to-date: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
}
