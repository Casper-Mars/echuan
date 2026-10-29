package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// ReportInput is the use-case input of POST /v1/tasks/report.
type ReportInput struct {
	TaskID    string
	LeaseID   string
	SummaryMD string
	Actor     string
}

// TaskDecisionInput is the use-case input of POST /v1/tasks/decide.
type TaskDecisionInput struct {
	TaskID        string
	Decision      domain.TaskDecision
	Reason        string
	ExpectVersion *int
	Actor         string
}

// ProjectAdvance reports one automatic project move triggered by task work:
// the batch approval (5 → 6) or the final settlement (6 → 7).
type ProjectAdvance struct {
	ProjectID  string
	FromStatus int
	ToStatus   int
}

// TaskDecisionResult is one applied verdict: the task as it looks afterwards,
// the edge taken and, when the verdict settled the last card, the project move.
type TaskDecisionResult struct {
	Detail     TaskDetail
	FromStatus int
	ToStatus   int
	Project    *ProjectAdvance
}

// SetMaterialWriter attaches the writer that owns data_dir/objects. Reports are
// the only material kind the task service writes, so the writer is injected
// rather than owned.
func (s *TaskService) SetMaterialWriter(writer *material.Writer) { s.materials = writer }

// MaxMaterialBytes returns the configured material_max_bytes so the HTTP layer
// can size its request body reader. It is 0 when no writer is attached.
func (s *TaskService) MaxMaterialBytes() int64 {
	if s.materials == nil {
		return 0
	}
	return s.materials.MaxBytes()
}

// ReportTask completes one leased task with a report (tech design §4.6).
//
// The whole write is one transaction: read the task → lease guard → append the
// report material version → conditional completion (status=2 AND lease_id=?)
// → read the detail back. Validation of the payload runs first, so a blank
// report never reaches the database.
//
// The report chain hangs off tasks.report_material_id: the writer derives
// version = head+1 from the pointer read here, and the conditional UPDATE is
// what makes a replayed report impossible — the second attempt finds status=3
// (or a cleared lease) and is answered with 409 lease_lost.
func (s *TaskService) ReportTask(ctx context.Context, in ReportInput) (TaskDetail, error) {
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return TaskDetail{}, invalidField("id", "is required")
	}
	leaseID := strings.TrimSpace(in.LeaseID)
	if leaseID == "" {
		return TaskDetail{}, invalidField("lease_id", "is required")
	}
	if strings.TrimSpace(in.SummaryMD) == "" {
		return TaskDetail{}, &domain.MaterialRequiredError{
			Field:  "summary_md",
			Reason: "must not be blank",
		}
	}
	if s.materials == nil {
		return TaskDetail{}, fmt.Errorf("task service has no material writer")
	}
	actor := actorOrDefault(in.Actor)
	now := s.now().UTC()

	var (
		out     TaskDetail
		written material.Stored
	)
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "task", Key: taskID}
			}
			return err
		}

		// One guard covers every rejection §4.6 asks for: not in_progress,
		// wrong lease id, or an expired deadline. It is evaluated inside the
		// transaction so the answer describes the state the write sees.
		if err := validateReportLease(row, leaseID, now); err != nil {
			return err
		}

		written, err = s.materials.Write(ctx, tx, material.WriteInput{
			Kind:               domain.MaterialKindReport,
			Title:              "报告：" + row.Title,
			Body:               []byte(in.SummaryMD),
			UploadedBy:         actor,
			PreviousMaterialID: row.ReportMaterialID,
		})
		if err != nil {
			return materialWriteError("summary_md", err)
		}

		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))
		affected, err := store.CompleteTaskReport(ctx, tx, row.ID, written.Material.ID,
			leaseID, formatLeaseTime(now), actor, updateTime)
		if err != nil {
			return err
		}
		if affected == 0 {
			// Defensive: the single-writer pool serialises the read and the
			// write, but a zero row count still means "you lost the lease".
			return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease is no longer valid"}
		}

		// The card left in_progress, so the worker no longer holds it: clear the
		// registry pointer in the same transaction and with the same timestamp
		// as the completion. A worker that reported is immediately free for the
		// next heartbeat, instead of waiting for the 30s maintenance sweep.
		if _, err := store.ClearTaskByTaskID(ctx, tx, row.ID, updateTime); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, row.ID)
		return err
	})
	if err != nil {
		// The material row died with the rolled-back transaction; the object
		// file did not. Drop it only when this request created it (same policy
		// as WriteRequirement/SubmitSpec).
		if written.NewObject {
			_ = s.materials.RemoveNewObject(written)
		}
		return TaskDetail{}, err
	}
	return out, nil
}

// validateReportLease is the report-specific lease guard.
//
// lostLeaseError (claim.go) answers a heartbeat that matched nothing, so
// reaching its end means "lost". A report must be accepted when the lease is
// live, which is why this variant returns nil instead of the generic error.
func validateReportLease(row store.Task, leaseID string, now time.Time) error {
	switch {
	case row.Status != domain.TaskInProgress:
		return &domain.LeaseLostError{TaskID: row.TaskID, Reason: fmt.Sprintf(
			"task is in status %s, not in_progress", domain.TaskStatusName(row.Status))}
	case row.LeaseID == nil || *row.LeaseID != leaseID:
		return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease_id does not match the active lease"}
	case row.LeaseUntil == nil:
		return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease has no deadline"}
	}

	until, err := parseTimestamp(*row.LeaseUntil)
	if err != nil {
		return err
	}
	if !until.After(now) {
		return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease has expired"}
	}
	return nil
}

// DecideTask applies one verdict to a pending_acceptance or failed task
// (tech design §3.3 / §4.2).
//
// Ordering matches the project decision endpoints: validate the payload, read
// the task, check the If-Match version, then the state edge. A stale client
// therefore sees invalid_version even when its verdict would also be illegal
// in the new state.
func (s *TaskService) DecideTask(ctx context.Context, in TaskDecisionInput) (TaskDecisionResult, error) {
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return TaskDecisionResult{}, invalidField("id", "is required")
	}
	if !in.Decision.Valid() {
		return TaskDecisionResult{}, invalidField("decision", "must be accept, rework, retry or abandon")
	}
	reason := strings.TrimSpace(in.Reason)
	if domain.TaskDecisionRequiresReason(in.Decision) && reason == "" {
		return TaskDecisionResult{}, invalidField("reason",
			fmt.Sprintf("is required when decision is %s", in.Decision))
	}
	if in.ExpectVersion == nil {
		return TaskDecisionResult{}, invalidField("If-Match", "is required")
	}
	actor := actorOrDefault(in.Actor)

	var result TaskDecisionResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "task", Key: taskID}
			}
			return err
		}

		if row.Version != *in.ExpectVersion {
			return &domain.InvalidVersionError{Current: row.Version, Expected: *in.ExpectVersion}
		}

		next, err := domain.TaskDecisionNextStatus(row.Status, in.Decision)
		if err != nil {
			return err
		}

		var reasonPtr *string
		if domain.TaskDecisionRequiresReason(in.Decision) {
			reasonPtr = &reason
		}
		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))

		affected, err := store.TransitionTaskDecision(ctx, tx, store.TaskDecisionWrite{
			ID:            row.ID,
			ExpectStatus:  row.Status,
			ExpectVersion: row.Version,
			NextStatus:    next,
			Reason:        reasonPtr,
			ResetAttempts: domain.TaskDecisionResetsAttempts(in.Decision),
			LastActor:     actor,
			UpdateTime:    updateTime,
		})
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

		// Both terminal verdicts can settle the project's last card: accept
		// (done) and abandon (cancelled). A rework or retry leaves work behind,
		// so only these two paths check for completion.
		if in.Decision == domain.TaskDecisionAccept || in.Decision == domain.TaskDecisionAbandon {
			result.Project, err = s.settleProjectIfDone(ctx, tx, row.ProjectCode, actor)
			if err != nil {
				return err
			}
		}

		result.Detail, err = s.detail(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		result.FromStatus = row.Status
		result.ToStatus = next
		return nil
	})
	if err != nil {
		return TaskDecisionResult{}, err
	}
	return result, nil
}

// settleProjectIfDone closes a pending_done project once no task of it is left
// in a non-terminal status (4 done and 7 cancelled are terminal).
//
// The check and the project write run in the same transaction as the task
// verdict, so the board can never observe "last card settled, project stuck" or
// a project that completed before its last card committed.
func (s *TaskService) settleProjectIfDone(ctx context.Context, tx *sql.Tx, projectCode, actor string) (*ProjectAdvance, error) {
	project, err := store.GetProjectByProjectID(ctx, tx, projectCode)
	if err != nil {
		return nil, err
	}
	// Only a pending_done project closes itself; every other status is left
	// untouched (draft/review states still have a human gate, terminal
	// projects take no action, cancelled projects never complete).
	if project.Status != domain.StatusPendingDone {
		return nil, nil
	}

	unsettled, err := store.CountUnsettledTasksByProject(ctx, tx, project.ID)
	if err != nil {
		return nil, err
	}
	if unsettled > 0 {
		return nil, nil
	}

	next, err := domain.ProjectNextStatus(project.Status, domain.ProjectActionSettleTasks, "")
	if err != nil {
		return nil, err
	}
	updateTime := formatTimestamp(s.nextTimestamp(project.UpdateTime))
	affected, err := store.TransitionProject(ctx, tx, project.ID,
		project.Status, project.Version, next,
		nil, actor, updateTime)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		fresh, readErr := store.GetProjectByProjectID(ctx, tx, project.ProjectID)
		if readErr != nil {
			return nil, readErr
		}
		if fresh.Status == next {
			return &ProjectAdvance{ProjectID: fresh.ProjectID, FromStatus: project.Status, ToStatus: fresh.Status}, nil
		}
		return nil, &domain.InvalidVersionError{Current: fresh.Version, Expected: project.Version}
	}

	return &ProjectAdvance{
		ProjectID:  project.ProjectID,
		FromStatus: project.Status,
		ToStatus:   next,
	}, nil
}

// ReviewTasksResult reports the outcome of one task-batch review.
type ReviewTasksResult struct {
	ProjectID  string
	FromStatus int
	ToStatus   int
	ReadyTasks int64
}

// ApproveTasks applies the human approval of a task batch (tech design §4.5):
// every backlog card of a tasks_review project becomes ready and the project
// moves to pending_done in the same transaction.
//
// Rejection is intentionally not implemented here yet: it belongs to the
// follow-up task and the HTTP layer answers it with 400 invalid_argument
// instead of pretending the batch was rejected.
func (s *TaskService) ApproveTasks(ctx context.Context, projectID, actor string) (ReviewTasksResult, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ReviewTasksResult{}, invalidField("project_id", "is required")
	}
	actor = actorOrDefault(actor)

	var result ReviewTasksResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		project, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}
		next, err := domain.ProjectNextStatus(project.Status, domain.ProjectActionReviewTasks, domain.DecisionApprove)
		if err != nil {
			return err
		}

		updateTime := formatTimestamp(s.nextTimestamp(project.UpdateTime))
		ready, err := store.SetTasksReadyByProject(ctx, tx, project.ID, actor, updateTime)
		if err != nil {
			return err
		}
		if ready == 0 {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q has no backlog task to approve", project.ProjectID)}
		}

		// An approval clears the rejection objection that produced this round.
		affected, err := store.TransitionProject(ctx, tx, project.ID,
			project.Status, project.Version, next, nil, actor, updateTime)
		if err != nil {
			return err
		}
		if affected == 0 {
			fresh, readErr := store.GetProjectByProjectID(ctx, tx, project.ProjectID)
			if readErr != nil {
				return readErr
			}
			return &domain.InvalidVersionError{Current: fresh.Version, Expected: project.Version}
		}

		result = ReviewTasksResult{
			ProjectID:  project.ProjectID,
			FromStatus: project.Status,
			ToStatus:   next,
			ReadyTasks: ready,
		}
		return nil
	})
	if err != nil {
		return ReviewTasksResult{}, err
	}
	return result, nil
}

// taskDomainFromView converts the task read view back into the domain entity.
// It is only needed by the legacy Decide entry point, whose result type
// predates the detail DTO.
func taskDomainFromView(view TaskView) domain.Task {
	return domain.Task{
		ID:           0,
		TaskID:       view.TaskID,
		ProjectID:    view.ProjectID,
		SeqNo:        view.SeqNo,
		Title:        view.Title,
		BodyMD:       view.BodyMD,
		Status:       view.Status,
		Reason:       view.Reason,
		LastActor:    view.LastActor,
		Version:      view.Version,
		AttemptCount: view.AttemptCount,
		CreateTime:   view.CreateTime,
		UpdateTime:   view.UpdateTime,
	}
}
