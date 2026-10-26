package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type queryProbe struct {
	ID    int64   `db:"id"`
	Name  string  `db:"name"`
	Note  *string `db:"note"`
	Extra *int64  `db:"extra"` // nullable, always NULL in the fixture
}

func prepareQueryProbe(t *testing.T, st *Store) {
	t.Helper()

	if _, err := st.Write().ExecContext(context.Background(), `
		CREATE TABLE query_probe (
			id   INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			note TEXT
		)`); err != nil {
		t.Fatalf("create query_probe: %v", err)
	}
	if _, err := st.Write().ExecContext(context.Background(),
		`INSERT INTO query_probe (id, name, note) VALUES (1, 'alice', 'hi'), (2, 'bob', NULL)`); err != nil {
		t.Fatalf("insert query_probe: %v", err)
	}
}

func TestAllScansStructs(t *testing.T) {
	st := openTestStore(t, 1)
	prepareQueryProbe(t, st)
	ctx := context.Background()

	rows, err := All[queryProbe](ctx, st.Read(), `SELECT * FROM query_probe ORDER BY id`)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].ID != 1 || rows[0].Name != "alice" || rows[0].Note == nil || *rows[0].Note != "hi" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].Note != nil {
		t.Errorf("row 1 note = %v, want nil", *rows[1].Note)
	}
	if rows[1].Extra != nil {
		t.Errorf("row 1 extra = %v, want nil", *rows[1].Extra)
	}
}

func TestAllEmptyIsNonNil(t *testing.T) {
	st := openTestStore(t, 1)
	prepareQueryProbe(t, st)

	rows, err := All[queryProbe](context.Background(), st.Read(), `SELECT * FROM query_probe WHERE id < 0`)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if rows == nil {
		t.Error("All returned nil slice, want empty non-nil slice")
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

func TestOne(t *testing.T) {
	st := openTestStore(t, 1)
	prepareQueryProbe(t, st)
	ctx := context.Background()

	row, err := One[queryProbe](ctx, st.Read(), `SELECT * FROM query_probe WHERE id = ?`, 2)
	if err != nil {
		t.Fatalf("One: %v", err)
	}
	if row.Name != "bob" {
		t.Errorf("Name = %q, want bob", row.Name)
	}

	if _, err := One[queryProbe](ctx, st.Read(), `SELECT * FROM query_probe WHERE id = ?`, 42); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestScanIntoNonStructFails(t *testing.T) {
	st := openTestStore(t, 1)
	prepareQueryProbe(t, st)

	if _, err := All[int](context.Background(), st.Read(), `SELECT id FROM query_probe`); err == nil {
		t.Fatal("All[int] succeeded, want a non-struct error")
	}
}
