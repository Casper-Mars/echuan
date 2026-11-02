package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// insertWorkItems inserts n queued items for projectID and returns their
// physical ids in seq order. priorities[i] is the priority of seq i+1.
func insertWorkItems(t *testing.T, st *Store, projectID int64, prefix string, priorities []int) []int64 {
	t.Helper()
	ctx := context.Background()
	ids := make([]int64, len(priorities))
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		for i, priority := range priorities {
			seq := i + 1
			id, err := CreateWorkItem(ctx, tx, NewWorkItem{
				WorkID:     workCode(prefix, seq),
				ProjectID:  projectID,
				Kind:       "dev",
				SeqNo:      seq,
				Title:      workCode(prefix, seq),
				BodyMD:     "b",
				Priority:   priority,
				State:      WorkItemStateQueued,
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
		t.Fatalf("insert work items: %v", err)
	}
	return ids
}

func workCode(prefix string, seq int) string {
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

func TestListWorkItemsOrdersByPriorityThenSeq(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	// seq 1..5 with priorities that force a different order.
	insertWorkItems(t, st, projectID, "APP-1", []int{0, 10, 5, 10, -1000})

	rows, err := ListWorkItems(ctx, st.Read(), WorkItemFilter{ProjectID: projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	want := []string{"APP-1-T0002", "APP-1-T0004", "APP-1-T0003", "APP-1-T0001", "APP-1-T0005"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i, row := range rows {
		if row.WorkID != want[i] {
			t.Errorf("rows[%d] = %s, want %s", i, row.WorkID, want[i])
		}
	}
}

func TestListWorkItemsKeysetPagesWithoutGapsOrDuplicates(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	// 25 rows; priorities repeat so the (priority, seq_no, id) tie-break is
	// exercised, not just the leading column.
	priorities := make([]int, 25)
	for i := range priorities {
		priorities[i] = i % 3
	}
	insertWorkItems(t, st, projectID, "APP-1", priorities)

	seen := make(map[string]int)
	var after *WorkItemCursor
	pages := 0
	for {
		rows, err := ListWorkItems(ctx, st.Read(), WorkItemFilter{
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
			seen[row.WorkID]++
		}
		last := rows[len(rows)-1]
		after = &WorkItemCursor{Priority: last.Priority, SeqNo: last.SeqNo, ID: last.ID}
		if len(rows) < 4 {
			break
		}
	}
	if len(seen) != 25 {
		t.Fatalf("distinct work items = %d, want 25", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("work item %s appeared %d times", id, count)
		}
	}
	if pages != 7 { // 6 full pages + 1 row
		t.Errorf("pages = %d, want 7", pages)
	}
}

func TestListWorkItemsFiltersStateKindAndProject(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	insertWorkItems(t, st, projectID, "APP-1", []int{0, 0, 0})

	if _, err := st.Write().ExecContext(ctx,
		`UPDATE work_items SET state = 'leased' WHERE work_id = 'APP-1-T0002'`); err != nil {
		t.Fatalf("force state: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE work_items SET kind = 'design' WHERE work_id = 'APP-1-T0003'`); err != nil {
		t.Fatalf("force kind: %v", err)
	}

	rows, err := ListWorkItems(ctx, st.Read(), WorkItemFilter{
		ProjectID: projectID, States: []string{WorkItemStateQueued}, Limit: 10})
	if err != nil {
		t.Fatalf("ListWorkItems(state=queued): %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("queued rows = %d, want 2", len(rows))
	}

	rows, err = ListWorkItems(ctx, st.Read(), WorkItemFilter{
		ProjectID: projectID, States: []string{WorkItemStateQueued, WorkItemStateLeased}, Limit: 10})
	if err != nil {
		t.Fatalf("ListWorkItems(state in queued,leased): %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("state in (queued,leased) rows = %d, want 3", len(rows))
	}

	rows, err = ListWorkItems(ctx, st.Read(), WorkItemFilter{Kinds: []string{"design"}, Limit: 10})
	if err != nil {
		t.Fatalf("ListWorkItems(kind=design): %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != "design" {
		t.Errorf("design rows = %+v, want one", rows)
	}

	// ProjectID 0 lists every project, not "no project".
	rows, err = ListWorkItems(ctx, st.Read(), WorkItemFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListWorkItems(all): %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("all rows = %d, want 3", len(rows))
	}
}

func TestUpdateWorkItemFieldsBumpsVersionOnly(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	ids := insertWorkItems(t, st, projectID, "APP-1", []int{0})

	// state and reason must survive a patch untouched.
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE work_items SET state = 'failed', reason = 'keep me' WHERE id = ?`, ids[0]); err != nil {
		t.Fatalf("force state: %v", err)
	}

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		return UpdateWorkItemFields(ctx, tx, ids[0], WorkItemFields{
			Title:      "新标题",
			BodyMD:     "新正文",
			Priority:   7,
			UpdateTime: "2026-02-02T00:00:00Z",
		})
	})
	if err != nil {
		t.Fatalf("UpdateWorkItemFields: %v", err)
	}

	row, err := GetWorkItemByID(ctx, st.Read(), ids[0])
	if err != nil {
		t.Fatalf("GetWorkItemByID: %v", err)
	}
	if row.Title != "新标题" || row.BodyMD != "新正文" || row.Priority != 7 {
		t.Errorf("fields = %q/%q/%d", row.Title, row.BodyMD, row.Priority)
	}
	if row.Version != 2 {
		t.Errorf("version = %d, want 2", row.Version)
	}
	if row.State != WorkItemStateFailed {
		t.Errorf("state = %s, want failed (unchanged)", row.State)
	}
	if row.Reason == nil || *row.Reason != "keep me" {
		t.Errorf("reason = %v, want keep me", row.Reason)
	}
	if row.UpdateTime != "2026-02-02T00:00:00Z" {
		t.Errorf("update_time = %q", row.UpdateTime)
	}
}

func TestDeleteWorkItemRemovesTouchedDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	ids := insertWorkItems(t, st, projectID, "APP-1", []int{0, 0, 0})

	// T0002 depends on T0001; T0003 depends on T0002. Deleting T0002 must drop
	// both the edge it owns and the edge that points at it, and leave T0003.
	err := st.Tx(ctx, func(tx *sql.Tx) error {
		if err := CreateWorkDependencyByWorkIDs(ctx, tx, "APP-1-T0002", "APP-1-T0001"); err != nil {
			return err
		}
		return CreateWorkDependencyByWorkIDs(ctx, tx, "APP-1-T0003", "APP-1-T0002")
	})
	if err != nil {
		t.Fatalf("insert deps: %v", err)
	}

	err = st.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := DeleteDependenciesTouching(ctx, tx, ids[1]); err != nil {
			return err
		}
		return DeleteWorkItem(ctx, tx, ids[1])
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	var deps int
	if err := st.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM work_deps`).Scan(&deps); err != nil {
		t.Fatalf("count deps: %v", err)
	}
	if deps != 0 {
		t.Errorf("work_deps = %d, want 0", deps)
	}
	if _, err := GetWorkItemByID(ctx, st.Read(), ids[1]); !IsNotFound(err) {
		t.Errorf("deleted item read error = %v, want sql.ErrNoRows", err)
	}
	if _, err := GetWorkItemByID(ctx, st.Read(), ids[2]); err != nil {
		t.Errorf("surviving item read: %v", err)
	}
}

func TestCreateWorkDependencyByWorkIDsResolvesAndOrders(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)
	ids := insertWorkItems(t, st, projectID, "APP-1", []int{0, 0, 0})

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		// Insert out of seq order so the read-back ordering is proven.
		if err := CreateWorkDependencyByWorkIDs(ctx, tx, "APP-1-T0003", "APP-1-T0002"); err != nil {
			return err
		}
		return CreateWorkDependencyByWorkIDs(ctx, tx, "APP-1-T0003", "APP-1-T0001")
	})
	if err != nil {
		t.Fatalf("insert deps: %v", err)
	}

	deps, err := ListDependenciesByWorkItem(ctx, st.Read(), ids[2])
	if err != nil {
		t.Fatalf("ListDependenciesByWorkItem: %v", err)
	}
	if len(deps) != 2 || deps[0] != "APP-1-T0001" || deps[1] != "APP-1-T0002" {
		t.Errorf("deps = %v, want [APP-1-T0001 APP-1-T0002]", deps)
	}

	byItem, err := WorkDependenciesByWorkItemIDs(ctx, st.Read(), []int64{ids[2]})
	if err != nil {
		t.Fatalf("WorkDependenciesByWorkItemIDs: %v", err)
	}
	if got := byItem[ids[2]]; len(got) != 2 || got[0] != "APP-1-T0001" {
		t.Errorf("batch deps = %v", got)
	}

	refs, err := WorkItemRefsByProject(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("WorkItemRefsByProject: %v", err)
	}
	if len(refs) != 3 || refs[0].WorkID != "APP-1-T0001" || refs[2].ID != ids[2] {
		t.Errorf("refs = %+v", refs)
	}

	// A missing business id inserts nothing and reports sql.ErrNoRows.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return CreateWorkDependencyByWorkIDs(ctx, tx, "APP-1-T9999", "APP-1-T0001")
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing work item error = %v, want sql.ErrNoRows", err)
	}
}

// TestWorkItemWorkerIDFollowsState pins the read-side rule: worker_id is
// rendered only while an item is leased, so a pointer left on a submitted or
// accepted row never leaks into a response.
func TestWorkItemWorkerIDFollowsState(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	ids := insertWorkItems(t, st, projectID, "APP-1", []int{10, 5, 0})
	leased, accepted, unbound := ids[0], ids[1], ids[2]

	const (
		workerID   = "w_00000000000000000000000001"
		staleID    = "w_00000000000000000000000002"
		updateTime = "2026-01-02T00:00:00Z"
	)
	if _, err := st.Write().ExecContext(ctx, `
		UPDATE work_items SET state = 'leased', worker_id = ? WHERE id = ?`, workerID, leased); err != nil {
		t.Fatalf("lease item: %v", err)
	}
	// A stale stamp on an accepted row: the state must win over the column.
	if _, err := st.Write().ExecContext(ctx, `
		UPDATE work_items SET state = 'accepted', worker_id = ? WHERE id = ?`, staleID, accepted); err != nil {
		t.Fatalf("accept item: %v", err)
	}
	if _, err := st.Write().ExecContext(ctx, `
		UPDATE work_items SET state = 'leased' WHERE id = ?`, unbound); err != nil {
		t.Fatalf("lease unbound item: %v", err)
	}

	byWorkID, err := GetWorkItemByWorkID(ctx, st.Read(), workCode("APP-1", 1))
	if err != nil {
		t.Fatalf("GetWorkItemByWorkID: %v", err)
	}
	if byWorkID.WorkerID == nil || *byWorkID.WorkerID != workerID {
		t.Errorf("detail worker_id = %v, want %s", byWorkID.WorkerID, workerID)
	}

	// leased without a worker stamp: null, not "".
	unboundItem, err := GetWorkItemByID(ctx, st.Read(), unbound)
	if err != nil {
		t.Fatalf("GetWorkItemByID unbound: %v", err)
	}
	if unboundItem.WorkerID != nil {
		t.Errorf("unbound leased worker_id = %q, want nil", *unboundItem.WorkerID)
	}

	// stale stamp on an accepted card: state wins, the column is dropped.
	acceptedItem, err := GetWorkItemByID(ctx, st.Read(), accepted)
	if err != nil {
		t.Fatalf("GetWorkItemByID accepted: %v", err)
	}
	if acceptedItem.WorkerID != nil {
		t.Errorf("accepted worker_id = %q, want nil", *acceptedItem.WorkerID)
	}

	rows, err := ListWorkItems(ctx, st.Read(), WorkItemFilter{ProjectID: projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListWorkItems rows = %d, want 3 (join duplicated rows?)", len(rows))
	}
	seen := make(map[string]*string, len(rows))
	for _, row := range rows {
		seen[row.WorkID] = row.WorkerID
	}
	if got := seen[workCode("APP-1", 1)]; got == nil || *got != workerID {
		t.Errorf("list worker_id of leased = %v, want %s", got, workerID)
	}
	for _, workID := range []string{workCode("APP-1", 2), workCode("APP-1", 3)} {
		if got := seen[workID]; got != nil {
			t.Errorf("list worker_id of %s = %q, want nil", workID, *got)
		}
	}
}
