package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Work-item report, verdict and plan review.

// ReportWorkItemInput is the use-case input of POST /v1/work-items/report.
type ReportWorkItemInput struct {
	WorkID     string
	MaterialID string
	// SummaryMD is the report body for non-breakdown cards. The service writes
	// it as a kind=3 material in the report transaction, so callers do not need
	// a separate material-creation endpoint. Mutually exclusive with MaterialID.
	SummaryMD string
	LeaseID   string
	// Plan is required for breakdown cards and rejected for every other kind.
	// Its items become dev cards in the same transaction as the report.
	Plan  *BreakdownPlan
	Actor string
}

// WorkItemDecisionInput is the use-case input of POST /v1/work-items/decide.
type WorkItemDecisionInput struct {
	WorkID   string
	Decision string
	Reason   string
	Actor    string
}

// ProjectAdvance reports one automatic project move triggered by work.
type ProjectAdvance struct {
	ProjectID  string
	FromStatus int
	ToStatus   int
}

// WorkItemDecisionResult is one applied verdict: the card as it looks
// afterwards, the state edge taken and, when the verdict settled the last card,
// the project move.
type WorkItemDecisionResult struct {
	Detail    WorkItemDetail
	FromState string
	ToState   string
	Project   *ProjectAdvance
	// Material is the result material version this verdict reviewed.
	Material *GateMaterial
}

// ReportWorkItem completes one leased card with an existing material.
//
// The whole write is one transaction: read the card → lease guard → resolve the
// material → conditional completion (state=leased AND lease_id=?) → release the
// worker pointer. The conditional UPDATE is what makes a replayed report
// impossible: the second attempt finds state=submitted (or a cleared lease) and
// is answered with 409 lease_lost.
//
// A breakdown card is the exception in two ways: its report must carry a plan
// instead of (or in addition to) a material, and the plan's dev cards are
// created inside this same transaction, blocked until the plan gate approves
// them. Any validation or write failure rolls the whole report back, so the
// card stays leased and the plan can be corrected and resubmitted.
func (s *WorkItemService) ReportWorkItem(ctx context.Context, in ReportWorkItemInput) (WorkItemDetail, error) {
	workID := strings.TrimSpace(in.WorkID)
	if workID == "" {
		return WorkItemDetail{}, invalidField("work_id", "is required")
	}
	leaseID := strings.TrimSpace(in.LeaseID)
	if leaseID == "" {
		return WorkItemDetail{}, invalidField("lease_id", "is required")
	}
	now := s.now().UTC()
	actor := actorOrDefault(in.Actor)

	var out WorkItemDetail
	var written material.Stored
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetWorkItemByWorkID(ctx, tx, workID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "work_item", Key: workID}
			}
			return err
		}

		var planItems []BreakdownPlanItem
		if row.Kind == phaseKindBreakdown {
			if in.Plan == nil {
				return invalidField("plan", "is required for breakdown work items")
			}
			if strings.TrimSpace(in.SummaryMD) != "" {
				return invalidField("summary_md", "is not allowed for breakdown work items")
			}
			// The duplicate guard runs before the lease guard so a replayed
			// success is answered with the stable conflict instead of a
			// lease_lost that hides what actually happened.
			children, err := store.CountWorkItemsByParent(ctx, tx, row.ID)
			if err != nil {
				return err
			}
			if children > 0 {
				return &domain.ConflictError{Reason: "plan already submitted for breakdown item"}
			}
		} else if in.Plan != nil {
			return invalidField("plan", fmt.Sprintf("is not allowed for %s work items", row.Kind))
		}

		if err := validateReportLease(row, leaseID, now); err != nil {
			return err
		}

		if row.Kind == phaseKindBreakdown {
			items, verr := validateBreakdownPlan(in.Plan, row.ProjectCode)
			if verr != nil {
				return verr
			}
			planItems = items
		} else {
			materialIDText := strings.TrimSpace(in.MaterialID)
			summaryMD := strings.TrimSpace(in.SummaryMD)
			switch {
			case materialIDText == "" && summaryMD == "":
				return invalidField("material_id", "material_id or summary_md is required")
			case materialIDText != "" && summaryMD != "":
				return invalidField("summary_md", "summary_md and material_id are mutually exclusive")
			}
		}

		var materialID *int64
		// A breakdown report persists the approved plan itself as a kind=4
		// material and points result_material_id at it. The write shares the
		// report transaction, so a later failure (children, gate move) rolls
		// the row back; the object file created here is removed below.
		if row.Kind == phaseKindBreakdown && len(planItems) > 0 && s.materials != nil {
			project, err := store.GetProjectByProjectID(ctx, tx, row.ProjectCode)
			if err != nil {
				if store.IsNotFound(err) {
					return &domain.NotFoundError{Resource: "project", Key: row.ProjectCode}
				}
				return err
			}
			body, err := breakdownPlanBody(planItems)
			if err != nil {
				return err
			}
			written, err = s.materials.Write(ctx, tx, material.WriteInput{
				Kind:       domain.MaterialKindPlan,
				Title:      "拆分计划：" + project.Title,
				Body:       body,
				UploadedBy: actor,
			})
			if err != nil {
				return materialWriteError("plan", err)
			}
			materialID = &written.Material.ID
		}
		// Non-breakdown cards may deliver a report body directly. The material
		// is written in this transaction (kind=3) and becomes result_material_id,
		// mirroring how a breakdown plan becomes a kind=4 material.
		if row.Kind != phaseKindBreakdown && strings.TrimSpace(in.MaterialID) == "" &&
			strings.TrimSpace(in.SummaryMD) != "" {
			if s.materials == nil {
				return invalidField("summary_md", "material writer is not configured")
			}
			written, err = s.materials.Write(ctx, tx, material.WriteInput{
				Kind:       domain.MaterialKindReport,
				Title:      "任务报告：" + row.Title,
				Body:       []byte(in.SummaryMD),
				UploadedBy: actor,
			})
			if err != nil {
				return materialWriteError("summary_md", err)
			}
			materialID = &written.Material.ID
		}
		if value := strings.TrimSpace(in.MaterialID); value != "" {
			material, err := store.GetMaterialByMaterialID(ctx, tx, value)
			if err != nil {
				if store.IsNotFound(err) {
					return &domain.NotFoundError{Resource: "material", Key: value}
				}
				return err
			}
			// The plan artifact is the primary result of a breakdown report;
			// an explicitly supplied material is still validated above but
			// does not replace it.
			if materialID == nil {
				materialID = &material.ID
			}
		}

		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))
		affected, err := store.CompleteWorkItemReportOptionalMaterial(ctx, tx, row.ID, materialID,
			leaseID, formatLeaseTime(now), updateTime)
		if err != nil {
			return err
		}
		if affected == 0 {
			return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease is no longer valid"}
		}

		if _, err := store.ClearWorkItemByWorkItemID(ctx, tx, row.ID, updateTime); err != nil {
			return err
		}

		// The plan's children land blocked: they are real work, but the plan
		// gate has not approved them yet. The dependency edges use the new
		// physical ids so the plan's 1..N references never leak into work_deps.
		if len(planItems) > 0 {
			if err := s.createBreakdownChildren(ctx, tx, row, planItems, updateTime); err != nil {
				return err
			}
		}

		// Phase cards drive the project: a reported requirement/design/
		// breakdown moves 0→1, 2→3 or 4→5 in this same transaction. A missing
		// prerequisite aborts here and rolls the completion (and the children)
		// back, so the card stays leased and the actor can supply the material
		// and report again.
		if _, err := s.advanceProjectOnPhaseReport(ctx, tx, row, actor); err != nil {
			return err
		}

		out, err = s.detail(ctx, tx, row.ID)
		return err
	})
	if err != nil {
		// The materials row died with the rolled-back transaction; the object
		// file did not. Drop it only when this write created it.
		if written.NewObject && s.materials != nil {
			_ = s.materials.RemoveNewObject(written)
		}
		return WorkItemDetail{}, err
	}
	return out, nil
}

// createBreakdownChildren inserts the planned dev cards and their dependency
// edges inside the caller's report transaction.
//
// Plan seq_no is only a reference inside the plan; the persisted seq_no comes
// from the project-wide NextSeqNo so a breakdown batch slots into the same
// numbering space as every other card of the project.
func (s *WorkItemService) createBreakdownChildren(ctx context.Context, tx *sql.Tx, parent store.WorkItem, items []BreakdownPlanItem, updateTime string) error {
	project, err := store.GetProjectByProjectID(ctx, tx, parent.ProjectCode)
	if err != nil {
		if store.IsNotFound(err) {
			return &domain.NotFoundError{Resource: "project", Key: parent.ProjectCode}
		}
		return err
	}

	firstSeq, err := store.NextSeqNo(ctx, tx, "work_items", "project_id", project.ID)
	if err != nil {
		return err
	}

	created := make([]int64, len(items))
	parentID := parent.ID
	for i, item := range items {
		workID, err := platform.NewWorkID(s.timestamp())
		if err != nil {
			return err
		}
		id, err := store.CreateWorkItem(ctx, tx, store.NewWorkItem{
			WorkID:      workID,
			ProjectID:   project.ID,
			ParentID:    &parentID,
			Kind:        "dev",
			SeqNo:       firstSeq + i,
			Title:       item.Title,
			BodyMD:      item.BodyMD,
			Priority:    item.Priority,
			State:       store.WorkItemStateBlocked,
			MaxAttempts: s.maxAttempts,
			CreateTime:  updateTime,
			UpdateTime:  updateTime,
		})
		if err != nil {
			if errors.Is(err, store.ErrUniqueViolation) {
				return &domain.ConflictError{Reason: fmt.Sprintf(
					"work item %q already exists in project %q", workID, parent.ProjectCode)}
			}
			return err
		}
		created[i] = id
	}

	for i, item := range items {
		for _, dependsOn := range item.DependsOn {
			if err := store.InsertWorkDependency(ctx, tx, created[i], created[dependsOn-1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateReportLease is the report-specific lease guard.
func validateReportLease(row store.WorkItem, leaseID string, now time.Time) error {
	switch {
	case row.State != store.WorkItemStateLeased:
		return &domain.LeaseLostError{WorkID: row.WorkID, Reason: fmt.Sprintf(
			"work item is in state %s, not leased", row.State)}
	case row.LeaseID == nil || *row.LeaseID != leaseID:
		return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease_id does not match the active lease"}
	case row.LeaseUntil == nil:
		return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease has no deadline"}
	}

	until, err := parseTimestamp(*row.LeaseUntil)
	if err != nil {
		return err
	}
	if !until.After(now) {
		return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease has expired"}
	}
	return nil
}

// DecideWorkItem applies one verdict to a submitted card.
//
// approve: submitted → accepted. reject: submitted (or failed) → queued; the
// attempt budget is deliberately preserved, so a rejected card returns to the
// queue as the same attempt instead of resetting to zero.
func (s *WorkItemService) DecideWorkItem(ctx context.Context, in WorkItemDecisionInput) (WorkItemDecisionResult, error) {
	workID := strings.TrimSpace(in.WorkID)
	if workID == "" {
		return WorkItemDecisionResult{}, invalidField("work_id", "is required")
	}
	decision := strings.TrimSpace(in.Decision)
	if decision != string(domain.DecisionApprove) && decision != string(domain.DecisionReject) {
		return WorkItemDecisionResult{}, invalidField("decision", "must be approve or reject")
	}
	reason := strings.TrimSpace(in.Reason)
	actor := actorOrDefault(in.Actor)

	var result WorkItemDecisionResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetWorkItemByWorkID(ctx, tx, workID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "work_item", Key: workID}
			}
			return err
		}

		var next string
		if decision == string(domain.DecisionApprove) {
			if row.State != store.WorkItemStateSubmitted {
				return &domain.InvalidStateError{Reason: fmt.Sprintf(
					"work item %q is in state %s; approve requires submitted", row.WorkID, row.State)}
			}
			next = store.WorkItemStateAccepted
		} else {
			if row.State != store.WorkItemStateSubmitted && row.State != store.WorkItemStateFailed {
				return &domain.InvalidStateError{Reason: fmt.Sprintf(
					"work item %q is in state %s; reject requires submitted or failed", row.WorkID, row.State)}
			}
			next = store.WorkItemStateQueued
		}

		var reasonPtr *string
		if reason != "" {
			reasonPtr = &reason
		}
		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))

		affected, err := store.TransitionWorkItemDecision(ctx, tx, store.WorkItemDecisionWrite{
			ID:            row.ID,
			ExpectState:   row.State,
			ExpectVersion: row.Version,
			NextState:     next,
			Reason:        reasonPtr,
			ResetAttempts: false,
			UpdateTime:    updateTime,
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			fresh, readErr := store.GetWorkItemByWorkID(ctx, tx, workID)
			if readErr != nil {
				return readErr
			}
			return &domain.InvalidVersionError{Current: fresh.Version, Expected: row.Version}
		}

		// The verdict is recorded only after the conditional state write won,
		// in the same transaction. A reject of a failed card carries whatever
		// result material the card still points at (often none).
		result.Material, err = recordGateDecision(ctx, tx, gateDecisionWrite{
			SubjectType:        store.GateSubjectWorkItem,
			SubjectID:          row.WorkID,
			Gate:               row.Kind,
			MaterialPhysicalID: row.ResultMaterialID,
			Decision:           decision,
			Reason:             reason,
			Actor:              actor,
			CreateTime:         updateTime,
		})
		if err != nil {
			return err
		}

		if next == store.WorkItemStateAccepted {
			result.Project, err = s.settleProjectIfDone(ctx, tx, row.ProjectCode, actor)
			if err != nil {
				return err
			}
		}

		result.Detail, err = s.detail(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		result.FromState = row.State
		result.ToState = next
		return nil
	})
	if err != nil {
		return WorkItemDecisionResult{}, err
	}
	return result, nil
}

// settleProjectIfDone closes a pending_done project once no item of it is left
// in a non-terminal state (accepted and cancelled are terminal).
func (s *WorkItemService) settleProjectIfDone(ctx context.Context, tx *sql.Tx, projectCode, actor string) (*ProjectAdvance, error) {
	project, err := store.GetProjectByProjectID(ctx, tx, projectCode)
	if err != nil {
		return nil, err
	}
	if project.Status != domain.StatusPendingDone {
		return nil, nil
	}

	unsettled, err := store.CountUnsettledWorkItemsByProject(ctx, tx, project.ID)
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
		project.Status, project.Version, next, nil, actor, updateTime)
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

// ReviewWorkItemsInput is the use-case input of POST /v1/work-items/review.
type ReviewWorkItemsInput struct {
	ProjectID string
	Decision  domain.Decision
	Reason    string
	Actor     string
}

// ReviewWorkItemsResult reports the outcome of one plan review.
type ReviewWorkItemsResult struct {
	ProjectID  string
	FromStatus int
	ToStatus   int
	Version    int
	ReadyItems int64
	Reason     string
	RejectedAt string
}

// ReviewWorkItems applies one human verdict to a project's plan batch.
//
// approve: every blocked card becomes queued and the project moves 5 → 6 (or
// repeats 6 → 6 when cards were added after an earlier approval) in the same
// transaction, which also clears any recorded objection. A batch with no
// blocked card is a conflict and writes nothing.
//
// reject: the cards and the project status stay exactly where they are; only
// tasks_review_reason / tasks_review_rejected_at (plus actor, version and
// update_time) are written. The reason is mandatory.
func (s *WorkItemService) ReviewWorkItems(ctx context.Context, in ReviewWorkItemsInput) (ReviewWorkItemsResult, error) {
	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		return ReviewWorkItemsResult{}, invalidField("project_id", "is required")
	}
	if !in.Decision.Valid() {
		return ReviewWorkItemsResult{}, invalidField("decision", "must be approve or reject")
	}
	reason := strings.TrimSpace(in.Reason)
	if in.Decision == domain.DecisionReject && reason == "" {
		return ReviewWorkItemsResult{}, invalidField("reason", "is required when decision is reject")
	}
	actor := actorOrDefault(in.Actor)

	var result ReviewWorkItemsResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		project, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}

		// The plan gate is bound to the breakdown card's kind=4 material. It
		// is read before the approve branch settles (accepts) that card.
		planMaterialID, err := store.LatestWorkItemResultMaterialID(ctx, tx, project.ID, phaseKindBreakdown)
		if err != nil {
			return err
		}

		updateTime := formatTimestamp(s.nextTimestamp(project.UpdateTime))
		if in.Decision == domain.DecisionReject {
			if project.Status != domain.StatusTasksReview && project.Status != domain.StatusPendingDone {
				return &domain.InvalidStateError{Reason: fmt.Sprintf(
					"project %q is in status %s; only tasks_review or pending_done batches can be rejected",
					project.ProjectID, domain.StatusName(project.Status))}
			}

			affected, err := store.SetTasksReviewRejection(ctx, tx, project.ID, project.Version,
				reason, updateTime, actor, updateTime)
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

			if _, err := recordGateDecision(ctx, tx, gateDecisionWrite{
				SubjectType:        store.GateSubjectProject,
				SubjectID:          project.ProjectID,
				Gate:               "plan",
				MaterialPhysicalID: planMaterialID,
				Decision:           string(in.Decision),
				Reason:             reason,
				Actor:              actor,
				CreateTime:         updateTime,
			}); err != nil {
				return err
			}

			result = ReviewWorkItemsResult{
				ProjectID:  project.ProjectID,
				FromStatus: project.Status,
				ToStatus:   project.Status,
				Version:    project.Version + 1,
				ReadyItems: 0,
				Reason:     reason,
				RejectedAt: updateTime,
			}
			return nil
		}

		next, err := domain.ProjectNextStatus(project.Status, domain.ProjectActionReviewTasks, domain.DecisionApprove)
		if err != nil {
			return err
		}

		ready, err := store.SetWorkItemsQueuedByProject(ctx, tx, project.ID, updateTime)
		if err != nil {
			return err
		}
		if ready == 0 {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q has no blocked work item to approve", project.ProjectID)}
		}

		affected, err := store.TransitionProjectClearTasksReview(ctx, tx, project.ID,
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

		if _, err := recordGateDecision(ctx, tx, gateDecisionWrite{
			SubjectType:        store.GateSubjectProject,
			SubjectID:          project.ProjectID,
			Gate:               "plan",
			MaterialPhysicalID: planMaterialID,
			Decision:           string(in.Decision),
			Reason:             reason,
			Actor:              actor,
			CreateTime:         updateTime,
		}); err != nil {
			return err
		}

		// The breakdown stage is over once the batch is approved; closing its
		// card here keeps 6→7 reachable and cannot touch a dev card.
		if err := settlePhaseWorkItem(ctx, tx, project.ID, phaseKindBreakdown, s.now()); err != nil {
			return err
		}

		result = ReviewWorkItemsResult{
			ProjectID:  project.ProjectID,
			FromStatus: project.Status,
			ToStatus:   next,
			Version:    project.Version + 1,
			ReadyItems: ready,
		}
		return nil
	})
	if err != nil {
		return ReviewWorkItemsResult{}, err
	}
	return result, nil
}
