package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// seedInboxProduct inserts the single product the inbox tests hang projects
// off and returns its physical id.
func seedInboxProduct(t *testing.T, st *Store) int64 {
	t.Helper()
	res, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP', 'APP 产品', 'git@example.com:APP.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("product id: %v", err)
	}
	return id
}

// insertInboxProject writes one project row and returns its physical id.
func insertInboxProject(t *testing.T, st *Store, productFK int64, projectID string, seq, status int, updateTime string, reason *string) int64 {
	t.Helper()
	res, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, reason, version, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		projectID, productFK, seq, "项目 "+projectID, status, reason, updateTime, updateTime)
	if err != nil {
		t.Fatalf("insert project %s: %v", projectID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id of %s: %v", projectID, err)
	}
	return id
}

// insertInboxTask writes one task row.
func insertInboxTask(t *testing.T, st *Store, projectFK int64, taskID string, seq, status int, updateTime string, reason *string) {
	t.Helper()
	if _, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO tasks (task_id, project_id, seq_no, title, body_md, priority, status,
		                   attempt_count, max_attempts, reason, version, create_time, update_time)
		VALUES (?, ?, ?, ?, 'body', 0, ?, 0, 3, ?, 1, ?, ?)`,
		taskID, projectFK, seq, "任务 "+taskID, status, reason, updateTime, updateTime); err != nil {
		t.Fatalf("insert task %s: %v", taskID, err)
	}
}

func inboxIDs(rows []InboxRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestListInboxEmpty(t *testing.T) {
	st := openTestStore(t, 1)
	rows, err := ListInbox(context.Background(), st.Read(), 50)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if rows == nil {
		t.Fatal("rows = nil, want an empty non-nil slice")
	}
	if len(rows) != 0 {
		t.Fatalf("len(rows) = %d, want 0", len(rows))
	}
}

// TestListInboxStatusCoverage pins the exact status sets of the contract:
// projects 1/3/5 and tasks 3/5, everything else absent.
func TestListInboxStatusCoverage(t *testing.T) {
	st := openTestStore(t, 1)
	productFK := seedInboxProduct(t, st)

	// A planning project (status 2) owns the tasks, so the project itself is
	// never a candidate and the task rows are the only task candidates.
	ownerFK := insertInboxProject(t, st, productFK, "OWNER", 100, 2, "2026-10-01T00:00:00Z", nil)
	for status := 0; status <= 7; status++ {
		insertInboxProject(t, st, productFK, fmt.Sprintf("P-%d", status), status+1,
			status, fmt.Sprintf("2026-10-02T00:00:%02dZ", status), nil)
		insertInboxTask(t, st, ownerFK, fmt.Sprintf("OWNER-T-%d", status), status+1,
			status, fmt.Sprintf("2026-10-03T00:00:%02dZ", status), nil)
	}

	rows, err := ListInbox(context.Background(), st.Read(), 200)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}

	want := map[string]int{
		"P-1": 1, "P-3": 3, "P-5": 5,
		"OWNER-T-3": 3, "OWNER-T-5": 5,
	}
	if len(rows) != len(want) {
		t.Fatalf("ids = %v, want exactly %d rows", inboxIDs(rows), len(want))
	}
	for _, row := range rows {
		state, ok := want[row.ID]
		if !ok {
			t.Errorf("unexpected inbox row %+v", row)
			continue
		}
		if row.State != state {
			t.Errorf("%s state = %d, want %d", row.ID, row.State, state)
		}
	}
}

// TestListInboxOldestFirst covers the sort rule: the oldest update_time wins,
// regardless of kind.
func TestListInboxOldestFirst(t *testing.T) {
	st := openTestStore(t, 1)
	productFK := seedInboxProduct(t, st)

	ownerFK := insertInboxProject(t, st, productFK, "OWNER", 1, 4, "2026-10-01T00:00:00Z", nil)
	insertInboxProject(t, st, productFK, "P-OLD", 2, 1, "2026-10-24T09:00:00Z", nil) // 3 days
	insertInboxTask(t, st, ownerFK, "T-MID", 1, 5, "2026-10-25T09:00:00Z", nil)      // 2 days
	insertInboxTask(t, st, ownerFK, "T-NEW", 2, 3, "2026-10-27T08:00:00Z", nil)      // 1 hour

	rows, err := ListInbox(context.Background(), st.Read(), 50)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	got := inboxIDs(rows)
	want := []string{"P-OLD", "T-MID", "T-NEW"}
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// TestListInboxFractionalSecondOrdering is the regression guard for plain
// string ordering. update_time is RFC3339Nano, so the fractional part has a
// variable length: as text, "…:00.500Z" sorts before "…:00Z" ('.' < 'Z'),
// even though it is half a second later. julianday() compares the instants, so
// the whole second comes first and the .500 row second.
func TestListInboxFractionalSecondOrdering(t *testing.T) {
	st := openTestStore(t, 1)
	productFK := seedInboxProduct(t, st)
	ownerFK := insertInboxProject(t, st, productFK, "OWNER", 1, 2, "2026-10-01T00:00:00Z", nil)

	insertInboxTask(t, st, ownerFK, "T-HALF", 1, 3, "2026-10-01T00:00:00.500Z", nil)
	insertInboxTask(t, st, ownerFK, "T-WHOLE-NEXT", 2, 3, "2026-10-01T00:00:01Z", nil)
	insertInboxTask(t, st, ownerFK, "T-WHOLE", 3, 3, "2026-10-01T00:00:00Z", nil)

	rows, err := ListInbox(context.Background(), st.Read(), 50)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	got := inboxIDs(rows)
	want := []string{"T-WHOLE", "T-HALF", "T-WHOLE-NEXT"}
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// TestListInboxStableTieBreak pins kind then id on equal timestamps.
func TestListInboxStableTieBreak(t *testing.T) {
	st := openTestStore(t, 1)
	productFK := seedInboxProduct(t, st)
	const sameTime = "2026-10-01T00:00:00Z"

	insertInboxProject(t, st, productFK, "APP-1", 1, 1, sameTime, nil)
	insertInboxProject(t, st, productFK, "APP-2", 2, 1, sameTime, nil)
	ownerFK := insertInboxProject(t, st, productFK, "OWNER", 3, 2, sameTime, nil)
	insertInboxTask(t, st, ownerFK, "APP-1-T0001", 1, 3, sameTime, nil)

	rows, err := ListInbox(context.Background(), st.Read(), 50)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	got := inboxIDs(rows)
	want := []string{"APP-1", "APP-2", "APP-1-T0001"} // project < task, then id
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// TestListInboxLimitIsGlobal inserts 60 candidates (30 per kind, interleaved
// by age) and checks that limit=50 returns the 50 oldest overall — not "the
// 50 oldest projects plus tasks", which is what two separate queries would
// produce.
func TestListInboxLimitIsGlobal(t *testing.T) {
	st := openTestStore(t, 1)
	productFK := seedInboxProduct(t, st)
	ownerFK := insertInboxProject(t, st, productFK, "OWNER", 1000, 4, "2026-01-01T00:00:00Z", nil)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	order := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		if i%2 == 0 {
			id := fmt.Sprintf("P-%02d", i)
			insertInboxProject(t, st, productFK, id, i+1, 1, ts, nil)
			order = append(order, id)
		} else {
			id := fmt.Sprintf("T-%02d", i)
			insertInboxTask(t, st, ownerFK, id, i+1, 5, ts, nil)
			order = append(order, id)
		}
	}

	rows, err := ListInbox(context.Background(), st.Read(), 50)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(rows) != 50 {
		t.Fatalf("len(rows) = %d, want 50", len(rows))
	}
	for i, row := range rows {
		if row.ID != order[i] {
			t.Fatalf("row %d = %s, want %s (global oldest 50)", i, row.ID, order[i])
		}
	}
}

// TestListInboxReason verifies the nullable column stays a nil pointer and the
// populated one round-trips.
func TestListInboxReason(t *testing.T) {
	st := openTestStore(t, 1)
	productFK := seedInboxProduct(t, st)

	reason := "attempt 用尽"
	insertInboxProject(t, st, productFK, "P-1", 1, 1, "2026-10-01T00:00:00Z", nil)
	insertInboxProject(t, st, productFK, "P-2", 2, 5, "2026-10-02T00:00:00Z", &reason)

	rows, err := ListInbox(context.Background(), st.Read(), 50)
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].ID != "P-1" || rows[0].Reason != nil {
		t.Errorf("first row = %+v, want P-1 with nil reason", rows[0])
	}
	if rows[1].ID != "P-2" || rows[1].Reason == nil || *rows[1].Reason != reason {
		t.Errorf("second row = %+v, want P-2 with reason %q", rows[1], reason)
	}
}
