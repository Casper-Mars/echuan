package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// TestClaimHTTPWithWorker covers acceptance 2–4 at the wire level: a claim
// naming a registered worker binds the card, a second claim of that worker is
// 409 worker_busy, an unknown id is 409 worker_unknown, and a report clears the
// pointer so the next claim can take the following card.
func TestClaimHTTPWithWorker(t *testing.T) {
	h, st := workerHTTPEnv(t)

	// Register while the queue is empty, so registration itself dispatches
	// nothing and the claims below are the only bindings.
	workerID := heartbeatWorker(t, h, map[string]any{}).Worker.WorkerID

	projectID := createTasksDraftHTTPProject(t, h)
	first := postTask(t, h, projectID, "第一张", "# 正文\n", 10)
	forceTaskColumns(t, st, first.ID, domain.TaskReady, 0, nil, nil, nil)
	second := postTask(t, h, projectID, "第二张", "# 正文\n", 5)
	forceTaskColumns(t, st, second.ID, domain.TaskReady, 0, nil, nil, nil)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"product": "APP", "worker_id": workerID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim with worker: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	claimed := decodeClaim(t, rec)
	if claimed.Task.ID != first.ID {
		t.Fatalf("claimed = %s, want %s", claimed.Task.ID, first.ID)
	}

	// GET /v1/workers must show the card.
	rec = doJSON(t, h, http.MethodGet, "/v1/workers?id="+workerID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get worker: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var worker workerDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &worker); err != nil {
		t.Fatalf("decode worker: %v", err)
	}
	if worker.CurrentTaskID == nil || *worker.CurrentTaskID != first.ID {
		t.Errorf("current_task_id = %v, want %s", worker.CurrentTaskID, first.ID)
	}
	if worker.State != domain.WorkerStateBusy {
		t.Errorf("state = %q, want busy", worker.State)
	}

	var leaseID *string
	if err := st.Read().QueryRowContext(t.Context(),
		`SELECT current_lease_id FROM workers WHERE worker_id = ?`, workerID).Scan(&leaseID); err != nil {
		t.Fatalf("read pointer lease: %v", err)
	}
	if leaseID == nil || *leaseID != claimed.Lease.LeaseID {
		t.Errorf("pointer lease = %v, want %s", leaseID, claimed.Lease.LeaseID)
	}

	// A worker that already holds a card cannot take a second one.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"product": "APP", "worker_id": workerID,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy claim: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "worker_busy" {
		t.Errorf("busy code = %q, want worker_busy", body.Code)
	}

	// An id the registry does not know is worker_unknown, not not_found.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"product": "APP", "worker_id": "w_00000000000000000000000000",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("unknown claim: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "worker_unknown" {
		t.Errorf("unknown code = %q, want worker_unknown", body.Code)
	}

	// Reporting the card clears the pointer in the same transaction.
	rec = postReport(t, h, first.ID, claimed.Lease.LeaseID, "# 报告\n\n做完了\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("report: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodGet, "/v1/workers?id="+workerID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get worker after report: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &worker); err != nil {
		t.Fatalf("decode worker after report: %v", err)
	}
	if worker.CurrentTaskID != nil {
		t.Errorf("current_task_id = %v after report, want null", worker.CurrentTaskID)
	}
	if worker.State != domain.WorkerStateIdle {
		t.Errorf("state = %q after report, want idle", worker.State)
	}

	// The freed worker takes the following card on the next claim.
	rec = doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"product": "APP", "worker_id": workerID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim after report: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if next := decodeClaim(t, rec); next.Task.ID != second.ID {
		t.Errorf("next claimed = %s, want %s", next.Task.ID, second.ID)
	}
}

// TestClaimHTTPWithoutWorkerKeepsLegacyShape guards acceptance 1: omitting
// worker_id leaves the response and the registry untouched.
func TestClaimHTTPWithoutWorkerKeepsLegacyShape(t *testing.T) {
	h, st, _, _ := claimHTTPEnv(t)

	rec := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"})
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy claim: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeClaim(t, rec); body.Task.ID == "" {
		t.Errorf("legacy claim body = %+v", body)
	}
	if n := countWorkers(t, st); n != 0 {
		t.Errorf("workers rows = %d, want 0 (no pointer written)", n)
	}
}

func countWorkers(t *testing.T, st *store.Store) int64 {
	t.Helper()
	var n int64
	if err := st.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workers`).Scan(&n); err != nil {
		t.Fatalf("count workers: %v", err)
	}
	return n
}
