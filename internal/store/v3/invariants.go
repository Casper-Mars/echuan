package v3

import (
	"context"
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

// InvariantError reports a state that the V3 data model says can never happen.
// It is used by tests and by the operator-facing health check; a production hit
// means a bug, not a user error.
type InvariantError struct {
	Name  string
	Count int
}

func (e *InvariantError) Error() string {
	return fmt.Sprintf("v3 invariant %q violated by %d row(s)", e.Name, e.Count)
}

// CheckInvariants verifies the two cross-row invariants that CHECK constraints
// cannot express:
//
//  1. no pending task whose dependencies are all done (a missed promotion);
//  2. no closed project that still has an active task.
func CheckInvariants(ctx context.Context, q storedb.Querier) error {
	stuckRow, err := storedb.One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM tasks t
		 WHERE t.status = ?
		   AND NOT EXISTS (
		         SELECT 1 FROM task_deps d JOIN tasks u ON u.id = d.depends_on_task_id
		          WHERE d.task_id = t.id AND u.status <> ?)`,
		enum.Pending, enum.Done)
	if err != nil {
		return fmt.Errorf("v3: check pending invariant: %w", err)
	}
	if stuckRow.Count > 0 {
		return &InvariantError{Name: "pending_with_satisfied_dependencies", Count: int(stuckRow.Count)}
	}

	leakedRow, err := storedb.One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `
		SELECT COUNT(*) AS count
		  FROM tasks t JOIN projects p ON p.id = t.project_id
		 WHERE p.status = ? AND t.status IN (?, ?, ?, ?)`,
		enum.ProjectClosed, enum.Pending, enum.Ready, enum.InProgress, enum.InReview)
	if err != nil {
		return fmt.Errorf("v3: check closed-project invariant: %w", err)
	}
	if leakedRow.Count > 0 {
		return &InvariantError{Name: "closed_project_with_active_task", Count: int(leakedRow.Count)}
	}
	return nil
}
