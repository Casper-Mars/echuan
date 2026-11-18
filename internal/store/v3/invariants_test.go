package v3

import (
	"context"
	"errors"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

func TestCheckInvariantsCleanAfterNormalFlow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "不变式", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Requirement, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_b", Kind: enum.Design, Title: "B",
		DependsOn: []string{"t_a"}, Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}
	claimReportApprove(t, st, "t_a", enum.Requirement)

	if err := CheckInvariants(ctx, st.Read()); err != nil {
		t.Fatalf("CheckInvariants after normal flow: %v", err)
	}
}

func TestCheckInvariantsCatchesStuckPending(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "卡住", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Requirement, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_b", Kind: enum.Design, Title: "B",
		DependsOn: []string{"t_a"}, Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}
	claimReportApprove(t, st, "t_a", enum.Requirement)

	// Simulate a missed promotion: force B back to pending even though A is done.
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE tasks SET status = ? WHERE task_id = 't_b'`, enum.Pending); err != nil {
		t.Fatalf("corrupt task: %v", err)
	}

	err := CheckInvariants(ctx, st.Read())
	var violation *InvariantError
	if !errors.As(err, &violation) {
		t.Fatalf("CheckInvariants = %v, want *InvariantError", err)
	}
	if violation.Name != "pending_with_satisfied_dependencies" || violation.Count != 1 {
		t.Errorf("violation = %+v", violation)
	}
}

func TestCheckInvariantsCatchesClosedProjectWithActiveTask(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "关而不关", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Dev, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Bypass CloseProject's force-cancel path on purpose.
	if _, err := st.Write().ExecContext(ctx,
		`UPDATE projects SET status = ? WHERE project_id = ?`, enum.ProjectClosed, project.ProjectID); err != nil {
		t.Fatalf("close project directly: %v", err)
	}

	err := CheckInvariants(ctx, st.Read())
	var violation *InvariantError
	if !errors.As(err, &violation) {
		t.Fatalf("CheckInvariants = %v, want *InvariantError", err)
	}
	if violation.Name != "closed_project_with_active_task" || violation.Count != 1 {
		t.Errorf("violation = %+v", violation)
	}
}
