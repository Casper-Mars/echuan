package v3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
)

func TestCreateTaskInitialStatus(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "状态", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	a, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Requirement, Title: "A", Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	if a.Status != enum.Ready {
		t.Errorf("A status = %s, want ready", a.Status)
	}

	b, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_b", Kind: enum.Design, Title: "B",
		DependsOn: []string{"t_a"}, Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}
	if b.Status != enum.Pending {
		t.Errorf("B status = %s, want pending", b.Status)
	}
}

func TestDecidePromotesDependent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "提升", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_req", Kind: enum.Requirement, Title: "需求", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask req: %v", err)
	}
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_design", Kind: enum.Design, Title: "方案",
		DependsOn: []string{"t_req"}, Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask design: %v", err)
	}

	claimReportApprove(t, st, "t_req", enum.Requirement)

	design, err := GetTask(ctx, st.Read(), "t_design")
	if err != nil {
		t.Fatalf("GetTask design: %v", err)
	}
	if design.Status != enum.Ready {
		t.Errorf("design status = %s, want ready", design.Status)
	}

	events, err := ListEvents(ctx, st.Read(), "t_design", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	promoted := false
	for _, event := range events {
		if event.Event == enum.EventPromoted {
			promoted = true
		}
	}
	if !promoted {
		t.Error("promoted event missing")
	}

	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Design, WorkerID: "w_design", LeaseID: "l_design", Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("ClaimTask design: %v", err)
	}
}

func TestPromoteDependentsIdempotent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "幂等", Now: testNow})
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

	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		var upstreamPK int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tasks WHERE task_id = 't_a'`).Scan(&upstreamPK); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = ? WHERE id = ?`, enum.Done, upstreamPK); err != nil {
			return err
		}
		if err := promoteDependents(ctx, tx, upstreamPK, "t_a", testNow); err != nil {
			return err
		}
		return promoteDependents(ctx, tx, upstreamPK, "t_a", testNow)
	}); err != nil {
		t.Fatalf("promote twice: %v", err)
	}

	events, err := ListEvents(ctx, st.Read(), "t_b", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	promoted := 0
	for _, event := range events {
		if event.Event == enum.EventPromoted {
			promoted++
		}
	}
	if promoted != 1 {
		t.Errorf("promoted events = %d, want exactly 1", promoted)
	}
}

func TestRejectReturnsToReady(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "驳回", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Requirement, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	claimAndReport(t, st, "t_a", enum.Requirement)
	if _, err := DecideTask(ctx, st, DecideInput{
		TaskID: "t_a", Decision: enum.Reject, DecidedBy: "cto", Reason: "范围不清", Now: testNow,
	}); err != nil {
		t.Fatalf("DecideTask reject: %v", err)
	}

	task, err := GetTask(ctx, st.Read(), "t_a")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Ready {
		t.Errorf("status = %s, want ready", task.Status)
	}
	if task.Reason == nil || *task.Reason != "范围不清" {
		t.Errorf("reason = %v", task.Reason)
	}

	// The rework must be claimable immediately, without an operator touch.
	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Requirement, WorkerID: "w_rework", LeaseID: "l_rework",
		Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("re-claim after reject: %v", err)
	}
}

func TestClaimRespectsDependencies(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "依赖", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_req", Kind: enum.Requirement, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_design", Kind: enum.Design, Title: "B",
		DependsOn: []string{"t_req"}, Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}

	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Design, WorkerID: "w1", LeaseID: "l1", Now: testNow, LeaseUntil: testUntil,
	}); !errors.Is(err, ErrNoWork) {
		t.Fatalf("Claim before dependency = %v, want ErrNoWork", err)
	}

	claimReportApprove(t, st, "t_req", enum.Requirement)

	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Design, WorkerID: "w1", LeaseID: "l1", Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("Claim after dependency: %v", err)
	}
}

func TestClaimConcurrent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "并发", Now: testNow})

	const tasks = 20
	for i := 0; i < tasks; i++ {
		if _, err := CreateTask(ctx, st, CreateTaskInput{
			ProjectID: project.ProjectID,
			TaskID:    fmt.Sprintf("t_%02d", i),
			Kind:      enum.Dev,
			Title:     "dev",
			Now:       testNow,
		}); err != nil {
			t.Fatalf("CreateTask %d: %v", i, err)
		}
	}

	const workers = 10
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		got   = make(map[string]bool, workers)
		errCh = make(chan error, workers)
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task, err := ClaimTask(ctx, st, ClaimInput{
				Kind:       enum.Dev,
				WorkerID:   fmt.Sprintf("w_%02d", i),
				LeaseID:    fmt.Sprintf("l_%02d", i),
				Now:        testNow,
				LeaseUntil: testUntil,
			})
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			if got[task.TaskID] {
				errCh <- fmt.Errorf("duplicate claim: %s", task.TaskID)
			}
			got[task.TaskID] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent claim: %v", err)
	}
	if len(got) != workers {
		t.Fatalf("claimed %d distinct tasks, want %d", len(got), workers)
	}
}

func TestDocActiveUniqueness(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "唯一", Now: testNow})

	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_req_1", Kind: enum.Requirement, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask first: %v", err)
	}
	_, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_req_2", Kind: enum.Requirement, Title: "B", Now: testNow,
	})
	if !errors.Is(err, storedb.ErrUniqueViolation) {
		t.Fatalf("second requirement error = %v, want unique violation", err)
	}
}

func TestLeaseRenewAndLost(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "租约", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Dev, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Dev, WorkerID: "w1", LeaseID: "l1", Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if err := RenewLease(ctx, st, "t_a", "wrong", testUntil, testNow); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("RenewLease wrong = %v, want ErrLeaseLost", err)
	}
	if err := RenewLease(ctx, st, "t_a", "l1", testUntil, testNow); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if err := ReportTask(ctx, st, "t_a", "wrong", testNow); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("ReportTask wrong = %v, want ErrLeaseLost", err)
	}
	if err := ReportTask(ctx, st, "t_a", "l1", testNow); err != nil {
		t.Fatalf("ReportTask: %v", err)
	}
}

func TestWorkerBinding(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "工人", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Dev, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := UpsertWorker(ctx, st, "w1", `{"host":"mac"}`, testNow); err != nil {
		t.Fatalf("UpsertWorker: %v", err)
	}
	task, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Dev, WorkerID: "w1", LeaseID: "l1", Now: testNow, LeaseUntil: testUntil,
	})
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	worker, err := GetWorker(ctx, st.Read(), "w1")
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if worker.State != enum.WorkerBusy || worker.CurrentTaskID == nil || *worker.CurrentTaskID != task.ID {
		t.Errorf("busy worker = %+v", worker)
	}

	if err := ReportTask(ctx, st, "t_a", "l1", testNow); err != nil {
		t.Fatalf("ReportTask: %v", err)
	}
	worker, err = GetWorker(ctx, st.Read(), "w1")
	if err != nil {
		t.Fatalf("GetWorker after report: %v", err)
	}
	if worker.State != enum.WorkerIdle || worker.CurrentTaskID != nil {
		t.Errorf("idle worker = %+v", worker)
	}
}

func TestAttachments(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, _ := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "附件", Now: testNow})
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_a", Kind: enum.Requirement, Title: "A", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	materialPK, err := CreateMaterialRecord(ctx, st, CreateMaterialInput{
		MaterialID: "m_1", Kind: enum.MaterialRequirement, Version: 1,
		Title: "需求 v1", Sha256: "abc", Size: 10, Path: "/tmp/m_1",
		UploadedBy: "pm", Now: testNow,
	})
	if err != nil {
		t.Fatalf("CreateMaterialRecord: %v", err)
	}

	if _, err := Attach(ctx, st, AttachInput{TaskID: "t_a", MaterialID: materialPK, Role: enum.Output, Now: testNow}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	output, err := LatestOutput(ctx, st.Read(), "t_a")
	if err != nil {
		t.Fatalf("LatestOutput: %v", err)
	}
	if output.MaterialID != materialPK || output.Role != enum.Output {
		t.Errorf("output = %+v", output)
	}

	if _, err := Attach(ctx, st, AttachInput{MaterialID: materialPK, Role: enum.Input, Now: testNow}); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("Attach without owner = %v, want ErrInvalidAttachment", err)
	}
	if _, err := Attach(ctx, st, AttachInput{
		ProjectID: project.ProjectID, TaskID: "t_a", MaterialID: materialPK, Role: enum.Input, Now: testNow,
	}); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("Attach with two owners = %v, want ErrInvalidAttachment", err)
	}
}
