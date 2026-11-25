package v3

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// MaterialRef is one attachment projected for a dispatch payload.
type MaterialRef struct {
	MaterialID string              `db:"material_id"`
	Title      string              `db:"title"`
	Kind       enum.MaterialKind   `db:"kind"`
	Role       enum.AttachmentRole `db:"role"`
	// SourceMaterialID is the material this one describes (a brief points at
	// its artifact version); nil for ordinary artifacts.
	SourceMaterialID *string `db:"source_material_id"`
}

// UpstreamRef is one finished dependency and its latest output, if any.
type UpstreamRef struct {
	TaskID     string  `db:"task_id"`
	MaterialID *string `db:"material_id"`
	Title      *string `db:"title"`
}

const materialRefColumns = `m.material_id, m.title, m.kind, a.role, src.material_id AS source_material_id`

// ListTaskInputs lists input attachments of one task.
func ListTaskInputs(ctx context.Context, q storedb.Querier, taskPK int64) ([]MaterialRef, error) {
	refs, err := storedb.All[MaterialRef](ctx, q, `
		SELECT `+materialRefColumns+`
		  FROM attachments a
		  JOIN materials m ON m.id = a.material_id
		  LEFT JOIN materials src ON src.id = m.source_material_id
		 WHERE a.task_id = ? AND a.role = ?
		 ORDER BY a.sort_no ASC, a.id ASC`, taskPK, enum.Input)
	if err != nil {
		return nil, fmt.Errorf("v3: list task inputs: %w", err)
	}
	return refs, nil
}

// ListProjectInputs lists project-level input attachments (background docs).
func ListProjectInputs(ctx context.Context, q storedb.Querier, projectPK int64) ([]MaterialRef, error) {
	refs, err := storedb.All[MaterialRef](ctx, q, `
		SELECT `+materialRefColumns+`
		  FROM attachments a
		  JOIN materials m ON m.id = a.material_id
		  LEFT JOIN materials src ON src.id = m.source_material_id
		 WHERE a.project_id = ? AND a.role = ?
		 ORDER BY a.sort_no ASC, a.id ASC`, projectPK, enum.Input)
	if err != nil {
		return nil, fmt.Errorf("v3: list project inputs: %w", err)
	}
	return refs, nil
}

// ListUpstreamOutputs lists the dependencies of one task and the latest artifact
// output of each, so the worker can hand the agent its input artifacts. Only
// requirement/design/plan/report materials count as artifacts: context/other
// attachments (e.g. a decision brief) are skipped, matching the gate contract
// in LatestOutput.
func ListUpstreamOutputs(ctx context.Context, q storedb.Querier, taskPK int64) ([]UpstreamRef, error) {
	rows, err := storedb.All[struct {
		TaskID     string         `db:"task_id"`
		MaterialID sql.NullString `db:"material_id"`
		Title      sql.NullString `db:"title"`
	}](ctx, q, `
		SELECT u.task_id,
		       (SELECT m.material_id FROM attachments a
		  JOIN materials m ON m.id = a.material_id
		  LEFT JOIN materials src ON src.id = m.source_material_id
		         WHERE a.task_id = u.id AND a.role = ? AND m.kind IN (1, 2, 3, 4)
		         ORDER BY a.id DESC LIMIT 1) AS material_id,
		       (SELECT m.title FROM attachments a
		  JOIN materials m ON m.id = a.material_id
		  LEFT JOIN materials src ON src.id = m.source_material_id
		         WHERE a.task_id = u.id AND a.role = ? AND m.kind IN (1, 2, 3, 4)
		         ORDER BY a.id DESC LIMIT 1) AS title
		  FROM task_deps d JOIN tasks u ON u.id = d.depends_on_task_id
		 WHERE d.task_id = ?
		 ORDER BY u.id ASC`, enum.Output, enum.Output, taskPK)
	if err != nil {
		return nil, fmt.Errorf("v3: list upstream outputs: %w", err)
	}
	out := make([]UpstreamRef, 0, len(rows))
	for _, row := range rows {
		ref := UpstreamRef{TaskID: row.TaskID}
		if row.MaterialID.Valid {
			value := row.MaterialID.String
			ref.MaterialID = &value
		}
		if row.Title.Valid {
			value := row.Title.String
			ref.Title = &value
		}
		out = append(out, ref)
	}
	return out, nil
}

// HasDownstream reports whether a downstream task of one kind depends on this
// task. The claim envelope uses it to answer "is this the last card of the
// stage": HasDownstreamDev is the built-in default flow's answer for dev.
func HasDownstream(ctx context.Context, q storedb.Querier, taskPK int64, kind enum.TaskKind) (bool, error) {
	if !kind.Valid() {
		return false, fmt.Errorf("v3: has downstream: invalid kind %d", int(kind))
	}
	var count int
	row, err := storedb.One[struct {
		Count int `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM task_deps d JOIN tasks t ON t.id = d.task_id
		 WHERE d.depends_on_task_id = ? AND t.kind = ?`, taskPK, kind)
	if err != nil {
		return false, fmt.Errorf("v3: count downstream tasks of kind %d: %w", int(kind), err)
	}
	count = row.Count
	return count > 0, nil
}

// HasDownstreamDev reports whether another dev task depends on this task; the
// last dev task opens the project merge PR. The stage is parameterised
// (HasDownstream) because a configured flow may move the closing stage off dev;
// this wrapper keeps the built-in default flow's meaning at every call site.
func HasDownstreamDev(ctx context.Context, q storedb.Querier, taskPK int64) (bool, error) {
	return HasDownstream(ctx, q, taskPK, enum.Dev)
}

// ListTaskAttachmentRefs lists every attachment of one task with the material
// business id, for the task detail drawer.
func ListTaskAttachmentRefs(ctx context.Context, q storedb.Querier, taskPK int64) ([]MaterialRef, error) {
	refs, err := storedb.All[MaterialRef](ctx, q, `
		SELECT `+materialRefColumns+`
		  FROM attachments a
		  JOIN materials m ON m.id = a.material_id
		  LEFT JOIN materials src ON src.id = m.source_material_id
		 WHERE a.task_id = ?
		 ORDER BY a.role ASC, a.sort_no ASC, a.id ASC`, taskPK)
	if err != nil {
		return nil, fmt.Errorf("v3: list task attachments: %w", err)
	}
	return refs, nil
}
