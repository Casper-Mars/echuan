package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"

	"github.com/Casper-Mars/echuan/migrations"
)

// MigrationError wraps a failure while applying one migration. The version is
// part of the message so that a failed startup log always names the migration
// that broke.
type MigrationError struct {
	Version int
	Name    string
	Err     error
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("migration %04d (%s): %v", e.Version, e.Name, e.Err)
}

// Unwrap exposes the underlying error for errors.Is/As.
func (e *MigrationError) Unwrap() error { return e.Err }

// SchemaVersionError reports a database schema newer than the embedded
// migrations, i.e. a downgraded binary. Applying nothing is the only safe
// behaviour.
type SchemaVersionError struct {
	Database int
	Binary   int
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("database schema version %d is newer than the binary's latest migration %d", e.Database, e.Binary)
}

// ForeignKeyViolation is one row reported by PRAGMA foreign_key_check: the
// child table/row whose reference has no parent row in Parent.
type ForeignKeyViolation struct {
	Table  string
	RowID  int64
	Parent string
	FKID   int
}

// ForeignKeyIntegrityError reports rows that survived the migrations with a
// dangling foreign key. Migrations suspend the foreign_keys guard (see
// applyMigration), so this check is the post-flight verification that the
// rebuilt schema still satisfies every reference.
type ForeignKeyIntegrityError struct {
	Violations []ForeignKeyViolation
}

func (e *ForeignKeyIntegrityError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for i, v := range e.Violations {
		if i == maxForeignKeyViolationsInError {
			parts = append(parts, fmt.Sprintf("... and %d more", len(e.Violations)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s(rowid=%d) → %s", v.Table, v.RowID, v.Parent))
	}
	return fmt.Sprintf("foreign key check failed: %d dangling reference(s): %s",
		len(e.Violations), strings.Join(parts, "; "))
}

// maxForeignKeyViolationsInError bounds the rendered violation list so that a
// badly broken database cannot produce an unbounded startup error message.
const maxForeignKeyViolationsInError = 10

// Migrate applies every embedded migration newer than PRAGMA user_version.
//
// It returns the number of migrations applied; a second call on an
// up-to-date database returns 0. Each migration runs in its own immediate
// transaction together with the PRAGMA user_version write-back, so a failing
// migration leaves both the schema and the version untouched
// (user_version is stored in the database header and participates in the
// transaction).
func (s *Store) Migrate(ctx context.Context) (int, error) {
	return s.MigrateFS(ctx, migrations.FS)
}

// MigrateFS is Migrate with an explicit file system, used by tests.
func (s *Store) MigrateFS(ctx context.Context, fsys fs.FS) (int, error) {
	list, err := migrations.Load(fsys)
	if err != nil {
		return 0, err
	}
	latest := list[len(list)-1].Version

	current, err := s.MigrationVersion(ctx)
	if err != nil {
		return 0, err
	}
	if current > latest {
		return 0, &SchemaVersionError{Database: current, Binary: latest}
	}

	applied := 0
	for _, m := range list {
		if m.Version <= current {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return applied, err
		}
		applied++
	}
	if applied > 0 {
		if err := s.checkForeignKeys(ctx); err != nil {
			return applied, err
		}
	}
	return applied, nil
}

// checkForeignKeys runs PRAGMA foreign_key_check and turns every violation
// into one error. It is called after the migration loop only when at least one
// migration was applied: an up-to-date database keeps its previous behaviour
// (the check is a verification of the rebuild, not a startup integrity scan).
func (s *Store) checkForeignKeys(ctx context.Context) error {
	rows, err := s.write.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer rows.Close()

	var violations []ForeignKeyViolation
	for rows.Next() {
		var (
			v      ForeignKeyViolation
			rowID  sql.NullInt64
			parent string
		)
		if err := rows.Scan(&v.Table, &rowID, &parent, &v.FKID); err != nil {
			return fmt.Errorf("foreign key check: scan: %w", err)
		}
		v.RowID = rowID.Int64
		v.Parent = parent
		violations = append(violations, v)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	if len(violations) > 0 {
		return &ForeignKeyIntegrityError{Violations: violations}
	}
	return nil
}

// applyMigration executes one file and writes back its version atomically.
//
// Foreign key enforcement is suspended for the duration of the migration.
// SQLite cannot alter a CHECK constraint, so a migration that widens one has
// to rebuild the table (create → copy → drop → rename). With enforcement on,
// the implicit DELETE performed by DROP TABLE fails as soon as child rows
// reference the parent; PRAGMA defer_foreign_keys does not rescue that case
// because the pending violation belongs to the dropped table and is not
// resolved by rows inserted into the replacement. Migrations run at startup,
// before the read pool is opened and before the HTTP server accepts writes,
// so the guard can be lifted here; it is restored before applyMigration
// returns, and MigrateFS verifies the result with PRAGMA foreign_key_check
// once every pending migration has been applied.
func (s *Store) applyMigration(ctx context.Context, m migrations.Migration) error {
	statements := splitStatements(string(m.SQL))
	if len(statements) == 0 {
		return &MigrationError{Version: m.Version, Name: m.Name, Err: fmt.Errorf("file contains no SQL statements")}
	}

	if err := s.setForeignKeys(ctx, false); err != nil {
		return &MigrationError{Version: m.Version, Name: m.Name, Err: err}
	}
	// Restore on the way out even when the migration fails; a cancelled ctx
	// must not leave the connection with the guard switched off.
	defer func() {
		if err := s.setForeignKeys(context.WithoutCancel(ctx), true); err != nil {
			s.logger.Error("restore foreign_keys after migration", "version", m.Version, "error", err)
		}
	}()

	s.logger.Info("applying migration", "version", m.Version, "name", m.Name, "statements", len(statements))

	err := s.Tx(ctx, func(tx *sql.Tx) error {
		for i, stmt := range statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("statement %d: %w", i+1, err)
			}
		}
		// PRAGMA does not accept bind parameters; version is a validated int.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
			return fmt.Errorf("set user_version: %w", err)
		}
		return nil
	})
	if err != nil {
		return &MigrationError{Version: m.Version, Name: m.Name, Err: err}
	}

	s.logger.Info("migration applied", "version", m.Version, "name", m.Name)
	return nil
}

// setForeignKeys toggles the write connection's foreign key guard and verifies
// that the pragma took effect. The write pool is limited to one connection, so
// the setting is visible to the transaction that follows.
func (s *Store) setForeignKeys(ctx context.Context, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	if _, err := s.write.ExecContext(ctx, fmt.Sprintf("PRAGMA foreign_keys = %d", value)); err != nil {
		return fmt.Errorf("set foreign_keys=%d: %w", value, err)
	}
	var got int
	if err := s.write.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&got); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if got != value {
		return fmt.Errorf("foreign_keys = %d, want %d (pragma ignored?)", got, value)
	}
	return nil
}

// splitStatements splits a migration file into individual statements so each
// one can be executed (and any failure attributed to a precise index).
//
// The splitter understands line comments (--), block comments, single-quoted
// strings (including ” escapes) and double-quoted identifiers (including ""
// escapes), which is enough for schema DDL. Semicolons inside strings or
// comments never split.
func splitStatements(script string) []string {
	var (
		statements []string
		current    strings.Builder
		runes      = []rune(script)

		inLineComment  bool
		inBlockComment bool
		inSingleQuote  bool
		inDoubleQuote  bool
	)

	flush := func() {
		stmt := strings.TrimSpace(current.String())
		current.Reset()
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}

	for i := 0; i < len(runes); i++ {
		c := runes[i]

		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
				current.WriteRune(' ')
			}
		case inBlockComment:
			if c == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				inBlockComment = false
				i++
				current.WriteRune(' ')
			}
		case inSingleQuote:
			current.WriteRune(c)
			if c == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					current.WriteRune(runes[i+1])
					i++
				} else {
					inSingleQuote = false
				}
			}
		case inDoubleQuote:
			current.WriteRune(c)
			if c == '"' {
				if i+1 < len(runes) && runes[i+1] == '"' {
					current.WriteRune(runes[i+1])
					i++
				} else {
					inDoubleQuote = false
				}
			}
		default:
			switch {
			case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
				inLineComment = true
				i++
			case c == '/' && i+1 < len(runes) && runes[i+1] == '*':
				inBlockComment = true
				i++
			case c == '\'':
				inSingleQuote = true
				current.WriteRune(c)
			case c == '"':
				inDoubleQuote = true
				current.WriteRune(c)
			case c == ';':
				flush()
			default:
				current.WriteRune(c)
			}
		}
	}
	flush()
	return statements
}
