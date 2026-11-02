package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
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
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, version, create_time, update_time)
		VALUES ('ORPHAN-1', 999999, 1, '孤儿卡', '正文', 0, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert orphan task: %v", err)
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
	if got.Table != "tasks" || got.Parent != "projects" || got.RowID == 0 {
		t.Errorf("violation = %+v, want tasks/rowid/parent=projects", got)
	}
	for _, want := range []string{"tasks", "projects", "rowid="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}

	// Deleting the orphan clears the report, so the check is not sticky.
	if _, err := st.Write().ExecContext(ctx, `DELETE FROM tasks WHERE task_id = 'ORPHAN-1'`); err != nil {
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

	// A no-op pending migration is enough to exercise the post-migration
	// guard; the orphan row inserted before it must make the run fail.
	if _, err := st.MigrateFS(ctx, migrationsUpTo(t, 4)); err != nil {
		t.Fatalf("MigrateFS(0001..0004): %v", err)
	}
	if err := st.setForeignKeys(ctx, false); err != nil {
		t.Fatalf("disable foreign_keys: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, version, create_time, update_time)
		VALUES ('ORPHAN-2', 999999, 1, '孤儿卡', '正文', 0, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert orphan task: %v", err)
	}
	if err := st.setForeignKeys(ctx, true); err != nil {
		t.Fatalf("restore foreign_keys: %v", err)
	}

	applied, err := st.Migrate(ctx)
	var integrity *ForeignKeyIntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("Migrate error = %v, want *ForeignKeyIntegrityError", err)
	}
	if applied == 0 {
		t.Errorf("applied = 0, want the pending migrations to be applied")
	}

	// Once the row is gone a second run applies nothing and skips the check.
	if _, err := st.Write().ExecContext(ctx, `DELETE FROM tasks WHERE task_id = 'ORPHAN-2'`); err != nil {
		t.Fatalf("delete orphan: %v", err)
	}
	applied, err = st.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate after cleanup: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
}
