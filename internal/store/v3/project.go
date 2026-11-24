package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

const projectColumns = `p.id, p.project_id, p.product_id, p.seq_no, p.name, p.context_md, p.status,
	p.create_time, p.update_time, p.closed_at, p.last_actor, pr.product_id AS product_code`

const projectFrom = ` FROM projects p JOIN products pr ON pr.id = p.product_id`

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
			ID:          pk,
			ProjectID:   projectID,
			ProductID:   productPK,
			ProductCode: in.ProductID,
			SeqNo:       seq,
			Name:        in.Name,
			ContextMD:   in.ContextMD,
			Status:      enum.ProjectOpen,
			CreateTime:  in.Now,
			UpdateTime:  in.Now,
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
		`SELECT `+projectColumns+projectFrom+` WHERE p.project_id = ?`, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	if err != nil {
		return Project{}, fmt.Errorf("v3: get project %s: %w", projectID, err)
	}
	return project, nil
}

// projectFirstPageAfterID is the keyset sentinel meaning "no after_id given".
//
// The list is newest first (ORDER BY p.id DESC), so the cursor direction is
// `p.id < ?` and the old "0 means the first page" convention no longer works:
// `p.id < 0` is always false and would return an empty first page. Using the
// largest possible id keeps the condition always true (every generated id is
// smaller), i.e. exactly the "always true" shape the ascending query had.
// Chosen over splicing the predicate conditionally (方案 §6 option ②) because
// it keeps one SQL shape for all callers: no query-string assembly, no risk of
// forgetting the predicate on a future branch.
const projectFirstPageAfterID int64 = math.MaxInt64

// ListProjects lists projects newest first (p.id DESC) with an optional status
// filter and an optional product business id filter.
//
// afterID is a keyset cursor: rows older than that id are returned, so passing
// the previous page's last id walks towards older projects. afterID <= 0 means
// "first page" (see projectFirstPageAfterID). productID is the business id of
// products.product_id (projects.product_id is the internal FK), trimmed; an
// empty value means "no product filter" and an unknown value yields no rows.
func ListProjects(ctx context.Context, q storedb.Querier, status *enum.ProjectStatus, productID string, afterID int64, limit int) ([]Project, error) {
	if limit <= 0 {
		limit = 50
	}
	if afterID <= 0 {
		afterID = projectFirstPageAfterID
	}

	query := `SELECT ` + projectColumns + projectFrom + ` WHERE p.id < ?`
	args := []any{afterID}
	if status != nil {
		query += ` AND p.status = ?`
		args = append(args, *status)
	}
	if code := strings.TrimSpace(productID); code != "" {
		query += ` AND pr.product_id = ?`
		args = append(args, code)
	}
	query += ` ORDER BY p.id DESC LIMIT ?`
	args = append(args, limit)

	return storedb.All[Project](ctx, q, query, args...)
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
// project, clears their leases and releases the workers that pointed at them.
// Each cancellation writes an event.
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
		// Force close is a bulk cancel, so it owes the same release a
		// single-task cancel owes (issue #165 §1).
		if err := releaseWorkerForTask(ctx, tx, item.pk, now); err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, item.pk, enum.EventCancelled, actor, `{"reason":"project closed"}`, now); err != nil {
			return err
		}
	}
	return nil
}
