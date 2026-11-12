package store

import (
	"context"
	"database/sql"
	"fmt"
)

const getIMActionQuery = `SELECT result FROM im_action_log WHERE idem_key = ?`

// GetIMActionResult returns the stored outcome of a card action.
//
// It scans the scalar column directly instead of going through One[T]: the
// generic helper is built for structs with db tags.
func GetIMActionResult(ctx context.Context, q Querier, idemKey string) (string, error) {
	rows, err := q.QueryContext(ctx, getIMActionQuery, idemKey)
	if err != nil {
		return "", fmt.Errorf("store: get im action %s: %w", idemKey, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("store: get im action %s: %w", idemKey, err)
		}
		return "", sql.ErrNoRows
	}
	var result string
	if err := rows.Scan(&result); err != nil {
		return "", fmt.Errorf("store: scan im action %s: %w", idemKey, err)
	}
	return result, nil
}

const putIMActionQuery = `INSERT INTO im_action_log (idem_key, subject, action, result, create_time)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(idem_key) DO NOTHING`

// PutIMActionResult records one applied action. A duplicate key is a no-op so a
// replay never overwrites the first outcome.
func PutIMActionResult(ctx context.Context, e Execer, idemKey, subject, action, result, createTime string) error {
	if _, err := e.ExecContext(ctx, putIMActionQuery, idemKey, subject, action, result, createTime); err != nil {
		return fmt.Errorf("store: put im action %s: %w", idemKey, err)
	}
	return nil
}
