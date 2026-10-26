package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

// identifierPattern guards the SQL identifiers interpolated into NextSeqNo.
// Table and column names are compile-time constants supplied by callers, never
// user input; the check exists so a typo fails loudly instead of producing a
// syntax error at runtime.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NextSeqNo returns MAX(seq_no)+1 for the rows of table whose scopeColumn
// equals scopeID (1 when the scope is empty).
//
// It MUST be called inside a write transaction: the SELECT is only stable
// while the write lock is held, which is what makes the returned number safe
// to insert under a UNIQUE(scope, seq_no) constraint. Passing a nil tx is a
// programming error and returns an error instead of silently using the pool.
func NextSeqNo(ctx context.Context, tx *sql.Tx, table, scopeColumn string, scopeID int64) (int, error) {
	if tx == nil {
		return 0, errors.New("store: NextSeqNo requires a transaction")
	}
	if !identifierPattern.MatchString(table) {
		return 0, fmt.Errorf("store: NextSeqNo: invalid table name %q", table)
	}
	if !identifierPattern.MatchString(scopeColumn) {
		return 0, fmt.Errorf("store: NextSeqNo: invalid scope column %q", scopeColumn)
	}

	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(seq_no), 0) + 1 FROM "%s" WHERE "%s" = ?`,
		table, scopeColumn,
	)
	var next int
	if err := tx.QueryRowContext(ctx, query, scopeID).Scan(&next); err != nil {
		return 0, fmt.Errorf("store: NextSeqNo(%s.%s=%d): %w", table, scopeColumn, scopeID, err)
	}
	return next, nil
}
