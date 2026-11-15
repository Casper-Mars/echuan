package app

import (
	"context"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// TestDependentCardWaitsForAcceptance pins the dispatch rule: a dev card that
// depends on another card must not be claimable until that dependency is
// accepted, not merely reported.
func TestDependentCardWaitsForAcceptance(t *testing.T) {
	f := newWorkItemFixture(t)
	ctx := context.Background()

	a := f.createItem(t, "dev", "A", 10)
	b, err := f.items.CreateWorkItem(ctx, CreateWorkItemInput{
		ProjectID: f.projectID, Kind: "dev", Title: "B", BodyMD: "# B\n",
		Priority: 0, DependsOn: []string{a.Item.WorkID},
	}, "human:cto")
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if got := b.Item.DependsOn; len(got) != 1 || got[0] != a.Item.WorkID {
		t.Fatalf("B depends_on = %v, want [%s]", got, a.Item.WorkID)
	}

	if _, err := f.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:pm",
	}); err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}

	workerID, first := f.registerWorker(t)
	if first == nil || first.WorkID != a.Item.WorkID {
		t.Fatalf("first dispatch = %+v, want A", first)
	}

	// A reported but not accepted: B must stay out of the queue.
	if _, err := f.items.ReportWorkItem(ctx, ReportWorkItemInput{
		WorkID: first.WorkID, LeaseID: first.Lease.LeaseID,
		MaterialID: f.specID, Actor: "agent:w",
	}); err != nil {
		t.Fatalf("report A: %v", err)
	}
	blocked, err := f.claims.Claim(ctx, ClaimInput{WorkerID: workerID})
	if err != nil {
		t.Fatalf("Claim while A is submitted: %v", err)
	}
	if blocked != nil {
		t.Fatalf("claim = %s, want nothing until A is accepted", blocked.WorkID)
	}
	// B never entered the queue: it is still blocked, not merely refused by the
	// claim predicate. That is what makes "queued" mean "dispatchable".
	if view := f.workItemView(t, b.Item.WorkID); view.State != store.WorkItemStateBlocked {
		t.Errorf("B state while A is submitted = %q, want blocked", view.State)
	} else if len(view.BlockedBy) != 1 || view.BlockedBy[0] != a.Item.WorkID {
		t.Errorf("B blocked_by = %v, want [%s]", view.BlockedBy, a.Item.WorkID)
	}

	// A directed claim must not bypass the dependency gate either.
	if directed, err := f.claims.Claim(ctx, ClaimInput{
		WorkerID: workerID, WorkID: b.Item.WorkID,
	}); err != nil || directed != nil {
		t.Fatalf("directed claim = (%+v, %v), want (nil, nil)", directed, err)
	}

	if _, err := f.items.DecideWorkItem(ctx, WorkItemDecisionInput{
		WorkID: first.WorkID, Decision: "approve", Actor: "human:qa",
	}); err != nil {
		t.Fatalf("accept A: %v", err)
	}
	next, err := f.claims.Claim(ctx, ClaimInput{WorkerID: workerID})
	if err != nil {
		t.Fatalf("Claim after acceptance: %v", err)
	}
	if next == nil || next.WorkID != b.Item.WorkID {
		t.Fatalf("claim after acceptance = %+v, want B", next)
	}
	// Accepting A is what released B, and the release cleared blocked_by.
	if view := f.workItemView(t, b.Item.WorkID); view.State != store.WorkItemStateLeased || len(view.BlockedBy) != 0 {
		t.Errorf("B after acceptance = %q/blocked_by=%v, want leased with no open dependency",
			view.State, view.BlockedBy)
	}
}

// workItemView reads one card through the service, the same path the API uses.
func (f *workItemFixture) workItemView(t *testing.T, workID string) WorkItemView {
	t.Helper()
	detail, err := f.items.GetWorkItem(context.Background(), workID)
	if err != nil {
		t.Fatalf("GetWorkItem(%s): %v", workID, err)
	}
	return detail.Item
}

// TestBreakdownChildrenWaitForAcceptance is the same rule for cards generated
// by a plan: reporting item 1 must not release item 2, only accepting it does.
func TestBreakdownChildrenWaitForAcceptance(t *testing.T) {
	f := newBreakdownFixture(t)
	ctx := context.Background()

	if _, err := f.reportPlan(planOf(
		item(1, "实现登录", 30),
		item(2, "登录测试", 20, 1),
	), ""); err != nil {
		t.Fatalf("reportPlan: %v", err)
	}
	if _, err := f.work.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	}); err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}

	first := f.claim(t, f.workerID)
	if _, err := f.report(t, first, f.materialID); err != nil {
		t.Fatalf("report first dev card: %v", err)
	}
	if next := f.claimOrNilDev(t); next != nil {
		t.Fatalf("claim = %s, want nothing until the dependency is accepted", next.WorkID)
	}
	if _, err := f.work.DecideWorkItem(ctx, WorkItemDecisionInput{
		WorkID: first.WorkID, Decision: "approve", Actor: "human:cto",
	}); err != nil {
		t.Fatalf("accept first dev card: %v", err)
	}
	if next := f.claimOrNilDev(t); next == nil || next.WorkID == first.WorkID {
		t.Fatalf("claim after acceptance = %+v, want the dependent card", next)
	}
}

// claimOrNilDev asks for a dev card without failing when the queue is empty.
func (f *breakdownFixture) claimOrNilDev(t *testing.T) *Dispatch {
	t.Helper()
	dispatch, err := f.claims.Claim(context.Background(), ClaimInput{WorkerID: f.workerID})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return dispatch
}

// TestGatedBatchApprovalStillSucceeds covers a batch whose every card waits on
// an unaccepted dependency: approving it must record the decision (not 409) and
// release nothing, with the release happening later on acceptance.
func TestGatedBatchApprovalStillSucceeds(t *testing.T) {
	f := newWorkItemFixture(t)
	ctx := context.Background()

	a := f.createItem(t, "dev", "A", 10)
	if _, err := f.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:pm",
	}); err != nil {
		t.Fatalf("ReviewWorkItems(first batch): %v", err)
	}

	// A later card that depends on A, which is still queued (not accepted).
	b, err := f.items.CreateWorkItem(ctx, CreateWorkItemInput{
		ProjectID: f.projectID, Kind: "dev", Title: "B", BodyMD: "# B\n",
		DependsOn: []string{a.Item.WorkID},
	}, "human:pm")
	if err != nil {
		t.Fatalf("create B: %v", err)
	}

	review, err := f.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:pm",
	})
	if err != nil {
		t.Fatalf("ReviewWorkItems(gated batch): %v", err)
	}
	if review.ReadyItems != 0 {
		t.Errorf("ready_work_items = %d, want 0 while B is gated", review.ReadyItems)
	}
	if view := f.workItemView(t, b.Item.WorkID); view.State != store.WorkItemStateBlocked {
		t.Errorf("B state = %q, want blocked", view.State)
	}
}
