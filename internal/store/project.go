package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// projectColumns selects one project together with its product's business id
// and its task counters.
//
// The counters are correlated subqueries so listing N projects stays one
// statement (no N+1) and both numbers are read in the same snapshot as the
// project row. tasks.status = 4 is domain.TaskStatusDone; the constant lives
// in domain, so it is spelled out here as a literal rather than making store
// depend on domain (the dependency runs domain ← store ← app ← api).
const projectColumns = `p.id, p.project_id, pr.product_id AS product_code, p.seq_no,
	p.title, p.requirement_material_id, p.spec_material_id, p.status, p.reason,
	p.last_actor, p.version,
	(SELECT COUNT(*) FROM tasks t WHERE t.project_id = p.id) AS task_total,
	(SELECT COUNT(*) FROM tasks t WHERE t.project_id = p.id AND t.status = 4) AS task_done,
	p.create_time, p.update_time`

// Project is the read model of one projects row.
//
// ProductCode is the product's business id ("APP"); projects.product_id itself
// is the physical foreign key and never leaves this package.
type Project struct {
	ID                    int64   `db:"id"`
	ProjectID             string  `db:"project_id"`
	ProductCode           string  `db:"product_code"`
	SeqNo                 int     `db:"seq_no"`
	Title                 string  `db:"title"`
	RequirementMaterialID *int64  `db:"requirement_material_id"`
	SpecMaterialID        *int64  `db:"spec_material_id"`
	Status                int     `db:"status"`
	Reason                *string `db:"reason"`
	LastActor             *string `db:"last_actor"`
	Version               int     `db:"version"`
	TaskTotal             int64   `db:"task_total"`
	TaskDone              int64   `db:"task_done"`
	CreateTime            string  `db:"create_time"`
	UpdateTime            string  `db:"update_time"`
}

// NewProject is the write model for INSERT. RequirementMaterialID is nil only
// for the instant between inserting the project and backfilling the pointer in
// the same transaction.
type NewProject struct {
	ProjectID             string
	ProductID             int64
	SeqNo                 int
	Title                 string
	RequirementMaterialID *int64
	Status                int
	Reason                *string
	Version               int
	CreateTime            string
	UpdateTime            string
}

// ProjectFilter narrows a project list. The zero value lists every project.
type ProjectFilter struct {
	// ProductCode filters by the product's business id; empty means "any".
	ProductCode string
	// Status filters by projects.status when non-nil.
	Status *int
	// AfterID is the physical id of the last item of the previous page.
	AfterID int64
	// Limit is the maximum number of rows to return.
	Limit int
}

// GetProjectByID returns the project with the given physical id.
func GetProjectByID(ctx context.Context, q Querier, id int64) (Project, error) {
	return One[Project](ctx, q,
		`SELECT `+projectColumns+`
		   FROM projects p JOIN products pr ON pr.id = p.product_id
		  WHERE p.id = ?`, id)
}

// GetProjectByProjectID returns the project with the given business id.
func GetProjectByProjectID(ctx context.Context, q Querier, projectID string) (Project, error) {
	return One[Project](ctx, q,
		`SELECT `+projectColumns+`
		   FROM projects p JOIN products pr ON pr.id = p.product_id
		  WHERE p.project_id = ?`, projectID)
}

// ListProjects returns up to filter.Limit projects ordered by physical id.
func ListProjects(ctx context.Context, q Querier, filter ProjectFilter) ([]Project, error) {
	where := []string{"p.id > ?"}
	args := []any{filter.AfterID}

	if filter.ProductCode != "" {
		where = append(where, "pr.product_id = ?")
		args = append(args, filter.ProductCode)
	}
	if filter.Status != nil {
		where = append(where, "p.status = ?")
		args = append(args, *filter.Status)
	}
	args = append(args, filter.Limit)

	query := `SELECT ` + projectColumns + `
	            FROM projects p JOIN products pr ON pr.id = p.product_id
	           WHERE ` + strings.Join(where, " AND ") + `
	           ORDER BY p.id ASC
	           LIMIT ?`
	return All[Project](ctx, q, query, args...)
}

// CreateProject inserts a project and returns its physical id.
//
// Both UNIQUE constraints (project_id, and (product_id, seq_no)) surface as
// ErrUniqueViolation; the write never relies on a prior SELECT.
func CreateProject(ctx context.Context, tx *sql.Tx, p NewProject) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO projects (project_id, product_id, seq_no, title,
		                      requirement_material_id, status, reason, version,
		                      create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectID, p.ProductID, p.SeqNo, p.Title, p.RequirementMaterialID,
		p.Status, p.Reason, p.Version, p.CreateTime, p.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return 0, fmt.Errorf("store: insert project: %w", err)
	}
	return res.LastInsertId()
}

// UpdateProjectTitle renames one project and bumps its version.
//
// Only the title is business-writable (tech design §4.3); every other column
// is platform-maintained.
func UpdateProjectTitle(ctx context.Context, tx *sql.Tx, id int64, title, updateTime string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE projects
		   SET title = ?, version = version + 1, update_time = ?
		 WHERE id = ?`, title, updateTime, id)
	if err != nil {
		return fmt.Errorf("store: update project %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update project %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update project %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// TransitionProject applies one state-machine edge with a conditional UPDATE.
//
// The WHERE clause carries both the status and the version that were read
// inside the same transaction, so a concurrent writer can never be overwritten
// even if the transaction is retried. The returned count is 0 when the row
// moved between the read and the write; the caller turns that into
// invalid_version (or invalid_state) after re-reading.
func TransitionProject(ctx context.Context, tx *sql.Tx, id int64, expectStatus, expectVersion, nextStatus int, reason *string, lastActor, updateTime string) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE projects
		   SET status = ?, reason = ?, last_actor = ?, version = version + 1, update_time = ?
		 WHERE id = ? AND status = ? AND version = ?`,
		nextStatus, reason, lastActor, updateTime, id, expectStatus, expectVersion)
	if err != nil {
		return 0, fmt.Errorf("store: transition project %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: transition project %d: rows affected: %w", id, err)
	}
	return n, nil
}

// AttachRequirementPointer sets requirement_material_id right after a project
// row is inserted. It deliberately does not bump version: creating a project
// is version 1, and the backfill is part of the same create.
func AttachRequirementPointer(ctx context.Context, tx *sql.Tx, id, materialID int64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE projects SET requirement_material_id = ? WHERE id = ?`, materialID, id)
	if err != nil {
		return fmt.Errorf("store: attach requirement material to project %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: attach requirement material to project %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: attach requirement material to project %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// MoveRequirementPointer points requirement_material_id at a new material
// version and bumps the project version.
func MoveRequirementPointer(ctx context.Context, tx *sql.Tx, id, materialID int64, updateTime string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE projects
		   SET requirement_material_id = ?, version = version + 1, update_time = ?
		 WHERE id = ?`, materialID, updateTime, id)
	if err != nil {
		return fmt.Errorf("store: move requirement pointer of project %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: move requirement pointer of project %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: move requirement pointer of project %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// DeleteProject removes one project by physical id. Materials are not touched:
// they are content-addressed versions, not project children (tech design
// §5.7 — no physical deletion).
func DeleteProject(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete project %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete project %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete project %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// CountTasksByProject counts the tasks owned by one project.
//
// It is meant to run inside the same transaction as the DELETE so that no task
// can be attached between the check and the removal.
func CountTasksByProject(ctx context.Context, q Querier, projectID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `SELECT COUNT(*) AS count FROM tasks WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, fmt.Errorf("store: count tasks for project %d: %w", projectID, err)
	}
	return row.Count, nil
}

// TaskStatusHistogram returns tasks per status for one project. Statuses with
// no task are absent from the map.
func TaskStatusHistogram(ctx context.Context, q Querier, projectID int64) (map[int]int64, error) {
	rows, err := All[struct {
		Status int   `db:"status"`
		Count  int64 `db:"count"`
	}](ctx, q, `SELECT status, COUNT(*) AS count FROM tasks WHERE project_id = ? GROUP BY status`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: task histogram for project %d: %w", projectID, err)
	}
	histogram := make(map[int]int64, len(rows))
	for _, row := range rows {
		histogram[row.Status] = row.Count
	}
	return histogram, nil
}
