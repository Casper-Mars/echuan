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

	seedWorker(t, st, "w_design", testNow)
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
	seedWorker(t, st, "w_rework", testNow)
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

	seedWorker(t, st, "w1", testNow)
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
		seedWorker(t, st, fmt.Sprintf("w_%02d", i), testNow)
	}
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

	seedWorker(t, st, "w1", testNow)
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

// TestListUpstreamOutputsSkipsContext pins the dispatch contract of the upstream
// line in a claim payload: a dependent task sees the upstream deliverable
// (requirement/design/plan/report) and never a decision brief (kind=context)
// attached afterwards as another output, and never kind=other — the same kind
// filter the gate applies in LatestOutput.
func TestListUpstreamOutputsSkipsContext(t *testing.T) {
	cases := []struct {
		name       string
		extraID    string
		extraKind  enum.MaterialKind
		extraTitle string
	}{
		{name: "brief_after_artifact", extraID: "m_brief", extraKind: enum.MaterialContext, extraTitle: "决策简报"},
		{name: "artifact_only"},
		{name: "other_kind_is_not_artifact", extraID: "m_other", extraKind: enum.MaterialOther, extraTitle: "杂项"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := testStore(t)
			ctx := context.Background()
			seedProduct(t, st, "APP")
			project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "上游", Now: testNow})
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			if _, err := CreateTask(ctx, st, CreateTaskInput{
				ProjectID: project.ProjectID, TaskID: "t_up", Kind: enum.Requirement, Title: "上游", Now: testNow,
			}); err != nil {
				t.Fatalf("CreateTask up: %v", err)
			}
			down, err := CreateTask(ctx, st, CreateTaskInput{
				ProjectID: project.ProjectID, TaskID: "t_down", Kind: enum.Design, Title: "下游",
				DependsOn: []string{"t_up"}, Now: testNow,
			})
			if err != nil {
				t.Fatalf("CreateTask down: %v", err)
			}

			attachOutput := func(materialID string, kind enum.MaterialKind, title string) {
				t.Helper()
				pk, err := CreateMaterialRecord(ctx, st, CreateMaterialInput{
					MaterialID: materialID, Kind: kind, Version: 1, Title: title,
					Sha256: "sha-" + materialID, Size: 1, Path: "/tmp/" + materialID,
					UploadedBy: "pm", Now: testNow,
				})
				if err != nil {
					t.Fatalf("CreateMaterialRecord %s: %v", materialID, err)
				}
				if _, err := Attach(ctx, st, AttachInput{
					TaskID: "t_up", MaterialID: pk, Role: enum.Output, Now: testNow,
				}); err != nil {
					t.Fatalf("Attach %s: %v", materialID, err)
				}
			}
			attachOutput("m_artifact", enum.MaterialRequirement, "需求 v1")
			if tc.extraID != "" {
				attachOutput(tc.extraID, tc.extraKind, tc.extraTitle)
			}

			got, err := ListUpstreamOutputs(ctx, st.Read(), down.ID)
			if err != nil {
				t.Fatalf("ListUpstreamOutputs: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("upstream = %+v, want exactly one dependency", got)
			}
			if got[0].TaskID != "t_up" {
				t.Errorf("upstream task = %s, want t_up", got[0].TaskID)
			}
			var materialID, title string
			if got[0].MaterialID != nil {
				materialID = *got[0].MaterialID
			}
			if got[0].Title != nil {
				title = *got[0].Title
			}
			if materialID != "m_artifact" {
				t.Errorf("upstream material = %q, want %q", materialID, "m_artifact")
			}
			if title != "需求 v1" {
				t.Errorf("upstream title = %q, want %q", title, "需求 v1")
			}
		})
	}
}

// claimOneTask seeds a product, a project and one task of kind, then leases it to
// workerID — the "worker holds a card" state ClaimTask leaves behind.
func claimOneTask(t *testing.T, st *storedb.Store, taskID string, kind enum.TaskKind, workerID string) Task {
	t.Helper()
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "释放占用", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := CreateTask(ctx, st, CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: taskID, Kind: kind, Title: "卡片", Now: testNow,
	}); err != nil {
		t.Fatalf("CreateTask %s: %v", taskID, err)
	}
	if err := UpsertWorker(ctx, st, workerID, "{}", testNow); err != nil {
		t.Fatalf("UpsertWorker %s: %v", workerID, err)
	}
	task, err := ClaimTask(ctx, st, ClaimInput{
		Kind: kind, WorkerID: workerID, LeaseID: "l_" + taskID, Now: testNow, LeaseUntil: testUntil,
	})
	if err != nil {
		t.Fatalf("ClaimTask %s: %v", taskID, err)
	}
	return task
}

// assertWorkerReleased pins the occupancy projection: idle, no task, no lease.
func assertWorkerReleased(t *testing.T, st *storedb.Store, workerID string) {
	t.Helper()
	worker, err := GetWorker(context.Background(), st.Read(), workerID)
	if err != nil {
		t.Fatalf("GetWorker %s: %v", workerID, err)
	}
	if worker.State != enum.WorkerIdle {
		t.Errorf("%s state = %s, want idle", workerID, worker.State)
	}
	if worker.CurrentTaskID != nil {
		t.Errorf("%s current_task_id = %d, want NULL", workerID, *worker.CurrentTaskID)
	}
	if worker.CurrentLeaseID != nil {
		t.Errorf("%s current_lease_id = %q, want NULL", workerID, *worker.CurrentLeaseID)
	}
}

// TestCancelTaskReleasesWorker is AC1 at the store layer: cancelling an
// in_progress task clears the holder's pointer, so the row can be pruned by
// retention instead of sticking around forever (issue #165 §1).
func TestCancelTaskReleasesWorker(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	claimOneTask(t, st, "t_cancel", enum.Dev, "w_cancel")

	if err := CancelTask(ctx, st, "t_cancel", "cto", testNow); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}

	task, err := GetTask(ctx, st.Read(), "t_cancel")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Cancelled {
		t.Errorf("task status = %s, want cancelled", task.Status)
	}
	if task.WorkerID != nil || task.LeaseID != nil {
		t.Errorf("cancelled task still has worker=%v lease=%v", task.WorkerID, task.LeaseID)
	}
	assertWorkerReleased(t, st, "w_cancel")
}

// TestCancelTaskReleasesOnlyItsHolder keeps the release conditional: cancelling
// one card must not clear the pointer of a worker holding a different card.
func TestCancelTaskReleasesOnlyItsHolder(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedProduct(t, st, "APP")
	project, err := CreateProject(ctx, st, CreateProjectInput{ProductID: "APP", Name: "两张卡", Now: testNow})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, taskID := range []string{"t_a", "t_b"} {
		if _, err := CreateTask(ctx, st, CreateTaskInput{
			ProjectID: project.ProjectID, TaskID: taskID, Kind: enum.Dev, Title: taskID, Now: testNow,
		}); err != nil {
			t.Fatalf("CreateTask %s: %v", taskID, err)
		}
	}
	for _, workerID := range []string{"w_a", "w_b"} {
		if err := UpsertWorker(ctx, st, workerID, "{}", testNow); err != nil {
			t.Fatalf("UpsertWorker %s: %v", workerID, err)
		}
	}
	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Dev, WorkerID: "w_a", LeaseID: "l_a", Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("ClaimTask w_a: %v", err)
	}
	if _, err := ClaimTask(ctx, st, ClaimInput{
		Kind: enum.Dev, WorkerID: "w_b", LeaseID: "l_b", Now: testNow, LeaseUntil: testUntil,
	}); err != nil {
		t.Fatalf("ClaimTask w_b: %v", err)
	}

	if err := CancelTask(ctx, st, "t_a", "cto", testNow); err != nil {
		t.Fatalf("CancelTask t_a: %v", err)
	}
	assertWorkerReleased(t, st, "w_a")

	held, err := GetWorker(ctx, st.Read(), "w_b")
	if err != nil {
		t.Fatalf("GetWorker w_b: %v", err)
	}
	if held.State != enum.WorkerBusy || held.CurrentTaskID == nil || held.CurrentLeaseID == nil {
		t.Errorf("w_b (holds t_b, still in_progress) = %+v, want busy with pointer", held)
	}
}

// TestFailTaskReleasesWorker is the failure-path half of AC1: FailTask also ends
// the occupancy, not just the lease.
func TestFailTaskReleasesWorker(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	claimOneTask(t, st, "t_fail", enum.Dev, "w_fail")

	if err := FailTask(ctx, st, "t_fail", "boom", testNow); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	task, err := GetTask(ctx, st.Read(), "t_fail")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != enum.Failed {
		t.Errorf("task status = %s, want failed", task.Status)
	}
	assertWorkerReleased(t, st, "w_fail")
}
