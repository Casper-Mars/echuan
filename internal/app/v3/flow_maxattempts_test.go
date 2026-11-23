package v3

import (
	"context"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// TestFlowDerivedTasksInheritMaxAttempts walks one full approval chain
// (requirement → design → breakdown → dev) on a service configured with 7 and
// checks every derived task, not just the explicitly created one. 7 is used on
// purpose: a regression that leaves the flow hooks on the store default would
// show 3.
func TestFlowDerivedTasksInheritMaxAttempts(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	svc.WithMaxAttempts(7)
	svc.WithMaterialReader(fakeReader{bodies: map[string][]byte{
		"m_plan": planBody(
			planItem{SeqNo: 1, Title: "A", BodyMD: "a", Priority: 1},
			planItem{SeqNo: 2, Title: "B", BodyMD: "b", DependsOn: []int{1}},
		),
	}})
	ctx := context.Background()
	projectID := seedProject(t, st, "APP")

	// requirement --approve--> design
	approveRequirement(t, svc, projectID)
	requirement, err := svc.GetTask(ctx, "t_req")
	if err != nil {
		t.Fatalf("GetTask requirement: %v", err)
	}
	if requirement.MaxAttempts != 7 {
		t.Errorf("requirement MaxAttempts = %d, want 7", requirement.MaxAttempts)
	}
	designs := listKind(t, svc, projectID, enum.Design)
	if len(designs) != 1 {
		t.Fatalf("design tasks = %d, want 1", len(designs))
	}
	if designs[0].MaxAttempts != 7 {
		t.Errorf("design MaxAttempts = %d, want 7", designs[0].MaxAttempts)
	}

	// design --approve--> breakdown
	designMaterial := createMaterial(t, svc, "m_design")
	design, err := svc.Claim(ctx, "w_design", enum.Design)
	if err != nil {
		t.Fatalf("Claim design: %v", err)
	}
	if _, err := svc.Report(ctx, design.TaskID, *design.LeaseID, designMaterial); err != nil {
		t.Fatalf("Report design: %v", err)
	}
	if _, err := svc.Decide(ctx, DecideRequest{
		TaskID: design.TaskID, Decision: enum.Approve, MaterialID: designMaterial, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide design: %v", err)
	}
	breaks := listKind(t, svc, projectID, enum.Breakdown)
	if len(breaks) != 1 {
		t.Fatalf("breakdown tasks = %d, want 1", len(breaks))
	}
	if breaks[0].MaxAttempts != 7 {
		t.Errorf("breakdown MaxAttempts = %d, want 7", breaks[0].MaxAttempts)
	}

	// breakdown --approve--> dev children
	planID, err := svc.CreateMaterial(ctx, CreateMaterialRequest{
		MaterialID: "m_plan", Kind: enum.MaterialPlan, Version: 1, Title: "方案",
		Sha256: "sha", Size: 1, Path: "/tmp/m_plan", UploadedBy: "cto",
	})
	if err != nil {
		t.Fatalf("CreateMaterial plan: %v", err)
	}
	breakdown, err := svc.Claim(ctx, "w_break", enum.Breakdown)
	if err != nil {
		t.Fatalf("Claim breakdown: %v", err)
	}
	if _, err := svc.Report(ctx, breakdown.TaskID, *breakdown.LeaseID, planID); err != nil {
		t.Fatalf("Report breakdown: %v", err)
	}
	if _, err := svc.Decide(ctx, DecideRequest{
		TaskID: breakdown.TaskID, Decision: enum.Approve, MaterialID: planID, DecidedBy: "cto",
	}); err != nil {
		t.Fatalf("Decide breakdown: %v", err)
	}
	devs := listKind(t, svc, projectID, enum.Dev)
	if len(devs) != 2 {
		t.Fatalf("dev tasks = %d, want 2", len(devs))
	}
	for _, dev := range devs {
		if dev.MaxAttempts != 7 {
			t.Errorf("dev %s MaxAttempts = %d, want 7", dev.Title, dev.MaxAttempts)
		}
	}
}
