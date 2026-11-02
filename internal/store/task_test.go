package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// seedProjectRow inserts the minimal product/project pair the task helpers
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

func TestCreateTaskAttemptsActorAndDependencies(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	const ts = "2026-01-02T00:00:00Z"
	var firstID, secondID int64

	err := st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		firstID, err = CreateTask(ctx, tx, NewTask{
			TaskID: "APP-1-T0001", ProjectID: projectID, SeqNo: 1,
			Title: "一", BodyMD: "b", Status: 0,
			MaxAttempts: 5, LastActor: "agent:cto",
			CreateTime: ts, UpdateTime: ts,
		})
		if err != nil {
			return err
		}
		secondID, err = CreateTask(ctx, tx, NewTask{
			TaskID: "APP-1-T0002", ProjectID: projectID, SeqNo: 2,
			Title: "二", BodyMD: "b", Status: 0,
			CreateTime: ts, UpdateTime: ts,
		})
		if err != nil {
			return err
		}

		if err := InsertTaskDependency(ctx, tx, secondID, firstID, ts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}

	rows, err := ListTasksByProject(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("ListTasksByProject: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("tasks = %d, want 2", len(rows))
	}
	if rows[0].MaxAttempts != 5 {
		t.Errorf("max_attempts = %d, want 5", rows[0].MaxAttempts)
	}
	if rows[0].LastActor == nil || *rows[0].LastActor != "agent:cto" {
		t.Errorf("last_actor = %v, want agent:cto", rows[0].LastActor)
	}
	// Unset MaxAttempts falls back to the column default, never 0.
	if rows[1].MaxAttempts != defaultTaskMaxAttempts {
		t.Errorf("default max_attempts = %d, want %d", rows[1].MaxAttempts, defaultTaskMaxAttempts)
	}
	if rows[1].LastActor != nil {
		t.Errorf("last_actor = %v, want NULL", rows[1].LastActor)
	}

	deps, err := ListTaskDependencies(ctx, st.Read(), projectID)
	if err != nil {
		t.Fatalf("ListTaskDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0].TaskID != secondID || deps[0].DependsOn != firstID {
		t.Errorf("deps = %+v, want one edge %d -> %d", deps, secondID, firstID)
	}

	unfinished, err := CountUnfinishedDependencies(ctx, st.Read(), secondID)
	if err != nil {
		t.Fatalf("CountUnfinishedDependencies: %v", err)
	}
	if unfinished != 1 {
		t.Errorf("unfinished = %d, want 1 (dependency is backlog)", unfinished)
	}

	// A duplicate edge surfaces as ErrUniqueViolation instead of silently
	// doubling the row.
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return InsertTaskDependency(ctx, tx, secondID, firstID, ts)
	})
	if !errors.Is(err, ErrUniqueViolation) {
		t.Fatalf("duplicate insert error = %v, want ErrUniqueViolation", err)
	}
}

func TestCreateTaskRejectsDuplicateTaskID(t *testing.T) {
	st := openTestStore(t, 1)
	ctx := context.Background()
	projectID := seedProjectRow(t, st)

	insert := func(seq int) error {
		return st.Tx(ctx, func(tx *sql.Tx) error {
			_, err := CreateTask(ctx, tx, NewTask{
				TaskID: "APP-1-T0001", ProjectID: projectID, SeqNo: seq,
				Title: "t", BodyMD: "b", Status: 0,
				CreateTime: "2026-01-01T00:00:00Z", UpdateTime: "2026-01-01T00:00:00Z",
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
