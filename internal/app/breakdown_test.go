package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// breakdownFixture is the pipeline parked on tasks_draft with the breakdown
// card leased to workerID: the exact state a breakdown report starts from.
type breakdownFixture struct {
	*phaseFixture
	projectID   string
	workerID    string
	dispatch    *Dispatch
	materialID  string
	breakdownID string
	leaseID     string
}

func newBreakdownFixture(t *testing.T) *breakdownFixture {
	t.Helper()
	f := newPhaseFixture(t)
	detail := f.createProject(t, "拆分项目", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID

	// Create already lands in requirement_review without a stage card, so the
	// pipeline starts at the approval that enqueues the design card.
	if approved := f.approve(t, projectID); approved.Status != domain.StatusSpecDraft {
		t.Fatalf("status = %d, want spec_draft", approved.Status)
	}

	// The idle registration heartbeat leases the design card the approval just
	// enqueued, so it is already the design dispatch the fixture walks next.
	workerID, design := f.register(t, phaseKindDesign, phaseKindBreakdown, "dev")
	if design == nil || design.Kind != phaseKindDesign {
		t.Fatalf("first dispatch = %+v, want design", design)
	}
	spec, _, err := f.env.service.SaveSpec(context.Background(), projectID, "", "agent:cto",
		titlePtr("方案"), "# 方案\n\n正文\n", 0, false)
	if err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := f.report(t, design, spec.Spec.MaterialID); err != nil {
		t.Fatalf("report design: %v", err)
	}
	if approved := f.approve(t, projectID); approved.Status != domain.StatusTasksDraft {
		t.Fatalf("status = %d, want tasks_draft", approved.Status)
	}

	breakdown := f.claim(t, workerID)
	if breakdown.Kind != phaseKindBreakdown {
		t.Fatalf("claimed kind = %q, want breakdown", breakdown.Kind)
	}
	return &breakdownFixture{
		phaseFixture: f,
		projectID:    projectID,
		workerID:     workerID,
		dispatch:     breakdown,
		materialID:   spec.Spec.MaterialID,
	}
}

func (f *breakdownFixture) reportPlan(plan *BreakdownPlan, materialID string) (WorkItemDetail, error) {
	return f.work.ReportWorkItem(context.Background(), ReportWorkItemInput{
		WorkID:     f.dispatch.WorkID,
		LeaseID:    f.dispatch.Lease.LeaseID,
		MaterialID: materialID,
		Plan:       plan,
		Actor:      "agent:w",
	})
}

func (f *breakdownFixture) devItems(t *testing.T) []store.WorkItem {
	t.Helper()
	return f.phaseItems(t, f.projectID, "dev")
}

func (f *breakdownFixture) childCount(t *testing.T) int64 {
	t.Helper()
	breakdown := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown)
	count, err := store.CountWorkItemsByParent(context.Background(), f.env.store.Read(), breakdown.ID)
	if err != nil {
		t.Fatalf("CountWorkItemsByParent: %v", err)
	}
	return count
}

func planOf(items ...BreakdownPlanItem) *BreakdownPlan {
	return &BreakdownPlan{Items: items}
}

func item(seq int, title string, priority int, deps ...int) BreakdownPlanItem {
	return BreakdownPlanItem{SeqNo: seq, Title: title, BodyMD: "# " + title + "\n", Priority: priority, DependsOn: deps}
}

// TestBreakdownPlanCreatesBlockedChildren is acceptance 2 and 3: a legal plan
// generates N dev cards with the breakdown as parent, blocked until the plan
// gate approves them.
func TestBreakdownPlanCreatesBlockedChildren(t *testing.T) {
	f := newBreakdownFixture(t)

	plan := planOf(
		item(1, "实现登录", 30),
		item(2, "登录测试", 20, 1),
		item(3, "登录文档", 10, 1),
	)
	detail, err := f.reportPlan(plan, "")
	if err != nil {
		t.Fatalf("report breakdown with plan: %v", err)
	}
	if detail.Item.State != store.WorkItemStateSubmitted {
		t.Fatalf("breakdown state = %q, want submitted", detail.Item.State)
	}
	if detail.Item.ResultMaterialID == nil {
		t.Fatal("result_material_id = nil, want the kind=4 plan material")
	}
	if detail.Result == nil || detail.Result.MaterialID != *detail.Item.ResultMaterialID {
		t.Fatalf("detail.Result = %+v, want material %q", detail.Result, *detail.Item.ResultMaterialID)
	}
	planMaterial, err := store.GetMaterialByMaterialID(context.Background(), f.env.store.Read(),
		*detail.Item.ResultMaterialID)
	if err != nil {
		t.Fatalf("read plan material: %v", err)
	}
	if planMaterial.Kind != domain.MaterialKindPlan {
		t.Errorf("plan material kind = %d, want %d", planMaterial.Kind, domain.MaterialKindPlan)
	}
	if planMaterial.Title != "拆分计划：拆分项目" {
		t.Errorf("plan material title = %q", planMaterial.Title)
	}
	if planMaterial.UploadedBy != "agent:w" {
		t.Errorf("plan material uploaded_by = %q, want agent:w", planMaterial.UploadedBy)
	}
	if planMaterial.PreviousMaterialID != nil || planMaterial.Version != 1 {
		t.Errorf("plan material version chain = %v/%d, want a new root version 1",
			planMaterial.PreviousMaterialID, planMaterial.Version)
	}

	planBody, err := os.ReadFile(filepath.Join(f.env.dataDir, planMaterial.Path))
	if err != nil {
		t.Fatalf("read plan object: %v", err)
	}
	var document struct {
		Items []struct {
			SeqNo     int    `json:"seq_no"`
			ProjectID string `json:"project_id"`
			Title     string `json:"title"`
			BodyMD    string `json:"body_md"`
			Priority  int    `json:"priority"`
			DependsOn []int  `json:"depends_on"`
		} `json:"items"`
	}
	if err := json.Unmarshal(planBody, &document); err != nil {
		t.Fatalf("plan object is not JSON: %v (%s)", err, planBody)
	}
	if len(document.Items) != 3 {
		t.Fatalf("plan document items = %d, want 3", len(document.Items))
	}
	wantPlanTitles := []string{"实现登录", "登录测试", "登录文档"}
	for i, item := range document.Items {
		if item.SeqNo != i+1 || item.Title != wantPlanTitles[i] {
			t.Errorf("plan item[%d] = %d/%q, want %d/%q",
				i, item.SeqNo, item.Title, i+1, wantPlanTitles[i])
		}
	}
	if got := document.Items[0].DependsOn; got == nil || len(got) != 0 {
		t.Errorf("plan item[0].depends_on = %v, want []", got)
	}
	if got := document.Items[1].DependsOn; len(got) != 1 || got[0] != 1 {
		t.Errorf("plan item[1].depends_on = %v, want [1]", got)
	}
	if state := f.env.projectState(t, f.projectID); state.Status != domain.StatusTasksReview {
		t.Fatalf("project status = %d, want tasks_review", state.Status)
	}

	breakdown := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown)
	if breakdown.State != store.WorkItemStateSubmitted {
		t.Errorf("stored breakdown state = %q, want submitted", breakdown.State)
	}

	devs := f.devItems(t)
	if len(devs) != 3 {
		t.Fatalf("dev cards = %d, want 3", len(devs))
	}
	for i, dev := range devs {
		if dev.Kind != "dev" {
			t.Errorf("dev[%d].kind = %q", i, dev.Kind)
		}
		if dev.State != store.WorkItemStateBlocked {
			t.Errorf("dev[%d].state = %q, want blocked before approval", i, dev.State)
		}
		if dev.ParentID == nil || *dev.ParentID != breakdown.ID {
			t.Errorf("dev[%d].parent_id = %v, want %d", i, dev.ParentID, breakdown.ID)
		}
		if i > 0 && dev.SeqNo != devs[i-1].SeqNo+1 {
			t.Errorf("dev[%d].seq_no = %d, want %d", i, dev.SeqNo, devs[i-1].SeqNo+1)
		}
	}
	if devs[0].SeqNo != breakdown.SeqNo+1 {
		t.Errorf("first dev seq_no = %d, want %d", devs[0].SeqNo, breakdown.SeqNo+1)
	}
	wantTitles := []string{"实现登录", "登录测试", "登录文档"}
	wantPriority := []int{30, 20, 10}
	for i := range devs {
		if devs[i].Title != wantTitles[i] || devs[i].Priority != wantPriority[i] {
			t.Errorf("dev[%d] = %q/%d, want %q/%d", i, devs[i].Title, devs[i].Priority, wantTitles[i], wantPriority[i])
		}
	}

	deps, err := store.ListWorkDependencies(context.Background(), f.env.store.Read(),
		f.env.physicalProjectID(t, f.projectID))
	if err != nil {
		t.Fatalf("ListWorkDependencies: %v", err)
	}
	byOwner := map[int64][]int64{}
	for _, dep := range deps {
		byOwner[dep.WorkItemID] = append(byOwner[dep.WorkItemID], dep.DependsOn)
	}
	if got := byOwner[devs[0].ID]; len(got) != 0 {
		t.Errorf("dev[0] deps = %v, want none", got)
	}
	for _, i := range []int{1, 2} {
		got := byOwner[devs[i].ID]
		if len(got) != 1 || got[0] != devs[0].ID {
			t.Errorf("dev[%d] deps = %v, want [%d]", i, got, devs[0].ID)
		}
	}

	// Blocked cards are invisible to the queue: the idle worker gets nothing.
	if dispatch, err := f.claims.Claim(context.Background(), ClaimInput{
		WorkerID: f.workerID, Actor: "agent:w",
	}); err != nil || dispatch != nil {
		t.Fatalf("claim before approval = (%v, %v), want (nil, nil)", dispatch, err)
	}

	// Approving the plan turns every blocked child queued in one write and
	// settles the breakdown card.
	review, err := f.work.ReviewWorkItems(context.Background(), ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	})
	if err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}
	// Only the head of the dependency graph is released: items 2 and 3 wait for
	// item 1 to be accepted, so they stay blocked.
	if review.ReadyItems != 1 {
		t.Errorf("ready_work_items = %d, want 1 (only the ungated head)", review.ReadyItems)
	}
	if review.ToStatus != domain.StatusPendingDone {
		t.Errorf("project status after review = %d, want pending_done", review.ToStatus)
	}
	if after := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown); after.State != store.WorkItemStateAccepted {
		t.Errorf("breakdown after review = %q, want accepted", after.State)
	}
	for i, dev := range f.devItems(t) {
		want := store.WorkItemStateBlocked
		if i == 0 {
			want = store.WorkItemStateQueued
		}
		if dev.State != want {
			t.Errorf("dev[%d] after review = %q, want %q", i, dev.State, want)
		}
	}

	// Every generated card can be claimed and finished; dependencies force the
	// order (1, then 2 and 3 ordered by priority).
	wantOrder := []string{devs[0].WorkID, devs[1].WorkID, devs[2].WorkID}
	for i, want := range wantOrder {
		dispatch := f.claim(t, f.workerID)
		if dispatch.WorkID != want {
			t.Fatalf("claim %d = %s, want %s", i, dispatch.WorkID, want)
		}
		if _, err := f.report(t, dispatch, f.materialID); err != nil {
			t.Fatalf("report dev %s: %v", want, err)
		}
		if _, err := f.work.DecideWorkItem(context.Background(), WorkItemDecisionInput{
			WorkID: want, Decision: "approve", Actor: "human:cto",
		}); err != nil {
			t.Fatalf("accept dev %s: %v", want, err)
		}
	}
	// Accepting the last card settles the cards, not the project: it stays
	// pending_done until a human completes it (issue #39). The completion path
	// itself is covered by TestCompleteProject* in project_test.go.
	if state := f.env.projectState(t, f.projectID); state.Status != domain.StatusPendingDone {
		t.Errorf("project status = %d, want pending_done after the last acceptance", state.Status)
	}
}

// TestBreakdownPlanMaterialRolledBackOnFailure forces a write failure after the
// kind=4 material has been persisted. The report transaction must roll the row
// back and remove the object file created by this write.
func TestBreakdownPlanMaterialRolledBackOnFailure(t *testing.T) {
	f := newBreakdownFixture(t)
	beforeObjects := countObjectFiles(t, f.env.dataDir)

	// The dependency edge is inserted after the plan material and the children.
	// Failing it therefore exercises the post-write cleanup path.
	if _, err := f.env.store.Write().ExecContext(context.Background(), `
		CREATE TRIGGER fail_work_deps BEFORE INSERT ON work_deps
		BEGIN SELECT RAISE(ABORT, 'forced work_deps failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, err := f.reportPlan(planOf(
		item(1, "实现登录", 30),
		item(2, "登录测试", 20, 1),
	), "")
	if err == nil {
		t.Fatal("reportPlan succeeded, want the forced failure")
	}

	if got := f.env.count(t, "SELECT COUNT(*) FROM materials WHERE kind = ?", domain.MaterialKindPlan); got != 0 {
		t.Errorf("kind=4 materials = %d, want 0 after rollback", got)
	}
	if got := countObjectFiles(t, f.env.dataDir); got != beforeObjects {
		t.Errorf("object files = %d, want %d (plan object must be removed)", got, beforeObjects)
	}
	if got := f.childCount(t); got != 0 {
		t.Errorf("children = %d, want 0 after rollback", got)
	}
	current := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown)
	if current.State != store.WorkItemStateLeased {
		t.Errorf("breakdown state = %q, want leased after rollback", current.State)
	}
	if current.ResultMaterialID != nil {
		t.Errorf("result_material_id = %v, want nil after rollback", *current.ResultMaterialID)
	}
}

// TestBreakdownPlanValidation covers the five documented rejection classes and
// proves each one rolls back completely: same lease, same project, no children.
func TestBreakdownPlanValidation(t *testing.T) {
	cases := []struct {
		name   string
		plan   func(projectID string) *BreakdownPlan
		field  string
		reason string
	}{
		{
			name:   "missing plan",
			plan:   func(string) *BreakdownPlan { return nil },
			field:  "plan",
			reason: "required",
		},
		{
			name:   "empty plan",
			plan:   func(string) *BreakdownPlan { return &BreakdownPlan{} },
			field:  "plan.items",
			reason: "required",
		},
		{
			name:   "no items",
			plan:   func(string) *BreakdownPlan { return &BreakdownPlan{Items: []BreakdownPlanItem{}} },
			field:  "plan.items",
			reason: "at least 1 item",
		},
		{
			name: "dependency cycle",
			plan: func(projectID string) *BreakdownPlan {
				one := item(1, "卡一", 10, 2)
				two := item(2, "卡二", 10, 1)
				one.ProjectID, two.ProjectID = projectID, projectID
				return planOf(one, two)
			},
			field:  "plan.items",
			reason: "dependency cycle: 1 → 2 → 1",
		},
		{
			name: "cross project",
			plan: func(projectID string) *BreakdownPlan {
				one := item(1, "卡一", 10)
				one.ProjectID = "OTHER-1"
				return planOf(one)
			},
			field:  "plan.items[0].project_id",
			reason: "must equal",
		},
		{
			name: "seq gap",
			plan: func(string) *BreakdownPlan {
				return planOf(item(1, "卡一", 10), item(3, "卡三", 10))
			},
			field:  "plan.items[1].seq_no",
			reason: "must be between 1 and 2",
		},
		{
			name: "seq duplicate",
			plan: func(string) *BreakdownPlan {
				return planOf(item(1, "卡一", 10), item(1, "卡二", 10))
			},
			field:  "plan.items[1].seq_no",
			reason: "ascending order",
		},
		{
			name: "seq missing",
			plan: func(string) *BreakdownPlan {
				return planOf(item(0, "卡一", 10))
			},
			field:  "plan.items[0].seq_no",
			reason: "required",
		},
		{
			name: "seq out of range",
			plan: func(string) *BreakdownPlan {
				return planOf(item(1, "卡一", 10), item(5, "卡二", 10))
			},
			field:  "plan.items[1].seq_no",
			reason: "must be between 1 and 2",
		},
		{
			name: "self dependency",
			plan: func(string) *BreakdownPlan {
				return planOf(item(1, "卡一", 10, 1))
			},
			field:  "plan.items[0].depends_on",
			reason: "itself",
		},
		{
			name: "duplicate dependency",
			plan: func(string) *BreakdownPlan {
				return planOf(item(1, "卡一", 10, 2, 2), item(2, "卡二", 10))
			},
			field:  "plan.items[0].depends_on",
			reason: "duplicate",
		},
		{
			name: "dependency out of range",
			plan: func(string) *BreakdownPlan {
				return planOf(item(1, "卡一", 10, 9))
			},
			field:  "plan.items[0].depends_on",
			reason: "must be between 1 and 1",
		},
		{
			name: "priority out of range",
			plan: func(string) *BreakdownPlan {
				bad := item(1, "卡一", 100)
				return planOf(bad)
			},
			field:  "plan.items[0].priority",
			reason: "must be between 0 and 99",
		},
		{
			name: "empty title",
			plan: func(string) *BreakdownPlan {
				bad := item(1, "  ", 10)
				return planOf(bad)
			},
			field:  "plan.items[0].title",
			reason: "required",
		},
		{
			name: "empty body",
			plan: func(string) *BreakdownPlan {
				bad := item(1, "卡一", 10)
				bad.BodyMD = "  "
				return planOf(bad)
			},
			field:  "plan.items[0].body_md",
			reason: "required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBreakdownFixture(t)
			breakdown := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown)
			before := f.env.projectState(t, f.projectID)

			_, err := f.reportPlan(tc.plan(f.projectID), "")
			if !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatalf("err = %v, want invalid_argument", err)
			}
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %T, want *domain.ValidationError", err)
			}
			if !hasFieldReason(verr, tc.field, tc.reason) {
				t.Fatalf("fields = %+v, want %q containing %q", verr.Fields, tc.field, tc.reason)
			}

			after := f.env.projectState(t, f.projectID)
			if after.Status != before.Status || after.Version != before.Version {
				t.Errorf("project = %+v, want unchanged %+v", after, before)
			}
			current := f.singlePhaseItem(t, f.projectID, phaseKindBreakdown)
			if current.State != store.WorkItemStateLeased {
				t.Errorf("breakdown state = %q, want leased after rollback", current.State)
			}
			if current.LeaseID == nil || *current.LeaseID != *breakdown.LeaseID {
				t.Errorf("lease_id = %v, want unchanged %v", current.LeaseID, breakdown.LeaseID)
			}
			if current.ResultMaterialID != nil {
				t.Errorf("result_material_id = %v, want nil", *current.ResultMaterialID)
			}
			if count := f.childCount(t); count != 0 {
				t.Errorf("children = %d, want 0 after rollback", count)
			}
		})
	}
}

// TestBreakdownPlanRejectedOnNonBreakdownCard keeps the plan member exclusive
// to breakdown: a dev report that carries one is a 400, not a silent ignore.
func TestBreakdownPlanRejectedOnNonBreakdownCard(t *testing.T) {
	f := newBreakdownFixture(t)

	// Create a dev card while the breakdown lease is held, then dispatch it to
	// a second worker so both cards can be reported independently.
	created, err := f.work.CreateWorkItem(context.Background(), CreateWorkItemInput{
		ProjectID: f.projectID, Kind: "dev", Title: "开发卡", BodyMD: "# 开发\n", Priority: 1,
	}, "human:cto")
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	// Manual cards land blocked by design; put this one on the queue so a
	// second worker can lease it and exercise the report path.
	if _, err := f.env.store.Write().ExecContext(context.Background(),
		`UPDATE work_items SET state = ? WHERE work_id = ?`,
		store.WorkItemStateQueued, created.Item.WorkID); err != nil {
		t.Fatalf("queue dev card: %v", err)
	}
	secondWorker, dispatch := f.register(t, "dev")
	if dispatch == nil {
		t.Fatal("no dev dispatch for the second worker")
	}
	if dispatch.Kind != "dev" {
		t.Fatalf("dispatch kind = %q, want dev", dispatch.Kind)
	}

	_, err = f.work.ReportWorkItem(context.Background(), ReportWorkItemInput{
		WorkID:     dispatch.WorkID,
		LeaseID:    dispatch.Lease.LeaseID,
		MaterialID: f.materialID,
		Plan:       planOf(item(1, "多余的计划", 10)),
		Actor:      "agent:w2",
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid_argument", err)
	}
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || !hasFieldReason(verr, "plan", "not allowed") {
		t.Fatalf("fields = %+v, want plan/not allowed", verr)
	}
	if item, err := store.GetWorkItemByWorkID(context.Background(), f.env.store.Read(), dispatch.WorkID); err != nil {
		t.Fatalf("GetWorkItemByWorkID: %v", err)
	} else if item.State != store.WorkItemStateLeased {
		t.Errorf("dev state = %q, want leased after rejection", item.State)
	}
	_ = secondWorker
}

// TestBreakdownDuplicateSubmissionIsConflict is acceptance 5: replaying the
// report of the same breakdown card is a 409 and creates nothing.
func TestBreakdownDuplicateSubmissionIsConflict(t *testing.T) {
	f := newBreakdownFixture(t)
	plan := planOf(item(1, "实现登录", 10), item(2, "登录测试", 5, 1))

	if _, err := f.reportPlan(plan, ""); err != nil {
		t.Fatalf("first report: %v", err)
	}
	before := f.env.projectState(t, f.projectID)
	children := f.childCount(t)
	if children != 2 {
		t.Fatalf("children after first report = %d, want 2", children)
	}

	_, err := f.reportPlan(plan, "")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second report = %v, want conflict", err)
	}
	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != "plan already submitted for breakdown item" {
		t.Fatalf("reason = %v, want the duplicate-plan conflict", err)
	}

	after := f.env.projectState(t, f.projectID)
	if after.Status != before.Status || after.Version != before.Version {
		t.Errorf("project = %+v, want unchanged %+v", after, before)
	}
	if count := f.childCount(t); count != children {
		t.Errorf("children = %d, want %d (no duplicate batch)", count, children)
	}
}

// hasField reports whether one field error matches field and contains reason.
func hasFieldReason(err *domain.ValidationError, field, reason string) bool {
	if err == nil {
		return false
	}
	for _, item := range err.Fields {
		if item.Field == field && strings.Contains(item.Reason, reason) {
			return true
		}
		if field == "plan.items" && item.Field == "plan.items" && strings.Contains(err.Message, reason) {
			return true
		}
	}
	return false
}
