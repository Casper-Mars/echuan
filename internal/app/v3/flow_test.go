package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

type fakeReader struct {
	bodies map[string][]byte
	// fallback is served for every material the map does not know. It lets a
	// test drive a whole chain through the real flow without registering the
	// throwaway report material of every intermediate card.
	fallback []byte
}

func (f fakeReader) Read(_ context.Context, materialID string) ([]byte, error) {
	body, ok := f.bodies[materialID]
	if ok {
		return body, nil
	}
	if f.fallback != nil {
		return f.fallback, nil
	}
	return nil, fmt.Errorf("material %s not found", materialID)
}

func planBody(items ...planItem) []byte {
	body, err := json.Marshal(planDocument{Items: items})
	if err != nil {
		panic(err)
	}
	return body
}

// approveRequirement drives one requirement task to done.
func approveRequirement(t *testing.T, svc *Service, st *storedb.Store, projectID string) {
	t.Helper()
	ctx := context.Background()
	seedWorker(t, st, "w_req")
	if _, err := svc.CreateTask(ctx, CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_req", Kind: enum.Requirement, Title: "需求",
	}); err != nil {
		t.Fatalf("CreateTask requirement: %v", err)
	}
	materialID := createMaterial(t, svc, "m_req")
	task, err := svc.Claim(ctx, "w_req", enum.Requirement)
	if err != nil {
		t.Fatalf("Claim requirement: %v", err)
	}
	if _, err := svc.Report(ctx, "t_req", *task.LeaseID, materialID); err != nil {
		t.Fatalf("Report requirement: %v", err)
	}
	if _, err := svc.Decide(ctx, DecideRequest{
		TaskID: "t_req", Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide requirement: %v", err)
	}
}

func listKind(t *testing.T, svc *Service, projectID string, kind enum.TaskKind) []storev3.Task {
	t.Helper()
	tasks, err := svc.ListTasks(context.Background(), storev3.TaskFilter{ProjectID: projectID, Kind: &kind})
	if err != nil {
		t.Fatalf("ListTasks %s: %v", kind, err)
	}
	return tasks
}

// TestFlowDisabled covers AC1: the switch is per stage, so a stage that omits
// derive.auto derives nothing even when its target is fully configured.
func TestFlowDisabled(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithFlow(flowFromYAML(t, `flow:
  stages:
    - { id: 1, name: requirement, material: requirement, derive: { to: design, mode: single } }
    - { id: 2, name: design, material: design, title: "技术方案", task: "", derive: { mode: none } }
`))
	projectID := seedProject(t, st, "APP")
	approveRequirement(t, svc, st, projectID)

	if tasks := listKind(t, svc, projectID, enum.Design); len(tasks) != 0 {
		t.Fatalf("design tasks = %d, want 0 when the stage switch is absent", len(tasks))
	}
}

func TestRequirementCreatesDesign(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	approveRequirement(t, svc, st, projectID)

	designs := listKind(t, svc, projectID, enum.Design)
	if len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs))
	}
	design := designs[0]
	if design.Status != enum.Ready {
		t.Errorf("design status = %s, want ready", design.Status)
	}
	deps, err := storev3.ListDependencies(context.Background(), st.Read(), design.TaskID)
	if err != nil {
		t.Fatalf("ListDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0] != "t_req" {
		t.Errorf("deps = %v, want [t_req]", deps)
	}

	events, err := svc.ListEvents(context.Background(), design.TaskID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Event == enum.EventFlowAutoCreate {
			found = true
		}
	}
	if !found {
		t.Error("flow.auto_create event missing on design task")
	}
}

func TestEnsureDoesNotDuplicate(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")

	// Pre-create an active design task: the ensure rule must reuse it.
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_design_existing", Kind: enum.Design, Title: "已有方案",
	}); err != nil {
		t.Fatalf("CreateTask design: %v", err)
	}
	approveRequirement(t, svc, st, projectID)

	if designs := listKind(t, svc, projectID, enum.Design); len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1 (ensure must reuse)", len(designs))
	}
}

func TestDoubleApproveDoesNotDuplicate(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	approveRequirement(t, svc, st, projectID)

	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_req", Decision: enum.Approve, DecidedBy: "cto",
	}); StatusOf(err) != 409 {
		t.Fatalf("second approve = %d (%v), want 409", StatusOf(err), err)
	}
	if designs := listKind(t, svc, projectID, enum.Design); len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs))
	}
}

func TestDesignCreatesBreakdown(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_design", Kind: enum.Design, Title: "方案",
	}); err != nil {
		t.Fatalf("CreateTask design: %v", err)
	}
	materialID := createMaterial(t, svc, "m_design")
	seedWorker(t, st, "w_design")
	task, err := svc.Claim(context.Background(), "w_design", enum.Design)
	if err != nil {
		t.Fatalf("Claim design: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_design", *task.LeaseID, materialID); err != nil {
		t.Fatalf("Report design: %v", err)
	}
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_design", Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide design: %v", err)
	}

	breaks := listKind(t, svc, projectID, enum.Breakdown)
	if len(breaks) != 1 {
		t.Fatalf("breakdown tasks = %d, want 1", len(breaks))
	}
}

func TestBreakdownMaterializesPlan(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithMaterialReader(fakeReader{bodies: map[string][]byte{
		"m_plan": planBody(
			planItem{SeqNo: 1, Title: "A", BodyMD: "a", Priority: 1},
			planItem{SeqNo: 2, Title: "B", BodyMD: "b", DependsOn: []int{1}},
			planItem{SeqNo: 3, Title: "C", BodyMD: "c", DependsOn: []int{1}},
		),
	}})
	projectID := seedProject(t, st, "APP")
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_break", Kind: enum.Breakdown, Title: "拆分",
	}); err != nil {
		t.Fatalf("CreateTask breakdown: %v", err)
	}

	planID, err := svc.CreateMaterial(context.Background(), CreateMaterialRequest{
		MaterialID: "m_plan", Kind: enum.MaterialPlan, Version: 1, Title: "方案",
		Sha256: "sha", Size: 1, Path: "/tmp/m_plan", UploadedBy: "cto",
	})
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}
	seedWorker(t, st, "w_break")
	task, err := svc.Claim(context.Background(), "w_break", enum.Breakdown)
	if err != nil {
		t.Fatalf("Claim breakdown: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_break", *task.LeaseID, planID); err != nil {
		t.Fatalf("Report breakdown: %v", err)
	}
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_break", Decision: enum.Approve, MaterialID: planID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide breakdown: %v", err)
	}

	devs := listKind(t, svc, projectID, enum.Dev)
	if len(devs) != 3 {
		t.Fatalf("dev tasks = %d, want 3", len(devs))
	}
	byTitle := map[string]storev3.Task{}
	for _, dev := range devs {
		byTitle[dev.Title] = dev
	}
	if byTitle["A"].Status != enum.Ready {
		t.Errorf("A status = %s, want ready", byTitle["A"].Status)
	}
	if byTitle["B"].Status != enum.Pending {
		t.Errorf("B status = %s, want pending", byTitle["B"].Status)
	}
	if byTitle["C"].Status != enum.Pending {
		t.Errorf("C status = %s, want pending", byTitle["C"].Status)
	}

	depsB, err := storev3.ListDependencies(context.Background(), st.Read(), byTitle["B"].TaskID)
	if err != nil {
		t.Fatalf("ListDependencies B: %v", err)
	}
	if len(depsB) != 2 || !containsString(depsB, "t_break") || !containsString(depsB, byTitle["A"].TaskID) {
		t.Errorf("B deps = %v, want t_break + A", depsB)
	}

	for _, dev := range devs {
		events, err := svc.ListEvents(context.Background(), dev.TaskID, 100)
		if err != nil {
			t.Fatalf("ListEvents %s: %v", dev.TaskID, err)
		}
		found := false
		for _, event := range events {
			if event.Event == enum.EventFlowAutoCreate {
				found = true
			}
		}
		if !found {
			t.Errorf("flow.auto_create missing on dev %s", dev.TaskID)
		}
	}
}

func TestBreakdownInvalidPlanRollsBack(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithMaterialReader(fakeReader{bodies: map[string][]byte{
		"m_plan": planBody(planItem{SeqNo: 2, Title: "B", DependsOn: []int{9}}),
	}})
	projectID := seedProject(t, st, "APP")
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_break", Kind: enum.Breakdown, Title: "拆分",
	}); err != nil {
		t.Fatalf("CreateTask breakdown: %v", err)
	}
	planID, err := svc.CreateMaterial(context.Background(), CreateMaterialRequest{
		MaterialID: "m_plan", Kind: enum.MaterialPlan, Version: 1, Title: "方案",
		Sha256: "sha", Size: 1, Path: "/tmp/m_plan", UploadedBy: "cto",
	})
	if err != nil {
		t.Fatalf("CreateMaterial: %v", err)
	}
	seedWorker(t, st, "w_break")
	task, err := svc.Claim(context.Background(), "w_break", enum.Breakdown)
	if err != nil {
		t.Fatalf("Claim breakdown: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_break", *task.LeaseID, planID); err != nil {
		t.Fatalf("Report breakdown: %v", err)
	}

	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_break", Decision: enum.Approve, MaterialID: planID, DecidedBy: "cto",
	}); StatusOf(err) != 409 {
		t.Fatalf("invalid plan approve = %d (%v), want 409", StatusOf(err), err)
	}

	after, err := svc.GetTask(context.Background(), "t_break")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != enum.InReview {
		t.Errorf("breakdown status = %s, want in_review (rollback)", after.Status)
	}
	if devs := listKind(t, svc, projectID, enum.Dev); len(devs) != 0 {
		t.Errorf("dev tasks = %d, want 0 after rollback", len(devs))
	}
}

func TestDevApprovalCreatesNothing(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_dev", enum.Dev)
	materialID := createMaterial(t, svc, "m_dev")
	seedWorker(t, st, "w_dev")
	task, err := svc.Claim(context.Background(), "w_dev", enum.Dev)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_dev", *task.LeaseID, materialID); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_dev", Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	all, err := svc.ListTasks(context.Background(), storev3.TaskFilter{ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("tasks = %d, want 1 (dev approval must not derive)", len(all))
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
