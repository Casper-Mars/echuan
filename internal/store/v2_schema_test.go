package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// v2 schema acceptance tests.
//
// These tests pin the v2 initial schema (tech design v0.8 §6): the five
// work-item tables, the workers foreign-key rename, the CHECK constraints, the
// three queue/lease/phase indexes and the schema-version guard. They are
// deliberately self-contained (raw SQL, no store business methods) so that the
// schema can be verified before the store refactor lands.

// insertV2WorkItem inserts one work_items row and returns its id. workerID is
// nil for an unowned item.
func insertV2WorkItem(t *testing.T, st *Store, projectID int64, workID, kind, state string, workerID any) int64 {
	t.Helper()

	res, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO work_items (work_id, project_id, kind, title, state, worker_id, create_time, update_time)
		VALUES (?, ?, ?, '工作项', ?, ?, '2026-11-02T00:00:00.000Z', '2026-11-02T00:00:00.000Z')`,
		workID, projectID, kind, state, workerID)
	if err != nil {
		t.Fatalf("insert work item %s (%s/%s): %v", workID, kind, state, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("work item %s id: %v", workID, err)
	}
	return id
}

// wantSQLError asserts that err is non-nil and its message contains fragment.
func wantSQLError(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", fragment)
	}
	if !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error = %v, want it to contain %q", err, fragment)
	}
}

// TestV2SchemaTables covers acceptance 1: a fresh database contains exactly the
// five work-item tables (and the retained domain tables), no tasks/task_deps,
// at user_version 1, with the documented index names.
func TestV2SchemaTables(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()

	tables := schemaObjects(t, st, "table")
	for _, name := range []string{
		"products", "materials", "projects", "project_specs",
		"work_items", "work_inputs", "work_deps", "work_item_events",
		"workers", "worker_kinds",
	} {
		if !tables[name] {
			t.Errorf("table %s missing; have %v", name, tables)
		}
	}
	for _, gone := range []string{"tasks", "task_deps"} {
		if tables[gone] {
			t.Errorf("table %s must not exist in v2", gone)
		}
	}

	indexes := schemaObjects(t, st, "index")
	for _, name := range []string{
		"ix_work_queue", "ix_work_lease", "ux_work_active_phase", "ux_work_worker",
		"ix_work_events_item", "ux_workers_current_work_item", "ix_workers_last_seen",
		"ix_materials_chain", "ix_projects_inbox",
	} {
		if !indexes[name] {
			t.Errorf("index %s missing; have %v", name, indexes)
		}
	}
	for _, gone := range []string{"ix_tasks_queue", "ix_tasks_lease", "ux_workers_current_task"} {
		if indexes[gone] {
			t.Errorf("index %s must not exist in v2", gone)
		}
	}

	version, err := st.MigrationVersion(ctx)
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
	if latest != 1 {
		t.Errorf("latest embedded migration = %d, want 1", latest)
	}

	workerColumns := schemaColumns(t, st, "workers")
	if !workerColumns["current_work_item_id"] {
		t.Errorf("workers.current_work_item_id missing; have %v", workerColumns)
	}
	if workerColumns["current_task_id"] {
		t.Errorf("workers.current_task_id must be gone; have %v", workerColumns)
	}

	// workers.current_work_item_id must reference work_items(id), not tasks.
	rows, err := st.Read().QueryContext(ctx, `PRAGMA foreign_key_list(workers)`)
	if err != nil {
		t.Fatalf("foreign_key_list(workers): %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			id, seq         int
			table, from, to string
			onUpdate        string
			onDelete        string
			match           string
		)
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign_key_list: %v", err)
		}
		if from == "current_work_item_id" && table == "work_items" && to == "id" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_list rows: %v", err)
	}
	if !found {
		t.Error("workers.current_work_item_id does not reference work_items(id)")
	}

	// worker_kinds uses a composite primary key (worker_id, kind).
	pk := map[string]int{}
	cols, err := st.Read().QueryContext(ctx, `PRAGMA table_info(worker_kinds)`)
	if err != nil {
		t.Fatalf("table_info(worker_kinds): %v", err)
	}
	defer cols.Close()
	for cols.Next() {
		var (
			cid       int
			name      string
			columnTyp string
			notNull   int
			def       any
			primary   int
		)
		if err := cols.Scan(&cid, &name, &columnTyp, &notNull, &def, &primary); err != nil {
			t.Fatalf("scan table_info(worker_kinds): %v", err)
		}
		if primary > 0 {
			pk[name] = primary
		}
	}
	if len(pk) != 2 || pk["worker_id"] != 1 || pk["kind"] != 2 {
		t.Errorf("worker_kinds primary key = %v, want worker_id=1,kind=2", pk)
	}
}

// TestV2SchemaKindCheck covers acceptance 2: the four v2 kinds insert, `merge`
// (and any unknown kind) is rejected by the CHECK constraint.
func TestV2SchemaKindCheck(t *testing.T) {
	st := openTestStore(t, 2)
	projectID := seedProject(t, st)

	for _, kind := range []string{"requirement", "design", "breakdown", "dev"} {
		insertV2WorkItem(t, st, projectID, "w_"+kind, kind, "queued", nil)
	}

	for _, kind := range []string{"merge", "review", "unknown"} {
		_, err := st.Write().ExecContext(context.Background(), `
			INSERT INTO work_items (work_id, project_id, kind, title, create_time, update_time)
			VALUES (?, ?, ?, 't', 't', 't')`,
			"w_bad_"+kind, projectID, kind)
		wantSQLError(t, err, "CHECK constraint failed")
	}
}

// TestV2SchemaActivePhaseUnique covers acceptance 3: at most one active phase
// item per (project, kind); terminal states do not block a new item.
func TestV2SchemaActivePhaseUnique(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)

	for _, state := range []string{"blocked", "queued", "leased", "submitted"} {
		// Each kind is exercised once, so the first insert always succeeds.
		var kind string
		switch state {
		case "blocked":
			kind = "requirement"
		case "submitted":
			kind = "design"
		case "leased":
			kind = "breakdown"
		default:
			kind = "requirement"
		}

		var workerID any
		if state == "leased" {
			workerID = "w_holder"
		}
		insertV2WorkItem(t, st, projectID, "w_first_"+state, kind, state, workerID)

		// A second active item of the same (project, kind) must be rejected.
		_, err := st.Write().ExecContext(ctx, `
			INSERT INTO work_items (work_id, project_id, kind, title, state, worker_id, create_time, update_time)
			VALUES (?, ?, ?, 't', ?, NULL, 't', 't')`,
			"w_second_"+state, projectID, kind, "queued")
		wantSQLError(t, err, "UNIQUE constraint failed")

		// Move the blocker to a terminal state so the next iteration starts clean.
		if state != "blocked" {
			if _, err := st.Write().ExecContext(ctx,
				`DELETE FROM work_items WHERE work_id = ?`, "w_first_"+state); err != nil {
				t.Fatalf("cleanup %s: %v", state, err)
			}
			continue
		}
		if _, err := st.Write().ExecContext(ctx,
			`UPDATE work_items SET state = 'accepted' WHERE work_id = ?`, "w_first_blocked"); err != nil {
			t.Fatalf("accept requirement: %v", err)
		}

		// Terminal states are not part of the partial index: after accepted the
		// same phase can be queued again.
		insertV2WorkItem(t, st, projectID, "w_after_accept", "requirement", "queued", nil)

		// A cancelled sibling never blocks the queue.
		insertV2WorkItem(t, st, projectID, "w_cancelled_sibling", "requirement", "cancelled", nil)

		// Cleanup for the remaining iterations.
		if _, err := st.Write().ExecContext(ctx,
			`DELETE FROM work_items WHERE work_id IN ('w_after_accept','w_cancelled_sibling')`); err != nil {
			t.Fatalf("cleanup requirement: %v", err)
		}
	}

	// dev items are not phase-scoped: any number may be active at once.
	insertV2WorkItem(t, st, projectID, "w_dev_1", "dev", "queued", nil)
	insertV2WorkItem(t, st, projectID, "w_dev_2", "dev", "blocked", nil)
	insertV2WorkItem(t, st, projectID, "w_dev_3", "dev", "submitted", nil)
}

// TestV2SchemaWorkerSingleLease covers acceptance 4: one worker holds at most
// one leased item; submitted/queued items are not restricted by that index.
// It also pins ux_workers_current_work_item on the workers table.
func TestV2SchemaWorkerSingleLease(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)

	first := insertV2WorkItem(t, st, projectID, "w_lease_1", "dev", "leased", "w_one")

	// Same worker, second leased item: rejected.
	_, err := st.Write().ExecContext(ctx, `
		INSERT INTO work_items (work_id, project_id, kind, title, state, worker_id, create_time, update_time)
		VALUES ('w_lease_2', ?, 'dev', 't', 'leased', 'w_one', 't', 't')`, projectID)
	wantSQLError(t, err, "UNIQUE constraint failed")

	// Same worker may hold submitted / queued items.
	insertV2WorkItem(t, st, projectID, "w_submitted", "dev", "submitted", "w_one")
	insertV2WorkItem(t, st, projectID, "w_queued", "dev", "queued", "w_one")

	// Another worker may lease a different item.
	insertV2WorkItem(t, st, projectID, "w_lease_3", "dev", "leased", "w_two")

	// workers.current_work_item_id: two rows may not point at one item.
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, current_work_item_id, first_seen, last_seen, create_time, update_time)
		VALUES ('w_one', '{}', 'busy', ?, 't', 't', 't', 't')`, first); err != nil {
		t.Fatalf("insert worker w_one: %v", err)
	}
	_, err = st.Write().ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, current_work_item_id, first_seen, last_seen, create_time, update_time)
		VALUES ('w_two', '{}', 'busy', ?, 't', 't', 't', 't')`, first)
	wantSQLError(t, err, "UNIQUE constraint failed")

	// NULL pointers are unrestricted: idle workers may coexist.
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, first_seen, last_seen, create_time, update_time)
		VALUES ('w_three', '{}', 'idle', 't', 't', 't', 't')`); err != nil {
		t.Fatalf("insert idle worker: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, first_seen, last_seen, create_time, update_time)
		VALUES ('w_four', '{}', 'idle', 't', 't', 't', 't')`); err != nil {
		t.Fatalf("insert second idle worker: %v", err)
	}
}

// TestV2SchemaStateCheck covers acceptance 5: unknown states are rejected.
func TestV2SchemaStateCheck(t *testing.T) {
	st := openTestStore(t, 2)
	projectID := seedProject(t, st)

	for _, state := range []string{"running", "in_progress", "backlog", "ready", "done", "pending_acceptance"} {
		_, err := st.Write().ExecContext(context.Background(), `
			INSERT INTO work_items (work_id, project_id, kind, title, state, create_time, update_time)
			VALUES (?, ?, 'dev', 't', ?, 't', 't')`,
			"w_bad_"+state, projectID, state)
		wantSQLError(t, err, "CHECK constraint failed")
	}

	for _, state := range []string{"blocked", "queued", "leased", "submitted", "accepted", "failed", "cancelled"} {
		insertV2WorkItem(t, st, projectID, "w_state_"+state, "dev", state, nil)
	}
}

// TestV2SchemaForeignKeys pins acceptance 6: work_inputs / work_deps /
// work_item_events / workers reject references to missing rows.
func TestV2SchemaForeignKeys(t *testing.T) {
	st := openTestStore(t, 2)
	ctx := context.Background()
	projectID := seedProject(t, st)
	itemID := insertV2WorkItem(t, st, projectID, "w_fk", "dev", "queued", nil)

	materialRes, err := st.Write().ExecContext(ctx, `
		INSERT INTO materials (material_id, kind, version, title, sha256, size, path, uploaded_by, create_time, update_time)
		VALUES ('m_1', 3, 1, '报告', 'sha', 1, '/tmp/m1', 'tester', 't', 't')`)
	if err != nil {
		t.Fatalf("insert material: %v", err)
	}
	materialID, err := materialRes.LastInsertId()
	if err != nil {
		t.Fatalf("material id: %v", err)
	}

	// Valid rows.
	if _, err := st.Write().ExecContext(ctx,
		`INSERT INTO work_inputs (work_item_id, material_id) VALUES (?, ?)`, itemID, materialID); err != nil {
		t.Fatalf("insert work_inputs: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx,
		`INSERT INTO work_deps (work_item_id, depends_on_id) VALUES (?, ?)`, itemID, itemID); err != nil {
		t.Fatalf("insert work_deps: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO work_item_events (work_item_id, event, actor, create_time) VALUES (?, 'claimed', 'w', 't')`,
		itemID); err != nil {
		t.Fatalf("insert work_item_events: %v", err)
	}

	// Dangling references.
	_, err = st.Write().ExecContext(ctx,
		`INSERT INTO work_inputs (work_item_id, material_id) VALUES (9999, ?)`, materialID)
	wantSQLError(t, err, "FOREIGN KEY constraint failed")

	_, err = st.Write().ExecContext(ctx,
		`INSERT INTO work_inputs (work_item_id, material_id) VALUES (?, 9999)`, itemID)
	wantSQLError(t, err, "FOREIGN KEY constraint failed")

	_, err = st.Write().ExecContext(ctx,
		`INSERT INTO work_deps (work_item_id, depends_on_id) VALUES (?, 9999)`, itemID)
	wantSQLError(t, err, "FOREIGN KEY constraint failed")

	_, err = st.Write().ExecContext(ctx,
		`INSERT INTO work_item_events (work_item_id, event, create_time) VALUES (9999, 'claimed', 't')`)
	wantSQLError(t, err, "FOREIGN KEY constraint failed")

	_, err = st.Write().ExecContext(ctx, `
		INSERT INTO workers (worker_id, metadata_json, state, current_work_item_id, first_seen, last_seen, create_time, update_time)
		VALUES ('w_dangling', '{}', 'busy', 9999, 't', 't', 't', 't')`)
	wantSQLError(t, err, "FOREIGN KEY constraint failed")

	// work_inputs is a composite primary key: the same pair cannot repeat.
	_, err = st.Write().ExecContext(ctx,
		`INSERT INTO work_inputs (work_item_id, material_id) VALUES (?, ?)`, itemID, materialID)
	wantSQLError(t, err, "UNIQUE constraint failed")
}

// TestV2SchemaRejectsNewerSchema pins the startup guard: a database newer than
// the binary must be refused instead of being silently downgraded.
func TestV2SchemaRejectsNewerSchema(t *testing.T) {
	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.Write().ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	_, err = st.Migrate(ctx)
	var versionErr *SchemaVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("error = %v, want SchemaVersionError", err)
	}
	if versionErr.Database != 99 || versionErr.Binary != 1 {
		t.Errorf("versions = %d/%d, want 99/1", versionErr.Database, versionErr.Binary)
	}

	var tables int
	if err := st.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'work_items'`).Scan(&tables); err != nil {
		t.Fatalf("count work_items: %v", err)
	}
	if tables != 0 {
		t.Error("work_items was created despite the version mismatch")
	}
}
