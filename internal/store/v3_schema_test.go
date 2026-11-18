package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// V3 schema acceptance tests.
//
// These tests pin the V3 initial schema (tech design v2.0 §2): the project /
// task / dependency / attachment / decision / event tables, integer enums that
// start at 1, the queue and lease indexes, and the schema-version guard.
//
// They are deliberately self-contained (raw SQL, no store business methods) so
// that the schema can be verified before the service refactor lands.

const testTime = "2026-11-18T00:00:00.000Z"

// mustExec runs one statement and fails the test on error.
func mustExec(t *testing.T, st *Store, query string, args ...any) sql.Result {
	t.Helper()
	res, err := st.Write().ExecContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	return res
}

// insertV3Product creates one product and returns its physical id.
func insertV3Product(t *testing.T, st *Store, code string) int64 {
	t.Helper()
	res := mustExec(t, st, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES (?, ?, 'https://example.com/repo.git', ?, ?)`, code, code, testTime, testTime)
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product id: %v", err)
	}
	return id
}

// insertV3Project creates one project and returns its physical id.
func insertV3Project(t *testing.T, st *Store, productPK int64, code string, seq int) int64 {
	t.Helper()
	res := mustExec(t, st, `
		INSERT INTO projects (project_id, product_id, seq_no, name, context_md, status, create_time, update_time)
		VALUES (?, ?, ?, ?, '背景上下文', 1, ?, ?)`, code, productPK, seq, code, testTime, testTime)
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

// insertV3Task creates one task. leaseWorker is empty for no lease; when set,
// the lease columns are populated (required for status=3).
func insertV3Task(t *testing.T, st *Store, projectPK int64, taskID string, kind, status int, leaseWorker string) int64 {
	t.Helper()

	var (
		leaseID    any
		leaseUntil any
		workerID   any
	)
	if leaseWorker != "" {
		leaseID = "l_" + leaseWorker
		leaseUntil = "2099-01-01T00:00:00.000Z"
		workerID = leaseWorker
	}
	res := mustExec(t, st, `
		INSERT INTO tasks (task_id, project_id, kind, title, description_md, context_md,
		                   status, lease_id, lease_until, worker_id, create_time, update_time)
		VALUES (?, ?, ?, ?, '描述', '上下文', ?, ?, ?, ?, ?, ?)`,
		taskID, projectPK, kind, taskID, status, leaseID, leaseUntil, workerID, testTime, testTime)
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("task id: %v", err)
	}
	return id
}

// insertV3Material creates one material and returns its physical id.
func insertV3Material(t *testing.T, st *Store, materialID string) int64 {
	t.Helper()
	res := mustExec(t, st, `
		INSERT INTO materials (material_id, kind, version, title, sha256, size, path, uploaded_by, create_time, update_time)
		VALUES (?, 1, 1, '资料', 'sha', 0, '/tmp/object', 'tester', ?, ?)`,
		materialID, testTime, testTime)
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("material id: %v", err)
	}
	return id
}

func wantSQLError(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", fragment)
	}
	msg := err.Error()
	if fragment != "" && !strings.Contains(msg, fragment) {
		t.Fatalf("error = %v, want it to contain %q", err, fragment)
	}
}

// TestV3SchemaTables covers the table and index surface: V3 tables exist, V2
// tables are gone, and the schema version is 1.
func TestV3SchemaTables(t *testing.T) {
	st := openTestStore(t, 2)

	tables := schemaObjects(t, st, "table")
	for _, name := range []string{
		"products", "materials", "projects", "tasks", "task_deps",
		"attachments", "gate_decisions", "task_events", "workers", "worker_kinds",
		"im_card", "im_action_log", "im_doc",
	} {
		if !tables[name] {
			t.Errorf("table %s missing; have %v", name, tables)
		}
	}
	for _, gone := range []string{"project_specs", "work_items", "work_inputs", "work_deps", "work_item_events"} {
		if tables[gone] {
			t.Errorf("table %s must not exist in V3", gone)
		}
	}

	indexes := schemaObjects(t, st, "index")
	for _, name := range []string{
		"ix_materials_chain", "ix_projects_status", "ix_projects_product",
		"ix_tasks_queue", "ix_tasks_lease", "ix_tasks_project",
		"ux_tasks_active_doc", "ux_tasks_worker_lease",
		"ix_task_deps_upstream", "ix_attachments_task", "ix_attachments_project",
		"ix_attachments_material", "ix_gate_decisions_task", "ix_gate_decisions_material",
		"ix_task_events_task", "ix_workers_last_seen", "ux_workers_current_task",
		"ix_im_card_open", "ux_im_card_message",
	} {
		if !indexes[name] {
			t.Errorf("index %s missing", name)
		}
	}
	for _, gone := range []string{"ix_work_queue", "ix_work_lease", "ux_work_active_phase", "ux_work_worker", "ux_projects_dev_branch"} {
		if indexes[gone] {
			t.Errorf("index %s must not exist in V3", gone)
		}
	}

	version, err := st.MigrationVersion(context.Background())
	if err != nil {
		t.Fatalf("MigrationVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
}

// TestV3SchemaColumns pins the shape changes: projects lose the workflow
// columns; tasks gain context/lease columns; workers point at tasks.
func TestV3SchemaColumns(t *testing.T) {
	st := openTestStore(t, 2)

	projectCols := schemaColumns(t, st, "projects")
	for _, gone := range []string{
		"requirement_material_id", "spec_material_id", "dev_branch", "reason",
		"version", "tasks_review_reason", "tasks_review_rejected_at",
	} {
		if projectCols[gone] {
			t.Errorf("projects.%s must not exist in V3", gone)
		}
	}
	for _, want := range []string{"project_id", "product_id", "seq_no", "name", "context_md", "status", "closed_at"} {
		if !projectCols[want] {
			t.Errorf("projects.%s missing", want)
		}
	}

	taskCols := schemaColumns(t, st, "tasks")
	for _, want := range []string{
		"task_id", "project_id", "parent_task_id", "kind", "title",
		"description_md", "context_md", "priority", "status", "attempt",
		"max_attempts", "not_before", "lease_id", "lease_until", "worker_id", "reason",
	} {
		if !taskCols[want] {
			t.Errorf("tasks.%s missing", want)
		}
	}

	workerCols := schemaColumns(t, st, "workers")
	if workerCols["current_work_item_id"] {
		t.Error("workers.current_work_item_id must not exist in V3")
	}
	if !workerCols["current_task_id"] {
		t.Error("workers.current_task_id missing")
	}
}

// TestV3EnumChecks covers every integer enum: 0 and out-of-range values are
// rejected by CHECK constraints.
func TestV3EnumChecks(t *testing.T) {
	st := openTestStore(t, 2)

	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)
	taskPK := insertV3Task(t, st, projectPK, "t_ok", 4, 1, "")
	materialPK := insertV3Material(t, st, "m_ok")

	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{"project status 0", `UPDATE projects SET status = 0 WHERE id = ?`, []any{projectPK}},
		{"project status 3", `UPDATE projects SET status = 3 WHERE id = ?`, []any{projectPK}},
		{"task kind 0", `UPDATE tasks SET kind = 0 WHERE id = ?`, []any{taskPK}},
		{"task kind 5", `UPDATE tasks SET kind = 5 WHERE id = ?`, []any{taskPK}},
		{"task status 0", `UPDATE tasks SET status = 0 WHERE id = ?`, []any{taskPK}},
		{"task status 8", `UPDATE tasks SET status = 8 WHERE id = ?`, []any{taskPK}},
		{"material kind 0", `UPDATE materials SET kind = 0 WHERE id = ?`, []any{materialPK}},
		{"material kind 7", `UPDATE materials SET kind = 7 WHERE id = ?`, []any{materialPK}},
		{"worker state 0", `INSERT INTO workers (worker_id, state, first_seen, last_seen, create_time, update_time) VALUES ('w_bad', 0, ?, ?, ?, ?)`, []any{testTime, testTime, testTime, testTime}},
		{"worker state 3", `INSERT INTO workers (worker_id, state, first_seen, last_seen, create_time, update_time) VALUES ('w_bad2', 3, ?, ?, ?, ?)`, []any{testTime, testTime, testTime, testTime}},
		{"worker kind 0", `INSERT INTO worker_kinds (worker_id, kind) VALUES ('w_bad', 0)`, nil},
		{"worker kind 5", `INSERT INTO worker_kinds (worker_id, kind) VALUES ('w_bad', 5)`, nil},
		{"im subject_kind 0", `INSERT INTO im_card (subject_kind, subject_id, chat_id, message_id, fingerprint, sent_at, rendered_at) VALUES (0, 'x', 'c', 'm', 'f', ?, ?)`, []any{testTime, testTime}},
		{"im subject_kind 3", `INSERT INTO im_card (subject_kind, subject_id, chat_id, message_id, fingerprint, sent_at, rendered_at) VALUES (3, 'x', 'c', 'm', 'f', ?, ?)`, []any{testTime, testTime}},
		{"event 0", `INSERT INTO task_events (task_id, event, create_time) VALUES (?, 0, ?)`, []any{taskPK, testTime}},
		{"event 12", `INSERT INTO task_events (task_id, event, create_time) VALUES (?, 12, ?)`, []any{taskPK, testTime}},
		{"decision 0", `INSERT INTO gate_decisions (task_id, gate, decision, decided_by, create_time) VALUES ('t_ok', 4, 0, 'tester', ?)`, []any{testTime}},
		{"decision 3", `INSERT INTO gate_decisions (task_id, gate, decision, decided_by, create_time) VALUES ('t_ok', 4, 3, 'tester', ?)`, []any{testTime}},
		{"attachment role 0", `INSERT INTO attachments (task_id, material_id, role, create_time) VALUES (?, ?, 0, ?)`, []any{taskPK, materialPK, testTime}},
		{"attachment role 3", `INSERT INTO attachments (task_id, material_id, role, create_time) VALUES (?, ?, 3, ?)`, []any{taskPK, materialPK, testTime}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.Write().ExecContext(context.Background(), tc.query, tc.args...)
			wantSQLError(t, err, "")
		})
	}
}

// TestV3TaskLeaseCheck pins the "in_progress implies lease" invariant.
func TestV3TaskLeaseCheck(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)

	_, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, kind, title, status, create_time, update_time)
		VALUES ('t_nolease', ?, 4, '无租约', 3, ?, ?)`, projectPK, testTime, testTime)
	wantSQLError(t, err, "CHECK")

	taskPK := insertV3Task(t, st, projectPK, "t_leased", 4, 3, "w_1")

	// Reporting releases the lease and moves to in_review in one update.
	mustExec(t, st, `UPDATE tasks SET status = 4, lease_id = NULL, lease_until = NULL, worker_id = NULL WHERE id = ?`, taskPK)

	var status int
	if err := st.Read().QueryRowContext(context.Background(), `SELECT status FROM tasks WHERE id = ?`, taskPK).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != 4 {
		t.Errorf("status = %d, want 4", status)
	}
}

// TestV3AttachmentOwnerCheck pins the "exactly one owner" rule.
func TestV3AttachmentOwnerCheck(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)
	taskPK := insertV3Task(t, st, projectPK, "t_1", 4, 1, "")
	materialPK := insertV3Material(t, st, "m_1")

	_, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO attachments (material_id, role, create_time) VALUES (?, 1, ?)`, materialPK, testTime)
	wantSQLError(t, err, "CHECK")

	_, err = st.Write().ExecContext(context.Background(), `
		INSERT INTO attachments (project_id, task_id, material_id, role, create_time)
		VALUES (?, ?, ?, 1, ?)`, projectPK, taskPK, materialPK, testTime)
	wantSQLError(t, err, "CHECK")

	mustExec(t, st, `INSERT INTO attachments (project_id, material_id, role, create_time) VALUES (?, ?, 2, ?)`, projectPK, materialPK, testTime)
	mustExec(t, st, `INSERT INTO attachments (task_id, material_id, role, create_time) VALUES (?, ?, 2, ?)`, taskPK, materialPK, testTime)
}

// TestV3TaskDepSelfCheck rejects a task depending on itself.
func TestV3TaskDepSelfCheck(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)
	taskPK := insertV3Task(t, st, projectPK, "t_1", 4, 1, "")

	_, err := st.Write().ExecContext(context.Background(),
		`INSERT INTO task_deps (task_id, depends_on_task_id) VALUES (?, ?)`, taskPK, taskPK)
	wantSQLError(t, err, "CHECK")
}

// TestV3ActiveDocUnique allows one active doc task per kind and unlimited dev
// tasks.
func TestV3ActiveDocUnique(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)

	first := insertV3Task(t, st, projectPK, "t_req_1", 1, 1, "")

	_, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, kind, title, status, create_time, update_time)
		VALUES ('t_req_2', ?, 1, '第二条同名需求', 1, ?, ?)`, projectPK, testTime, testTime)
	wantSQLError(t, err, "UNIQUE")

	// Once the first is done it leaves the active set.
	mustExec(t, st, `UPDATE tasks SET status = 5 WHERE id = ?`, first)
	insertV3Task(t, st, projectPK, "t_req_3", 1, 1, "")

	// dev is exempt from the per-kind active uniqueness.
	insertV3Task(t, st, projectPK, "t_dev_1", 4, 1, "")
	insertV3Task(t, st, projectPK, "t_dev_2", 4, 1, "")
}

// TestV3WorkerLeaseUnique pins one lease per worker.
func TestV3WorkerLeaseUnique(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)

	insertV3Task(t, st, projectPK, "t_dev_1", 4, 3, "w_1")

	_, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, kind, title, status, lease_id, lease_until, worker_id, create_time, update_time)
		VALUES ('t_dev_2', ?, 4, '第二张租约', 3, 'l_2', '2099-01-01T00:00:00.000Z', 'w_1', ?, ?)`, projectPK, testTime, testTime)
	wantSQLError(t, err, "UNIQUE")

	// A different worker can hold its own lease.
	mustExec(t, st, `
		INSERT INTO tasks (task_id, project_id, kind, title, status, lease_id, lease_until, worker_id, create_time, update_time)
		VALUES ('t_dev_3', ?, 4, '另一个 worker', 3, 'l_3', '2099-01-01T00:00:00.000Z', 'w_2', ?, ?)`, projectPK, testTime, testTime)
}

// TestV3Defaults pins the defaults that encode "0 is illegal".
func TestV3Defaults(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)

	var projectStatus int
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT status FROM projects WHERE id = ?`, projectPK).Scan(&projectStatus); err != nil {
		t.Fatalf("project status: %v", err)
	}
	if projectStatus != 1 {
		t.Errorf("project status = %d, want 1", projectStatus)
	}

	mustExec(t, st, `
		INSERT INTO tasks (task_id, project_id, kind, title, create_time, update_time)
		VALUES ('t_default', ?, 4, '默认值', ?, ?)`, projectPK, testTime, testTime)

	var (
		status      int
		attempt     int
		maxAttempts int
	)
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT status, attempt, max_attempts FROM tasks WHERE task_id = 't_default'`).
		Scan(&status, &attempt, &maxAttempts); err != nil {
		t.Fatalf("task defaults: %v", err)
	}
	if status != 1 || attempt != 0 || maxAttempts != 3 {
		t.Errorf("task defaults = status %d, attempt %d, max %d; want 1, 0, 3", status, attempt, maxAttempts)
	}
}

// TestV3SchemaVersionGuard refuses a database newer than the binary.
func TestV3SchemaVersionGuard(t *testing.T) {
	st := openTestStore(t, 2)

	if _, err := st.Write().ExecContext(context.Background(), `PRAGMA user_version = 6`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	_, err := st.Migrate(context.Background())
	var versionErr *SchemaVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("Migrate error = %v, want *SchemaVersionError", err)
	}
	if versionErr.Database != 6 || versionErr.Binary != 1 {
		t.Errorf("SchemaVersionError = %+v, want database 6 / binary 1", versionErr)
	}
}

// TestV3MigrationIdempotent runs Migrate twice: the second call must apply 0.
func TestV3MigrationIdempotent(t *testing.T) {
	st := openTestStore(t, 2)

	applied, err := st.Migrate(context.Background())
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied = %d, want 0", applied)
	}
}

// TestV3ProjectDeleteRestricted pins the foreign key from tasks to projects.
func TestV3ProjectDeleteRestricted(t *testing.T) {
	st := openTestStore(t, 2)
	productPK := insertV3Product(t, st, "APP")
	projectPK := insertV3Project(t, st, productPK, "APP-1", 1)
	insertV3Task(t, st, projectPK, "t_1", 4, 1, "")

	_, err := st.Write().ExecContext(context.Background(), `DELETE FROM projects WHERE id = ?`, projectPK)
	wantSQLError(t, err, "FOREIGN KEY")

	// Clean up the task first, then the delete succeeds.
	mustExec(t, st, `DELETE FROM tasks WHERE project_id = ?`, projectPK)
	mustExec(t, st, `DELETE FROM projects WHERE id = ?`, projectPK)
}
