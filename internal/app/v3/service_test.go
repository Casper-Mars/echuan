package v3

import (
	"context"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

func TestCreateTaskBoundaries(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_existing", enum.Dev)

	cases := []struct {
		name   string
		req    CreateTaskRequest
		status int
	}{
		{
			name:   "missing title",
			req:    CreateTaskRequest{ProjectID: projectID, TaskID: "t_1", Kind: enum.Dev},
			status: 400,
		},
		{
			// Any positive id is a legal stage since 0003 (the vocabulary belongs
			// to the flow configuration); only 0 is still rejected.
			name:   "invalid kind",
			req:    CreateTaskRequest{ProjectID: projectID, TaskID: "t_2", Kind: enum.TaskKind(0), Title: "x"},
			status: 400,
		},
		{
			name:   "missing project",
			req:    CreateTaskRequest{ProjectID: "NOPE-1", TaskID: "t_3", Kind: enum.Dev, Title: "x"},
			status: 404,
		},
		{
			name:   "duplicate task id",
			req:    CreateTaskRequest{ProjectID: projectID, TaskID: "t_existing", Kind: enum.Dev, Title: "x"},
			status: 409,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateTask(context.Background(), tc.req)
			if got := StatusOf(err); got != tc.status {
				t.Fatalf("status = %d (%v), want %d", got, err, tc.status)
			}
		})
	}

	// Two active requirement tasks in one project hit the doc uniqueness index.
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_req_1", Kind: enum.Requirement, Title: "需求 1",
	}); err != nil {
		t.Fatalf("create first requirement: %v", err)
	}
	_, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_req_2", Kind: enum.Requirement, Title: "需求 2",
	})
	if got := StatusOf(err); got != 409 {
		t.Fatalf("duplicate requirement status = %d (%v), want 409", got, err)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	now := testClock
	svc, _ := newTestService(t, &now)
	_, err := svc.GetTask(context.Background(), "t_missing")
	if got := StatusOf(err); got != 404 {
		t.Fatalf("status = %d (%v), want 404", got, err)
	}
}

func TestUpdateTask(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_a", enum.Dev)

	updated, err := svc.UpdateTask(context.Background(), UpdateTaskRequest{
		TaskID: "t_a", Title: "新标题", DescriptionMD: "新描述", ContextMD: "新上下文", Priority: 5,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Title != "新标题" || updated.Priority != 5 {
		t.Errorf("updated = %+v", updated)
	}

	seedWorker(t, st, "w1")
	if _, err := svc.Claim(context.Background(), "w1", enum.Dev); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, err = svc.UpdateTask(context.Background(), UpdateTaskRequest{TaskID: "t_a", Title: "x"})
	if got := StatusOf(err); got != 409 {
		t.Fatalf("update in_progress status = %d (%v), want 409", got, err)
	}

	_, err = svc.UpdateTask(context.Background(), UpdateTaskRequest{TaskID: "t_missing", Title: "x"})
	if got := StatusOf(err); got != 404 {
		t.Fatalf("update missing status = %d (%v), want 404", got, err)
	}
}

func TestDeleteTask(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")

	createReadyTask(t, svc, projectID, "t_free", enum.Dev)
	if err := svc.DeleteTask(context.Background(), "t_free"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, err := svc.GetTask(context.Background(), "t_free"); StatusOf(err) != 404 {
		t.Fatalf("deleted task is still readable: %v", err)
	}

	// A task with a dependent cannot be deleted.
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_up", Kind: enum.Requirement, Title: "上游",
	}); err != nil {
		t.Fatalf("create upstream: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_down", Kind: enum.Design, Title: "下游", DependsOn: []string{"t_up"},
	}); err != nil {
		t.Fatalf("create downstream: %v", err)
	}
	if err := svc.DeleteTask(context.Background(), "t_up"); StatusOf(err) != 409 {
		t.Fatalf("delete with dependent = %d (%v), want 409", StatusOf(err), err)
	}

	// A started task cannot be deleted either.
	createReadyTask(t, svc, projectID, "t_started", enum.Dev)
	seedWorker(t, st, "w1")
	if _, err := svc.Claim(context.Background(), "w1", enum.Dev); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := svc.DeleteTask(context.Background(), "t_started"); StatusOf(err) != 409 {
		t.Fatalf("delete started = %d (%v), want 409", StatusOf(err), err)
	}
}

func TestClaimNoWork(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	seedWorker(t, st, "w1")
	_, err := svc.Claim(context.Background(), "w1", enum.Dev)
	if !IsNoWork(err) {
		t.Fatalf("Claim = %v, want NoWorkError", err)
	}
	if got := StatusOf(err); got != 204 {
		t.Fatalf("status = %d, want 204", got)
	}
}

func TestReportAndDecide(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_a", enum.Dev)
	materialID := createMaterial(t, svc, "m_v1")

	seedWorker(t, st, "w1")
	task, err := svc.Claim(context.Background(), "w1", enum.Dev)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	reported, err := svc.Report(context.Background(), "t_a", *task.LeaseID, materialID)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if reported.Status != enum.InReview {
		t.Errorf("status = %s, want in_review", reported.Status)
	}
	output, err := svc.LatestOutput(context.Background(), "t_a")
	if err != nil {
		t.Fatalf("LatestOutput: %v", err)
	}
	material, err := storev3.GetMaterial(context.Background(), st.Read(), materialID)
	if err != nil {
		t.Fatalf("GetMaterial: %v", err)
	}
	if output.MaterialID != material.ID {
		t.Errorf("output material = %d, want %d", output.MaterialID, material.ID)
	}

	// A second report on the released lease is a conflict.
	if _, err := svc.Report(context.Background(), "t_a", *task.LeaseID, materialID); StatusOf(err) != 409 {
		t.Fatalf("second report = %d (%v), want 409", StatusOf(err), err)
	}

	decided, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_a", Decision: enum.Approve, MaterialID: materialID, DecidedBy: "cto",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != enum.Done {
		t.Errorf("status = %s, want done", decided.Status)
	}

	decisions, err := svc.ListDecisions(context.Background(), "t_a", 10)
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].MaterialID == nil || *decisions[0].MaterialID != material.ID {
		t.Fatalf("decision = %+v, want material %d", decisions, material.ID)
	}
}

func TestDecideReject(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_a", enum.Requirement)
	materialID := createMaterial(t, svc, "m_v1")

	seedWorker(t, st, "w1")
	task, err := svc.Claim(context.Background(), "w1", enum.Requirement)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_a", *task.LeaseID, materialID); err != nil {
		t.Fatalf("Report: %v", err)
	}

	// Reject without a reason is a 400.
	if _, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_a", Decision: enum.Reject, DecidedBy: "cto",
	}); StatusOf(err) != 400 {
		t.Fatalf("reject without reason = %d (%v), want 400", StatusOf(err), err)
	}

	rejected, err := svc.Decide(context.Background(), DecideRequest{
		TaskID: "t_a", Decision: enum.Reject, DecidedBy: "cto", Reason: "范围不清",
	})
	if err != nil {
		t.Fatalf("Decide reject: %v", err)
	}
	if rejected.Status != enum.Ready {
		t.Errorf("status = %s, want ready", rejected.Status)
	}
	if rejected.Reason == nil || *rejected.Reason != "范围不清" {
		t.Errorf("reason = %v", rejected.Reason)
	}
}

func TestReclaimExpired(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_a", enum.Dev)

	seedWorker(t, st, "w1")
	task, err := svc.Claim(context.Background(), "w1", enum.Dev)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if task.LeaseUntil == nil {
		t.Fatal("claim did not set lease_until")
	}

	now = now.Add(3 * time.Second)
	reclaimed, err := svc.ReclaimExpired(context.Background())
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if reclaimed != 1 {
		t.Fatalf("reclaimed = %d, want 1", reclaimed)
	}
	after, err := svc.GetTask(context.Background(), "t_a")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != enum.Ready {
		t.Errorf("status = %s, want ready", after.Status)
	}
	seedWorker(t, st, "w2")
	if _, err := svc.Claim(context.Background(), "w2", enum.Dev); err != nil {
		t.Fatalf("re-claim after reclaim: %v", err)
	}
}

func TestReclaimExpiredExhaustsBudget(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	if _, err := svc.CreateTask(context.Background(), CreateTaskRequest{
		ProjectID: projectID, TaskID: "t_once", Kind: enum.Dev, Title: "一次性", MaxAttempts: 1,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	seedWorker(t, st, "w1")
	if _, err := svc.Claim(context.Background(), "w1", enum.Dev); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	now = now.Add(3 * time.Second)
	if _, err := svc.ReclaimExpired(context.Background()); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	task, err := svc.GetTask(context.Background(), "t_once")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Failed {
		t.Errorf("status = %s, want failed", task.Status)
	}
}

func TestAttachBoundaries(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_a", enum.Requirement)
	materialID := createMaterial(t, svc, "m_v1")

	if _, err := svc.Attach(context.Background(), AttachRequest{
		ProjectID: projectID, TaskID: "t_a", MaterialID: materialID, Role: enum.Input,
	}); StatusOf(err) != 400 {
		t.Fatalf("two owners = %d (%v), want 400", StatusOf(err), err)
	}
	if _, err := svc.Attach(context.Background(), AttachRequest{
		MaterialID: materialID, Role: enum.Input,
	}); StatusOf(err) != 400 {
		t.Fatalf("no owner = %d (%v), want 400", StatusOf(err), err)
	}
	if _, err := svc.Attach(context.Background(), AttachRequest{
		TaskID: "t_a", MaterialID: "m_missing", Role: enum.Input,
	}); StatusOf(err) != 404 {
		t.Fatalf("missing material = %d (%v), want 404", StatusOf(err), err)
	}
	if _, err := svc.Attach(context.Background(), AttachRequest{
		TaskID: "t_a", MaterialID: materialID, Role: enum.Input,
	}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
}

func TestStatusOfMaps(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{invalid("x"), 400},
		{notFound("x"), 404},
		{conflict("x"), 409},
		{&NoWorkError{}, 204},
		{context.Canceled, 500},
	}
	for _, tc := range cases {
		if got := StatusOf(tc.err); got != tc.status {
			t.Errorf("StatusOf(%v) = %d, want %d", tc.err, got, tc.status)
		}
	}
}

// TestLatestOutputSkipsContext pins the gate contract: a context material (e.g.
// a decision brief) attached as a second output must never become the artifact
// a gate reviews.
func TestLatestOutputSkipsContext(t *testing.T) {
	now := testClock
	svc, st := newTestService(t, &now)
	projectID := seedProject(t, st, "APP")
	createReadyTask(t, svc, projectID, "t_ctx", enum.Dev)
	artifactID := createMaterial(t, svc, "m_artifact")

	seedWorker(t, st, "w1")
	task, err := svc.Claim(context.Background(), "w1", enum.Dev)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Report(context.Background(), "t_ctx", *task.LeaseID, artifactID); err != nil {
		t.Fatalf("Report: %v", err)
	}

	briefID, err := svc.CreateMaterial(context.Background(), CreateMaterialRequest{
		MaterialID: "m_brief",
		Kind:       enum.MaterialContext,
		Version:    1,
		Title:      "决策简报",
		Sha256:     "sha-brief",
		Size:       1,
		Path:       "/tmp/brief",
		UploadedBy: "pm",
	})
	if err != nil {
		t.Fatalf("CreateMaterial(brief): %v", err)
	}
	if _, err := svc.Attach(context.Background(), AttachRequest{
		TaskID: "t_ctx", MaterialID: briefID, Role: enum.Output,
	}); err != nil {
		t.Fatalf("Attach(brief): %v", err)
	}

	output, err := svc.LatestOutput(context.Background(), "t_ctx")
	if err != nil {
		t.Fatalf("LatestOutput: %v", err)
	}
	artifact, err := storev3.GetMaterial(context.Background(), st.Read(), artifactID)
	if err != nil {
		t.Fatalf("GetMaterial: %v", err)
	}
	if output.MaterialID != artifact.ID {
		t.Errorf("gate artifact = %d, want %d (brief must be skipped)", output.MaterialID, artifact.ID)
	}
}
