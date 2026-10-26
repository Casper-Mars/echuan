package store

import (
	"context"
	"database/sql"
	"fmt"
)

// materialColumns selects one full materials row.
//
// previous_material_id is the chain link, not a project reference: materials
// have no owning column, the holder (projects.requirement_material_id,
// projects.spec_material_id, tasks.report_material_id) points at the chain
// head.
const materialColumns = `m.id, m.material_id, m.kind, m.version, m.title,
	m.sha256, m.size, m.path, m.uploaded_by, m.previous_material_id,
	m.create_time, m.update_time`

// Material is the read model of one materials row.
type Material struct {
	ID                 int64  `db:"id"`
	MaterialID         string `db:"material_id"`
	Kind               int    `db:"kind"`
	Version            int    `db:"version"`
	Title              string `db:"title"`
	SHA256             string `db:"sha256"`
	Size               int64  `db:"size"`
	Path               string `db:"path"`
	UploadedBy         string `db:"uploaded_by"`
	PreviousMaterialID *int64 `db:"previous_material_id"`
	CreateTime         string `db:"create_time"`
	UpdateTime         string `db:"update_time"`
}

// NewMaterial is the write model for INSERT. Path is relative to the data
// directory so the store survives a relocation of data_dir.
type NewMaterial struct {
	MaterialID         string
	Kind               int
	Version            int
	Title              string
	SHA256             string
	Size               int64
	Path               string
	UploadedBy         string
	PreviousMaterialID *int64
	CreateTime         string
	UpdateTime         string
}

// InsertMaterial inserts one material version and returns its physical id.
func InsertMaterial(ctx context.Context, tx *sql.Tx, m NewMaterial) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO materials (material_id, kind, version, title, sha256, size, path,
		                       uploaded_by, previous_material_id, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.MaterialID, m.Kind, m.Version, m.Title, m.SHA256, m.Size, m.Path,
		m.UploadedBy, m.PreviousMaterialID, m.CreateTime, m.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return 0, fmt.Errorf("store: insert material: %w", err)
	}
	return res.LastInsertId()
}

// GetMaterialByID returns the material with the given physical id.
func GetMaterialByID(ctx context.Context, q Querier, id int64) (Material, error) {
	return One[Material](ctx, q,
		`SELECT `+materialColumns+` FROM materials m WHERE m.id = ?`, id)
}

// GetMaterialByMaterialID returns the material with the given business id.
func GetMaterialByMaterialID(ctx context.Context, q Querier, materialID string) (Material, error) {
	return One[Material](ctx, q,
		`SELECT `+materialColumns+` FROM materials m WHERE m.material_id = ?`, materialID)
}

// CountMaterialsBySHA counts the material versions recorded for one object.
// Content addressing means N versions of the same bytes share one file, so
// this number is expected to exceed 1 for re-submitted content.
func CountMaterialsBySHA(ctx context.Context, q Querier, sha256 string) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `SELECT COUNT(*) AS count FROM materials WHERE sha256 = ?`, sha256)
	if err != nil {
		return 0, fmt.Errorf("store: count materials for sha256 %s: %w", sha256, err)
	}
	return row.Count, nil
}
