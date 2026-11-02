package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// phaseFixture drives the whole pipeline through the same services main.go
// wires, starting from an empty requirement_draft project.
type phaseFixture struct {
	env    *projectTestEnv
	work   *WorkItemService
	claims *ClaimService
	// workers is only used to register a worker; claiming goes through claims.
	workers *WorkerService
}

func newPhaseFixture(t *testing.T) *phaseFixture {
	t.Helper()
	env := newProjectTestEnv(t)
	env.createProduct(t, "APP")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &phaseFixture{
		env:    env,
		work:   NewWorkItemService(env.store, env.writer),
		claims: NewClaimService(env.store, logger, 60*time.Second),
		workers: NewWorkerService(env.store, logger, WorkerServiceOptions{
			LeaseTTL:          60 * time.Second,
			WorkerTTL:         time.Minute,
			HeartbeatInterval: 15 * time.Second,
			MetadataMaxBytes:  4096,
		}),
	}
}

func (f *phaseFixture) createProject(t *testing.T, title, requirementMD string) domain.ProjectDetail {
	t.Helper()
	detail, err := f.env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "APP",
		Title:         title,
		RequirementMD: requirementMD,
		UploadedBy:    "human:pm",
	})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", title, err)
	}
	return detail
}

// register declares the worker's kinds and returns its id. An idle
// registration heartbeat also dispatches the best queued card, so callers that
// need the first lease read it from the returned dispatch.
func (f *phaseFixture) register(t *testing.T, kinds ...string) (string, *Dispatch) {
	t.Helper()
	result, err := f.workers.Heartbeat(context.Background(), HeartbeatInput{
		State: "idle", Kinds: kinds, Actor: "agent:w",
	})
	if err != nil {
		t.Fatalf("Heartbeat(%v): %v", kinds, err)
	}
	if len(result.Commands) == 0 {
		return result.Worker.WorkerID, nil
	}
	return result.Worker.WorkerID, result.Commands[0].Dispatch
}

func (f *phaseFixture) claim(t *testing.T, workerID string) *Dispatch {
	t.Helper()
	dispatch, err := f.claims.Claim(context.Background(), ClaimInput{
		WorkerID: workerID, Actor: "agent:w",
	})
	if err != nil {
		t.Fatalf("Claim(%s): %v", workerID, err)
	}
	if dispatch == nil {
		t.Fatalf("Claim(%s) = nil, want a dispatch", workerID)
	}
	return dispatch
}

func (f *phaseFixture) report(t *testing.T, dispatch *Dispatch, materialID string) (WorkItemDetail, error) {
	t.Helper()
	return f.work.ReportWorkItem(context.Background(), ReportWorkItemInput{
		WorkID:     dispatch.WorkID,
		MaterialID: materialID,
		LeaseID:    dispatch.Lease.LeaseID,
		Actor:      "agent:w",
	})
}

// oneItemPlan is the smallest legal breakdown plan: one dev card that names
// the target project explicitly.
func oneItemPlan(projectID, title string) *BreakdownPlan {
	return &BreakdownPlan{Items: []BreakdownPlanItem{{
		SeqNo: 1, ProjectID: projectID, Title: title,
		BodyMD: "# " + title + "\n", Priority: 10,
	}}}
}

func (f *phaseFixture) approve(t *testing.T, projectID string) *domain.Project {
	t.Helper()
	state := f.env.projectState(t, projectID)
	result, err := f.env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: &state.Version,
		Actor:         "human:cto",
	})
	if err != nil {
		t.Fatalf("Decide(approve %s): %v", projectID, err)
	}
	return result.Project
}

// phaseItems returns the items of one kind, ordered by seq_no.
func (f *phaseFixture) phaseItems(t *testing.T, projectID, kind string) []store.WorkItem {
	t.Helper()
	rows, err := store.ListWorkItemsByProject(context.Background(), f.env.store.Read(),
		f.env.physicalProjectID(t, projectID))
	if err != nil {
		t.Fatalf("ListWorkItemsByProject: %v", err)
	}
	out := make([]store.WorkItem, 0, 1)
	for _, row := range rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}
	return out
}

func (f *phaseFixture) singlePhaseItem(t *testing.T, projectID, kind string) store.WorkItem {
	t.Helper()
	items := f.phaseItems(t, projectID, kind)
	if len(items) != 1 {
		t.Fatalf("%s items = %d, want 1", kind, len(items))
	}
	return items[0]
}

// TestCreateProjectEnqueuesRequirement is acceptance 1.
func TestCreateProjectEnqueuesRequirement(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "登录改版", "# 需求\n\n正文\n")

	if detail.Project.Status != domain.StatusRequirementDraft {
		t.Fatalf("status = %d, want requirement_draft", detail.Project.Status)
	}
	item := f.singlePhaseItem(t, detail.Project.ProjectID, phaseKindRequirement)
	if item.State != store.WorkItemStateQueued {
		t.Errorf("state = %q, want queued", item.State)
	}
	if item.Priority != 990 {
		t.Errorf("priority = %d, want 990", item.Priority)
	}
	wantTitle := "需求撰写：登录改版"
	if item.Title != wantTitle {
		t.Errorf("title = %q, want %q", item.Title, wantTitle)
	}
	if item.BodyMD == "" {
		t.Error("body is empty, want the stage description")
	}
	if detail.Requirement == nil {
		t.Fatal("requirement material is missing")
	}
}

// TestPhaseReportAdvancesProject is acceptance 2 and 3: a reported
// requirement moves 0→1, the approval moves 1→2 and enqueues design, and a
// stale replay cannot create a second design card.
func TestPhaseReportAdvancesProject(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "登录改版", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	workerID, dispatch := f.register(t, phaseKindRequirement)
	if dispatch == nil || dispatch.Kind != phaseKindRequirement {
		t.Fatalf("first dispatch = %+v, want the requirement card", dispatch)
	}
	if _, err := f.report(t, dispatch, detail.Requirement.MaterialID); err != nil {
		t.Fatalf("report requirement: %v", err)
	}

	if state := f.env.projectState(t, projectID); state.Status != domain.StatusRequirementReview {
		t.Fatalf("status after report = %d, want requirement_review", state.Status)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindRequirement); item.State != store.WorkItemStateSubmitted {
		t.Errorf("requirement state = %q, want submitted", item.State)
	}

	approved := f.approve(t, projectID)
	if approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status after approve = %d, want spec_draft", approved.Status)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindRequirement); item.State != store.WorkItemStateAccepted {
		t.Errorf("requirement state after approve = %q, want accepted", item.State)
	}
	design := f.singlePhaseItem(t, projectID, phaseKindDesign)
	if design.State != store.WorkItemStateQueued || design.Priority != 980 {
		t.Errorf("design = %q/%d, want queued/980", design.State, design.Priority)
	}

	// Replaying the approval with the consumed version is rejected before any
	// side effect, so the project still owns exactly one design card.
	stale := approved.Version - 1
	if _, err := f.env.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: &stale,
		Actor:         "human:cto",
	}); !errors.Is(err, domain.ErrInvalidVersion) {
		t.Fatalf("replayed approve = %v, want invalid_version", err)
	}
	if items := f.phaseItems(t, projectID, phaseKindDesign); len(items) != 1 {
		t.Fatalf("design items = %d, want exactly 1 after replay", len(items))
	}
	_ = workerID
}

// TestPhaseReportWithoutRequirementRollsBack is the transaction red line: the
// report must fail as a whole, leaving the project and the lease untouched.
func TestPhaseReportWithoutRequirementRollsBack(t *testing.T) {
	f := newPhaseFixture(t)
	project := f.createProject(t, "无需求项目", "")
	// A second project supplies a real material for the doomed report.
	donor := f.createProject(t, "供料项目", "# 需求\n\n供料\n")

	_, dispatch := f.register(t, phaseKindRequirement)
	if dispatch == nil {
		t.Fatal("no requirement dispatch")
	}

	before := f.env.projectState(t, project.Project.ProjectID)
	_, err := f.report(t, dispatch, donor.Requirement.MaterialID)
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("report without requirement = %v, want invalid_state", err)
	}

	after := f.env.projectState(t, project.Project.ProjectID)
	if after.Status != before.Status || after.Version != before.Version {
		t.Errorf("project after rollback = %+v, want unchanged %+v", after, before)
	}
	item := f.singlePhaseItem(t, project.Project.ProjectID, phaseKindRequirement)
	if item.State != store.WorkItemStateLeased {
		t.Errorf("item state = %q, want leased (rollback must keep the lease)", item.State)
	}
	if item.LeaseID == nil || *item.LeaseID != dispatch.Lease.LeaseID {
		t.Errorf("lease_id = %v, want %q", item.LeaseID, dispatch.Lease.LeaseID)
	}
	if item.ResultMaterialID != nil {
		t.Errorf("result_material_id = %v, want nil", *item.ResultMaterialID)
	}

	// The worker can upload the missing requirement and report again on the
	// same lease.
	if _, err := f.env.service.WriteRequirement(context.Background(), project.Project.ProjectID, WriteRequirementInput{
		BodyMD: "# 补齐的需求\n\n正文\n", UploadedBy: "agent:w",
	}); err != nil {
		t.Fatalf("WriteRequirement: %v", err)
	}
	if _, err := f.report(t, dispatch, donor.Requirement.MaterialID); err != nil {
		t.Fatalf("report after upload: %v", err)
	}
	if state := f.env.projectState(t, project.Project.ProjectID); state.Status != domain.StatusRequirementReview {
		t.Errorf("status after retry = %d, want requirement_review", state.Status)
	}
}

// TestDesignAndBreakdownReportsAdvance covers 2→3 and 4→5, including the spec
// precondition and the gate enqueues (acceptance 3/4).
func TestDesignAndBreakdownReportsAdvance(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "全链路", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	workerID, dispatch := f.register(t, phaseKindRequirement, phaseKindDesign, phaseKindBreakdown)
	if _, err := f.report(t, dispatch, detail.Requirement.MaterialID); err != nil {
		t.Fatalf("report requirement: %v", err)
	}
	if approved := f.approve(t, projectID); approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}

	designDispatch := f.claim(t, workerID)
	if designDispatch.Kind != phaseKindDesign {
		t.Fatalf("claimed kind = %q, want design", designDispatch.Kind)
	}
	// Without a spec the report must roll back and keep the lease.
	before := f.env.projectState(t, projectID)
	if _, err := f.report(t, designDispatch, detail.Requirement.MaterialID); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("design report without spec = %v, want invalid_state", err)
	}
	if after := f.env.projectState(t, projectID); after.Status != before.Status || after.Version != before.Version {
		t.Errorf("project after failed design report = %+v, want unchanged", after)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindDesign); item.State != store.WorkItemStateLeased {
		t.Errorf("design state = %q, want leased", item.State)
	}

	spec, _, err := f.env.service.SaveSpec(context.Background(), projectID, "", "agent:cto",
		titlePtr("方案"), "# 方案\n", 0, false)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := f.report(t, designDispatch, spec.Spec.MaterialID); err != nil {
		t.Fatalf("report design: %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusSpecReview {
		t.Fatalf("status after design report = %d, want spec_review", state.Status)
	}

	if approved := f.approve(t, projectID); approved.Status != domain.StatusTasksDraft {
		t.Fatalf("status = %d, want tasks_draft", approved.Status)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindDesign); item.State != store.WorkItemStateAccepted {
		t.Errorf("design state after approve = %q, want accepted", item.State)
	}
	breakdown := f.singlePhaseItem(t, projectID, phaseKindBreakdown)
	if breakdown.State != store.WorkItemStateQueued || breakdown.Priority != 970 {
		t.Errorf("breakdown = %q/%d, want queued/970", breakdown.State, breakdown.Priority)
	}

	breakdownDispatch := f.claim(t, workerID)
	if breakdownDispatch.Kind != phaseKindBreakdown {
		t.Fatalf("claimed kind = %q, want breakdown", breakdownDispatch.Kind)
	}
	if _, err := f.work.ReportWorkItem(context.Background(), ReportWorkItemInput{
		WorkID:  breakdownDispatch.WorkID,
		LeaseID: breakdownDispatch.Lease.LeaseID,
		Plan:    oneItemPlan(projectID, "实现卡"),
		Actor:   "agent:w",
	}); err != nil {
		t.Fatalf("report breakdown: %v", err)
	}
	if state := f.env.projectState(t, projectID); state.Status != domain.StatusTasksReview {
		t.Fatalf("status after breakdown report = %d, want tasks_review", state.Status)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindBreakdown); item.State != store.WorkItemStateSubmitted {
		t.Errorf("breakdown state = %q, want submitted", item.State)
	}

	// A dev card added in tasks_review satisfies the batch-approval guard; the
	// approval moves 5→6 and accepts the reported breakdown card.
	if _, err := f.work.CreateWorkItem(context.Background(), CreateWorkItemInput{
		ProjectID: projectID, Kind: "dev", Title: "实现卡", BodyMD: "# 实现\n", Priority: 10,
	}, "human:cto"); err != nil {
		t.Fatalf("CreateWorkItem(dev): %v", err)
	}
	review, err := f.work.ReviewWorkItems(context.Background(), ReviewWorkItemsInput{
		ProjectID: projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	})
	if err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}
	if review.ToStatus != domain.StatusPendingDone {
		t.Fatalf("status after review = %d, want pending_done", review.ToStatus)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindBreakdown); item.State != store.WorkItemStateAccepted {
		t.Errorf("breakdown state after review = %q, want accepted", item.State)
	}
}

// TestGateApprovalSettlesBypassedCard covers the manual-submit path: the stage
// card is still queued when the human approves the gate, and closing it keeps
// the project able to reach completed later.
func TestGateApprovalSettlesBypassedCard(t *testing.T) {
	f := newPhaseFixture(t)
	detail := f.createProject(t, "手工推进", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	if _, err := f.env.service.SubmitProject(context.Background(), projectID, "human:pm"); err != nil {
		t.Fatalf("SubmitProject: %v", err)
	}
	approved := f.approve(t, projectID)
	if approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}
	if item := f.singlePhaseItem(t, projectID, phaseKindRequirement); item.State != store.WorkItemStateCancelled {
		t.Errorf("bypassed requirement = %q, want cancelled", item.State)
	}
	if design := f.singlePhaseItem(t, projectID, phaseKindDesign); design.State != store.WorkItemStateQueued {
		t.Errorf("design = %q, want queued", design.State)
	}
}
