package api

import (
	"net/http"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// TestTaskResponsesExposeWorkerID covers task-07 at the wire level: the detail
// and the list render worker_id from one shared conversion, only an
// in-progress card carries a value, reporting clears it, and a dangling worker
// pointer never leaks out.
func TestTaskResponsesExposeWorkerID(t *testing.T) {
	h, st := workerHTTPEnv(t)
	// Register while the queue is empty so registration dispatches nothing.
	workerID := heartbeatWorker(t, h, map[string]any{}).Worker.WorkerID
	projectID := createTasksDraftHTTPProject(t, h)

	claimed := postTask(t, h, projectID, "执行中的卡", "# 正文\n", 10)
	forceTaskColumns(t, st, claimed.ID, domain.TaskReady, 0, nil, nil, nil)
	backlog := postTask(t, h, projectID, "未领取的卡", "# 正文\n", 5)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"product": "APP", "worker_id": workerID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	claim := decodeClaim(t, rec)
	if claim.Task.ID != claimed.ID {
		t.Fatalf("claimed = %s, want %s", claim.Task.ID, claimed.ID)
	}

	// 1. detail of the claimed card names the executor.
	detail := getTaskHTTP(t, h, claimed.ID)
	if detail.Status != domain.TaskInProgress {
		t.Fatalf("claimed status = %d, want in_progress", detail.Status)
	}
	if detail.WorkerID == nil || *detail.WorkerID != workerID {
		t.Errorf("detail worker_id = %v, want %s", detail.WorkerID, workerID)
	}

	// A card that was never claimed has no executor.
	if other := getTaskHTTP(t, h, backlog.ID); other.WorkerID != nil {
		t.Errorf("backlog detail worker_id = %q, want null", *other.WorkerID)
	}

	// 2. the list uses the same rule for every card.
	page := decodeTaskList(t, doJSON(t, h, http.MethodGet,
		"/v1/tasks?project_id="+projectID, nil))
	if len(page.Items) != 2 {
		t.Fatalf("list items = %d, want 2", len(page.Items))
	}
	for _, item := range page.Items {
		switch item.Status {
		case domain.TaskInProgress:
			if item.WorkerID == nil || *item.WorkerID != workerID {
				t.Errorf("list worker_id of in-progress %s = %v, want %s",
					item.ID, item.WorkerID, workerID)
			}
		default:
			if item.WorkerID != nil {
				t.Errorf("list worker_id of %s (status %d) = %q, want null",
					item.ID, item.Status, *item.WorkerID)
			}
		}
	}

	// 3. a successful report releases the pointer, and both forms report null.
	rec = postReport(t, h, claimed.ID, claim.Lease.LeaseID, "# 报告\n\n做完了\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("report: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if reported := decodeTaskDetail(t, rec); reported.WorkerID != nil {
		t.Errorf("report response worker_id = %q, want null", *reported.WorkerID)
	}
	if detail := getTaskHTTP(t, h, claimed.ID); detail.WorkerID != nil {
		t.Errorf("detail worker_id after report = %q, want null", *detail.WorkerID)
	}

	// 4. a dangling pointer (worker still points at a done card) must not leak
	// through the join: the status decides.
	dangling := postTask(t, h, projectID, "已完成的卡", "# 正文\n", 1)
	forceTaskColumns(t, st, dangling.ID, domain.TaskDone, 1, nil, nil, nil)
	if _, err := st.Write().ExecContext(t.Context(), `
		UPDATE workers
		   SET current_task_id = (SELECT id FROM tasks WHERE task_id = ?), state = 'busy'
		 WHERE worker_id = ?`, dangling.ID, workerID); err != nil {
		t.Fatalf("force dangling pointer: %v", err)
	}
	if done := getTaskHTTP(t, h, dangling.ID); done.WorkerID != nil {
		t.Errorf("done card worker_id = %q, want null", *done.WorkerID)
	}

	// 5. a legacy in-progress card without a pointer is null, not "" or 500.
	legacy := postTask(t, h, projectID, "历史卡", "# 正文\n", 0)
	forceTaskColumns(t, st, legacy.ID, domain.TaskInProgress, 1, nil, nil, nil)

	page = decodeTaskList(t, doJSON(t, h, http.MethodGet,
		"/v1/tasks?project_id="+projectID, nil))
	if len(page.Items) != 4 {
		t.Fatalf("list items = %d, want 4 (WORKER JOIN must not duplicate rows)", len(page.Items))
	}
	for _, item := range page.Items {
		if item.WorkerID != nil {
			t.Errorf("list worker_id of %s (status %d) = %q, want null",
				item.ID, item.Status, *item.WorkerID)
		}
	}
}
