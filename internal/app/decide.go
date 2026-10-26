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
	// DecideKindProject drives projects.status (0–7).
	DecideKindProject DecideKind = "project"
	// DecideKindTask drives tasks.status (0–7). Task-11 exposes it over HTTP;
	// Task-06 already implements and tests it here so the state machine has a
	// single entry point from day one.
	DecideKindTask DecideKind = "task"
)

// DecideInput is the unified decision request.
//
// ID is the business id ("APP-1" or "APP-1-T0001"). ExpectVersion is the
// If-Match version; the decision endpoints require it, while accept/cancel
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

// AcceptProject moves a draft project to spec_review (tech design §5.5).
//
// The requirement guard is enforced inside the same transaction: a project
// whose requirement_material_id is NULL cannot enter review.
func (s *ProjectService) AcceptProject(ctx context.Context, projectID, actor string) (DecideResult, error) {
	return s.transitionProject(ctx, projectID, domain.ProjectActionAccept, "", "", nil, actor)
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

		// Accept has one extra precondition: the requirement document pointer
		// must exist. It is checked in the transaction so no concurrent delete
		// can slip in.
		if action == domain.ProjectActionAccept && row.RequirementMaterialID == nil {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q has no requirement document; accept requires requirement_material_id", projectID)}
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

// applyProjectSideEffects performs the writes that accompany one transition.
func (s *ProjectService) applyProjectSideEffects(ctx context.Context, tx *sql.Tx, row store.Project, action string, decision domain.Decision, reason, actor, updateTime string) error {
	switch {
	case action == domain.ProjectActionCancel:
		_, err := store.CancelUnfinishedTasks(ctx, tx, row.ID, actor, updateTime)
		return err

	case row.Status == domain.StatusPlanReview && decision == domain.DecisionApprove:
		// The approved breakdown becomes claimable.
		_, err := store.SetTasksReadyByProject(ctx, tx, row.ID, actor, updateTime)
		return err

	case row.Status == domain.StatusPlanReview && decision == domain.DecisionReject:
		// The plan is rebuilt from scratch: drop the rejected tasks and their
		// dependency edges. The CTO re-plans on the next round.
		_, err := store.DeleteTasksByProject(ctx, tx, row.ID)
		return err

	case row.Status == domain.StatusDeliveryReview && decision == domain.DecisionReject:
		return s.createReworkTask(ctx, tx, row, reason, actor, updateTime)
	}
	return nil
}

// createReworkTask adds one ready task describing what must be reworked. It is
// inserted in backlog and then moved through the state machine to ready, so
// the task machine stays the only authority over tasks.status.
func (s *ProjectService) createReworkTask(ctx context.Context, tx *sql.Tx, project store.Project, reason, actor, updateTime string) error {
	seq, err := store.NextSeqNo(ctx, tx, "tasks", "project_id", project.ID)
	if err != nil {
		return err
	}
	taskID := fmt.Sprintf("%s-T%04d", project.ProjectID, seq)

	body := "返工说明：\n\n" + reason
	id, err := store.CreateTask(ctx, tx, store.NewTask{
		TaskID:      taskID,
		ProjectID:   project.ID,
		SeqNo:       seq,
		Title:       "返工：" + project.Title,
		BodyMD:      body,
		Status:      domain.TaskBacklog,
		MaxAttempts: s.maxAttempts,
		LastActor:   actor,
		CreateTime:  updateTime,
		UpdateTime:  updateTime,
	})
	if err != nil {
		return err
	}

	_, err = store.TransitionTask(ctx, tx, id, domain.TaskBacklog, 1, domain.TaskReady, nil, actor, updateTime)
	return err
}

// decideTask is the task half of the unified decision entry point. Only a
// pending_acceptance task can be decided: approve → done, reject → ready
// (rework) with the reason persisted. Task-11 will expose it over HTTP.
func (s *ProjectService) decideTask(ctx context.Context, in DecideInput) (DecideResult, error) {
	taskID := strings.TrimSpace(in.ID)
	if taskID == "" {
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}
	if !in.Decision.Valid() {
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "decision", Reason: "must be approve or reject"},
		}}
	}
	reason := strings.TrimSpace(in.Reason)
	if in.Decision == domain.DecisionReject && reason == "" {
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "reason", Reason: "is required when decision is reject"},
		}}
	}
	if in.ExpectVersion == nil {
		return DecideResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "If-Match", Reason: "is required"},
		}}
	}
	actor := actorOrDefault(in.Actor)

	var result DecideResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "task", Key: taskID}
			}
			return err
		}

		// Version first, for the same reason as the project branch: a stale
		// request must re-read rather than learn a misleading state error.
		if row.Version != *in.ExpectVersion {
			return &domain.InvalidVersionError{Current: row.Version, Expected: *in.ExpectVersion}
		}

		// A decision is only meaningful while the report waits for review.
		if row.Status != domain.TaskPendingAcceptance {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"task %q is in status %s; a decision requires pending_acceptance",
				taskID, domain.TaskStatusName(row.Status))}
		}

		next := domain.TaskDone
		if in.Decision == domain.DecisionReject {
			next = domain.TaskReady
		}
		if err := domain.ValidateTaskTransition(row.Status, next); err != nil {
			return err
		}

		var reasonPtr *string
		if reason != "" {
			reasonPtr = &reason
		}
		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))

		affected, err := store.TransitionTask(ctx, tx, row.ID,
			row.Status, row.Version, next, reasonPtr, actor, updateTime)
		if err != nil {
			return err
		}
		if affected == 0 {
			fresh, readErr := store.GetTaskByTaskID(ctx, tx, taskID)
			if readErr != nil {
				return readErr
			}
			return &domain.InvalidVersionError{Current: fresh.Version, Expected: row.Version}
		}

		fresh, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			return err
		}
		task, err := taskFromRow(fresh)
		if err != nil {
			return err
		}
		result = DecideResult{Task: &task, FromStatus: row.Status, ToStatus: next}
		return nil
	})
	if err != nil {
		return DecideResult{}, err
	}
	return result, nil
}

// decideReason computes the reason column for one project transition:
// approvals and accept clear it (SQL NULL), rejections and a non-empty cancel
// reason persist it.
func decideReason(action string, decision domain.Decision, reason string) *string {
	switch {
	case action == domain.ProjectActionAccept:
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
