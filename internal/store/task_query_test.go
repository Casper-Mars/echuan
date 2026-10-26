package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// insertTasks inserts n backlog tasks for projectID and returns their physical
// ids in seq order. priorities[i] is the priority of seq i+1.
func insertTasks(t *testing.T, st *Store, projectID int64, prefix string, priorities []int) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, len(priorities))
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		for i, priority := range priorities {
			seq := i + 1
			id, err := CreateTask(ctx, tx, NewTask{
				TaskID:     taskCode(prefix, seq),
				ProjectID:  projectID,
				SeqNo:      seq,
				Title:      taskCode(prefix, seq),
				BodyMD:     "b",
				Priority:   priority,
				Status:     0,
				CreateTime: "2026-01-01T00:00:00Z",
				UpdateTime: "2026-01-01T00:00:00Z",
			})
			if err != nil {
				return err
			}
			ids[i] = id
		}
		return nil
	})
	if err != nil {
		t.Fatalf("insert tasks: %v", err)
	}
	return ids
}

func taskCode(prefix string, seq int) string {
	return prefix + "-T" + pad4(seq)
}

func pad4(n int) string {
	digits := []byte{'0', '0', '0', '0'}
	for i := 3; i >= 0 && n > 0; i-- {
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits)
}

func TestListTasksOrdersByPriorityThenSeq(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	// seq 1..5 with priorities that force a different order.
	insertTasks(t, st, projectID, "APP-1", []int{0, 10, 5, 10, -1000})

	rows, err := ListTasks(ctx, st.Read(), TaskFilter{ProjectID: projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	want := []string{"APP-1-T0002", "APP-1-T0004", "APP-1-T0003", "APP-1-T0001", "APP-1-T0005"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i, row := range rows {
		if row.TaskID != want[i] {
			t.Errorf("rows[%d] = %s, want %s", i, row.TaskID, want[i])
		}
	}
}

func TestListTasksKeysetPagesWithoutGapsOrDuplicates(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	// 25 rows; priorities repeat so the (priority, seq_no, id) tie-break is
	// exercised, not just the leading column.
	priorities := make([]int, 25)
	for i := range priorities {
		priorities[i] = i % 3
	}
	insertTasks(t, st, projectID, "APP-1", priorities)

	seen := make(map[string]int)
	var after *TaskCursor
	pages := 0
	for {
		rows, err := ListTasks(ctx, st.Read(), TaskFilter{
			ProjectID: projectID,
			After:     after,
			Limit:     4,
		})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			seen[row.TaskID]++
		}
		last := rows[len(rows)-1]
		after = &TaskCursor{Priority: last.Priority, SeqNo: last.SeqNo, ID: last.ID}
		if len(rows) < 4 {
			break
		}
	}
	if len(seen) != 25 {
		t.Fatalf("distinct tasks = %d, want 25", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("task %s appeared %d times", id, count)
		}
	}
	if pages != 7 { // 6 full pages + 1 row
		t.Errorf("pages = %d, want 7", pages)
	}
}

func TestListTasksFiltersStatusAndProject(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	insertTasks(t, st, projectID, "APP-1", []int{0, 0, 0})

	if _, err := st.Write().ExecContext(ctx,
		`UPDATE tasks SET status = 2 WHERE task_id = 'APP-1-T0002'`); err != nil {
		t.Fatalf("force status: %v", err)
	}

	rows, err := ListTasks(ctx, st.Read(), TaskFilter{ProjectID: projectID, Statuses: []int{0}, Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks(status=0): %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("status=0 rows = %d, want 2", len(rows))
	}

	rows, err = ListTasks(ctx, st.Read(), TaskFilter{ProjectID: projectID, Statuses: []int{0, 2}, Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks(status in 0,2): %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("status in (0,2) rows = %d, want 3", len(rows))
	}

	// ProjectID 0 lists every project, not "no project".
	rows, err = ListTasks(ctx, st.Read(), TaskFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks(all): %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("all rows = %d, want 3", len(rows))
	}
}

func TestUpdateTaskFieldsBumpsVersionOnly(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	ids := insertTasks(t, st, projectID, "APP-1", []int{0})

	// status and reason must survive a patch untouched.
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE tasks SET status = 2, reason = 'keep me' WHERE id = ?`, ids[0]); err != nil {
		t.Fatalf("force state: %v", err)
	}

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateTaskFields(ctx, tx, ids[0], TaskFields{
			Title:      "新标题",
			BodyMD:     "新正文",
			Priority:   7,
			LastActor:  "alice",
			UpdateTime: "2026-02-02T00:00:00Z",
		})
	})
	if err != nil {
		t.Fatalf("UpdateTaskFields: %v", err)
	}

	row, err := GetTaskByID(ctx, st.Read(), ids[0])
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if row.Title != "新标题" || row.BodyMD != "新正文" || row.Priority != 7 {
		t.Errorf("fields = %q/%q/%d", row.Title, row.BodyMD, row.Priority)
	}
	if row.Version != 2 {
		t.Errorf("version = %d, want 2", row.Version)
	}
	if row.Status != 2 {
		t.Errorf("status = %d, want 2 (unchanged)", row.Status)
	}
	if row.Reason == nil || *row.Reason != "keep me" {
		t.Errorf("reason = %v, want keep me", row.Reason)
	}
	if row.LastActor == nil || *row.LastActor != "alice" {
		t.Errorf("last_actor = %v, want alice", row.LastActor)
	}
	if row.UpdateTime != "2026-02-02T00:00:00Z" {
		t.Errorf("update_time = %q", row.UpdateTime)
	}
}

func TestDeleteTaskRemovesTouchedDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	ids := insertTasks(t, st, projectID, "APP-1", []int{0, 0, 0})

	// T0002 depends on T0001; T0003 depends on T0002. Deleting T0002 must drop
	// both the edge it owns and the edge that points at it, and leave T0003.
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		if err := CreateTaskDependencyByTaskIDs(ctx, tx, "APP-1-T0002", "APP-1-T0001", "2026-01-01T00:00:00Z"); err != nil {
			return err
		}
		return CreateTaskDependencyByTaskIDs(ctx, tx, "APP-1-T0003", "APP-1-T0002", "2026-01-01T00:00:00Z")
	})
	if err != nil {
		t.Fatalf("insert deps: %v", err)
	}

	err = st.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := DeleteDependenciesTouching(ctx, tx, ids[1]); err != nil {
			return err
		}
		return DeleteTask(ctx, tx, ids[1])
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	var deps int
	if err := st.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM task_deps`).Scan(&deps); err != nil {
		t.Fatalf("count deps: %v", err)
	}
	if deps != 0 {
		t.Errorf("task_deps = %d, want 0", deps)
	}
	if _, err := GetTaskByID(ctx, st.Read(), ids[1]); !IsNotFound(err) {
		t.Errorf("deleted task read error = %v, want sql.ErrNoRows", err)
	}
	if _, err := GetTaskByID(ctx, st.Read(), ids[2]); err != nil {
		t.Errorf("surviving task read: %v", err)
	}
}

func TestCreateTaskDependencyByTaskIDsResolvesAndOrders(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	ids := insertTasks(t, st, projectID, "APP-1", []int{0, 0, 0})

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		// Insert out of seq order so the read-back ordering is proven.
		if err := CreateTaskDependencyByTaskIDs(ctx, tx, "APP-1-T0003", "APP-1-T0002", "2026-01-01T00:00:00Z"); err != nil {
			return err
		}
		return CreateTaskDependencyByTaskIDs(ctx, tx, "APP-1-T0003", "APP-1-T0001", "2026-01-01T00:00:00Z")
	})
	if err != nil {
		t.Fatalf("insert deps: %v", err)
	}

	deps, err := ListDependenciesByTask(ctx, st.Read(), ids[2])
	if err != nil {
		t.Fatalf("ListDependenciesByTask: %v", err)
	}
	if len(deps) != 2 || deps[0] != "APP-1-T0001" || deps[1] != "APP-1-T0002" {
		t.Errorf("deps = %v, want [APP-1-T0001 APP-1-T0002]", deps)
	}

	byTask, err := TaskDependenciesByTaskIDs(ctx, st.Read(), []int64{ids[2]})
	if err != nil {
		t.Fatalf("TaskDependenciesByTaskIDs: %v", err)
	}
	if got := byTask[ids[2]]; len(got) != 2 || got[0] != "APP-1-T0001" {
		t.Errorf("batch deps = %v", got)
	}

	refs, err := TaskIDsByProject(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("TaskIDsByProject: %v", err)
	}
	if len(refs) != 3 || refs[0].TaskID != "APP-1-T0001" || refs[2].ID != ids[2] {
		t.Errorf("refs = %+v", refs)
	}

	// A missing business id inserts nothing and reports sql.ErrNoRows.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return CreateTaskDependencyByTaskIDs(ctx, tx, "APP-1-T9999", "APP-1-T0001", "2026-01-01T00:00:00Z")
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing task error = %v, want sql.ErrNoRows", err)
	}
}
