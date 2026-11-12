package store

import (
	"context"
	"fmt"
)

const getIMDocQuery = `SELECT url FROM im_doc WHERE material_id = ?`

// GetIMDocURL returns the cached document URL for a material version.
func GetIMDocURL(ctx context.Context, q Querier, materialID string) (string, error) {
	rows, err := q.QueryContext(ctx, getIMDocQuery, materialID)
	if err != nil {
		return "", fmt.Errorf("store: get im doc %s: %w", materialID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", fmt.Errorf("store: get im doc %s: %w", materialID, rows.Err())
	}
	var url string
	if err := rows.Scan(&url); err != nil {
		return "", fmt.Errorf("store: scan im doc %s: %w", materialID, err)
	}
	return url, nil
}

const putIMDocQuery = `INSERT INTO im_doc (material_id, doc_token, url, create_time)
VALUES (?, ?, ?, ?)
ON CONFLICT(material_id) DO NOTHING`

// PutIMDoc records the document a material version was imported into.
func PutIMDoc(ctx context.Context, e Execer, materialID, docToken, url, createTime string) error {
	if _, err := e.ExecContext(ctx, putIMDocQuery, materialID, docToken, url, createTime); err != nil {
		return fmt.Errorf("store: put im doc %s: %w", materialID, err)
	}
	return nil
}
