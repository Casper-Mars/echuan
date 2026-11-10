package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// DecideKind selects which state machine a unified decision runs against.
type DecideKind string

const (
	// DecideKindProject drives projects.status (0–8).
	DecideKindProject DecideKind = "project"
)

// DecideInput is the unified decision request.
//
// ID is the business id ("APP-1" or "APP-1-T0001"). ExpectVersion is the
// If-Match version; the decision endpoints require it, while submit/cancel
// call the project transition helper directly and pass nil.
type DecideInput struct {
	Kind          DecideKind
	ID            string
	Decision      domain.Decision
	Reason        string
	ExpectVersion *int
	Actor         string
}

// DecideResult reports the project after a transition plus the edge that was
// taken.
type DecideResult struct {
	Project    *domain.Project
	FromStatus int
	ToStatus   int
	// Material is the material version this verdict was bound to. It is nil
	// for transitions that are not human decisions (submit/cancel) and for
	// gates the project has not produced a material for.
	Material *GateMaterial
}

// SubmitProject sends the current draft for review.
//
//   - requirement_draft → requirement_review requires the requirement document
//     to exist (a project created without one cannot be submitted);
//   - spec_draft → spec_review requires at least one spec document.
//
// Both preconditions are checked inside the write transaction, and a submit
// always clears reason: the objection that sent the draft back is answered by
// the submission itself.
func (s *ProjectService) SubmitProject(ctx context.Context, projectID, actor string) (DecideResult, error) {
	return s.transitionProject(ctx, projectID, domain.ProjectActionSubmit, "", "", nil, actor)
}

// CancelProject moves any non-terminal project to cancelled and cancels every
// unfinished task it owns.
func (s *ProjectService) CancelProject(ctx context.Context, projectID, actor, reason string) (DecideResult, error) {
	return s.transitionProject(ctx, projectID, domain.ProjectActionCancel, "", reason, nil, actor)
}

// Decide is the unified entry point for human verdicts.
func (s *ProjectService) Decide(ctx context.Context, in DecideInput) (DecideResult, error) {
	switch in.Kind {
	case DecideKindProject:
		return s.transitionProject(ctx, in.ID, domain.ProjectActionDecide, in.Decision, in.Reason, in.ExpectVersion, in.Actor)
	default:
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "kind", Reason: "must be project"},
		}}
	}
}

// transitionProject is the one implementation of every project status change.
//
// Everything runs inside one write transaction: read → state machine guard →
// version guard → conditional UPDATE → side effects. The conditional UPDATE
// repeats the (status, version) predicate, so the row cannot be overwritten
// between the read and the write. All writes share a single connection, which
// serialises concurrent decisions; the losers observe the new version and fail
// with invalid_version.
func (s *ProjectService) transitionProject(ctx context.Context, projectID, action string, decision domain.Decision, reason string, expectVersion *int, actor string) (DecideResult, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	reason = strings.TrimSpace(reason)
	if action == domain.ProjectActionDecide {
		if !decision.Valid() {
			return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
				{Field: "decision", Reason: "must be approve or reject"},
			}}
		}
		if decision == domain.DecisionReject && reason == "" {
			return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
				{Field: "reason", Reason: "is required when decision is reject"},
			}}
		}
		if expectVersion == nil {
			return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
				{Field: "If-Match", Reason: "is required"},
			}}
		}
	}

	actor = actorOrDefault(actor)

	var result DecideResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}

		// The version guard runs before the state machine: a stale client must
		// re-read the resource (invalid_version), even when the new state would
		// also reject the action. With the correct version an illegal edge is
		// reported as invalid_state.
		if expectVersion != nil && row.Version != *expectVersion {
			return &domain.InvalidVersionError{Current: row.Version, Expected: *expectVersion}
		}

		next, err := domain.ProjectNextStatus(row.Status, action, decision)
		if err != nil {
			return err
		}

		if err := s.validateSubmitPrecondition(ctx, tx, row, action); err != nil {
			return err
		}

		nextReason := decideReason(action, decision, reason)
		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))

		affected, err := store.TransitionProject(ctx, tx, row.ID,
			row.Status, row.Version, next, nextReason, actor, updateTime)
		if err != nil {
			return err
		}
		if affected == 0 {
			// Defensive: with the single-writer pool this is unreachable, but
			// reporting the freshly read version is still the right answer.
			fresh, readErr := store.GetProjectByProjectID(ctx, tx, projectID)
			if readErr != nil {
				return readErr
			}
			return &domain.InvalidVersionError{Current: fresh.Version, Expected: row.Version}
		}

		if err := s.applyProjectSideEffects(ctx, tx, row, action, decision, reason, actor, updateTime); err != nil {
			return err
		}

		// One append-only record per human verdict. The record shares the
		// transition's transaction, and the material pointer is the one the
		// gate was reviewing (requirement or spec of the current status), so a
		// later v2 never rewrites what an earlier verdict referred to.
		var material *GateMaterial
		if action == domain.ProjectActionDecide {
			gate, ok := projectGate(row.Status)
			if ok {
				var materialID *int64
				switch gate {
				case "requirement":
					materialID = row.RequirementMaterialID
				case "spec":
					materialID = row.SpecMaterialID
				}
				material, err = recordGateDecision(ctx, tx, gateDecisionWrite{
					SubjectType:        store.GateSubjectProject,
					SubjectID:          row.ProjectID,
					Gate:               gate,
					MaterialPhysicalID: materialID,
					Decision:           string(decision),
					Reason:             reason,
					Actor:              actor,
					CreateTime:         updateTime,
				})
				if err != nil {
					return err
				}
			}
		}

		fresh, err := store.GetProjectByID(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		project, err := projectFromRow(fresh)
		if err != nil {
			return err
		}
		result = DecideResult{Project: &project, FromStatus: row.Status, ToStatus: next, Material: material}
		return nil
	})
	if err != nil {
		return DecideResult{}, err
	}
	return result, nil
}

// validateSubmitPrecondition enforces what a submit needs beyond the state
// edge: a requirement for requirement_draft and at least one spec document for
// spec_draft. Both run in the transaction so no concurrent delete can slip in.
func (s *ProjectService) validateSubmitPrecondition(ctx context.Context, tx *sql.Tx, row store.Project, action string) error {
	if action != domain.ProjectActionSubmit {
		return nil
	}
	switch row.Status {
	case domain.StatusRequirementDraft:
		if row.RequirementMaterialID == nil {
			// Kept as a literal shared with the web client so the pre-check
			// and the 409 body are byte-for-byte identical.
			return &domain.InvalidStateError{Reason: "请先上传需求文档"}
		}
	case domain.StatusSpecDraft:
		docs, err := store.ListSpecDocuments(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q has no spec document; submit requires at least one spec", row.ProjectID)}
		}
	}
	return nil
}

// applyProjectSideEffects performs the writes that accompany one transition.
//
// Cancel cancels every unfinished card. A rejected requirement gate (1→0) sends
// the project back to requirement_draft, so it enqueues the requirement card
// that owns that stage; every other gate keeps its draft card-free on purpose
// (spec 3→2, and the plan-batch reject that only records an objection). An
// approved review gate does two things: it closes the stage card that produced
// the approved artifact (a reported card becomes accepted, a bypassed one is
// cancelled) and enqueues the next stage's card, all in the same transaction as
// the status write. The task-batch review (5→6) lives in the work-item service
// because it is driven by the batch endpoint, not by a project decision.
func (s *ProjectService) applyProjectSideEffects(ctx context.Context, tx *sql.Tx, row store.Project, action string, decision domain.Decision, reason, actor, updateTime string) error {
	if action == domain.ProjectActionCancel {
		_, err := store.CancelUnfinishedWorkItems(ctx, tx, row.ID, updateTime)
		return err
	}
	if action != domain.ProjectActionDecide {
		return nil
	}

	enqueuer := phaseEnqueuer{now: s.now, maxAttempts: s.maxAttempts}

	if decision == domain.DecisionReject {
		// Only the requirement gate returns to a producing stage: 1→0 leaves
		// the project in requirement_draft with nobody to pick it up, which is
		// exactly what the rework card exists to prevent. ensure() is the whole
		// idempotency layer: an already active requirement card (reported, or
		// left over from a legacy draft) is kept instead of duplicated.
		if row.Status != domain.StatusRequirementReview {
			return nil
		}
		return enqueuer.ensure(ctx, tx, row.ID, row.ProjectID, row.Title, phaseKindRequirement)
	}

	if decision != domain.DecisionApprove {
		return nil
	}

	var settledKind, nextKind string
	switch row.Status {
	case domain.StatusRequirementReview:
		settledKind, nextKind = phaseKindRequirement, phaseKindDesign
	case domain.StatusSpecReview:
		settledKind, nextKind = phaseKindDesign, phaseKindBreakdown
	default:
		return nil
	}

	if err := settlePhaseWorkItem(ctx, tx, row.ID, settledKind, s.now()); err != nil {
		return err
	}
	return enqueuer.ensure(ctx, tx, row.ID, row.ProjectID, row.Title, nextKind)
}
