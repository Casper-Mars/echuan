package app

// Phase pipeline: the three cards that make the waiting states (0/2/4) real.
//
// A project does not advance by itself. Every producing stage owns exactly one
// active work item that says "someone still has to write this":
//
//	requirement_draft (0) --report--> requirement_review (1) --approve--> spec_draft (2)
//	spec_draft (2)        --report--> spec_review (3)        --approve--> tasks_draft (4)
//	tasks_draft (4)       --report--> tasks_review (5)       --approve--> pending_done (6)
//
// Enqueue and settle always run inside the transaction that performs the
// project write, so a failure leaves neither a project without its card nor a
// card whose stage was never entered. The functions in this file therefore
// never open a transaction of their own: they take the caller's *sql.Tx, which
// is what keeps the report path from nesting store.Tx.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Phase work-item kinds. They are the schema kinds; the constants exist so a
// typo cannot silently create a dev card.
const (
	phaseKindRequirement = "requirement"
	phaseKindDesign      = "design"
	phaseKindBreakdown   = "breakdown"
)

// phasePriority keeps the pipeline ahead of dev cards in the shared queue: the
// earlier the stage, the more urgent the card.
func phasePriority(kind string) int {
	switch kind {
	case phaseKindRequirement:
		return 990
	case phaseKindDesign:
		return 980
	case phaseKindBreakdown:
		return 970
	default:
		return 0
	}
}

// phaseStageName is the human name of one producing stage.
func phaseStageName(kind string) string {
	switch kind {
	case phaseKindRequirement:
		return "需求撰写"
	case phaseKindDesign:
		return "方案设计"
	case phaseKindBreakdown:
		return "任务拆分"
	default:
		return kind
	}
}

// phaseTitle renders the card title: "需求撰写：{project.title}".
func phaseTitle(kind, projectTitle string) string {
	return phaseStageName(kind) + "：" + projectTitle
}

// phaseBody gives the card its stage context without inventing inputs: the
// work_inputs chain is not part of this task, so the body only names the
// project and the stage contract.
func phaseBody(kind, projectCode string) string {
	return fmt.Sprintf(
		"项目 %s 的%s阶段工作项。\n提交后平台会在同一事务内把项目推进到评审态；资料未就绪时提交会被拒绝，租约保留，可补齐资料后重报。\n",
		projectCode, phaseStageName(kind))
}

// phaseReportStatus is the project status a report of this kind advances from.
// The second result is false for dev cards, which never move the project.
func phaseReportStatus(kind string) (int, bool) {
	switch kind {
	case phaseKindRequirement:
		return domain.StatusRequirementDraft, true
	case phaseKindDesign:
		return domain.StatusSpecDraft, true
	case phaseKindBreakdown:
		return domain.StatusTasksDraft, true
	default:
		return 0, false
	}
}

// phaseEnqueuer creates the active card of one stage when the project has none.
// It carries the per-service clock and attempt budget so ProjectService and
// WorkItemService share exactly one implementation.
type phaseEnqueuer struct {
	now         func() time.Time
	maxAttempts int
}

// ensure inserts the active phase card when none exists.
//
// Idempotency has two layers: the lookup below is the normal path, and
// ux_work_active_phase is the backstop. A concurrent insert that wins the
// unique index is not an error here — the loser re-reads the winner and treats
// the card as existing, which is what makes "approve twice" harmless.
func (e phaseEnqueuer) ensure(ctx context.Context, tx *sql.Tx, projectID int64, projectCode, projectTitle, kind string) error {
	if _, err := store.GetActivePhaseWorkItem(ctx, tx, projectID, kind); err == nil {
		return nil
	} else if !store.IsNotFound(err) {
		return err
	}

	now := timestampAt(e.now())
	workID, err := platform.NewWorkID(now)
	if err != nil {
		return err
	}
	seq, err := store.NextSeqNo(ctx, tx, "work_items", "project_id", projectID)
	if err != nil {
		return err
	}

	ts := formatTimestamp(now)
	_, err = store.CreateWorkItem(ctx, tx, store.NewWorkItem{
		WorkID:      workID,
		ProjectID:   projectID,
		Kind:        kind,
		SeqNo:       seq,
		Title:       phaseTitle(kind, projectTitle),
		BodyMD:      phaseBody(kind, projectCode),
		Priority:    phasePriority(kind),
		State:       store.WorkItemStateQueued,
		MaxAttempts: e.maxAttempts,
		CreateTime:  ts,
		UpdateTime:  ts,
	})
	if err != nil {
		if errors.Is(err, store.ErrUniqueViolation) {
			if _, readErr := store.GetActivePhaseWorkItem(ctx, tx, projectID, kind); readErr == nil {
				return nil
			} else if !store.IsNotFound(readErr) {
				return readErr
			}
		}
		return err
	}
	return nil
}

// settlePhaseWorkItem closes the stage card whose review gate just passed.
//
// A card that was reported (submitted) becomes accepted: the human approved the
// artifact it produced. A card that was bypassed (still blocked/queued/leased,
// for example because the requirement was uploaded and submitted through the
// project API without a worker) is cancelled: it is scaffolding that no longer
// represents outstanding work, and leaving it active would both trip
// ux_work_active_phase and keep CountUnsettledWorkItemsByProject above zero, so
// the project could never reach completed.
func settlePhaseWorkItem(ctx context.Context, tx *sql.Tx, projectID int64, kind string, now time.Time) error {
	item, err := store.GetActivePhaseWorkItem(ctx, tx, projectID, kind)
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return err
	}

	updateTime := formatTimestamp(nextTimestampAfter(now, item.UpdateTime))
	if item.State != store.WorkItemStateSubmitted {
		_, err := store.CancelWorkItem(ctx, tx, item.ID, updateTime)
		return err
	}

	affected, err := store.TransitionWorkItemDecision(ctx, tx, store.WorkItemDecisionWrite{
		ID:            item.ID,
		ExpectState:   item.State,
		ExpectVersion: item.Version,
		NextState:     store.WorkItemStateAccepted,
		UpdateTime:    updateTime,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		fresh, err := store.GetWorkItemByID(ctx, tx, item.ID)
		if err != nil {
			return err
		}
		if fresh.State == store.WorkItemStateAccepted {
			return nil
		}
		return &domain.InvalidVersionError{Current: fresh.Version, Expected: item.Version}
	}
	return nil
}

// isPhaseKind reports whether kind is one of the three producing-stage cards
// (需求撰写 / 方案设计 / 任务拆分) rather than a dev card.
func isPhaseKind(kind string) bool {
	switch kind {
	case phaseKindRequirement, phaseKindDesign, phaseKindBreakdown:
		return true
	default:
		return false
	}
}

// exhaustedReason renders the objection of a card whose attempt budget is used
// up, so the failure explains itself in the inbox instead of looking like an
// agent error.
func exhaustedReason(base string, attempt, maxAttempts int) string {
	note := fmt.Sprintf("本轮 %d/%d 次尝试已用完，需人工驳回该卡重开", attempt, maxAttempts)
	if trimmed := strings.TrimSpace(base); trimmed != "" {
		return trimmed + "｜" + note
	}
	return note
}

// requeueTarget decides the state a human rejection sends a card to.
//
// The attempt budget is per round, and the claim query only takes cards with
// attempt < max_attempts. Requeueing the last allowed attempt would therefore
// park the card in a queue nothing dispatches and the inbox does not show — the
// reported "stuck in queued forever". Such a card goes to failed instead, which
// is an inbox candidate; the human can then reject it again to start a new
// round (DecideWorkItem resets the budget on a failed card).
func requeueTarget(state string, attempt, maxAttempts int, reason string) (string, *string) {
	trimmed := strings.TrimSpace(reason)
	if state == store.WorkItemStateFailed || attempt >= maxAttempts {
		text := exhaustedReason(trimmed, attempt, maxAttempts)
		return store.WorkItemStateFailed, &text
	}
	if trimmed == "" {
		return store.WorkItemStateQueued, nil
	}
	return store.WorkItemStateQueued, &trimmed
}

// requeuePhaseWorkItem sends a stage card whose artifact the gate just rejected
// back to the queue, in the caller's transaction.
//
// The card keeps its attempt budget (a rework is the same attempt, not a fresh
// one) and carries the objection, so the worker that claims it reads why it came
// back. Nothing is created here: a project whose gate was reached by a manual
// upload owns no card, and enqueueing one would take the rework away from the
// human who is already editing the document. Only a reported (submitted) or
// failed card is moved; a queued/leased one is already on its way.
func requeuePhaseWorkItem(ctx context.Context, tx *sql.Tx, projectID int64, kind, reason string, now time.Time) error {
	item, err := store.GetActivePhaseWorkItem(ctx, tx, projectID, kind)
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return err
	}
	if item.State != store.WorkItemStateSubmitted && item.State != store.WorkItemStateFailed {
		return nil
	}

	next, reasonPtr := requeueTarget(item.State, item.Attempt, item.MaxAttempts, reason)
	updateTime := formatTimestamp(nextTimestampAfter(now, item.UpdateTime))
	affected, err := store.TransitionWorkItemDecision(ctx, tx, store.WorkItemDecisionWrite{
		ID:            item.ID,
		ExpectState:   item.State,
		ExpectVersion: item.Version,
		NextState:     next,
		Reason:        reasonPtr,
		ResetAttempts: false,
		UpdateTime:    updateTime,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		fresh, readErr := store.GetWorkItemByID(ctx, tx, item.ID)
		if readErr != nil {
			return readErr
		}
		if fresh.State == store.WorkItemStateQueued {
			return nil
		}
		return &domain.InvalidVersionError{Current: fresh.Version, Expected: item.Version}
	}
	return nil
}

// advanceProjectOnPhaseReport moves the project to its review gate after a
// phase card was reported, inside the caller's report transaction.
//
// The mapping is fixed (0→1, 2→3, 4→5) and only applies when the project still
// sits in the expected status; a mismatching status means the gate already
// moved (replay, manual submit) and is a silent no-op rather than an error.
// Missing prerequisites are the opposite: they abort the whole report with
// invalid_state, so the card stays leased and the actor can supply the material
// and report again.
func (s *WorkItemService) advanceProjectOnPhaseReport(ctx context.Context, tx *sql.Tx, item store.WorkItem, actor string) (*ProjectAdvance, error) {
	expected, isPhase := phaseReportStatus(item.Kind)
	if !isPhase {
		return nil, nil
	}

	project, err := store.GetProjectByProjectID(ctx, tx, item.ProjectCode)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, &domain.NotFoundError{Resource: "project", Key: item.ProjectCode}
		}
		return nil, err
	}
	if project.Status != expected {
		return nil, nil
	}

	if err := validatePhaseReportPrerequisite(ctx, tx, project, item.Kind); err != nil {
		return nil, err
	}

	next, err := domain.ProjectNextStatus(project.Status, domain.ProjectActionSubmit, "")
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
			return &ProjectAdvance{
				ProjectID:  fresh.ProjectID,
				FromStatus: project.Status,
				ToStatus:   fresh.Status,
			}, nil
		}
		return nil, &domain.InvalidVersionError{Current: fresh.Version, Expected: project.Version}
	}

	return &ProjectAdvance{
		ProjectID:  project.ProjectID,
		FromStatus: project.Status,
		ToStatus:   next,
	}, nil
}

// validatePhaseReportPrerequisite mirrors validateSubmitPrecondition: the same
// artifacts a manual submit needs are required before the card may move the
// gate. The requirement literal is shared with the HTTP 409 body on purpose.
func validatePhaseReportPrerequisite(ctx context.Context, tx *sql.Tx, project store.Project, kind string) error {
	switch kind {
	case phaseKindRequirement:
		if project.RequirementMaterialID == nil {
			return &domain.InvalidStateError{Reason: "请先上传需求文档"}
		}
	case phaseKindDesign:
		docs, err := store.ListSpecDocuments(ctx, tx, project.ID)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q has no spec document; report requires at least one spec", project.ProjectID)}
		}
	}
	return nil
}
