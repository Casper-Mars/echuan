package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrChainBroken reports a material chain that cannot be walked back to its
// root: a previous_material_id pointing at a missing row, a repeated row (cycle)
// or a link that jumps version or kind. It is data corruption rather than a
// client error, so the API layer answers 500 and logs it.
var ErrChainBroken = errors.New("store: material chain is broken")

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

// ListChainByHead walks the version chain back from headID and returns it
// ordered by version ASC (oldest first).
//
// The chain is read through previous_material_id, never guessed from kind and
// timestamps: the head is whatever the holder (projects.requirement_material_id
// or spec_material_id) points at. A broken link (missing row, cycle, or a jump
// in version or kind) is reported as ErrChainBroken instead of returning a
// partial chain, because a partial chain would silently misrepresent the
// project history.
func ListChainByHead(ctx context.Context, q Querier, headID int64) ([]Material, error) {
	if headID <= 0 {
		return nil, fmt.Errorf("%w: invalid head id %d", ErrChainBroken, headID)
	}

	var reversed []Material
	seen := make(map[int64]struct{})
	current := headID

	for {
		if _, ok := seen[current]; ok {
			return nil, fmt.Errorf("%w: cycle at material id %d", ErrChainBroken, current)
		}
		seen[current] = struct{}{}

		row, err := GetMaterialByID(ctx, q, current)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: material id %d does not exist", ErrChainBroken, current)
			}
			return nil, fmt.Errorf("store: read material chain link %d: %w", current, err)
		}

		if len(reversed) > 0 {
			child := reversed[len(reversed)-1]
			if row.Kind != child.Kind {
				return nil, fmt.Errorf("%w: material %d has kind %d but its child %d has kind %d",
					ErrChainBroken, row.ID, row.Kind, child.ID, child.Kind)
			}
			if row.Version != child.Version-1 {
				return nil, fmt.Errorf("%w: material %d has version %d but its child %d has version %d",
					ErrChainBroken, row.ID, row.Version, child.ID, child.Version)
			}
		}

		reversed = append(reversed, row)
		if row.PreviousMaterialID == nil {
			break
		}
		current = *row.PreviousMaterialID
	}

	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}
