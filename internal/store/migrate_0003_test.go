package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/Casper-Mars/echuan/migrations"
)

// 0003_kind_from_config.sql acceptance tests.
//
// The migration rebuilds tasks / gate_decisions / worker_kinds (SQLite cannot
// widen a CHECK in place) and swaps the "one active doc card" index for the
// kind_parallel one. It is a *structural* migration: not a single existing value
// may change (ADR-0003 D6″), the kind CHECK only widens to > 0, and the rebuilt
// tables must stay referentially intact.

const testTimeV2 = "2026-11-25T00:00:00.000Z"

// migrationsThrough replays the embedded migration chain up to version v, so a
// test can build the exact schema the previous release shipped.
func migrationsThrough(t *testing.T, v int) fstest.MapFS {
	t.Helper()

	list, err := migrations.Load(migrations.FS)
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	fsys := fstest.MapFS{}
	for _, m := range list {
		if m.Version > v {
			break
		}
		fsys[m.Name] = &fstest.MapFile{Data: m.SQL}
	}
	return fsys
}

// openStoreAtV2 opens a store whose schema is exactly 0001 + 0002
// (user_version = 2), i.e. the database a running 0002 binary owns.
func openStoreAtV2(t *testing.T) *Store {
	t.Helper()

	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db"), ReadPoolSize: 2})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	applied, err := st.MigrateFS(context.Background(), migrationsThrough(t, 2))
	if err != nil {
		t.Fatalf("migrate to v2: %v", err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}
	if version, err := st.MigrationVersion(context.Background()); err != nil || version != 2 {
		t.Fatalf("user_version = %d (%v), want 2", version, err)
	}
	return st
}

// seedV2Data fills a 0002 database with one row of every shape the migration
// has to carry over: four kinds, active and finished statuses, dependencies,
// attachments, a leased worker, gate decisions and declared worker kinds.
func seedV2Data(t *testing.T, st *Store) int64 {
	t.Helper()

	res := mustExec(t, st, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP', '驿传', 'https://example.com/repo.git', ?, ?)`, testTimeV2, testTimeV2)
	productPK, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product id: %v", err)
	}
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)

	// kind = 1 done, kind = 1 active (the old index allows exactly one),
	// kind = 2 pending, kind = 3 ready, and three dev cards at once (the old
	// index exempts kind 4, which is what kind_parallel has to preserve).
	tasks := []struct {
		taskID string
		kind   int
		status int
		title  string
		desc   string
		ctxMD  string
		prio   int
		lease  string
	}{
		{"t_req_done", 1, 5, "需求", "需求正文", "需求上下文", 1, ""},
		{"t_req_open", 1, 4, "第二版需求", "第二版正文", "第二版上下文", 2, ""},
		{"t_design", 2, 1, "方案", "方案正文", "方案上下文", 0, ""},
		{"t_breakdown", 3, 2, "拆解", "拆解正文", "拆解上下文", 3, ""},
		{"t_dev_1", 4, 3, "开发一", "开发一正文", "开发一上下文", 0, "w_1"},
		{"t_dev_2", 4, 2, "开发二", "开发二正文", "", 0, ""},
		{"t_dev_3", 4, 5, "开发三", "开发三正文", "开发三上下文", 0, ""},
	}
	pk := make(map[string]int64, len(tasks))
	for _, task := range tasks {
		var (
			leaseID    any
			leaseUntil any
			workerID   any
		)
		if task.lease != "" {
			leaseID = "l_" + task.lease
			leaseUntil = "2099-01-01T00:00:00.000Z"
			workerID = task.lease
		}
		res := mustExec(t, st, `
			INSERT INTO tasks (task_id, project_id, kind, title, description_md, context_md,
			                   priority, status, lease_id, lease_until, worker_id, create_time, update_time)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.taskID, projectPK, task.kind, task.title, task.desc, task.ctxMD,
			task.prio, task.status, leaseID, leaseUntil, workerID, testTimeV2, testTimeV2)
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("task %s id: %v", task.taskID, err)
		}
		pk[task.taskID] = id
	}

	deps := [][2]string{
		{"t_design", "t_req_done"},
		{"t_breakdown", "t_design"},
		{"t_dev_1", "t_breakdown"},
	}
	for _, dep := range deps {
		mustExec(t, st, `INSERT INTO task_deps (task_id, depends_on_task_id) VALUES (?, ?)`,
			pk[dep[0]], pk[dep[1]])
	}

	materialPK := insertV3Material(t, st, "m_req")
	mustExec(t, st, `INSERT INTO attachments (task_id, material_id, role, create_time) VALUES (?, ?, 2, ?)`,
		pk["t_req_done"], materialPK, testTimeV2)
	mustExec(t, st, `INSERT INTO attachments (project_id, material_id, role, create_time) VALUES (?, ?, 1, ?)`,
		projectPK, materialPK, testTimeV2)

	mustExec(t, st, `
		INSERT INTO workers (worker_id, state, current_task_id, current_lease_id, first_seen, last_seen, create_time, update_time)
		VALUES ('w_1', 2, ?, 'l_w_1', ?, ?, ?, ?)`, pk["t_dev_1"], testTimeV2, testTimeV2, testTimeV2, testTimeV2)
	for _, kind := range []int{1, 2, 4} {
		mustExec(t, st, `INSERT INTO worker_kinds (worker_id, kind) VALUES ('w_1', ?)`, kind)
	}

	mustExec(t, st, `
		INSERT INTO gate_decisions (task_id, gate, material_id, decision, decided_by, reason, create_time)
		VALUES ('t_req_done', 1, ?, 1, 'cto', 'ok', ?)`, materialPK, testTimeV2)
	mustExec(t, st, `
		INSERT INTO gate_decisions (task_id, gate, material_id, decision, decided_by, reason, create_time)
		VALUES ('t_design', 2, NULL, 2, 'cto', NULL, ?)`, testTimeV2)

	return projectPK
}

// taskSnapshot is the value set the migration must not touch, plus the physical
// id it has to carry over (the foreign keys of the child tables point at it).
type taskSnapshot struct {
	ID            int64
	TaskID        string
	Kind          int
	Title         string
	DescriptionMD string
	ContextMD     string
	Priority      int
	Status        int
}

func snapshotTasks(t *testing.T, db *sql.DB) []taskSnapshot {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), `
		SELECT id, task_id, kind, title, description_md, context_md, priority, status
		  FROM tasks ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("snapshot tasks: %v", err)
	}
	defer rows.Close()

	var out []taskSnapshot
	for rows.Next() {
		var snap taskSnapshot
		if err := rows.Scan(&snap.ID, &snap.TaskID, &snap.Kind, &snap.Title,
			&snap.DescriptionMD, &snap.ContextMD, &snap.Priority, &snap.Status); err != nil {
			t.Fatalf("scan task: %v", err)
		}
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tasks: %v", err)
	}
	return out
}

// snapshotDeps renders the dependency graph as business-id edges so a rowid
// change would show up as a difference.
func snapshotDeps(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), `
		SELECT t.task_id, u.task_id
		  FROM task_deps d
		  JOIN tasks t ON t.id = d.task_id
		  JOIN tasks u ON u.id = d.depends_on_task_id
		 ORDER BY t.task_id ASC, u.task_id ASC`)
	if err != nil {
		t.Fatalf("snapshot deps: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatalf("scan dep: %v", err)
		}
		out = append(out, from+"->"+to)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate deps: %v", err)
	}
	return out
}

func scalarInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var value int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("scalar %q: %v", query, err)
	}
	return value
}

// TestMigrate0003RebuildPreservesRows is the acceptance test for the 0003
// rebuild: counts, values, ids, dependencies and foreign keys survive, and the
// new column is backfilled from the old behaviour.
func TestMigrate0003RebuildPreservesRows(t *testing.T) {
	st := openStoreAtV2(t)
	seedV2Data(t, st)
	ctx := context.Background()

	before := struct {
		tasks         []taskSnapshot
		deps          []string
		taskCount     int
		gateCount     int
		workerKindCnt int
		maxTaskID     int64
	}{
		tasks:         snapshotTasks(t, st.Write()),
		deps:          snapshotDeps(t, st.Write()),
		taskCount:     scalarInt(t, st.Write(), `SELECT COUNT(*) FROM tasks`),
		gateCount:     scalarInt(t, st.Write(), `SELECT COUNT(*) FROM gate_decisions`),
		workerKindCnt: scalarInt(t, st.Write(), `SELECT COUNT(*) FROM worker_kinds`),
		maxTaskID:     int64(scalarInt(t, st.Write(), `SELECT MAX(id) FROM tasks`)),
	}

	applied, err := st.Migrate(ctx)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	if version, err := st.MigrationVersion(ctx); err != nil || version != 3 {
		t.Fatalf("user_version = %d (%v), want 3", version, err)
	}
	if err := st.OpenReadPool(ctx); err != nil {
		t.Fatalf("open read pool: %v", err)
	}

	t.Run("counts", func(t *testing.T) {
		for _, c := range []struct {
			table string
			want  int
		}{
			{"tasks", before.taskCount},
			{"gate_decisions", before.gateCount},
			{"worker_kinds", before.workerKindCnt},
		} {
			if got := scalarInt(t, st.Read(), `SELECT COUNT(*) FROM `+c.table); got != c.want {
				t.Errorf("%s rows = %d, want %d", c.table, got, c.want)
			}
		}
	})

	t.Run("kind_zero_conversion", func(t *testing.T) {
		// Every migrated kind is still one of the four values 0002 could store.
		if got := scalarInt(t, st.Read(), `SELECT COUNT(*) FROM tasks WHERE kind NOT BETWEEN 1 AND 4`); got != 0 {
			t.Errorf("tasks with kind outside 1..4 = %d, want 0", got)
		}
	})

	t.Run("kind_parallel_backfill", func(t *testing.T) {
		if got := scalarInt(t, st.Read(), `SELECT COUNT(*) FROM tasks WHERE kind = 4 AND kind_parallel = 1`); got != 3 {
			t.Errorf("dev cards with kind_parallel = 1 = %d, want 3", got)
		}
		if got := scalarInt(t, st.Read(), `SELECT COUNT(*) FROM tasks WHERE kind <> 4 AND kind_parallel = 1`); got != 0 {
			t.Errorf("non-dev cards with kind_parallel = 1 = %d, want 0", got)
		}
		if got := scalarInt(t, st.Read(), `SELECT COUNT(*) FROM tasks WHERE kind_parallel NOT IN (0, 1)`); got != 0 {
			t.Errorf("kind_parallel outside 0/1 = %d, want 0", got)
		}
	})

	t.Run("rows_identical", func(t *testing.T) {
		after := snapshotTasks(t, st.Read())
		if !reflect.DeepEqual(before.tasks, after) {
			t.Fatalf("tasks changed across 0003:\nbefore = %+v\nafter  = %+v", before.tasks, after)
		}
		if after := snapshotDeps(t, st.Read()); !reflect.DeepEqual(before.deps, after) {
			t.Fatalf("dependencies changed across 0003:\nbefore = %v\nafter  = %v", before.deps, after)
		}
	})

	t.Run("foreign_keys", func(t *testing.T) {
		if err := st.checkForeignKeys(ctx); err != nil {
			t.Fatalf("foreign_key_check: %v", err)
		}
		rows, err := st.Read().QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatalf("PRAGMA foreign_key_check: %v", err)
		}
		defer rows.Close()
		if rows.Next() {
			t.Fatalf("PRAGMA foreign_key_check reported a dangling reference")
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("PRAGMA foreign_key_check rows: %v", err)
		}

		// The children of the rebuilt tables still resolve to their parent rows.
		for _, c := range []struct {
			query string
			label string
		}{
			{`SELECT COUNT(*) FROM task_deps`, "task_deps"},
			{`SELECT COUNT(*) FROM attachments`, "attachments"},
			{`SELECT COUNT(*) FROM workers WHERE current_task_id IS NOT NULL`, "workers.current_task_id"},
			{`SELECT COUNT(*) FROM gate_decisions`, "gate_decisions"},
		} {
			if got := scalarInt(t, st.Read(), c.query); got == 0 {
				t.Errorf("%s: no surviving row", c.label)
			}
		}
	})

	t.Run("indexes", func(t *testing.T) {
		indexes := schemaObjects(t, st, "index")
		for _, name := range []string{
			"ix_tasks_queue", "ix_tasks_lease", "ix_tasks_project",
			"ux_tasks_worker_lease", "ux_tasks_open_per_kind",
			"ix_task_deps_upstream", "ix_attachments_task", "ix_attachments_project",
			"ix_gate_decisions_task", "ix_gate_decisions_material",
		} {
			if !indexes[name] {
				t.Errorf("index %s missing after 0003", name)
			}
		}
		if indexes["ux_tasks_active_doc"] {
			t.Error("ux_tasks_active_doc still exists, want it replaced by ux_tasks_open_per_kind")
		}
	})

	t.Run("ids_continue", func(t *testing.T) {
		// A rowid copy leaves sqlite_sequence behind; the next card must not
		// reuse an id (users would see a recycled t_... reference a different task).
		mustExec(t, st, `
			INSERT INTO tasks (task_id, project_id, kind, kind_parallel, title, create_time, update_time)
			VALUES ('t_fresh', (SELECT id FROM projects WHERE project_id = 'APP-1'), 4, 1, '新卡', ?, ?)`,
			testTimeV2, testTimeV2)
		got := scalarInt(t, st.Read(), `SELECT id FROM tasks WHERE task_id = 't_fresh'`)
		if int64(got) != before.maxTaskID+1 {
			t.Errorf("new task id = %d, want %d (max(id) before + 1)", got, before.maxTaskID+1)
		}
		seq := scalarInt(t, st.Read(), `SELECT seq FROM sqlite_sequence WHERE name = 'tasks'`)
		if seq != got {
			t.Errorf("sqlite_sequence(tasks) = %d, want %d", seq, got)
		}
	})
}

// TestMigrate0003WidensKindChecks pins the widened CHECKs: any positive id is
// now legal for a stage, 0 never is.
func TestMigrate0003WidensKindChecks(t *testing.T) {
	st := openStoreAtV2(t)
	projectPK := seedV2Data(t, st)
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cases := []struct {
		name     string
		exec     bool // true: the statement must succeed, false: it must be rejected
		query    string
		args     []any
		fragment string
	}{
		{"task kind 5 accepted", true, `
			INSERT INTO tasks (task_id, project_id, kind, kind_parallel, title, create_time, update_time)
			VALUES ('t_security', ?, 5, 0, '安全评审', ?, ?)`, []any{projectPK, testTimeV2, testTimeV2}, ""},
		{"task kind 0 rejected", false, `
			INSERT INTO tasks (task_id, project_id, kind, title, create_time, update_time)
			VALUES ('t_zero', ?, 0, '零环节', ?, ?)`, []any{projectPK, testTimeV2, testTimeV2}, "CHECK"},
		{"gate 5 accepted", true, `
			INSERT INTO gate_decisions (task_id, gate, decision, decided_by, create_time)
			VALUES ('t_req_done', 5, 1, 'cto', ?)`, []any{testTimeV2}, ""},
		{"gate 0 rejected", false, `
			INSERT INTO gate_decisions (task_id, gate, decision, decided_by, create_time)
			VALUES ('t_req_done', 0, 1, 'cto', ?)`, []any{testTimeV2}, "CHECK"},
		{"worker kind 5 accepted", true, `INSERT INTO worker_kinds (worker_id, kind) VALUES ('w_1', 5)`, nil, ""},
		{"worker kind 0 rejected", false, `INSERT INTO worker_kinds (worker_id, kind) VALUES ('w_1', 0)`, nil, "CHECK"},
		{"kind_parallel 2 rejected", false, `
			UPDATE tasks SET kind_parallel = 2 WHERE task_id = 't_design'`, nil, "CHECK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.Write().ExecContext(context.Background(), tc.query, tc.args...)
			if tc.exec {
				if err != nil {
					t.Fatalf("statement failed: %v", err)
				}
				return
			}
			wantSQLError(t, err, tc.fragment)
		})
	}
}

// TestMigrate0003OpenPerKindIndex pins D4″ behaviour: the new index rejects a
// second open card of a single-card stage and lets a multiple-card stage keep
// several.
func TestMigrate0003OpenPerKindIndex(t *testing.T) {
	st := openStoreAtV2(t)
	projectPK := seedV2Data(t, st)
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()

	// t_design (kind 2, pending, kind_parallel = 0) already occupies the slot.
	_, err := st.Write().ExecContext(ctx, `
		INSERT INTO tasks (task_id, project_id, kind, title, create_time, update_time)
		VALUES ('t_design_2', ?, 2, '第二份方案', ?, ?)`, projectPK, testTimeV2, testTimeV2)
	wantSQLError(t, err, "UNIQUE")

	// A finished card leaves the active set, so the next one is accepted.
	mustExec(t, st, `UPDATE tasks SET status = 5 WHERE task_id = 't_design'`)
	mustExec(t, st, `
		INSERT INTO tasks (task_id, project_id, kind, title, create_time, update_time)
		VALUES ('t_design_3', ?, 2, '第三份方案', ?, ?)`, projectPK, testTimeV2, testTimeV2)

	// kind_parallel = 1 (every migrated dev card) is exempt from the cap.
	for i := 1; i <= 3; i++ {
		mustExec(t, st, `
			INSERT INTO tasks (task_id, project_id, kind, kind_parallel, title, create_time, update_time)
			VALUES (?, ?, 4, 1, '并行开发', ?, ?)`, fmt.Sprintf("t_dev_extra_%d", i), projectPK, testTimeV2, testTimeV2)
	}
	if got := scalarInt(t, st.Read(), `SELECT COUNT(*) FROM tasks WHERE kind = 4 AND status IN (1, 2, 3, 4)`); got != 5 {
		t.Errorf("open dev cards = %d, want 5", got)
	}

	// The cap is per (project, kind): another project is unaffected.
	res := mustExec(t, st, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP2', '驿传二', 'https://example.com/repo2.git', ?, ?)`, testTimeV2, testTimeV2)
	productPK, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product id: %v", err)
	}
	otherProject := insertV3Project(t, st, productPK, "APP2-1", 1)
	mustExec(t, st, `
		INSERT INTO tasks (task_id, project_id, kind, title, create_time, update_time)
		VALUES ('t_design_other', ?, 2, '另一个项目的方案', ?, ?)`, otherProject, testTimeV2, testTimeV2)
}
