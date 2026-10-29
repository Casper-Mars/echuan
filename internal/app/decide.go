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
	// DecideKindTask drives tasks.status (0–7).
	DecideKindTask DecideKind = "task"
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

// DecideResult reports the object after a transition plus the edge that was
// taken. Exactly one of Project/Task is set.
type DecideResult struct {
	Project    *domain.Project
	Task       *domain.Task
	FromStatus int
	ToStatus   int
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
	case DecideKindTask:
		return s.decideTask(ctx, in)
	default:
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "kind", Reason: "must be project or task"},
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

		fresh, err := store.GetProjectByID(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		project, err := projectFromRow(fresh)
		if err != nil {
			return err
		}
		result = DecideResult{Project: &project, FromStatus: row.Status, ToStatus: next}
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
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q has no requirement document; submit requires requirement_material_id", row.ProjectID)}
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
// The review gates (requirement/spec) need no side effect: the artifacts are
// already stored and the task-batch review lives in the task service. Cancel
// is the only transition that touches other rows.
func (s *ProjectService) applyProjectSideEffects(ctx context.Context, tx *sql.Tx, row store.Project, action string, decision domain.Decision, reason, actor, updateTime string) error {
	if action == domain.ProjectActionCancel {
		_, err := store.CancelUnfinishedTasks(ctx, tx, row.ID, actor, updateTime)
		return err
	}
	return nil
}

// decideTask is the legacy task branch of the unified decision entry point.
//
// approve/reject are aliases for accept/rework so older callers keep working.
// The transaction is the TaskService one, which also settles a pending_done
// project whose last card was just accepted.
func (s *ProjectService) decideTask(ctx context.Context, in DecideInput) (DecideResult, error) {
	var decision domain.TaskDecision
	switch in.Decision {
	case domain.DecisionApprove:
		decision = domain.TaskDecisionAccept
	case domain.DecisionReject:
		decision = domain.TaskDecisionRework
	default:
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "decision", Reason: "must be approve or reject"},
		}}
	}

	tasks := NewTaskService(s.store)
	if s.materials != nil {
		tasks.SetMaterialWriter(s.materials)
	}
	result, err := tasks.DecideTask(ctx, TaskDecisionInput{
		TaskID:        in.ID,
		Decision:      decision,
		Reason:        in.Reason,
		ExpectVersion: in.ExpectVersion,
		Actor:         in.Actor,
	})
	if err != nil {
		return DecideResult{}, err
	}

	task := taskDomainFromView(result.Detail.Task)
	return DecideResult{
		Task:       &task,
		FromStatus: result.FromStatus,
		ToStatus:   result.ToStatus,
	}, nil
}

// decideReason computes the reason column for one project transition:
// approvals and submits clear it (SQL NULL), rejections and a non-empty cancel
// reason persist it.
func decideReason(action string, decision domain.Decision, reason string) *string {
	switch {
	case action == domain.ProjectActionSubmit:
		return nil
	case action == domain.ProjectActionAddFirstTask, action == domain.ProjectActionReviewTasks, action == domain.ProjectActionSettleTasks:
		return nil
	case action == domain.ProjectActionDecide && decision == domain.DecisionApprove:
		return nil
	case strings.TrimSpace(reason) == "":
		return nil
	default:
		value := strings.TrimSpace(reason)
		return &value
	}
}

// taskFromRow converts the store read model into the domain entity.
func taskFromRow(row store.Task) (domain.Task, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return domain.Task{}, err
	}
	updateTime, err := parseTimestamp(row.UpdateTime)
	if err != nil {
		return domain.Task{}, err
	}
	return domain.Task{
		ID:           row.ID,
		TaskID:       row.TaskID,
		ProjectID:    row.ProjectCode,
		SeqNo:        row.SeqNo,
		Title:        row.Title,
		BodyMD:       row.BodyMD,
		Status:       row.Status,
		Reason:       stringValue(row.Reason),
		LastActor:    stringValue(row.LastActor),
		Version:      row.Version,
		AttemptCount: row.AttemptCount,
		CreateTime:   createTime,
		UpdateTime:   updateTime,
	}, nil
}
