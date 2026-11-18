package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

const projectColumns = `id, project_id, product_id, seq_no, name, context_md, status,
	create_time, update_time, closed_at, last_actor`

// CreateProject inserts a project for an existing product. The business id is
// `<product_id>-<seq_no>`; seq_no is allocated under the write lock so two
// concurrent creates cannot collide.
func CreateProject(ctx context.Context, s *storedb.Store, in CreateProjectInput) (Project, error) {
	var out Project

	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var productPK int64
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM products WHERE product_id = ?`, in.ProductID).Scan(&productPK); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: product %s", ErrProductNotFound, in.ProductID)
			}
			return fmt.Errorf("v3: resolve product %s: %w", in.ProductID, err)
		}

		seq, err := storedb.NextSeqNo(ctx, tx, "projects", "product_id", productPK)
		if err != nil {
			return err
		}
		projectID := fmt.Sprintf("%s-%d", in.ProductID, seq)

		res, err := tx.ExecContext(ctx, `
			INSERT INTO projects (project_id, product_id, seq_no, name, context_md, status, create_time, update_time, last_actor)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, productPK, seq, in.Name, in.ContextMD, enum.ProjectOpen, in.Now, in.Now, in.Actor)
		if err != nil {
			return fmt.Errorf("v3: insert project: %w", err)
		}
		pk, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("v3: project id: %w", err)
		}

		out = Project{
			ID:         pk,
			ProjectID:  projectID,
			ProductID:  productPK,
			SeqNo:      seq,
			Name:       in.Name,
			ContextMD:  in.ContextMD,
			Status:     enum.ProjectOpen,
			CreateTime: in.Now,
			UpdateTime: in.Now,
		}
		if in.Actor != "" {
			out.LastActor = &in.Actor
		}
		return nil
	})
	return out, err
}

// GetProject loads one project by business id.
func GetProject(ctx context.Context, q storedb.Querier, projectID string) (Project, error) {
	project, err := storedb.One[Project](ctx, q,
		`SELECT `+projectColumns+` FROM projects WHERE project_id = ?`, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	if err != nil {
		return Project{}, fmt.Errorf("v3: get project %s: %w", projectID, err)
	}
	return project, nil
}

// ListProjects lists projects ordered by id with an optional status filter.
func ListProjects(ctx context.Context, q storedb.Querier, status *enum.ProjectStatus, afterID int64, limit int) ([]Project, error) {
	if limit <= 0 {
		limit = 50
	}
	if status == nil {
		return storedb.All[Project](ctx, q,
			`SELECT `+projectColumns+` FROM projects WHERE id > ? ORDER BY id ASC LIMIT ?`,
			afterID, limit)
	}
	return storedb.All[Project](ctx, q,
		`SELECT `+projectColumns+` FROM projects WHERE id > ? AND status = ? ORDER BY id ASC LIMIT ?`,
		afterID, *status, limit)
}

// CloseProject closes a project. Active tasks block the close unless force is
// set; force cancels every unfinished task in the same transaction.
func CloseProject(ctx context.Context, s *storedb.Store, projectID string, force bool, actor, now string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var (
			pk     int64
			status enum.ProjectStatus
		)
		if err := tx.QueryRowContext(ctx,
			`SELECT id, status FROM projects WHERE project_id = ?`, projectID).Scan(&pk, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
			}
			return fmt.Errorf("v3: close project %s: %w", projectID, err)
		}
		if status == enum.ProjectClosed {
			return nil
		}

		var active int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM tasks
			 WHERE project_id = ? AND status IN (?, ?, ?, ?)`,
			pk, enum.Pending, enum.Ready, enum.InProgress, enum.InReview).Scan(&active); err != nil {
			return fmt.Errorf("v3: count active tasks: %w", err)
		}
		if active > 0 {
			if !force {
				return fmt.Errorf("%w: %d unfinished", ErrProjectNotEmpty, active)
			}
			if err := cancelActiveTasks(ctx, tx, pk, actor, now); err != nil {
				return err
			}
		}

		_, err := tx.ExecContext(ctx, `
			UPDATE projects
			   SET status = ?, closed_at = ?, update_time = ?, last_actor = ?
			 WHERE id = ?`,
			enum.ProjectClosed, now, now, actor, pk)
		if err != nil {
			return fmt.Errorf("v3: close project %s: %w", projectID, err)
		}
		return nil
	})
}

// ReopenProject flips a closed project back to open. Cancelled tasks stay
// cancelled: reopening is not a time machine.
func ReopenProject(ctx context.Context, s *storedb.Store, projectID, actor, now string) error {
	res, err := s.Write().ExecContext(ctx, `
		UPDATE projects
		   SET status = ?, closed_at = NULL, update_time = ?, last_actor = ?
		 WHERE project_id = ? AND status = ?`,
		enum.ProjectOpen, now, actor, projectID, enum.ProjectClosed)
	if err != nil {
		return fmt.Errorf("v3: reopen project %s: %w", projectID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("v3: reopen project %s: rows: %w", projectID, err)
	}
	if n == 0 {
		if _, err := GetProject(ctx, s.Read(), projectID); err != nil {
			return err
		}
	}
	return nil
}

// UpdateProjectContext updates the mutable container fields.
func UpdateProjectContext(ctx context.Context, s *storedb.Store, projectID, name, contextMD, actor, now string) error {
	res, err := s.Write().ExecContext(ctx, `
		UPDATE projects
		   SET name = ?, context_md = ?, update_time = ?, last_actor = ?
		 WHERE project_id = ?`,
		name, contextMD, now, actor, projectID)
	if err != nil {
		return fmt.Errorf("v3: update project %s: %w", projectID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("v3: update project %s: rows: %w", projectID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	return nil
}

// cancelActiveTasks cancels pending/ready/in_progress/in_review tasks of one
// project and clears their leases. Each cancellation writes an event.
func cancelActiveTasks(ctx context.Context, tx *sql.Tx, projectPK int64, actor, now string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, task_id FROM tasks
		 WHERE project_id = ? AND status IN (?, ?, ?, ?)`,
		projectPK, enum.Pending, enum.Ready, enum.InProgress, enum.InReview)
	if err != nil {
		return fmt.Errorf("v3: list active tasks: %w", err)
	}
	type active struct {
		pk  int64
		key string
	}
	var items []active
	for rows.Next() {
		var item active
		if err := rows.Scan(&item.pk, &item.key); err != nil {
			rows.Close()
			return fmt.Errorf("v3: scan active task: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("v3: iterate active tasks: %w", err)
	}
	rows.Close()

	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, lease_id = NULL, lease_until = NULL, worker_id = NULL, update_time = ?
			 WHERE id = ?`,
			enum.Cancelled, now, item.pk); err != nil {
			return fmt.Errorf("v3: cancel task %s: %w", item.key, err)
		}
		if err := appendEvent(ctx, tx, item.pk, enum.EventCancelled, actor, `{"reason":"project closed"}`, now); err != nil {
			return err
		}
	}
	return nil
}
