package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Gate decisions (tech design §5, task-0011).
//
// One row answers "which material version was approved or rejected, by whom
// and why". The material is stored by physical id, and every version of a
// chain has its own row, so a record keeps pointing at v1 after the document
// moves on to v2. Rows are append-only: nothing in the service updates or
// deletes them.

// Gate decision subject types.
const (
	GateSubjectProject  = "project"
	GateSubjectWorkItem = "work_item"
)

// GateDecisionSubjectTypes lists the values accepted by the schema CHECK.
var GateDecisionSubjectTypes = []string{GateSubjectProject, GateSubjectWorkItem}

// IsValidGateDecisionSubjectType reports whether subjectType is a schema value.
func IsValidGateDecisionSubjectType(subjectType string) bool {
	switch subjectType {
	case GateSubjectProject, GateSubjectWorkItem:
		return true
	}
	return false
}

// GateDecision is the read model of one gate_decisions row.
//
// MaterialVersion is resolved through the materials join and is nil when the
// record carries no material (the documented "plan without a result material"
// path).
type GateDecision struct {
	ID              int64   `db:"id"`
	SubjectType     string  `db:"subject_type"`
	SubjectID       string  `db:"subject_id"`
	Gate            string  `db:"gate"`
	MaterialID      *int64  `db:"material_id"`
	MaterialCode    *string `db:"material_code"`
	MaterialVersion *int    `db:"material_version"`
	Decision        string  `db:"decision"`
	DecidedBy       string  `db:"decided_by"`
	Reason          *string `db:"reason"`
	CreateTime      string  `db:"create_time"`
}

// NewGateDecision is the write model for INSERT. MaterialID is nil for the
// "plan batch without a result material" path; DecidedBy is already normalised
// to "unknown" by the caller.
type NewGateDecision struct {
	SubjectType string
	SubjectID   string
	Gate        string
	MaterialID  *int64
	Decision    string
	DecidedBy   string
	Reason      *string
	CreateTime  string
}

// GateDecisionFilter narrows one project's decision history.
//
// ProjectID (business id) and ProjectPhysicalID select the project-level rows
// plus every work-item row of that project. SubjectType/SubjectID are the
// optional front-end filters; they apply on top of the project scope. AfterID
// is the keyset cursor (records with id < AfterID).
type GateDecisionFilter struct {
	ProjectID         string
	ProjectPhysicalID int64
	SubjectType       string
	SubjectID         string
	AfterID           int64
	Limit             int
}

// CreateGateDecision appends one decision record inside the caller's
// transaction, so a failed status write rolls the record back with it.
func CreateGateDecision(ctx context.Context, tx *sql.Tx, d NewGateDecision) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO gate_decisions (subject_type, subject_id, gate, material_id,
		                            decision, decided_by, reason, create_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.SubjectType, d.SubjectID, d.Gate, d.MaterialID,
		d.Decision, d.DecidedBy, d.Reason, d.CreateTime)
	if err != nil {
		return 0, fmt.Errorf("store: create gate decision: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: create gate decision id: %w", err)
	}
	return id, nil
}

// ListGateDecisions returns one page of a project's decision history ordered
// newest first (descending id).
//
// The project scope is expressed in SQL rather than by pre-resolving work ids,
// so the list stays one statement and a project with thousands of cards does
// not build a parameter list. Work-item rows are matched by the project's
// business code, exactly like the API's subject ids.
func ListGateDecisions(ctx context.Context, q Querier, filter GateDecisionFilter) ([]GateDecision, error) {
	var where []string
	var args []any

	if filter.AfterID > 0 {
		where = append(where, "g.id < ?")
		args = append(args, filter.AfterID)
	}

	if filter.ProjectID != "" {
		where = append(where, `((g.subject_type = ? AND g.subject_id = ?)
			OR (g.subject_type = ? AND g.subject_id IN (
				SELECT w.work_id FROM work_items w WHERE w.project_id = ?)))`)
		args = append(args, GateSubjectProject, filter.ProjectID,
			GateSubjectWorkItem, filter.ProjectPhysicalID)
	}
	if filter.SubjectType != "" {
		where = append(where, "g.subject_type = ?")
		args = append(args, filter.SubjectType)
	}
	if filter.SubjectID != "" {
		where = append(where, "g.subject_id = ?")
		args = append(args, filter.SubjectID)
	}
	args = append(args, filter.Limit)

	query := `SELECT g.id, g.subject_type, g.subject_id, g.gate, g.material_id,
	                 g.decision, g.decided_by, g.reason, g.create_time,
	                 m.material_id AS material_code,
	                 m.version AS material_version
	            FROM gate_decisions g
	            LEFT JOIN materials m ON m.id = g.material_id`
	if len(where) > 0 {
		query += `
	           WHERE ` + strings.Join(where, " AND ")
	}
	query += `
	           ORDER BY g.id DESC
	           LIMIT ?`
	return All[GateDecision](ctx, q, query, args...)
}

// LatestWorkItemResultMaterialID returns result_material_id of the newest card
// of one kind in a project, or nil when the project has no such card or the
// newest card has no result.
//
// The plan gate uses it to bind the approved plan to the breakdown card's
// kind=4 material. It is read before settlePhaseWorkItem accepts the card, so
// the material pointer is still the one produced by the report.
func LatestWorkItemResultMaterialID(ctx context.Context, q Querier, projectID int64, kind string) (*int64, error) {
	item, err := One[WorkItem](ctx, q,
		`SELECT `+workItemColumns+`
		   FROM work_items t JOIN projects p ON p.id = t.project_id
		  WHERE t.project_id = ? AND t.kind = ?
		  ORDER BY t.id DESC
		  LIMIT 1`, projectID, kind)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: latest %s work item: %w", kind, err)
	}
	return item.ResultMaterialID, nil
}
