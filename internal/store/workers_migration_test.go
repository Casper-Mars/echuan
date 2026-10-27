package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// workersColumn is one row of PRAGMA table_info(workers).
type workersColumn struct {
	name       string
	columnType string
	notNull    int
	defaultVal sql.NullString
	primaryKey int
}

// TestWorkersTableSchema pins the 0003 workers schema to the technical design
// (§2.1): exact column order, types, NOT NULL flags, defaults and the primary
// key, so a drift in the migration is caught here rather than at runtime.
func TestWorkersTableSchema(t *testing.T) {
	st := openTestStore(t, 1)

	want := []workersColumn{
		{name: "id", columnType: "INTEGER", notNull: 0, primaryKey: 1},
		{name: "worker_id", columnType: "TEXT", notNull: 1},
		{name: "metadata_json", columnType: "TEXT", notNull: 1, defaultVal: sql.NullString{String: "'{}'", Valid: true}},
		{name: "state", columnType: "TEXT", notNull: 1, defaultVal: sql.NullString{String: "'idle'", Valid: true}},
		{name: "current_task_id", columnType: "INTEGER"},
		{name: "current_lease_id", columnType: "TEXT"},
		{name: "first_seen", columnType: "TEXT", notNull: 1},
		{name: "last_seen", columnType: "TEXT", notNull: 1},
		{name: "create_time", columnType: "TEXT", notNull: 1},
		{name: "update_time", columnType: "TEXT", notNull: 1},
	}

	rows, err := st.Read().QueryContext(context.Background(), `PRAGMA table_info(workers)`)
	if err != nil {
		t.Fatalf("table_info workers: %v", err)
	}
	defer rows.Close()

	var got []workersColumn
	for rows.Next() {
		var (
			cid  int
			col  workersColumn
			defE sql.NullString
		)
		if err := rows.Scan(&cid, &col.name, &col.columnType, &col.notNull, &defE, &col.primaryKey); err != nil {
			t.Fatalf("scan table_info workers: %v", err)
		}
		col.defaultVal = defE
		got = append(got, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("workers columns = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	var indexSQL string
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'ux_workers_current_task'`).
		Scan(&indexSQL); err != nil {
		t.Fatalf("select ux_workers_current_task: %v", err)
	}
	if !strings.Contains(indexSQL, "UNIQUE INDEX") ||
		!strings.Contains(indexSQL, "WHERE current_task_id IS NOT NULL") {
		t.Errorf("ux_workers_current_task = %q, want a partial UNIQUE index", indexSQL)
	}

	var lastSeenSQL string
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'ix_workers_last_seen'`).
		Scan(&lastSeenSQL); err != nil {
		t.Fatalf("select ix_workers_last_seen: %v", err)
	}
	if !strings.Contains(lastSeenSQL, "(last_seen)") {
		t.Errorf("ix_workers_last_seen = %q, want an index on last_seen", lastSeenSQL)
	}
}

// TestWorkersCurrentTaskUnique enforces the one-task-per-worker invariant: a
// second worker may not point at an already claimed task, while idle workers
// (NULL current_task_id) are unrestricted.
func TestWorkersCurrentTaskUnique(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProject(t, st)

	insertTask := func(seq int, taskID string) int64 {
		t.Helper()
		res, err := st.Write().ExecContext(ctx,
			`INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, create_time, update_time)
			 VALUES (?, ?, ?, 't', 'b', 'T', 'T')`, taskID, projectID, seq)
		if err != nil {
			t.Fatalf("insert task %s: %v", taskID, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("task id: %v", err)
		}
		return id
	}
	insertWorker := func(workerID string, taskID *int64) error {
		t.Helper()
		_, err := st.Write().ExecContext(ctx,
			`INSERT INTO workers (worker_id, state, current_task_id, first_seen, last_seen, create_time, update_time)
			 VALUES (?, CASE WHEN ? IS NULL THEN 'idle' ELSE 'busy' END, ?, 'T', 'T', 'T', 'T')`,
			workerID, taskID, taskID)
		return err
	}

	taskID := insertTask(1, "APP-1-T0001")
	otherTaskID := insertTask(2, "APP-1-T0002")

	if err := insertWorker("w_first", &taskID); err != nil {
		t.Fatalf("insert first busy worker: %v", err)
	}
	err := insertWorker("w_second", &taskID)
	if err == nil {
		t.Fatal("second worker claimed the same task, want UNIQUE constraint error")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Errorf("error = %v, want UNIQUE constraint failure", err)
	}

	// A different task is fine.
	if err := insertWorker("w_third", &otherTaskID); err != nil {
		t.Fatalf("insert worker for another task: %v", err)
	}

	// NULL does not participate in the partial unique index: two idle workers
	// may coexist.
	if err := insertWorker("w_idle_1", nil); err != nil {
		t.Fatalf("insert first idle worker: %v", err)
	}
	if err := insertWorker("w_idle_2", nil); err != nil {
		t.Fatalf("insert second idle worker: %v", err)
	}

	var busy int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workers WHERE current_task_id IS NOT NULL`).Scan(&busy); err != nil {
		t.Fatalf("count busy workers: %v", err)
	}
	if busy != 2 {
		t.Errorf("busy workers = %d, want 2", busy)
	}
}

// TestWorkersMigrationIdempotent checks the runner applies 0003 exactly once:
// the first Migrate on an empty database applies 3 migrations, the second
// returns 0 and leaves user_version at 3.
func TestWorkersMigrationIdempotent(t *testing.T) {
	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	applied, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if applied != 3 {
		t.Errorf("first applied = %d, want 3", applied)
	}

	applied, err = st.Migrate(ctx)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if applied != 0 {
		t.Errorf("second applied = %d, want 0", applied)
	}

	version, err := st.MigrationVersion(ctx)
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if version != 3 {
		t.Errorf("user_version = %d, want 3", version)
	}
}
