package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// seedProjectRow inserts the minimal product/project pair the work-item helpers
// need, returning the physical project id.
func seedProjectRow(t *testing.T, st *Store) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Write().ExecContext(ctx, `
		INSERT INTO products (product_id, name, git_url, create_time, update_time)
		VALUES ('APP', 'APP 产品', 'git@example.com:APP.git', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	res, err := st.Write().ExecContext(ctx, `
		INSERT INTO projects (project_id, product_id, seq_no, title, status, version, create_time, update_time)
		VALUES ('APP-1', 1, 1, '项目', 2, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

func TestCreateWorkItemKindStateAndDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	const ts = "2026-01-02T00:00:00Z"
	parentID := int64(0)
	var firstID, secondID, childID int64

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		firstID, err = CreateWorkItem(ctx, tx, NewWorkItem{
			WorkID: "w_01", ProjectID: projectID, Kind: "requirement", SeqNo: 1,
			Title: "一", BodyMD: "b", State: WorkItemStateAccepted,
			MaxAttempts: 5,
			CreateTime:  ts, UpdateTime: ts,
		})
		if err != nil {
			return err
		}
		secondID, err = CreateWorkItem(ctx, tx, NewWorkItem{
			WorkID: "w_02", ProjectID: projectID, Kind: "design", SeqNo: 2,
			Title: "二", BodyMD: "b",
			CreateTime: ts, UpdateTime: ts,
		})
		if err != nil {
			return err
		}
		parentID = firstID
		childID, err = CreateWorkItem(ctx, tx, NewWorkItem{
			WorkID: "w_03", ProjectID: projectID, ParentID: &parentID,
			Kind: "breakdown", SeqNo: 3, Title: "三", State: WorkItemStateBlocked,
			CreateTime: ts, UpdateTime: ts,
		})
		if err != nil {
			return err
		}

		if err := InsertWorkDependency(ctx, tx, secondID, firstID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	rows, err := ListWorkItemsByProject(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("ListWorkItemsByProject: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("work items = %d, want 3", len(rows))
	}
	if rows[0].Kind != "requirement" || rows[0].State != WorkItemStateAccepted {
		t.Errorf("item 1 = kind %q state %q, want requirement/accepted", rows[0].Kind, rows[0].State)
	}
	if rows[1].Kind != "design" || rows[1].State != WorkItemStateQueued {
		t.Errorf("item 2 = kind %q state %q, want design/queued (state defaults)", rows[1].Kind, rows[1].State)
	}
	if rows[2].ParentID == nil || *rows[2].ParentID != parentID {
		t.Errorf("item 3 parent = %v, want %d", rows[2].ParentID, parentID)
	}
	if rows[0].MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", rows[0].MaxAttempts)
	}
	// Unset MaxAttempts falls back to the column default, never 0.
	if rows[1].MaxAttempts != defaultWorkItemMaxAttempts {
		t.Errorf("default max_attempts = %d, want %d", rows[1].MaxAttempts, defaultWorkItemMaxAttempts)
	}

	deps, err := ListWorkDependencies(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("ListWorkDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0].WorkItemID != secondID || deps[0].DependsOn != firstID {
		t.Errorf("deps = %+v, want one edge %d -> %d", deps, secondID, firstID)
	}

	unfinished, err := CountUnfinishedDependencies(ctx, st.Read(), secondID)
	if err != nil {
		t.Fatalf("CountUnfinishedDependencies: %v", err)
	}
	if unfinished != 0 {
		t.Errorf("unfinished = %d, want 0 (dependency is accepted)", unfinished)
	}

	// A duplicate edge surfaces as ErrUniqueViolation instead of silently
	// doubling the row.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return InsertWorkDependency(ctx, tx, secondID, firstID)
	})
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("duplicate insert error = %v, want ErrUniqueViolation", err)
	}

	if childID == 0 {
		t.Fatal("child item was not inserted")
	}
}

func TestCreateWorkItemRejectsDuplicateWorkID(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	insert := func(seq int) error {
		return st.Tx(ctx, func(tx *sql.Tx) error {
			_, err := CreateWorkItem(ctx, tx, NewWorkItem{
				WorkID: "w_dup", ProjectID: projectID, Kind: "dev", SeqNo: seq,
				Title: "t", BodyMD: "b", CreateTime: "2026-01-01T00:00:00Z",
				UpdateTime: "2026-01-01T00:00:00Z",
			})
			return err
		})
	}
	if err := insert(1); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert(2); !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("second insert error = %v, want ErrUniqueViolation", err)
	}
}

func TestWorkItemKindsAreValidated(t *testing.T) {
	for _, kind := range []string{"requirement", "design", "breakdown", "dev"} {
		if !IsValidWorkItemKind(kind) {
			t.Errorf("IsValidWorkItemKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"", "merge", "review", "Task"} {
		if IsValidWorkItemKind(kind) {
			t.Errorf("IsValidWorkItemKind(%q) = true, want false", kind)
		}
	}
}
