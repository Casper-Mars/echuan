// Package migrations embeds the SQLite schema migrations and exposes them to
// the migration runner in internal/store.
//
// The SQL files live next to this file so that the repository keeps a single,
// obvious source of truth (migrations/README.md documents the naming rules).
// The package has no dependencies beyond the standard library: it only parses
// file names and reads the embedded bytes, which keeps the SQL layout
// independent from the store package's internal structure.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
)

// FS holds every embedded migration file.
//
//go:embed *.sql
var FS embed.FS

// namePattern is the enforced migration file naming scheme:
// 4-digit zero-padded version, an underscore, a lowercase description and the
// .sql extension (for example 0001_init.sql).
var namePattern = regexp.MustCompile(`^(\d{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

// Migration is one versioned schema change.
type Migration struct {
	// Version is the numeric prefix of the file name (1 for 0001_init.sql).
	Version int
	// Name is the file name, used in logs and error messages.
	Name string
	// SQL is the raw file content.
	SQL []byte
}

// Load reads every *.sql file from fsys, validates the naming scheme and
// returns the migrations ordered by ascending version.
//
// A file whose name does not match the scheme is an error rather than being
// skipped: a typo such as 2025_10_26_patch.sql would otherwise be silently
// ignored and the schema would drift.
func Load(fsys fs.FS) ([]Migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("migrations: list files: %w", err)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("migrations: no *.sql files found")
	}

	list := make([]Migration, 0, len(names))
	seen := make(map[int]string, len(names))
	for _, name := range names {
		match := namePattern.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migrations: file %q does not match NNNN_description.sql", name)
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migrations: file %q has an invalid version", name)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations: version %d defined twice (%s and %s)", version, other, name)
		}
		// The file name is the only embedded metadata; keeping the stored SQL
		// here means the runner never has to know the layout of FS.
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migrations: read %s: %w", name, err)
		}
		seen[version] = name
		list = append(list, Migration{Version: version, Name: name, SQL: data})
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Version < list[j].Version })
	return list, nil
}

// Latest returns the highest embedded migration version.
func Latest(fsys fs.FS) (int, error) {
	list, err := Load(fsys)
	if err != nil {
		return 0, err
	}
	return list[len(list)-1].Version, nil
}
