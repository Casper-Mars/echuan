package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Casper-Mars/echuan/migrations"
)

// migrationsUpTo builds an in-memory migration set containing only the files
// with a version <= maxVersion. It lets a test recreate the schema a previous
// binary shipped, then hand the same database to the real runner so the next
// migration is exercised exactly as it is in production.
func migrationsUpTo(t *testing.T, maxVersion int) fstest.MapFS {
	t.Helper()

	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("glob embedded migrations: %v", err)
	}
	files := fstest.MapFS{}
	for _, name := range names {
		version, err := strconv.Atoi(name[:4])
		if err != nil {
			t.Fatalf("migration %q: parse version: %v", name, err)
		}
		if version > maxVersion {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	if len(files) != maxVersion {
		t.Fatalf("migrations up to %d = %d files, want %d", maxVersion, len(files), maxVersion)
	}
	return files
}

// TestProjectStatusRangeMigrationPreservesData covers 0005, the only migration
// that rebuilds a table: play the 0001–0004 schema, insert a fully referenced
// project row (including children), then apply 0005 and check that nothing was
// lost and that the widened CHECK now accepts cancelled(8).
func TestProjectStatusRangeMigrationPreservesData(t *testing.T) {
	ctx := context.Background()

	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// Old binary schema: 0001 init + 0002 last_actor + 0003 workers +
	// 0004 project_specs. projects.status is still CHECK (0..7).
	if _, err := st.MigrateFS(ctx, migrationsUpTo(t, 4)); err != nil {
		t.Fatalf("MigrateFS(0001..0004): %v", err)
	}
	if version, err := st.MigrationVersion(ctx); err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	} else if version != 4 {
		t.Fatalf("user_version = %d, want 4", version)
	}

	insert := func(query string, args ...any) int64 {
		t.Helper()
		res, err := st.Write().ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("last insert id: %v", err)
		}
		return id
	}

	productPK := insert(
		`INSERT INTO products (product_id, name, git_url, create_time, update_time)
		 VALUES ('MIG-1', 'migration product', 'https://example.com/mig.git', 'T', 'T')`)
	requirementPK := insert(
		`INSERT INTO materials (material_id, kind, version, title, sha256, size, path, uploaded_by, create_time, update_time)
		 VALUES ('MIG-1-R0001', 1, 1, 'requirement', 'sha-req', 10, 'objects/req', 'mig-test', 'T', 'T')`)
	specPK := insert(
		`INSERT INTO materials (material_id, kind, version, title, sha256, size, path, uploaded_by, create_time, update_time)
		 VALUES ('MIG-1-S0001', 2, 1, 'spec', 'sha-spec', 20, 'objects/spec', 'mig-test', 'T', 'T')`)
	projectPK := insert(
		`INSERT INTO projects (project_id, product_id, seq_no, title, requirement_material_id,
		                       spec_material_id, status, reason, version, create_time, update_time, last_actor)
		 VALUES ('MIG-1-1', ?, 1, 'migration project', ?, ?, 7, NULL, 2, 'T', 'T', 'mig-test')`,
		productPK, requirementPK, specPK)
	insert(
		`INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, status, create_time, update_time)
		 VALUES ('MIG-1-1-T0001', ?, 1, 'card', 'body', 4, 'T', 'T')`,
		projectPK)
	insert(
		`INSERT INTO project_specs (spec_id, project_id, seq_no, material_id, create_time, update_time)
		 VALUES ('MIG-1-1-S0001', ?, 1, ?, 'T', 'T')`,
		projectPK, specPK)

	// The old CHECK must reject the new terminal state; otherwise the test
	// could pass without 0005 doing anything.
	if _, err := st.Write().ExecContext(ctx, `UPDATE projects SET status = 8 WHERE id = ?`, projectPK); err == nil {
		t.Fatal("old schema accepted status=8, want CHECK constraint failure")
	} else if !strings.Contains(strings.ToUpper(err.Error()), "CHECK") {
		t.Fatalf("old schema status=8 error = %v, want CHECK failure", err)
	}

	applied, err := st.MigrateFS(ctx, migrations.FS)
	if err != nil {
		t.Fatalf("MigrateFS(0005): %v", err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2 (0005 + 0006)", applied)
	}
	if err := st.OpenReadPool(ctx); err != nil {
		t.Fatalf("OpenReadPool: %v", err)
	}

	// The rebuilt table must keep every project column, including the two
	// material references added before this migration.
	var (
		gotProjectID string
		gotTitle     string
		gotStatus    int
		gotVersion   int
		gotReq       int64
		gotSpec      int64
		gotActor     sql.NullString
	)
	if err := st.Read().QueryRowContext(ctx,
		`SELECT project_id, title, status, version, requirement_material_id, spec_material_id, last_actor
		   FROM projects WHERE id = ?`, projectPK).
		Scan(&gotProjectID, &gotTitle, &gotStatus, &gotVersion, &gotReq, &gotSpec, &gotActor); err != nil {
		t.Fatalf("read project after 0005: %v", err)
	}
	if gotProjectID != "MIG-1-1" || gotTitle != "migration project" || gotStatus != 7 ||
		gotVersion != 2 || gotReq != requirementPK || gotSpec != specPK || gotActor.String != "mig-test" {
		t.Errorf("project after 0005 = %s/%q status=%d version=%d req=%d spec=%d actor=%q",
			gotProjectID, gotTitle, gotStatus, gotVersion, gotReq, gotSpec, gotActor.String)
	}

	for _, child := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM tasks WHERE project_id = ?`, 1},
		{`SELECT COUNT(*) FROM project_specs WHERE project_id = ?`, 1},
	} {
		var count int
		if err := st.Read().QueryRowContext(ctx, child.query, projectPK).Scan(&count); err != nil {
			t.Fatalf("count child rows: %v", err)
		}
		if count != child.want {
			t.Errorf("%s = %d, want %d", child.query, count, child.want)
		}
	}

	// A rebuild that dropped and recreated the table must leave every child
	// reference resolvable; a single orphan would show up here.
	rows, err := st.Read().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			table  string
			rowid  sql.NullInt64
			parent string
			fkid   int
		)
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("scan foreign_key_check: %v", err)
		}
		t.Errorf("foreign key violation: table=%s rowid=%v parent=%s fk=%d", table, rowid, parent, fkid)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check rows: %v", err)
	}

	// The widened CHECK accepts cancelled(8) but still rejects 9.
	res, err := st.Write().ExecContext(ctx, `UPDATE projects SET status = 8 WHERE id = ?`, projectPK)
	if err != nil {
		t.Fatalf("write status=8: %v", err)
	}
	if affected, err := res.RowsAffected(); err != nil {
		t.Fatalf("rows affected: %v", err)
	} else if affected != 1 {
		t.Fatalf("status=8 affected %d rows, want 1", affected)
	}
	if _, err := st.Write().ExecContext(ctx, `UPDATE projects SET status = 9 WHERE id = ?`, projectPK); err == nil {
		t.Fatal("new schema accepted status=9, want CHECK constraint failure")
	} else if !strings.Contains(strings.ToUpper(err.Error()), "CHECK") {
		t.Fatalf("new schema status=9 error = %v, want CHECK failure", err)
	}

	var indexSQL string
	if err := st.Read().QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'ix_projects_inbox'`).
		Scan(&indexSQL); err != nil {
		t.Fatalf("select ix_projects_inbox: %v", err)
	}
	if !strings.Contains(indexSQL, "(status, update_time)") {
		t.Errorf("ix_projects_inbox = %q, want an index on (status, update_time)", indexSQL)
	}

	if version, err := st.MigrationVersion(ctx); err != nil {
		t.Fatalf("MigrationVersion after 0005: %v", err)
	} else if version != 6 {
		t.Errorf("user_version = %d, want 6", version)
	}
}
