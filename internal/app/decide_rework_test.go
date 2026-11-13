package app

// Rejected requirement gate → rework card (plan v0.4 §5.3 / §7.2 D3).
//
// Create lands the project in requirement_review without a stage card, so a
// reject that only moved 1→0 would leave requirement_draft with nobody to pick
// it up. These tests pin the compensating side effect: one queued requirement
// card in the same transaction, idempotent, and no card at all for the two
// later gates.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// reject drives one 1→0 (or later gate) reject with the project's current
// version and returns the raw result and error.
func (f *phaseFixture) reject(t *testing.T, projectID, reason string) (DecideResult, error) {
	t.Helper()
	state := f.env.projectState(t, projectID)
	return f.env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionReject,
		Reason:        reason,
		ExpectVersion: &state.Version,
		Actor:         "human:cto",
	})
}

// projectItems returns every card the project owns, ordered by seq_no.
func (f *phaseFixture) projectItems(t *testing.T, projectID string) []store.WorkItem {
	t.Helper()
	rows, err := store.ListWorkItemsByProject(context.Background(), f.env.store.Read(),
		f.env.physicalProjectID(t, projectID))
	if err != nil {
		t.Fatalf("ListWorkItemsByProject: %v", err)
	}
	return rows
}

// TestRequirementRejectEnqueuesReworkCard is AC1 and AC2 at the service layer:
// rejecting the requirement gate lands in requirement_draft with the objection
// persisted and exactly one queued requirement card next to it.
func TestRequirementRejectEnqueuesReworkCard(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "登录改版", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	if items := f.phaseItems(t, projectID, phaseKindRequirement); len(items) != 0 {
		t.Fatalf("requirement items after create = %d (%+v), want 0", len(items), items)
	}

	rejected, err := f.reject(t, projectID, "需求缺少验收标准")
	if err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
	if rejected.FromStatus != domain.StatusRequirementReview || rejected.ToStatus != domain.StatusRequirementDraft {
		t.Errorf("transition = %d→%d, want 1→0", rejected.FromStatus, rejected.ToStatus)
	}
	if rejected.Project.Status != domain.StatusRequirementDraft {
		t.Errorf("status = %d, want requirement_draft", rejected.Project.Status)
	}
	if rejected.Project.Reason != "需求缺少验收标准" {
		t.Errorf("reason = %q, want the objection", rejected.Project.Reason)
	}

	// The persisted row agrees with the result the service returned.
	state := f.env.projectState(t, projectID)
	if state.Status != domain.StatusRequirementDraft {
		t.Errorf("persisted status = %d, want requirement_draft", state.Status)
	}
	if state.Reason == nil || *state.Reason != "需求缺少验收标准" {
		t.Errorf("persisted reason = %v, want the objection", state.Reason)
	}

	items := f.phaseItems(t, projectID, phaseKindRequirement)
	if len(items) != 1 {
		t.Fatalf("requirement items = %d, want exactly 1 rework card", len(items))
	}
	card := items[0]
	if card.State != store.WorkItemStateQueued {
		t.Errorf("rework state = %q, want queued", card.State)
	}
	if card.Priority != 990 {
		t.Errorf("rework priority = %d, want 990", card.Priority)
	}
	if card.Title != "需求撰写：登录改版" {
		t.Errorf("rework title = %q, want 需求撰写：登录改版", card.Title)
	}
	if card.BodyMD == "" {
		t.Error("rework body is empty, want the stage description")
	}
	if card.MaxAttempts <= 0 {
		t.Errorf("rework max_attempts = %d, want the service budget", card.MaxAttempts)
	}
	// The card belongs to the rejected project and is the only one it owns.
	if all := f.projectItems(t, projectID); len(all) != 1 {
		t.Errorf("project cards = %d (%+v), want only the rework card", len(all), all)
	}
}

// TestRequirementReworkCardLifecycle is AC5: the rework card drives the
// resubmission. Reporting it returns the project to review and the following
// approve accepts it; a manual submit does the same and the card is cancelled
// as scaffolding.
func TestRequirementReworkCardLifecycle(t *testing.T) {
	t.Run("reported card is accepted", func(t *testing.T) {
		f := newPhaseFixture(t)
		detail := f.createProject(t, "报告返工", "# 需求\n\n正文\n")
		projectID := detail.Project.ProjectID

		if _, err := f.reject(t, projectID, "补验收标准"); err != nil {
			t.Fatalf("Decide(reject): %v", err)
		}

		_, dispatch := f.register(t, phaseKindRequirement)
		if dispatch == nil || dispatch.Kind != phaseKindRequirement {
			t.Fatalf("first dispatch = %+v, want the rework card", dispatch)
		}
		if _, err := f.report(t, dispatch, detail.Requirement.MaterialID); err != nil {
			t.Fatalf("report rework: %v", err)
		}
		if state := f.env.projectState(t, projectID); state.Status != domain.StatusRequirementReview {
			t.Fatalf("status after report = %d, want requirement_review", state.Status)
		}

		approved := f.approve(t, projectID)
		if approved.Status != domain.StatusSpecDraft {
			t.Fatalf("status after approve = %d, want spec_draft", approved.Status)
		}
		if item := f.singlePhaseItem(t, projectID, phaseKindRequirement); item.State != store.WorkItemStateAccepted {
			t.Errorf("rework state after approve = %q, want accepted", item.State)
		}
		design := f.singlePhaseItem(t, projectID, phaseKindDesign)
		if design.State != store.WorkItemStateQueued || design.Priority != 980 {
			t.Errorf("design = %q/%d, want queued/980", design.State, design.Priority)
		}
	})

	t.Run("manual submit cancels the queued card", func(t *testing.T) {
		f := newPhaseFixture(t)
		detail := f.createProject(t, "手工返工", "# 需求\n\n正文\n")
		projectID := detail.Project.ProjectID

		if _, err := f.reject(t, projectID, "补验收标准"); err != nil {
			t.Fatalf("Decide(reject): %v", err)
		}
		if _, err := f.env.service.SubmitProject(context.Background(), projectID, "human:pm"); err != nil {
			t.Fatalf("SubmitProject: %v", err)
		}
		if state := f.env.projectState(t, projectID); state.Status != domain.StatusRequirementReview {
			t.Fatalf("status after submit = %d, want requirement_review", state.Status)
		}
		if item := f.singlePhaseItem(t, projectID, phaseKindRequirement); item.State != store.WorkItemStateQueued {
			t.Errorf("rework state after submit = %q, want still queued", item.State)
		}

		approved := f.approve(t, projectID)
		if approved.Status != domain.StatusSpecDraft {
			t.Fatalf("status after approve = %d, want spec_draft", approved.Status)
		}
		if item := f.singlePhaseItem(t, projectID, phaseKindRequirement); item.State != store.WorkItemStateCancelled {
			t.Errorf("bypassed rework = %q, want cancelled", item.State)
		}
		if design := f.singlePhaseItem(t, projectID, phaseKindDesign); design.State != store.WorkItemStateQueued {
			t.Errorf("design = %q, want queued", design.State)
		}
	})
}

// TestSpecRejectEnqueuesNoCard is the first half of AC4: the spec gate still
// falls back 3→2 and touches no card.
func TestSpecRejectEnqueuesNoCard(t *testing.T) {
	f := newPhaseFixture(t)
	ctx := context.Background()
	detail := f.createProject(t, "方案驳回", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	if approved := f.approve(t, projectID); approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}
	if _, _, err := f.env.service.SaveSpec(ctx, projectID, "", "agent:cto",
		titlePtr("方案"), "# 方案\n", 0, false); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := f.env.service.SubmitProject(ctx, projectID, "human:pm"); err != nil {
		t.Fatalf("SubmitProject(spec): %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusSpecReview {
		t.Fatalf("status = %d, want spec_review", state.Status)
	}

	before := f.projectItems(t, projectID)
	rejected, err := f.reject(t, projectID, "方案缺回滚")
	if err != nil {
		t.Fatalf("Decide(reject spec): %v", err)
	}
	if rejected.ToStatus != domain.StatusSpecDraft || rejected.Project.Status != domain.StatusSpecDraft {
		t.Fatalf("spec reject to = %d/%d, want spec_draft", rejected.ToStatus, rejected.Project.Status)
	}
	after := f.projectItems(t, projectID)
	if len(after) != len(before) {
		t.Fatalf("cards after spec reject = %d, want unchanged %d (%+v)", len(after), len(before), after)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindDesign); item.State != store.WorkItemStateQueued {
		t.Errorf("design state after spec reject = %q, want queued", item.State)
	}
	if items := f.phaseItems(t, projectID, phaseKindRequirement); len(items) != 0 {
		t.Errorf("requirement items after spec reject = %d, want 0", len(items))
	}
}

// TestTasksReviewRejectEnqueuesNoCard is the second half of AC4 for the path
// that actually exists: the plan batch is rejected through /v1/work-items/review,
// which records the objection and leaves the status on tasks_review (5) — the
// state machine owns no 5→4 edge. Whatever the status does, no stage card may
// appear.
func TestTasksReviewRejectEnqueuesNoCard(t *testing.T) {
	f := newPhaseFixture(t)
	ctx := context.Background()
	detail := f.createProject(t, "拆分驳回", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	if approved := f.approve(t, projectID); approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}
	if _, _, err := f.env.service.SaveSpec(ctx, projectID, "", "agent:cto",
		titlePtr("方案"), "# 方案\n", 0, false); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := f.env.service.SubmitProject(ctx, projectID, "human:pm"); err != nil {
		t.Fatalf("SubmitProject(spec): %v", err)
	}
	if approved := f.approve(t, projectID); approved.Status != domain.StatusTasksDraft {
		t.Fatalf("status = %d, want tasks_draft", approved.Status)
	}
	if _, err := f.env.service.SubmitProject(ctx, projectID, "human:pm"); err != nil {
		t.Fatalf("SubmitProject(tasks): %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusTasksReview {
		t.Fatalf("status = %d, want tasks_review", state.Status)
	}

	before := f.projectItems(t, projectID)
	review, err := f.work.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: projectID,
		Decision:  domain.DecisionReject,
		Reason:    "拆分粒度过粗",
		Actor:     "human:pm",
	})
	if err != nil {
		t.Fatalf("ReviewWorkItems(reject): %v", err)
	}
	// Existing behaviour, pinned as-is: the batch reject records the objection
	// and does not move the project (plan gate §5).
	if review.ToStatus != domain.StatusTasksReview {
		t.Errorf("tasks reject to = %d, want tasks_review (unchanged)", review.ToStatus)
	}
	state := f.env.projectState(t, projectID)
	if state.Status != domain.StatusTasksReview {
		t.Errorf("status after tasks reject = %d, want tasks_review", state.Status)
	}
	if review.Reason != "拆分粒度过粗" || review.RejectedAt == "" {
		t.Errorf("review = %+v, want the recorded objection and timestamp", review)
	}

	after := f.projectItems(t, projectID)
	if len(after) != len(before) {
		t.Fatalf("cards after tasks reject = %d, want unchanged %d (%+v)", len(after), len(before), after)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindBreakdown); item.State != store.WorkItemStateQueued {
		t.Errorf("breakdown state after tasks reject = %q, want queued", item.State)
	}
	for _, kind := range []string{phaseKindRequirement, phaseKindDesign} {
		if items := f.phaseItems(t, projectID, kind); len(items) > 1 {
			t.Errorf("%s items after tasks reject = %d, want at most 1", kind, len(items))
		}
	}
}

// TestRequirementRejectIsIdempotent is AC3 and the sequential half of AC6: a
// replayed reject is refused with the version error, a reject with the correct
// version is refused by the state machine, and neither adds a second card.
func TestRequirementRejectIsIdempotent(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "重复驳回", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	stale := f.env.projectState(t, projectID).Version
	if _, err := f.reject(t, projectID, "缺少验收标准"); err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}

	// Replay with the consumed version.
	if _, err := f.env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionReject,
		Reason:        "缺少验收标准",
		ExpectVersion: &stale,
		Actor:         "human:cto",
	}); !errors.Is(err, domain.ErrInvalidVersion) {
		t.Fatalf("replayed reject = %v, want invalid_version", err)
	}

	// The fresh version is accepted by the guard, so the state machine must
	// refuse the second 1→0 (the project already sits in 0).
	fresh := f.env.projectState(t, projectID)
	if _, err := f.env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionReject,
		Reason:        "缺少验收标准",
		ExpectVersion: &fresh.Version,
		Actor:         "human:cto",
	}); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("second reject = %v, want invalid_state", err)
	}

	if items := f.phaseItems(t, projectID, phaseKindRequirement); len(items) != 1 {
		t.Fatalf("requirement items = %d, want exactly 1", len(items))
	}
	if state := f.env.projectState(t, projectID); state.Version != fresh.Version || state.Status != domain.StatusRequirementDraft {
		t.Errorf("project after replays = %+v, want unchanged version/status", state)
	}
}

// TestRequirementRejectConcurrentOnlyOneWins is AC6: two goroutines race the
// same reject on the same version; the write transaction lets exactly one
// through and the unique active-phase index keeps a single rework card.
func TestRequirementRejectConcurrentOnlyOneWins(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "并发驳回", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID
	version := f.env.projectState(t, projectID).Version

	const attempts = 2
	results := make([]error, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := f.env.service.Decide(context.Background(), DecideInput{
				Kind:          DecideKindProject,
				ID:            projectID,
				Decision:      domain.DecisionReject,
				Reason:        "并发驳回",
				ExpectVersion: &version,
				Actor:         "human:cto",
			})
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes, versionConflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrInvalidVersion):
			// The single-writer pool serialises the two transactions, so the
			// loser re-reads the bumped version and fails on the If-Match guard
			// (HTTP 409 invalid_version).
			versionConflicts++
		default:
			t.Fatalf("concurrent reject = %v, want success or invalid_version", err)
		}
	}
	if successes != 1 || versionConflicts != 1 {
		t.Fatalf("successes/version conflicts = %d/%d, want 1/1 (results %v)", successes, versionConflicts, results)
	}

	if state := f.env.projectState(t, projectID); state.Status != domain.StatusRequirementDraft {
		t.Errorf("status = %d, want requirement_draft", state.Status)
	}
	items := f.phaseItems(t, projectID, phaseKindRequirement)
	if len(items) != 1 {
		t.Fatalf("requirement items = %d, want exactly 1 after the race", len(items))
	}
	if items[0].State != store.WorkItemStateQueued || items[0].Priority != 990 {
		t.Errorf("rework = %q/%d, want queued/990", items[0].State, items[0].Priority)
	}
}

// TestSpecRejectRequeuesReportedDesignCard is the 3→2 half of the same rule:
// the design card that reported the spec is sent back to the queue with the
// objection, so the agent reworks it without a second human verdict.
func TestSpecRejectRequeuesReportedDesignCard(t *testing.T) {
	f := newPhaseFixture(t)
	ctx := context.Background()
	detail := f.createProject(t, "方案返工", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	if approved := f.approve(t, projectID); approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}
	_, design := f.register(t, phaseKindDesign)
	if design == nil || design.Kind != phaseKindDesign {
		t.Fatalf("dispatch = %+v, want the design card", design)
	}
	spec, _, err := f.env.service.SaveSpec(ctx, projectID, "", "agent:cto",
		titlePtr("方案"), "# 方案\n\n正文\n", 0, false)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := f.report(t, design, spec.Spec.MaterialID); err != nil {
		t.Fatalf("report design: %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusSpecReview {
		t.Fatalf("status after report = %d, want spec_review", state.Status)
	}

	if _, err := f.reject(t, projectID, "方案缺少接口约定"); err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusSpecDraft {
		t.Fatalf("status after reject = %d, want spec_draft", state.Status)
	}
	items := f.phaseItems(t, projectID, phaseKindDesign)
	if len(items) != 1 {
		t.Fatalf("design items = %d, want the same single card", len(items))
	}
	if items[0].State != store.WorkItemStateQueued {
		t.Errorf("design state after reject = %q, want queued", items[0].State)
	}
	if items[0].Reason == nil || *items[0].Reason != "方案缺少接口约定" {
		t.Errorf("design reason = %v, want the objection", items[0].Reason)
	}
	if items[0].Attempt != 1 {
		t.Errorf("design attempt = %d, want 1 (rework keeps the attempt)", items[0].Attempt)
	}
}

// TestRequirementRejectRequeuesReportedCard pins the card-driven half of the
// rework rule for the requirement gate, and that the card-level endpoints no
// longer split the decision: the gate owns a submitted stage card.
func TestRequirementRejectRequeuesReportedCard(t *testing.T) {
	f := newPhaseFixture(t)
	ctx := context.Background()
	detail := f.createProject(t, "需求返工", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	// Create lands in requirement_review with no card; the first reject is what
	// enqueues the rework card.
	if _, err := f.reject(t, projectID, "先退回一版"); err != nil {
		t.Fatalf("first reject: %v", err)
	}
	_, dispatch := f.register(t, phaseKindRequirement)
	if dispatch == nil || dispatch.Kind != phaseKindRequirement {
		t.Fatalf("dispatch = %+v, want the requirement card", dispatch)
	}
	if _, err := f.report(t, dispatch, detail.Requirement.MaterialID); err != nil {
		t.Fatalf("report requirement: %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusRequirementReview {
		t.Fatalf("status after report = %d, want requirement_review", state.Status)
	}
	reported := f.singlePhaseItem(t, projectID, phaseKindRequirement)
	if reported.State != store.WorkItemStateSubmitted {
		t.Fatalf("state after report = %q, want submitted", reported.State)
	}

	// A submitted stage card is not decidable on its own: approve would accept
	// the artifact without moving the gate, reject would leave the gate waiting
	// with the card back in the queue.
	for _, decision := range []string{"approve", "reject"} {
		_, err := f.work.DecideWorkItem(ctx, WorkItemDecisionInput{
			WorkID: reported.WorkID, Decision: decision, Reason: "越权", Actor: "human:cto",
		})
		var invalid *domain.InvalidStateError
		if !errors.As(err, &invalid) {
			t.Errorf("DecideWorkItem(%s) = %v, want invalid_state", decision, err)
		}
	}

	if _, err := f.reject(t, projectID, "补验收标准"); err != nil {
		t.Fatalf("second reject: %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusRequirementDraft {
		t.Fatalf("status after reject = %d, want requirement_draft", state.Status)
	}
	items := f.phaseItems(t, projectID, phaseKindRequirement)
	if len(items) != 1 {
		t.Fatalf("requirement items = %d, want the same single card", len(items))
	}
	if items[0].State != store.WorkItemStateQueued {
		t.Errorf("state after reject = %q, want queued", items[0].State)
	}
	if items[0].Reason == nil || *items[0].Reason != "补验收标准" {
		t.Errorf("reason = %v, want the objection", items[0].Reason)
	}
	if items[0].Attempt != 1 {
		t.Errorf("attempt = %d, want 1 (rework keeps the attempt)", items[0].Attempt)
	}
}

// TestSpecRejectExhaustsBudgetIntoFailed pins the fix for "after three
// rejections the card sits in the queue forever": the claim query only takes
// attempt < max_attempts, so the last rejection fails the card instead. A
// failed card is an inbox candidate, and rejecting it again starts a new round
// with a fresh budget.
func TestSpecRejectExhaustsBudgetIntoFailed(t *testing.T) {
	f := newPhaseFixture(t)
	ctx := context.Background()
	detail := f.createProject(t, "方案反复驳回", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID
	if approved := f.approve(t, projectID); approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}

	for round := 1; round <= defaultWorkItemMaxAttempts; round++ {
		_, dispatch := f.register(t, phaseKindDesign)
		if dispatch == nil || dispatch.Kind != phaseKindDesign {
			t.Fatalf("round %d: dispatch = %+v, want the design card", round, dispatch)
		}
		spec, _, err := f.env.service.SaveSpec(ctx, projectID, "", "agent:cto",
			titlePtr(fmt.Sprintf("方案 v%d", round)), "# 方案\n", 0, false)
		if err != nil {
			t.Fatalf("round %d: SaveSpec: %v", round, err)
		}
		if _, err := f.report(t, dispatch, spec.Spec.MaterialID); err != nil {
			t.Fatalf("round %d: report: %v", round, err)
		}
		if _, err := f.reject(t, projectID, fmt.Sprintf("第 %d 次驳回", round)); err != nil {
			t.Fatalf("round %d: reject: %v", round, err)
		}
	}

	card := f.singlePhaseItem(t, projectID, phaseKindDesign)
	if card.State != store.WorkItemStateFailed {
		t.Fatalf("state after %d rejections = %q, want failed (queued would never be dispatched)",
			defaultWorkItemMaxAttempts, card.State)
	}
	if card.Attempt != defaultWorkItemMaxAttempts {
		t.Errorf("attempt = %d, want %d", card.Attempt, defaultWorkItemMaxAttempts)
	}
	if card.Reason == nil || !strings.Contains(*card.Reason, "已用完") {
		t.Errorf("reason = %v, want the budget note", card.Reason)
	}

	// No worker can pick it up at this point.
	if dispatch := f.claimOrNil(t); dispatch != nil {
		t.Fatalf("claim = %+v, want nothing while the budget is exhausted", dispatch)
	}

	// Rejecting the failed card is the human's "another round": the budget is
	// reset so the card is claimable again.
	decided, err := f.work.DecideWorkItem(ctx, WorkItemDecisionInput{
		WorkID: card.WorkID, Decision: "reject", Reason: "再给一轮", Actor: "human:cto",
	})
	if err != nil {
		t.Fatalf("DecideWorkItem(reject failed): %v", err)
	}
	if decided.ToState != store.WorkItemStateQueued {
		t.Errorf("to_state = %q, want queued", decided.ToState)
	}
	if decided.Detail.Item.Attempt != 0 {
		t.Errorf("attempt = %d, want 0 after the human reopened the card", decided.Detail.Item.Attempt)
	}
	if next := f.claimOrNil(t); next == nil || next.Kind != phaseKindDesign {
		t.Fatalf("claim after reopen = %+v, want the design card", next)
	}
}

// claimOrNil registers a design worker and returns the dispatch, if any.
func (f *phaseFixture) claimOrNil(t *testing.T) *Dispatch {
	t.Helper()
	_, dispatch := f.register(t, phaseKindDesign)
	return dispatch
}
