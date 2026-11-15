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

	// A reported but not accepted: B stays out of the dispatch queue even though
	// the approval already moved it to queued.
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
	// B is queued (the approval put it on the board) but not claimable: the
	// claim predicate, not the state transition, is what gates dispatch.
	if view := f.workItemView(t, b.Item.WorkID); view.State != store.WorkItemStateQueued {
		t.Errorf("B state while A is submitted = %q, want queued", view.State)
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
	// Accepting A is what made B claimable; B was already queued, and the
	// acceptance cleared blocked_by without touching the row's state.
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
// by a plan: the approval queues both children, but reporting item 1 must not
// make item 2 claimable — only accepting item 1 does.
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

// TestGatedBatchApprovalQueuesDependents covers a batch whose every card waits
// on an unaccepted dependency: approving it must succeed (not 409), queue all of
// them, and still hand out nothing downstream until the dependency is accepted.
func TestGatedBatchApprovalQueuesDependents(t *testing.T) {
	f := newWorkItemFixture(t)
	ctx := context.Background()

	a := f.createItem(t, "dev", "A", 10)
	first, err := f.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:pm",
	})
	if err != nil {
		t.Fatalf("ReviewWorkItems(first batch): %v", err)
	}
	if first.QueuedItems != 1 || first.ReadyItems != 1 {
		t.Fatalf("first batch queued/ready = %d/%d, want 1/1", first.QueuedItems, first.ReadyItems)
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
	// The gated batch approves and queues its card; only "claimable right now"
	// is zero, because A is queued but not accepted yet.
	if review.QueuedItems != 1 {
		t.Errorf("queued_work_items = %d, want 1 (B)", review.QueuedItems)
	}
	if review.ReadyItems != 0 {
		t.Errorf("ready_work_items = %d, want 0 while B is gated", review.ReadyItems)
	}
	if review.ReadyItems > review.QueuedItems {
		t.Errorf("ready = %d > queued = %d", review.ReadyItems, review.QueuedItems)
	}
	if view := f.workItemView(t, b.Item.WorkID); view.State != store.WorkItemStateQueued {
		t.Errorf("B state = %q, want queued", view.State)
	} else if len(view.BlockedBy) != 1 || view.BlockedBy[0] != a.Item.WorkID {
		t.Errorf("B blocked_by = %v, want [%s]", view.BlockedBy, a.Item.WorkID)
	}

	// Both cards are queued now, but dispatch still follows the dependency: the
	// head is handed out, the dependent one is not, until A is accepted.
	workerID, dispatched := f.registerWorker(t)
	if dispatched == nil || dispatched.WorkID != a.Item.WorkID {
		t.Fatalf("first dispatch = %+v, want A", dispatched)
	}
	if _, err := f.items.ReportWorkItem(ctx, ReportWorkItemInput{
		WorkID: dispatched.WorkID, LeaseID: dispatched.Lease.LeaseID,
		MaterialID: f.specID, Actor: "agent:w",
	}); err != nil {
		t.Fatalf("report A: %v", err)
	}
	if next, err := f.claims.Claim(ctx, ClaimInput{WorkerID: workerID}); err != nil || next != nil {
		t.Fatalf("claim while A is submitted = (%+v, %v), want (nil, nil)", next, err)
	}
	if directed, err := f.claims.Claim(ctx, ClaimInput{
		WorkerID: workerID, WorkID: b.Item.WorkID,
	}); err != nil || directed != nil {
		t.Fatalf("directed claim = (%+v, %v), want (nil, nil)", directed, err)
	}
}
