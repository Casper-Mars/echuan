package store

import (
	"context"
	"fmt"
)

// WorkItemInput is one row of work_inputs joined with its material.
//
// MaterialID is the business id (MAT-…), Kind is the material kind (the same
// 1..4 vocabulary as materials.kind) and Version is the material version. The
// dispatch payload carries exactly these three fields, so the join happens
// here instead of leaving the application layer to resolve each input.
type WorkItemInput struct {
	MaterialID string `db:"material_id"`
	Kind       int    `db:"kind"`
	Version    int    `db:"version"`
}

// ListWorkItemInputs returns the ordered inputs of one work item.
//
// The order is the material id (the join key), which is stable for a given
// item; dispatch does not promise insertion order, only a deterministic one.
func ListWorkItemInputs(ctx context.Context, q Querier, workItemID int64) ([]WorkItemInput, error) {
	rows, err := All[WorkItemInput](ctx, q, `
		SELECT m.material_id, m.kind, m.version
		  FROM work_inputs wi
		  JOIN materials m ON m.id = wi.material_id
		 WHERE wi.work_item_id = ?
		 ORDER BY m.material_id ASC`, workItemID)
	if err != nil {
		return nil, fmt.Errorf("store: list inputs of work item %d: %w", workItemID, err)
	}
	return rows, nil
}
