package migrations

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmbeddedMigrations guards the embedded FS itself: v2 ships the initial
// schema, the gate-decisions increment and the IM card projection, at
// versions 1, 2 and 3.
func TestEmbeddedMigrations(t *testing.T) {
	list, err := Load(FS)
	if err != nil {
		t.Fatalf("Load(FS): %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("migrations = %d, want 5", len(list))
	}
	if list[0].Version != 1 || list[0].Name != "0001_init.sql" {
		t.Errorf("migration[0] = %d/%s, want 1/0001_init.sql", list[0].Version, list[0].Name)
	}
	if list[1].Version != 2 || list[1].Name != "0002_gate_decisions.sql" {
		t.Errorf("migration[1] = %d/%s, want 2/0002_gate_decisions.sql", list[1].Version, list[1].Name)
	}
	if list[2].Version != 3 || list[2].Name != "0003_im_cards.sql" {
		t.Errorf("migration[2] = %d/%s, want 3/0003_im_cards.sql", list[2].Version, list[2].Name)
	}
	if list[3].Version != 4 || list[3].Name != "0004_im_actions.sql" {
		t.Errorf("migration[3] = %d/%s, want 4/0004_im_actions.sql", list[3].Version, list[3].Name)
	}
	if list[4].Version != 5 || list[4].Name != "0005_im_docs.sql" {
		t.Errorf("migration[4] = %d/%s, want 5/0005_im_docs.sql", list[4].Version, list[4].Name)
	}
	for _, migration := range list {
		if len(migration.SQL) == 0 {
			t.Errorf("embedded migration %s has no SQL", migration.Name)
		}
	}

	latest, err := Latest(FS)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest != 5 {
		t.Errorf("latest = %d, want 5", latest)
	}
}

func TestLoadSortsByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_second.sql": {Data: []byte("SELECT 2;")},
		"0001_first.sql":  {Data: []byte("SELECT 1;")},
		"notes.md":        {Data: []byte("ignored")},
	}

	list, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("migrations = %d, want 2", len(list))
	}
	if list[0].Version != 1 || list[1].Version != 2 {
		t.Errorf("order = %d,%d, want 1,2", list[0].Version, list[1].Version)
	}
	if string(list[1].SQL) != "SELECT 2;" {
		t.Errorf("SQL = %q", list[1].SQL)
	}
}

func TestLoadRejectsMalformedNames(t *testing.T) {
	fsys := fstest.MapFS{
		"0001-init.sql": {Data: []byte("SELECT 1;")},
	}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v, want naming error", err)
	}
}

func TestLoadRejectsDuplicateVersions(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_a.sql": {Data: []byte("SELECT 1;")},
		"0001_b.sql": {Data: []byte("SELECT 1;")},
	}
	if _, err := Load(fsys); err == nil || !strings.Contains(err.Error(), "defined twice") {
		t.Fatalf("err = %v, want duplicate error", err)
	}
}

func TestLoadRejectsEmptySet(t *testing.T) {
	if _, err := Load(fstest.MapFS{}); err == nil {
		t.Fatal("Load: expected error for empty directory")
	}
}
