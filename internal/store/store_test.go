package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// testLogger writes nothing but keeps the production log contract.
func openTestStore(t *testing.T, readPoolSize int) *Store {
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

func schemaObjects(t *testing.T, st *Store, typ string) map[string]bool {
	t.Helper()

	rows, err := st.Read().QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = ? AND name NOT LIKE 'sqlite_%'`, typ)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan name: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return names
}

func TestMigrateCreatesSchema(t *testing.T) {
	st := openTestStore(t, 4)

	wantTables := []string{"products", "projects", "tasks", "task_deps", "materials"}
	tables := schemaObjects(t, st, "table")
	for _, name := range wantTables {
		if !tables[name] {
			t.Errorf("table %s missing; have %v", name, tables)
		}
	}

	wantIndexes := []string{"ix_projects_inbox", "ix_tasks_queue", "ix_tasks_lease", "ix_materials_chain"}
	indexes := schemaObjects(t, st, "index")
	for _, name := range wantIndexes {
		if !indexes[name] {
			t.Errorf("index %s missing; have %v", name, indexes)
		}
	}

	version, err := st.MigrationVersion(context.Background())
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}

	latest, err := LatestMigrationVersion()
	if err != nil {
		t.Fatalf("LatestMigrationVersion: %v", err)
	}
	if latest != version {
		t.Errorf("latest embedded migration = %d, database = %d", latest, version)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	first, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("Open first: %v", err)
	}
	applied, err := first.Migrate(context.Background())
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if applied != 1 {
		t.Errorf("first applied = %d, want 1", applied)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second, err := Open(Config{Path: path})
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}
	defer second.Close()

	applied, err = second.Migrate(context.Background())
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if applied != 0 {
		t.Errorf("second applied = %d, want 0", applied)
	}

	version, err := second.MigrationVersion(context.Background())
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
}

func TestConnectionPragmasAndPools(t *testing.T) {
	st := openTestStore(t, 3)
	ctx := context.Background()

	var journal string
	if err := st.Write().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var foreignKeys int
	if err := st.Write().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := st.Write().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if busyTimeout <= 0 {
		t.Errorf("busy_timeout = %d, want > 0", busyTimeout)
	}

	var synchronous int
	if err := st.Write().QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("synchronous: %v", err)
	}
	if synchronous != 1 { // 1 = NORMAL
		t.Errorf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}

	if got := st.Write().Stats().MaxOpenConnections; got != 1 {
		t.Errorf("write pool max connections = %d, want 1", got)
	}
	if got := st.Read().Stats().MaxOpenConnections; got != 3 {
		t.Errorf("read pool max connections = %d, want 3", got)
	}

	t.Logf("journal_mode=%s foreign_keys=%d busy_timeout=%d synchronous=%d write_max_conns=%d read_max_conns=%d",
		journal, foreignKeys, busyTimeout, synchronous,
		st.Write().Stats().MaxOpenConnections, st.Read().Stats().MaxOpenConnections)
}

func TestReadPoolRejectsWrites(t *testing.T) {
	st := openTestStore(t, 2)

	_, err := st.Read().ExecContext(context.Background(),
		`INSERT INTO products (product_id, name, git_url, create_time, update_time) VALUES ('APP','p','g','t','t')`)
	if err == nil {
		t.Fatal("read pool accepted a write, want error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Errorf("error = %v, want a readonly error", err)
	}
}

func TestForeignKeyEnforced(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()

	res, err := st.Write().ExecContext(ctx,
		`INSERT INTO products (product_id, name, git_url, create_time, update_time) VALUES ('APP','驿传','git@example.com','t','t')`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	productID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product id: %v", err)
	}

	if _, err := st.Write().ExecContext(ctx,
		`INSERT INTO projects (project_id, product_id, seq_no, title, create_time, update_time) VALUES ('APP-1', ?, 1, 'p', 't', 't')`,
		productID); err != nil {
		t.Fatalf("insert valid project: %v", err)
	}

	_, err = st.Write().ExecContext(ctx,
		`INSERT INTO projects (project_id, product_id, seq_no, title, create_time, update_time) VALUES ('APP-2', 9999, 1, 'p', 't', 't')`)
	if err == nil {
		t.Fatal("insert with unknown product_id succeeded, want foreign key error")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("error = %v, want FOREIGN KEY constraint failure", err)
	}
}

func TestNextSeqNo(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProject(t, st)

	for want := 1; want <= 5; want++ {
		err := st.Tx(ctx, func(tx *sql.Tx) error {
			next, err := NextSeqNo(ctx, tx, "tasks", "project_id", projectID)
			if err != nil {
				return err
			}
			if next != want {
				t.Errorf("NextSeqNo = %d, want %d", next, want)
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, create_time, update_time)
				 VALUES (?, ?, ?, 't', 'b', 't', 't')`,
				fmt.Sprintf("APP-1-T%04d", next), projectID, next)
			return err
		})
		if err != nil {
			t.Fatalf("insert task %d: %v", want, err)
		}
	}
}

func TestNextSeqNoConcurrent(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProject(t, st)

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.Tx(ctx, func(tx *sql.Tx) error {
				seq, err := NextSeqNo(ctx, tx, "tasks", "project_id", projectID)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx,
					`INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, create_time, update_time)
					 VALUES (?, ?, ?, 't', 'b', 't', 't')`,
					fmt.Sprintf("APP-1-T%04d", seq), projectID, seq)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent NextSeqNo: %v", err)
		}
	}

	var count, distinct, maxSeq int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(DISTINCT seq_no), MAX(seq_no) FROM tasks WHERE project_id = ?`,
		projectID).Scan(&count, &distinct, &maxSeq); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if count != workers || distinct != workers || maxSeq != workers {
		t.Errorf("count=%d distinct=%d max=%d, want %d/%d/%d", count, distinct, maxSeq, workers, workers, workers)
	}
}

func TestNextSeqNoRequiresTransaction(t *testing.T) {
	if _, err := NextSeqNo(context.Background(), nil, "tasks", "project_id", 1); err == nil {
		t.Fatal("NextSeqNo(nil tx) succeeded, want error")
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	boom := errors.New("boom")

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO products (product_id, name, git_url, create_time, update_time) VALUES ('APP','p','g','t','t')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx error = %v, want boom", err)
	}

	var count int
	if err := st.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("products = %d, want 0 after rollback", count)
	}
}

func TestTxRollsBackOnPanic(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()

	recovered := func() (recovered any) {
		defer func() { recovered = recover() }()
		_ = st.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO products (product_id, name, git_url, create_time, update_time) VALUES ('APP','p','g','t','t')`); err != nil {
				return err
			}
			panic("boom")
		})
		return nil
	}()
	if recovered != "boom" {
		t.Fatalf("recovered = %v, want boom", recovered)
	}

	var count int
	if err := st.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("products = %d, want 0 after panic rollback", count)
	}
}

func TestMigrateIsAtomic(t *testing.T) {
	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	fsys := fstest.MapFS{
		"0001_ok.sql": {Data: []byte("CREATE TABLE t1 (id INTEGER);")},
		"0002_bad.sql": {Data: []byte(
			"CREATE TABLE t2 (id INTEGER);\nINSERT INTO missing_table VALUES (1);")},
	}

	applied, err := st.MigrateFS(context.Background(), fsys)
	if err == nil {
		t.Fatal("MigrateFS: expected error")
	}
	var me *MigrationError
	if !errors.As(err, &me) || me.Version != 2 {
		t.Errorf("error = %v, want MigrationError for version 2", err)
	}
	t.Logf("migration failure: %v", err)
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	version, err := st.MigrationVersion(context.Background())
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1 (0002 must not be recorded)", version)
	}

	// t2 was created by the failing migration's first statement and must have
	// been rolled back with it.
	var count int
	if err := st.Write().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 't2'`).Scan(&count); err != nil {
		t.Fatalf("count t2: %v", err)
	}
	if count != 0 {
		t.Error("table t2 survived the failed migration, want rollback")
	}
}

func TestMigrateRejectsNewerSchema(t *testing.T) {
	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.Write().ExecContext(context.Background(), "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	_, err = st.Migrate(context.Background())
	var sv *SchemaVersionError
	if !errors.As(err, &sv) {
		t.Fatalf("error = %v, want SchemaVersionError", err)
	}
	if sv.Database != 99 || sv.Binary != 1 {
		t.Errorf("versions = %d/%d, want 99/1", sv.Database, sv.Binary)
	}
}

func TestClosedStore(t *testing.T) {
	st := openTestStore(t, 1)
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close is idempotent.
	if err := st.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	if err := st.Ping(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("Ping = %v, want ErrClosed", err)
	}
	if _, err := st.MigrationVersion(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("MigrationVersion = %v, want ErrClosed", err)
	}
	if err := NewDBChecker(st).Check(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("db check = %v, want ErrClosed", err)
	}
	if err := st.Tx(context.Background(), func(*sql.Tx) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Errorf("Tx = %v, want ErrClosed", err)
	}
}

func TestSplitStatements(t *testing.T) {
	script := `
-- leading comment
CREATE TABLE a (
  x TEXT DEFAULT 'a;b',   -- semicolon inside a string
  y TEXT DEFAULT 'it''s'
);
/* block ; comment */
CREATE TABLE "b;c" (z INTEGER);
INSERT INTO a (x) VALUES ('--not a comment');
`
	got := splitStatements(script)
	want := 3
	if len(got) != want {
		t.Fatalf("statements = %d, want %d: %#v", len(got), want, got)
	}
	if !strings.Contains(got[0], "CREATE TABLE a") || !strings.Contains(got[0], "'a;b'") {
		t.Errorf("statement 0 = %q", got[0])
	}
	if !strings.Contains(got[1], `CREATE TABLE "b;c"`) {
		t.Errorf("statement 1 = %q", got[1])
	}
	if !strings.Contains(got[2], "'--not a comment'") {
		t.Errorf("statement 2 = %q", got[2])
	}
}

// seedProject inserts one product and one project and returns the project id.
func seedProject(t *testing.T, st *Store) int64 {
	t.Helper()
	ctx := context.Background()

	var projectID int64
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO products (product_id, name, git_url, create_time, update_time) VALUES ('APP','驿传','git@example.com','t','t')`)
		if err != nil {
			return err
		}
		productID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx,
			`INSERT INTO projects (project_id, product_id, seq_no, title, create_time, update_time) VALUES ('APP-1', ?, 1, 'p', 't', 't')`,
			productID)
		if err != nil {
			return err
		}
		projectID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projectID
}
