package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// CreateMaterialRecord inserts a material row. The object bytes are written by
// the caller (material writer); this function owns only the metadata row.
func CreateMaterialRecord(ctx context.Context, s *storedb.Store, in CreateMaterialInput) (int64, error) {
	if in.Version <= 0 {
		in.Version = 1
	}
	var pk int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO materials (material_id, kind, version, title, sha256, size, path,
			                       uploaded_by, previous_material_id, create_time, update_time)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.MaterialID, in.Kind, in.Version, in.Title, in.Sha256, in.Size, in.Path,
			in.UploadedBy, in.PreviousMaterialID, in.Now, in.Now)
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: %v", storedb.ErrUniqueViolation, err)
			}
			return fmt.Errorf("v3: insert material %s: %w", in.MaterialID, err)
		}
		pk, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("v3: material id: %w", err)
		}
		return nil
	})
	return pk, err
}

// GetMaterial loads one material by business id.
func GetMaterial(ctx context.Context, q storedb.Querier, materialID string) (Material, error) {
	material, err := storedb.One[Material](ctx, q,
		`SELECT id, material_id, kind, version, title, sha256, size, path, uploaded_by,
		        previous_material_id, create_time, update_time
		   FROM materials WHERE material_id = ?`, materialID)
	if errors.Is(err, sql.ErrNoRows) {
		return Material{}, fmt.Errorf("%w: %s", ErrMaterialNotFound, materialID)
	}
	if err != nil {
		return Material{}, fmt.Errorf("v3: get material %s: %w", materialID, err)
	}
	return material, nil
}

// GetMaterialByID loads one material by physical id. Flow hooks hold physical
// ids (attachments point at them) and need the business id to read the object.
func GetMaterialByID(ctx context.Context, q storedb.Querier, id int64) (Material, error) {
	material, err := storedb.One[Material](ctx, q,
		`SELECT id, material_id, kind, version, title, sha256, size, path, uploaded_by,
		        previous_material_id, create_time, update_time
		   FROM materials WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Material{}, fmt.Errorf("%w: id %d", ErrMaterialNotFound, id)
	}
	if err != nil {
		return Material{}, fmt.Errorf("v3: get material %d: %w", id, err)
	}
	return material, nil
}

// Attach links one material to a project or a task. Exactly one owner must be
// set; the database enforces the same rule.
func Attach(ctx context.Context, s *storedb.Store, in AttachInput) (Attachment, error) {
	if (in.ProjectID == "") == (in.TaskID == "") {
		return Attachment{}, ErrInvalidAttachment
	}

	var out Attachment
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var (
			projectPK *int64
			taskPK    *int64
		)
		if in.ProjectID != "" {
			pk, err := resolveProjectPK(ctx, tx, in.ProjectID)
			if err != nil {
				return err
			}
			projectPK = &pk
		}
		if in.TaskID != "" {
			pk, err := resolveTaskPK(ctx, tx, in.TaskID)
			if err != nil {
				return err
			}
			taskPK = &pk
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO attachments (project_id, task_id, material_id, role, sort_no, create_time)
			VALUES (?, ?, ?, ?, ?, ?)`,
			projectPK, taskPK, in.MaterialID, in.Role, in.SortNo, in.Now)
		if err != nil {
			return fmt.Errorf("v3: insert attachment: %w", err)
		}
		pk, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("v3: attachment id: %w", err)
		}
		out = Attachment{
			ID:         pk,
			ProjectID:  projectPK,
			TaskID:     taskPK,
			MaterialID: in.MaterialID,
			Role:       in.Role,
			SortNo:     in.SortNo,
			CreateTime: in.Now,
		}
		return nil
	})
	return out, err
}

// ListTaskAttachments lists attachments of one task, optionally by role.
func ListTaskAttachments(ctx context.Context, q storedb.Querier, taskID string, role *enum.AttachmentRole) ([]Attachment, error) {
	const columns = `a.id, a.project_id, a.task_id, a.material_id, a.role, a.sort_no, a.create_time`
	base := `SELECT ` + columns + ` FROM attachments a
	          JOIN tasks t ON t.id = a.task_id
	         WHERE t.task_id = ?`
	if role == nil {
		return storedb.All[Attachment](ctx, q, base+` ORDER BY a.id ASC`, taskID)
	}
	return storedb.All[Attachment](ctx, q, base+` AND a.role = ? ORDER BY a.id ASC`, taskID, *role)
}

// ListProjectAttachments lists attachments of one project, optionally by role.
func ListProjectAttachments(ctx context.Context, q storedb.Querier, projectID string, role *enum.AttachmentRole) ([]Attachment, error) {
	const columns = `a.id, a.project_id, a.task_id, a.material_id, a.role, a.sort_no, a.create_time`
	base := `SELECT ` + columns + ` FROM attachments a
	          JOIN projects p ON p.id = a.project_id
	         WHERE p.project_id = ?`
	if role == nil {
		return storedb.All[Attachment](ctx, q, base+` ORDER BY a.id ASC`, projectID)
	}
	return storedb.All[Attachment](ctx, q, base+` AND a.role = ? ORDER BY a.id ASC`, projectID, *role)
}

// ProjectMaterialRef is one project material together with the identity of the
// task that produced it. It is flat on purpose: the store's column planner skips
// anonymous fields (internal/store/query.go), so MaterialRef cannot be embedded.
type ProjectMaterialRef struct {
	TaskID           string              `db:"task_id"`
	MaterialID       string              `db:"material_id"`
	Title            string              `db:"title"`
	Kind             enum.MaterialKind   `db:"kind"`
	Role             enum.AttachmentRole `db:"role"`
	SourceMaterialID *string             `db:"source_material_id"`
}

// ListProjectMaterialRefs lists the materials produced inside a project, derived
// from the tasks that produced them (attachments belong to tasks; a project has
// no attachments of its own). Version order follows attachment id; each row also
// carries the task_id that produced it.
func ListProjectMaterialRefs(ctx context.Context, q storedb.Querier, projectPK int64) ([]ProjectMaterialRef, error) {
	refs, err := storedb.All[ProjectMaterialRef](ctx, q, `
		SELECT t.task_id, `+materialRefColumns+`
		  FROM attachments a
		  JOIN materials m ON m.id = a.material_id
		  LEFT JOIN materials src ON src.id = m.source_material_id
		  JOIN tasks t ON t.id = a.task_id
		 WHERE t.project_id = ?
		 ORDER BY a.id ASC`, projectPK)
	if err != nil {
		return nil, fmt.Errorf("v3: list project materials: %w", err)
	}
	return refs, nil
}

// LatestOutput returns the newest artifact output of a task: the material a gate
// decision reviews. Context/other materials attached to the same task (e.g. a
// decision brief) are skipped, so they can never become the reviewed artifact.
func LatestOutput(ctx context.Context, q storedb.Querier, taskID string) (Attachment, error) {
	attachment, err := storedb.One[Attachment](ctx, q, `
		SELECT a.id, a.project_id, a.task_id, a.material_id, a.role, a.sort_no, a.create_time
		  FROM attachments a
		  JOIN tasks t ON t.id = a.task_id
		  JOIN materials m ON m.id = a.material_id
		 WHERE t.task_id = ? AND a.role = ? AND m.kind IN (1, 2, 3, 4)
		 ORDER BY a.id DESC LIMIT 1`, taskID, enum.Output)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, fmt.Errorf("v3: task %s has no output", taskID)
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("v3: latest output %s: %w", taskID, err)
	}
	return attachment, nil
}

// ListDecisions lists gate decisions for one task, oldest first.
func ListDecisions(ctx context.Context, q storedb.Querier, taskID string, limit int) ([]Decision, error) {
	if limit <= 0 {
		limit = 100
	}
	return storedb.All[Decision](ctx, q, `
		SELECT id, task_id, gate, material_id, decision, decided_by, reason, create_time
		  FROM gate_decisions WHERE task_id = ? ORDER BY id ASC LIMIT ?`, taskID, limit)
}

// ListEvents lists the append-only timeline of one task.
func ListEvents(ctx context.Context, q storedb.Querier, taskID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	return storedb.All[Event](ctx, q, `
		SELECT e.id, e.task_id, e.event, e.actor, e.detail_json, e.create_time
		  FROM task_events e JOIN tasks t ON t.id = e.task_id
		 WHERE t.task_id = ? ORDER BY e.id ASC LIMIT ?`, taskID, limit)
}

func resolveProjectPK(ctx context.Context, tx *sql.Tx, projectID string) (int64, error) {
	var pk int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE project_id = ?`, projectID).Scan(&pk); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
		}
		return 0, fmt.Errorf("v3: resolve project %s: %w", projectID, err)
	}
	return pk, nil
}
