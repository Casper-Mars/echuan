package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Gate decision recording and history (tech design §5, task-0011).
//
// Every human approve/reject writes one gate_decisions row inside the same
// transaction as the state change. Automatic settlements (settlePhaseWorkItem)
// are not decisions and write nothing.

// GateMaterial identifies the material version a decision was made on.
type GateMaterial struct {
	MaterialID string
	Version    int
}

// gateDecisionWrite is the internal input of one append.
//
// MaterialPhysicalID is the materials(id) pointer of the reviewed artifact;
// nil is allowed only for the documented plan-without-material path.
type gateDecisionWrite struct {
	SubjectType        string
	SubjectID          string
	Gate               string
	MaterialPhysicalID *int64
	Decision           string
	Reason             string
	Actor              string
	CreateTime         string
}

// recordGateDecision appends one decision row and returns the material it was
// bound to (nil when the record has no material).
//
// The caller passes the transaction that performs the state change, so a
// failed transition rolls the record back and a recorded decision can never
// describe a write that did not happen.
func recordGateDecision(ctx context.Context, tx *sql.Tx, in gateDecisionWrite) (*GateMaterial, error) {
	var reasonPtr *string
	if reason := strings.TrimSpace(in.Reason); reason != "" {
		reasonPtr = &reason
	}

	var material *GateMaterial
	if in.MaterialPhysicalID != nil {
		row, err := store.GetMaterialByID(ctx, tx, *in.MaterialPhysicalID)
		if err != nil {
			return nil, err
		}
		material = &GateMaterial{MaterialID: row.MaterialID, Version: row.Version}
	}

	_, err := store.CreateGateDecision(ctx, tx, store.NewGateDecision{
		SubjectType: in.SubjectType,
		SubjectID:   in.SubjectID,
		Gate:        in.Gate,
		MaterialID:  in.MaterialPhysicalID,
		Decision:    in.Decision,
		DecidedBy:   actorOrDefault(in.Actor),
		Reason:      reasonPtr,
		CreateTime:  in.CreateTime,
	})
	if err != nil {
		return nil, err
	}
	return material, nil
}

// projectGate names the review gate a project status represents. The second
// result is false for statuses that are not review gates.
func projectGate(status int) (string, bool) {
	switch status {
	case domain.StatusRequirementReview:
		return "requirement", true
	case domain.StatusSpecReview:
		return "spec", true
	}
	return "", false
}

// ListGateDecisionsInput is the use-case input of GET /v1/gate-decisions.
type ListGateDecisionsInput struct {
	ProjectID   string
	SubjectType string
	SubjectID   string
	AfterID     int64
	Limit       int
}

// GateDecisionView is one decision row rendered by the API.
type GateDecisionView struct {
	ID              int64
	SubjectType     string
	SubjectID       string
	Gate            string
	MaterialID      string
	MaterialVersion int
	Decision        string
	DecidedBy       string
	Reason          string
	CreateTime      string
}

// GateDecisionPage is one page of history plus the physical id of the last
// item when more rows exist.
type GateDecisionPage struct {
	Items  []GateDecisionView
	NextID int64
}

// ListGateDecisions returns a project's whole decision history (project-level
// plus every work-item-level record) ordered newest first.
//
// An unknown project is a 404 rather than an empty page, mirroring the
// work-item list: a mistyped project number must not look like a project
// without decisions.
func (s *ProjectService) ListGateDecisions(ctx context.Context, in ListGateDecisionsInput) (GateDecisionPage, error) {
	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		return GateDecisionPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "project_id", Reason: "is required"},
		}}
	}
	if in.Limit < 1 || in.Limit > maxListLimit {
		return GateDecisionPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "limit", Reason: fmt.Sprintf("must be between 1 and %d", maxListLimit)},
		}}
	}
	if in.AfterID < 0 {
		return GateDecisionPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "cursor", Reason: "is invalid"},
		}}
	}

	subjectType := strings.TrimSpace(in.SubjectType)
	if subjectType != "" && !store.IsValidGateDecisionSubjectType(subjectType) {
		return GateDecisionPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "subject_type", Reason: "must be project or work_item"},
		}}
	}
	subjectID := strings.TrimSpace(in.SubjectID)

	project, err := store.GetProjectByProjectID(ctx, s.store.Read(), projectID)
	if err != nil {
		if store.IsNotFound(err) {
			return GateDecisionPage{}, &domain.NotFoundError{Resource: "project", Key: projectID}
		}
		return GateDecisionPage{}, err
	}

	rows, err := store.ListGateDecisions(ctx, s.store.Read(), store.GateDecisionFilter{
		ProjectID:         project.ProjectID,
		ProjectPhysicalID: project.ID,
		SubjectType:       subjectType,
		SubjectID:         subjectID,
		AfterID:           in.AfterID,
		Limit:             in.Limit + 1,
	})
	if err != nil {
		return GateDecisionPage{}, err
	}

	page := GateDecisionPage{Items: make([]GateDecisionView, 0, min(len(rows), in.Limit))}
	if len(rows) > in.Limit {
		rows = rows[:in.Limit]
		page.NextID = rows[len(rows)-1].ID
	}
	for _, row := range rows {
		view := GateDecisionView{
			ID:          row.ID,
			SubjectType: row.SubjectType,
			SubjectID:   row.SubjectID,
			Gate:        row.Gate,
			Decision:    row.Decision,
			DecidedBy:   row.DecidedBy,
			Reason:      stringValue(row.Reason),
			CreateTime:  row.CreateTime,
		}
		if row.MaterialCode != nil {
			view.MaterialID = *row.MaterialCode
		}
		if row.MaterialVersion != nil {
			view.MaterialVersion = *row.MaterialVersion
		}
		page.Items = append(page.Items, view)
	}
	return page, nil
}
