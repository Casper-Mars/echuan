package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// newChainStore opens a migrated store on a temporary file.
func newChainStore(t *testing.T) *Store {
	t.Helper()

	st, err := Open(Config{Path: filepath.Join(t.TempDir(), "state.db"), ReadPoolSize: 2})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.OpenReadPool(context.Background()); err != nil {
		t.Fatalf("OpenReadPool: %v", err)
	}
	return st
}

// insertMaterial writes one materials row and returns its physical id.
func insertMaterial(t *testing.T, st *Store, materialID string, kind, version int, previous *int64) int64 {
	t.Helper()

	res, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO materials (material_id, kind, version, title, sha256, size, path,
		                       uploaded_by, previous_material_id, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'test', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		materialID, kind, version, "标题 "+materialID, materialID, 3,
		"objects/"+materialID+"/"+materialID, previous)
	if err != nil {
		t.Fatalf("insert material %s: %v", materialID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

// corruptWithoutForeignKeys opens a second connection with foreign keys off so a
// test can point a chain link at a row that does not exist. The store's own
// pools refuse such a write, which is the point.
func corruptWithoutForeignKeys(t *testing.T, st *Store, query string, args ...any) {
	t.Helper()

	db, err := sql.Open(DriverName, st.Path()+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("open corrupting connection: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
}

func materialIDs(chain []Material) []string {
	ids := make([]string, 0, len(chain))
	for _, m := range chain {
		ids = append(ids, m.MaterialID)
	}
	return ids
}

func TestListChainByHeadLinear(t *testing.T) {
	st := newChainStore(t)

	v1 := insertMaterial(t, st, "M1", 2, 1, nil)
	v2 := insertMaterial(t, st, "M2", 2, 2, &v1)
	v3 := insertMaterial(t, st, "M3", 2, 3, &v2)

	chain, err := ListChainByHead(context.Background(), st.Read(), v3)
	if err != nil {
		t.Fatalf("ListChainByHead: %v", err)
	}
	got := materialIDs(chain)
	want := []string{"M1", "M2", "M3"}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain = %v, want %v", got, want)
		}
	}
	if chain[0].PreviousMaterialID != nil {
		t.Errorf("root previous = %v, want nil", *chain[0].PreviousMaterialID)
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].Version != chain[i-1].Version+1 {
			t.Errorf("version at %d = %d, want %d", i, chain[i].Version, chain[i-1].Version+1)
		}
	}
}

func TestListChainByHeadMissingLink(t *testing.T) {
	st := newChainStore(t)

	v1 := insertMaterial(t, st, "M1", 1, 1, nil)
	v2 := insertMaterial(t, st, "M2", 1, 2, &v1)
	// Point the head at a row that does not exist: the kind of damage a manual
	// DB edit or a partially restored backup produces.
	corruptWithoutForeignKeys(t, st, `UPDATE materials SET previous_material_id = 999999 WHERE id = ?`, v2)

	_, err := ListChainByHead(context.Background(), st.Read(), v2)
	if !errors.Is(err, ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
}

func TestListChainByHeadRejectsCycles(t *testing.T) {
	st := newChainStore(t)

	v1 := insertMaterial(t, st, "M1", 1, 1, nil)
	v2 := insertMaterial(t, st, "M2", 1, 2, &v1)
	corruptWithoutForeignKeys(t, st, `UPDATE materials SET previous_material_id = ? WHERE id = ?`, v2, v1)

	if _, err := ListChainByHead(context.Background(), st.Read(), v2); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
}

func TestListChainByHeadRejectsVersionJump(t *testing.T) {
	st := newChainStore(t)

	v1 := insertMaterial(t, st, "M1", 2, 1, nil)
	// Version 3 pointing straight at version 1: the v2 row is missing, so the
	// chain has a hole even though every link resolves to a row.
	v3 := insertMaterial(t, st, "M3", 2, 3, &v1)

	if _, err := ListChainByHead(context.Background(), st.Read(), v3); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
}

func TestListChainByHeadRejectsKindMix(t *testing.T) {
	st := newChainStore(t)

	requirement := insertMaterial(t, st, "M1", 1, 1, nil)
	spec := insertMaterial(t, st, "M2", 2, 2, &requirement)

	if _, err := ListChainByHead(context.Background(), st.Read(), spec); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
}

func TestListChainByHeadRejectsBadHead(t *testing.T) {
	st := newChainStore(t)

	if _, err := ListChainByHead(context.Background(), st.Read(), 0); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("err = %v, want ErrChainBroken", err)
	}
}
