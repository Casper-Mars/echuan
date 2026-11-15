package app

import (
	"context"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// TestPlanApprovalQueuesEveryCard pins issue #49: one batch approval moves every
// blocked card of the project to queued in the same transaction — the gated ones
// included — and reports both counts (queued = the whole batch, ready = the
// subset that is claimable right now).
func TestPlanApprovalQueuesEveryCard(t *testing.T) {
	f := newWorkItemFixture(t)
	ctx := context.Background()

	head := f.createItem(t, "dev", "实现登录", 30)
	dependents := make([]WorkItemDetail, 0, 2)
	for i, title := range []string{"登录测试", "登录文档"} {
		detail, err := f.items.CreateWorkItem(ctx, CreateWorkItemInput{
			ProjectID: f.projectID, Kind: "dev", Title: title, BodyMD: "# " + title + "\n",
			Priority: 20 - i*10, DependsOn: []string{head.Item.WorkID},
		}, "human:cto")
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		dependents = append(dependents, detail)
	}

	// Before the approval the queue is closed: every card is still blocked.
	page, err := f.items.ListWorkItems(ctx, ListWorkItemsInput{
		ProjectID: f.projectID, Kinds: []string{"dev"}, States: []string{store.WorkItemStateBlocked}, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListWorkItems(blocked): %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("blocked dev cards before approval = %d, want 3", len(page.Items))
	}

	review, err := f.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	})
	if err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}
	// All three blocked cards were queued; only the head is claimable while the
	// two dependents wait for it to be accepted.
	if review.QueuedItems != 3 {
		t.Errorf("queued_work_items = %d, want 3", review.QueuedItems)
	}
	if review.ReadyItems != 1 {
		t.Errorf("ready_work_items = %d, want 1 (only the ungated head)", review.ReadyItems)
	}
	// AC7: ready is the claimable subset of queued, never larger.
	if review.ReadyItems > review.QueuedItems {
		t.Errorf("ready_work_items = %d > queued_work_items = %d", review.ReadyItems, review.QueuedItems)
	}
	if review.ToStatus != domain.StatusPendingDone {
		t.Errorf("project status after review = %d, want pending_done", review.ToStatus)
	}

	// Every card of the batch is now queued — including the two whose dependency
	// is still only queued, not accepted.
	for _, item := range []WorkItemDetail{head, dependents[0], dependents[1]} {
		view := f.workItemView(t, item.Item.WorkID)
		if view.State != store.WorkItemStateQueued {
			t.Errorf("%s state after approval = %q, want queued", item.Item.WorkID, view.State)
		}
	}
	queued, err := f.items.ListWorkItems(ctx, ListWorkItemsInput{
		ProjectID: f.projectID, Kinds: []string{"dev"}, States: []string{store.WorkItemStateQueued}, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListWorkItems(queued): %v", err)
	}
	if len(queued.Items) != 3 {
		t.Errorf("queued dev cards after approval = %d, want 3", len(queued.Items))
	}
	// No dev card is left in "待评审": that is the visible half of the bug.
	stillBlocked, err := f.items.ListWorkItems(ctx, ListWorkItemsInput{
		ProjectID: f.projectID, Kinds: []string{"dev"}, States: []string{store.WorkItemStateBlocked}, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListWorkItems(blocked after approval): %v", err)
	}
	if len(stillBlocked.Items) != 0 {
		t.Errorf("blocked dev cards after approval = %d, want 0", len(stillBlocked.Items))
	}

	// Dispatch is unchanged: only the head comes out of the queue, the two
	// gated cards wait for its acceptance (they are covered by
	// TestGatedBatchApprovalQueuesDependents).
	_, dispatched := f.registerWorker(t)
	if dispatched == nil || dispatched.WorkID != head.Item.WorkID {
		t.Fatalf("first dispatch = %+v, want the head", dispatched)
	}
}

// TestAcceptanceDoesNotTouchDependents pins the other half of issue #49: the
// acceptance of a card must not write any downstream row. The dependents were
// already queued by the approval, so their state/version/update_time must be
// byte-for-byte identical before and after the upstream submitted → accepted
// transition.
func TestAcceptanceDoesNotTouchDependents(t *testing.T) {
	f := newWorkItemFixture(t)
	ctx := context.Background()

	head := f.createItem(t, "dev", "实现登录", 30)
	dependentIDs := make([]string, 0, 2)
	for i, title := range []string{"登录测试", "登录文档"} {
		detail, err := f.items.CreateWorkItem(ctx, CreateWorkItemInput{
			ProjectID: f.projectID, Kind: "dev", Title: title, BodyMD: "# " + title + "\n",
			Priority: 20 - i*10, DependsOn: []string{head.Item.WorkID},
		}, "human:cto")
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		dependentIDs = append(dependentIDs, detail.Item.WorkID)
	}

	if _, err := f.items.ReviewWorkItems(ctx, ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	}); err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}

	workerID, dispatched := f.registerWorker(t)
	if dispatched == nil || dispatched.WorkID != head.Item.WorkID {
		t.Fatalf("first dispatch = %+v, want the head", dispatched)
	}
	if _, err := f.items.ReportWorkItem(ctx, ReportWorkItemInput{
		WorkID: dispatched.WorkID, LeaseID: dispatched.Lease.LeaseID,
		MaterialID: f.specID, Actor: "agent:w",
	}); err != nil {
		t.Fatalf("report head: %v", err)
	}

	// Snapshot the dependents in the state the approval left them in.
	type cardRow struct {
		state      string
		version    int
		updateTime string
	}
	before := make(map[string]cardRow, len(dependentIDs))
	for _, workID := range dependentIDs {
		view := f.workItemView(t, workID)
		if view.State != store.WorkItemStateQueued {
			t.Fatalf("%s state before acceptance = %q, want queued", workID, view.State)
		}
		before[workID] = cardRow{state: view.State, version: view.Version, updateTime: view.UpdateTime.String()}
	}

	if _, err := f.items.DecideWorkItem(ctx, WorkItemDecisionInput{
		WorkID: head.Item.WorkID, Decision: "approve", Actor: "human:qa",
	}); err != nil {
		t.Fatalf("accept head: %v", err)
	}

	for _, workID := range dependentIDs {
		after := f.workItemView(t, workID)
		was := before[workID]
		if after.State != was.state || after.Version != was.version || after.UpdateTime.String() != was.updateTime {
			t.Errorf("%s after acceptance = %q/v%d/%s, want untouched %q/v%d/%s",
				workID, after.State, after.Version, after.UpdateTime, was.state, was.version, was.updateTime)
		}
	}
	if headView := f.workItemView(t, head.Item.WorkID); headView.State != store.WorkItemStateAccepted {
		t.Errorf("head state after acceptance = %q, want accepted", headView.State)
	}

	// The dependents became claimable without any state write: dispatch, not the
	// acceptance, is what turns "waiting for the dependency" into "in flight".
	next, err := f.claims.Claim(ctx, ClaimInput{WorkerID: workerID})
	if err != nil {
		t.Fatalf("claim after acceptance: %v", err)
	}
	if next == nil || next.WorkID == head.Item.WorkID {
		t.Fatalf("claim after acceptance = %+v, want a dependent card", next)
	}
}
