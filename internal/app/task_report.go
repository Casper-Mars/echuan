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

// ProjectAdvance reports the developing → delivery_review transition that the
// last accepted card triggered.
type ProjectAdvance struct {
	ProjectID  string
	FromStatus int
	ToStatus   int
}

// TaskDecisionResult is one applied verdict: the task as it looks afterwards,
// the edge taken and, when the acceptance closed the project, the project move.
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

		affected, err := store.CompleteTaskReport(ctx, tx, row.ID, written.Material.ID,
			leaseID, formatLeaseTime(now), actor,
			formatTimestamp(s.nextTimestamp(row.UpdateTime)))
		if err != nil {
			return err
		}
		if affected == 0 {
			// Defensive: the single-writer pool serialises the read and the
			// write, but a zero row count still means "you lost the lease".
			return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease is no longer valid"}
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

		if in.Decision == domain.TaskDecisionAccept {
			result.Project, err = s.advanceProjectAfterAccept(ctx, tx, row.ProjectCode, actor)
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

// advanceProjectAfterAccept moves a developing project to delivery_review when
// the acceptance left no task in a non-terminal status (4 done and 7 cancelled
// are terminal).
//
// The check and the project write run in the same transaction as the task
// acceptance, so the board can never observe "task done, project stuck" or a
// project that advanced before its last card committed.
func (s *TaskService) advanceProjectAfterAccept(ctx context.Context, tx *sql.Tx, projectCode, actor string) (*ProjectAdvance, error) {
	project, err := store.GetProjectByProjectID(ctx, tx, projectCode)
	if err != nil {
		return nil, err
	}
	// Only a developing project advances; any other status is left untouched
	// (draft/planning do not have a delivery gate, delivery_review already
	// moved, terminal projects take no action).
	if project.Status != domain.StatusDeveloping {
		return nil, nil
	}

	unsettled, err := store.CountUnsettledTasksByProject(ctx, tx, project.ID)
	if err != nil {
		return nil, err
	}
	if unsettled > 0 {
		return nil, nil
	}

	updateTime := formatTimestamp(s.nextTimestamp(project.UpdateTime))
	affected, err := store.TransitionProject(ctx, tx, project.ID,
		project.Status, project.Version, domain.StatusDeliveryReview,
		project.Reason, actor, updateTime)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		fresh, readErr := store.GetProjectByProjectID(ctx, tx, project.ProjectID)
		if readErr != nil {
			return nil, readErr
		}
		if fresh.Status == domain.StatusDeliveryReview {
			return &ProjectAdvance{ProjectID: fresh.ProjectID, FromStatus: project.Status, ToStatus: fresh.Status}, nil
		}
		return nil, &domain.InvalidVersionError{Current: fresh.Version, Expected: project.Version}
	}

	return &ProjectAdvance{
		ProjectID:  project.ProjectID,
		FromStatus: project.Status,
		ToStatus:   domain.StatusDeliveryReview,
	}, nil
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
