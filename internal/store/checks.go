package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DBChecker is the "db" readiness probe: it proves the database answers
// queries and is writable.
//
// Writability is verified with a real write inside a transaction that is
// always rolled back. A read-only database or file permission problem makes
// the probe fail, which is exactly the condition that must take the instance
// out of rotation.
type DBChecker struct {
	store *Store
}

// NewDBChecker returns the database readiness probe for s.
func NewDBChecker(s *Store) *DBChecker { return &DBChecker{store: s} }

// Name implements api.HealthChecker.
func (*DBChecker) Name() string { return "db" }

// Check runs the probe.
func (c *DBChecker) Check(ctx context.Context) error {
	if c.store == nil || c.store.closed.Load() || c.store.write == nil {
		return ErrClosed
	}

	var one int
	if err := c.store.write.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("read probe: %w", err)
	}

	tx, err := c.store.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write probe: begin: %w", err)
	}
	// The probe must leave no trace, whatever happens.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "CREATE TABLE __echuan_readyz_probe (id INTEGER)"); err != nil {
		return fmt.Errorf("write probe: create: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE __echuan_readyz_probe"); err != nil {
		return fmt.Errorf("write probe: drop: %w", err)
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("write probe: rollback: %w", err)
	}
	return nil
}

// MigrationChecker is the "migration" readiness probe: the schema version in
// the database must equal the newest migration embedded in the binary.
type MigrationChecker struct {
	store *Store
}

// NewMigrationChecker returns the migration readiness probe for s.
func NewMigrationChecker(s *Store) *MigrationChecker { return &MigrationChecker{store: s} }

// Name implements api.HealthChecker.
func (*MigrationChecker) Name() string { return "migration" }

// Check runs the probe.
func (c *MigrationChecker) Check(ctx context.Context) error {
	if c.store == nil {
		return ErrClosed
	}
	want, err := LatestMigrationVersion()
	if err != nil {
		return fmt.Errorf("embedded migrations: %w", err)
	}
	got, err := c.store.MigrationVersion(ctx)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("schema version %d does not match the binary's latest migration %d", got, want)
	}
	return nil
}
