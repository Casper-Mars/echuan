package store

import (
	"context"
	"database/sql"
)

type SpecDocument struct {
	ID         int64  `db:"id"`
	SpecID     string `db:"spec_id"`
	ProjectID  int64  `db:"project_id"`
	SeqNo      int    `db:"seq_no"`
	MaterialID int64  `db:"material_id"`
	CreateTime string `db:"create_time"`
	UpdateTime string `db:"update_time"`
}

func ListSpecDocuments(ctx context.Context, q Querier, projectID int64) ([]SpecDocument, error) {
	return All[SpecDocument](ctx, q, `SELECT * FROM project_specs WHERE project_id = ? ORDER BY seq_no`, projectID)
}

func GetSpecDocument(ctx context.Context, q Querier, specID string) (SpecDocument, error) {
	return One[SpecDocument](ctx, q, `SELECT * FROM project_specs WHERE spec_id = ?`, specID)
}

func InsertSpecDocument(ctx context.Context, tx *sql.Tx, d SpecDocument) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO project_specs(spec_id,project_id,seq_no,material_id,create_time,update_time) VALUES(?,?,?,?,?,?)`, d.SpecID, d.ProjectID, d.SeqNo, d.MaterialID, d.CreateTime, d.UpdateTime)
	return err
}

func MoveSpecDocument(ctx context.Context, tx *sql.Tx, id, materialID int64, updated string) error {
	_, err := tx.ExecContext(ctx, `UPDATE project_specs SET material_id=?, update_time=? WHERE id=?`, materialID, updated, id)
	return err
}
