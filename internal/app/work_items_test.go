package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// workItemFixture is the common setup of the work-item tests: a project in
// tasks_draft, the services wired the way main.go wires them, and the material
// the reports point at.
type workItemFixture struct {
	env        *projectTestEnv
	items      *WorkItemService
	claims     *ClaimService
	workers    *WorkerService
	projectID  string
	specID     string
	specSHA256 string
}

func newWorkItemFixture(t *testing.T) *workItemFixture {
	t.Helper()
	env := newProjectTestEnv(t)

	projectID := env.tasksDraftProject(t, "调度项目")
	var specMaterialID, specSHA string
	if err := env.store.Read().QueryRowContext(context.Background(), `
		SELECT m.material_id, m.sha256
		  FROM projects p JOIN materials m ON m.id = p.spec_material_id
		 WHERE p.project_id = ?`, projectID).Scan(&specMaterialID, &specSHA); err != nil {
		t.Fatalf("read spec material: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	items := NewWorkItemService(env.store)
	claims := NewClaimService(env.store, logger, 60*time.Second)
	workers := NewWorkerService(env.store, logger, WorkerServiceOptions{
		LeaseTTL:          60 * time.Second,
		WorkerTTL:         time.Minute,
		HeartbeatInterval: 15 * time.Second,
		MetadataMaxBytes:  4096,
	})
	return &workItemFixture{
		env:        env,
		items:      items,
		claims:     claims,
		workers:    workers,
		projectID:  projectID,
		specID:     specMaterialID,
		specSHA256: specSHA,
	}
}

// tasksDraftProject walks requirement and spec approvals, leaving the project
// in tasks_draft with one spec material.
func (e *projectTestEnv) tasksDraftProject(t *testing.T, title string) string {
	t.Helper()
	projectID := e.newSpecReviewProject(t, title)

	if _, _, err := e.service.SaveSpec(context.Background(), projectID, "", "agent:cto",
		titlePtr("方案"), "# 方案\n", 0, false); err != nil {
		t.Fatalf("SaveSpec: %v", err)
	}
	if _, err := e.service.SubmitProject(context.Background(), projectID, "agent:cto"); err != nil {
		t.Fatalf("SubmitProject(spec): %v", err)
	}
	version := e.projectState(t, projectID).Version
	if _, err := e.service.Decide(context.Background(), DecideInput{
		Kind:          DecideKindProject,
		ID:            projectID,
		Decision:      domain.DecisionApprove,
		ExpectVersion: &version,
		Actor:         "human:cto",
	}); err != nil {
		t.Fatalf("Decide(approve spec): %v", err)
	}
	if state := e.projectState(t, projectID); state.Status != domain.StatusTasksDraft {
		t.Fatalf("project status = %d, want tasks_draft", state.Status)
	}
	return projectID
}

func (f *workItemFixture) createItem(t *testing.T, kind, title string, priority int) WorkItemDetail {
	t.Helper()
	detail, err := f.items.CreateWorkItem(context.Background(), CreateWorkItemInput{
		ProjectID: f.projectID,
		Kind:      kind,
		Title:     title,
		BodyMD:    "# " + title + "\n",
		Priority:  priority,
	}, "human:cto")
	if err != nil {
		t.Fatalf("CreateWorkItem(%s): %v", title, err)
	}
	return detail
}

func TestCreateWorkItemLandsBlockedAndOpensReviewGate(t *testing.T) {
	f := newWorkItemFixture(t)

	detail := f.createItem(t, "dev", "第一张卡", 0)
	if detail.Item.State != store.WorkItemStateBlocked {
		t.Errorf("state = %q, want blocked", detail.Item.State)
	}
	if detail.Item.Attempt != 0 || detail.Item.MaxAttempts != defaultWorkItemMaxAttempts {
		t.Errorf("attempt/max = %d/%d", detail.Item.Attempt, detail.Item.MaxAttempts)
	}
	// The requirement, design and breakdown stage cards already took seq 1–3
	// while the fixture walked to tasks_draft; the first dev card is 4.
	if detail.Item.SeqNo != 4 {
		t.Errorf("seq_no = %d, want 4", detail.Item.SeqNo)
	}
	if detail.Item.Kind != "dev" || detail.Item.ProjectID != f.projectID {
		t.Errorf("kind/project = %q/%q", detail.Item.Kind, detail.Item.ProjectID)
	}

	// The first dev card opens the batch review gate in the same transaction.
	if state := f.env.projectState(t, f.projectID); state.Status != domain.StatusTasksReview {
		t.Errorf("project status = %d, want tasks_review", state.Status)
	}

	// Non-dev cards do not open the gate.
	other := f.createItem(t, "design", "设计卡", 0)
	if other.Item.State != store.WorkItemStateBlocked {
		t.Errorf("design state = %q, want blocked", other.Item.State)
	}
}

func TestCreateWorkItemValidation(t *testing.T) {
	f := newWorkItemFixture(t)

	cases := []struct {
		name  string
		input CreateWorkItemInput
		field string
	}{
		{"missing kind", CreateWorkItemInput{ProjectID: f.projectID, Title: "t", BodyMD: "b"}, "kind"},
		{"bad kind", CreateWorkItemInput{ProjectID: f.projectID, Kind: "epic", Title: "t", BodyMD: "b"}, "kind"},
		{"missing title", CreateWorkItemInput{ProjectID: f.projectID, Kind: "dev", BodyMD: "b"}, "title"},
		{"missing body", CreateWorkItemInput{ProjectID: f.projectID, Kind: "dev", Title: "t"}, "body_md"},
		{"priority high", CreateWorkItemInput{ProjectID: f.projectID, Kind: "dev", Title: "t", BodyMD: "b", Priority: 1001}, "priority"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.items.CreateWorkItem(context.Background(), tc.input, "human:cto")
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error = %T, want *domain.ValidationError", err)
			}
			if !hasField(verr, tc.field) {
				t.Errorf("details = %+v, want field %q", verr.Fields, tc.field)
			}
		})
	}

	// An unknown dependency is a 400 naming depends_on, not a 404.
	_, err := f.items.CreateWorkItem(context.Background(), CreateWorkItemInput{
		ProjectID: f.projectID, Kind: "dev", Title: "t", BodyMD: "b",
		DependsOn: []string{"w_missing"},
	}, "human:cto")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || !hasField(verr, "depends_on") {
		t.Fatalf("error = %v, want a depends_on validation error", err)
	}
}

func TestUpdateAndDeleteWorkItem(t *testing.T) {
	f := newWorkItemFixture(t)
	detail := f.createItem(t, "dev", "原标题", 0)

	newTitle := "新标题"
	updated, err := f.items.UpdateWorkItem(context.Background(), detail.Item.WorkID,
		UpdateWorkItemInput{Title: &newTitle}, "human:cto")
	if err != nil {
		t.Fatalf("UpdateWorkItem: %v", err)
	}
	if updated.Item.Title != newTitle || updated.Item.Version != detail.Item.Version+1 {
		t.Errorf("title/version = %q/%d", updated.Item.Title, updated.Item.Version)
	}

	if err := f.items.DeleteWorkItem(context.Background(), detail.Item.WorkID, "human:cto"); err != nil {
		t.Fatalf("DeleteWorkItem: %v", err)
	}
	if _, err := f.items.GetWorkItem(context.Background(), detail.Item.WorkID); !isNotFound(err) {
		t.Errorf("GetWorkItem after delete = %v, want not found", err)
	}

	// A card that was claimed once is not deletable, even when it is blocked.
	kept := f.createItem(t, "dev", "被领过", 0)
	if _, err := f.env.store.Write().ExecContext(context.Background(),
		`UPDATE work_items SET attempt = 1 WHERE work_id = ?`, kept.Item.WorkID); err != nil {
		t.Fatalf("bump attempt: %v", err)
	}
	var conflict *domain.ConflictError
	if err := f.items.DeleteWorkItem(context.Background(), kept.Item.WorkID, "human:cto"); !errors.As(err, &conflict) {
		t.Errorf("delete claimed card = %v, want conflict", err)
	}
}

func TestHeartbeatRequiresKinds(t *testing.T) {
	f := newWorkItemFixture(t)

	cases := []struct {
		name  string
		kinds []string
	}{
		{"missing", nil},
		{"empty", []string{}},
		{"illegal", []string{"merge"}},
		{"blank", []string{" "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.workers.Heartbeat(context.Background(), HeartbeatInput{
				WorkerID: "", State: "idle", Kinds: tc.kinds,
			})
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error = %T, want validation", err)
			}
			if !hasField(verr, "kinds") {
				t.Errorf("details = %+v, want field kinds", verr.Fields)
			}
		})
	}
}

func TestHeartbeatKindsReplaceAndGateDispatch(t *testing.T) {
	f := newWorkItemFixture(t)
	first := f.createItem(t, "dev", "高优先级", 10)
	f.createItem(t, "dev", "低优先级", 1)
	if _, err := f.items.ReviewWorkItems(context.Background(), ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	}); err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}

	// A design-only worker sees an empty queue even though two dev cards wait.
	result, err := f.workers.Heartbeat(context.Background(), HeartbeatInput{
		Kinds: []string{"design"}, Actor: "agent:w",
	})
	if err != nil {
		t.Fatalf("Heartbeat(register): %v", err)
	}
	workerID := result.Worker.WorkerID
	if workerID == "" || workerID[:2] != "w_" {
		t.Fatalf("worker_id = %q, want w_ prefix", workerID)
	}
	if len(result.Commands) != 0 {
		t.Fatalf("commands = %d, want 0", len(result.Commands))
	}

	claim, err := f.claims.Claim(context.Background(), ClaimInput{WorkerID: workerID})
	if err != nil {
		t.Fatalf("Claim(design): %v", err)
	}
	if claim != nil {
		t.Fatalf("claim = %+v, want nil for undeclared kind", claim)
	}

	// Re-declaring dev replaces the row wholesale and dispatches the best card.
	second, err := f.workers.Heartbeat(context.Background(), HeartbeatInput{
		WorkerID: workerID, State: "idle", Kinds: []string{"dev", "dev"}, Actor: "agent:w",
	})
	if err != nil {
		t.Fatalf("Heartbeat(dev): %v", err)
	}
	if got := second.Worker.Kinds; len(got) != 1 || got[0] != "dev" {
		t.Fatalf("kinds = %v, want [dev]", got)
	}
	if len(second.Commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(second.Commands))
	}

	command := second.Commands[0]
	if command.Kind != CommandDispatch || command.Dispatch == nil {
		t.Fatalf("command = %+v, want dispatch", command)
	}
	dispatch := command.Dispatch
	if dispatch.Protocol != DispatchProtocol {
		t.Errorf("protocol = %q", dispatch.Protocol)
	}
	if dispatch.WorkID != first.Item.WorkID || dispatch.Kind != "dev" {
		t.Errorf("dispatch = %s/%s, want %s/dev", dispatch.WorkID, dispatch.Kind, first.Item.WorkID)
	}
	if len(dispatch.Inputs) != 0 {
		t.Errorf("inputs = %v, want empty", dispatch.Inputs)
	}
	if dispatch.WorkItem.Attempt != 1 || dispatch.WorkItem.ProjectID != f.projectID {
		t.Errorf("work item = %+v", dispatch.WorkItem)
	}
	if dispatch.Lease.LeaseID == "" || !dispatch.Lease.Until.After(time.Now()) {
		t.Errorf("lease = %+v", dispatch.Lease)
	}
	if dispatch.Context.Product.ID != "APP" || dispatch.Context.SpecVersion != 1 {
		t.Errorf("context = %+v", dispatch.Context)
	}
	if dispatch.Context.SpecDigest != "sha256:"+f.specSHA256 {
		t.Errorf("spec_digest = %q", dispatch.Context.SpecDigest)
	}

	// The DB declaration matches the request, not a merge of old and new.
	stored, err := store.ListWorkerKinds(context.Background(), f.env.store.Read(), workerID)
	if err != nil {
		t.Fatalf("ListWorkerKinds: %v", err)
	}
	if len(stored) != 1 || stored[0] != "dev" {
		t.Errorf("stored kinds = %v, want [dev]", stored)
	}

	// A busy worker is not given a second card.
	again, err := f.workers.Heartbeat(context.Background(), HeartbeatInput{
		WorkerID: workerID, State: "idle", Kinds: []string{"dev"},
	})
	if err != nil {
		t.Fatalf("Heartbeat(busy): %v", err)
	}
	if len(again.Commands) != 0 {
		t.Errorf("commands while holding a card = %d, want 0", len(again.Commands))
	}
}

func TestLeaseRenewalAndLoss(t *testing.T) {
	f := newWorkItemFixture(t)
	f.createItem(t, "dev", "租约卡", 0)
	if _, err := f.items.ReviewWorkItems(context.Background(), ReviewWorkItemsInput{
		ProjectID: f.projectID, Decision: domain.DecisionApprove, Actor: "human:cto",
	}); err != nil {
		t.Fatalf("ReviewWorkItems: %v", err)
	}

	workerID, dispatch := f.registerWorker(t)
	if dispatch == nil {
		t.Fatalf("registration heartbeat did not dispatch the queued card")
	}
	_ = workerID

	renewal, err := f.claims.RenewLease(context.Background(), dispatch.WorkID, dispatch.Lease.LeaseID)
	if err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if !renewal.Until.After(time.Now()) || renewal.TTL <= 0 {
		t.Errorf("renewal = %+v", renewal)
	}

	var lost *domain.LeaseLostError
	if _, err := f.claims.RenewLease(context.Background(), dispatch.WorkID, "l_wrong"); !errors.As(err, &lost) {
		t.Errorf("wrong lease = %v, want lease_lost", err)
	}

	// After a report the lease is gone for good.
	if _, err := f.items.ReportWorkItem(context.Background(), ReportWorkItemInput{
		WorkID: dispatch.WorkID, LeaseID: dispatch.Lease.LeaseID, MaterialID: f.specID, Actor: "agent:w",
	}); err != nil {
		t.Fatalf("ReportWorkItem: %v", err)
	}
	if _, err := f.claims.RenewLease(context.Background(), dispatch.WorkID, dispatch.Lease.LeaseID); !errors.As(err, &lost) {
		t.Errorf("renew after report = %v, want lease_lost", err)
	}
}

func TestRejectKeepsAttempt(t *testing.T) {
	f := newWorkItemFixture(t)
	detail := f.createItem(t, "dev", "驳回卡", 0)
	if _, err := f.env.store.Write().ExecContext(context.Background(), `
		UPDATE work_items SET state = ?, attempt = 2 WHERE work_id = ?`,
		store.WorkItemStateSubmitted, detail.Item.WorkID); err != nil {
		t.Fatalf("seed submitted: %v", err)
	}

	result, err := f.items.DecideWorkItem(context.Background(), WorkItemDecisionInput{
		WorkID: detail.Item.WorkID, Decision: "reject", Reason: "补测试", Actor: "human:qa",
	})
	if err != nil {
		t.Fatalf("DecideWorkItem(reject): %v", err)
	}
	if result.ToState != store.WorkItemStateQueued {
		t.Errorf("to_state = %q, want queued", result.ToState)
	}
	if result.Detail.Item.Attempt != 2 {
		t.Errorf("attempt = %d, want 2 (reject keeps it)", result.Detail.Item.Attempt)
	}
	if result.Detail.Item.Reason != "补测试" {
		t.Errorf("reason = %q", result.Detail.Item.Reason)
	}
}

func TestClaimRejectsUnknownWorker(t *testing.T) {
	f := newWorkItemFixture(t)
	var unknown *domain.WorkerUnknownError
	if _, err := f.claims.Claim(context.Background(), ClaimInput{WorkerID: "w_unknown"}); !errors.As(err, &unknown) {
		t.Errorf("Claim = %v, want worker_unknown", err)
	}
}

// registerWorker registers a dev worker and returns its id plus the dispatch
// the registration heartbeat produced (nil when the queue was empty).
func (f *workItemFixture) registerWorker(t *testing.T) (string, *Dispatch) {
	t.Helper()
	result, err := f.workers.Heartbeat(context.Background(), HeartbeatInput{
		Kinds: []string{"dev"}, Actor: "agent:w",
	})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if len(result.Commands) == 0 {
		return result.Worker.WorkerID, nil
	}
	return result.Worker.WorkerID, result.Commands[0].Dispatch
}

// hasField reports whether a validation error names one field.
func hasField(err *domain.ValidationError, field string) bool {
	for _, detail := range err.Fields {
		if detail.Field == field {
			return true
		}
	}
	return false
}

// isNotFound reports whether err is the store's not-found sentinel.
func isNotFound(err error) bool {
	var notFound *domain.NotFoundError
	return errors.As(err, &notFound) || errors.Is(err, sql.ErrNoRows) || store.IsNotFound(err)
}
