package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// httpLeaseLayout mirrors the fixed-width tasks.lease_until layout (app's
// leaseTimeLayout) so a forced deadline compares correctly with the column.
const httpLeaseLayout = "2006-01-02T15:04:05.000Z"

func futureLease(t *testing.T) *string {
	t.Helper()
	value := time.Now().UTC().Add(time.Hour).Format(httpLeaseLayout)
	return &value
}

func pastLease(t *testing.T) *string {
	t.Helper()
	value := time.Now().UTC().Add(-time.Hour).Format(httpLeaseLayout)
	return &value
}

// newInProgressHTTPTask creates one card and forces it into in_progress with a
// live lease, the state a worker holds right before reporting.
func newInProgressHTTPTask(t *testing.T, h http.Handler, st *store.Store, projectID string) (taskID, leaseID string) {
	t.Helper()
	detail := postTask(t, h, projectID, "交付卡", "# 正文\n", 5)
	leaseID = "l_0123456789abcdef"
	forceTaskColumns(t, st, detail.ID, domain.TaskInProgress, 1, &leaseID, futureLease(t), nil)
	return detail.ID, leaseID
}

func postReport(t *testing.T, h http.Handler, taskID, leaseID, summary string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/report?id="+taskID,
		map[string]any{"lease_id": leaseID, "summary_md": summary}, nil)
}

func postTaskDecision(t *testing.T, h http.Handler, taskID string, version int, decision, reason string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"decision": decision}
	if reason != "" {
		payload["reason"] = reason
	}
	return doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/decide?id="+taskID, payload,
		map[string]string{"If-Match": strconv.Itoa(version)})
}

func getTaskHTTP(t *testing.T, h http.Handler, taskID string) taskDetailResponse {
	t.Helper()
	rec := doRequest(t, h, http.MethodGet, "/v1/tasks?id="+taskID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET task %s: status = %d (body %s)", taskID, rec.Code, rec.Body.String())
	}
	return decodeTaskDetail(t, rec)
}

func getProjectHTTP(t *testing.T, h http.Handler, projectID string) projectResponse {
	t.Helper()
	rec := doRequest(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET project %s: status = %d (body %s)", projectID, rec.Code, rec.Body.String())
	}
	return decodeProjectFromResponse(t, rec)
}

func countReportMaterials(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM materials WHERE kind = 3`).Scan(&n); err != nil {
		t.Fatalf("count report materials: %v", err)
	}
	return n
}

func materialPhysicalID(t *testing.T, st *store.Store, materialID string) int64 {
	t.Helper()
	var id int64
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT id FROM materials WHERE material_id = ?`, materialID).Scan(&id); err != nil {
		t.Fatalf("physical id of %s: %v", materialID, err)
	}
	return id
}

func previousMaterialID(t *testing.T, st *store.Store, materialID string) *int64 {
	t.Helper()
	var previous *int64
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT previous_material_id FROM materials WHERE material_id = ?`, materialID).Scan(&previous); err != nil {
		t.Fatalf("previous id of %s: %v", materialID, err)
	}
	return previous
}

func TestReportTaskHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)
	taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
	summary := "改了登录页 (#12)\n\n- 自检：go test ./...\n- emoji 🚀 原样保存\n"

	rec := postReport(t, h, taskID, leaseID, summary)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	detail := decodeTaskDetail(t, rec)

	if detail.Status != domain.TaskPendingAcceptance || detail.StatusName != "pending_acceptance" {
		t.Errorf("status = %d (%s), want pending_acceptance", detail.Status, detail.StatusName)
	}
	if detail.Lease != nil {
		t.Errorf("lease = %+v, want null", detail.Lease)
	}
	if detail.Report == nil {
		t.Fatal("report = nil")
	}
	if detail.Report.Kind != domain.MaterialKindReport || detail.Report.Version != 1 {
		t.Errorf("report kind/version = %d/%d, want 3/1", detail.Report.Kind, detail.Report.Version)
	}
	if detail.Report.Title != "报告：交付卡" {
		t.Errorf("report title = %q", detail.Report.Title)
	}
	if detail.Report.UploadedBy != "alice" {
		t.Errorf("uploaded_by = %q, want alice", detail.Report.UploadedBy)
	}
	if detail.Report.Size != int64(len(summary)) {
		t.Errorf("size = %d, want %d", detail.Report.Size, len(summary))
	}

	// Acceptance 2: the stored bytes are exactly the request body.
	materialRec := doRequest(t, h, http.MethodGet, "/v1/materials?id="+detail.Report.MaterialID, nil)
	if materialRec.Code != http.StatusOK {
		t.Fatalf("GET material: status = %d (body %s)", materialRec.Code, materialRec.Body.String())
	}
	if got := materialRec.Body.String(); got != summary {
		t.Errorf("material bytes = %q, want %q", got, summary)
	}

	// The task detail read back through the normal GET agrees.
	again := getTaskHTTP(t, h, taskID)
	if again.Report == nil || again.Report.MaterialID != detail.Report.MaterialID {
		t.Errorf("GET task report = %+v, want %s", again.Report, detail.Report.MaterialID)
	}
}

func TestReportTaskBlankSummaryHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)
	taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)

	cases := []struct {
		name    string
		payload map[string]any
	}{
		{"empty", map[string]any{"lease_id": leaseID, "summary_md": ""}},
		{"whitespace", map[string]any{"lease_id": leaseID, "summary_md": "   \n\t"}},
		{"missing", map[string]any{"lease_id": leaseID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/report?id="+taskID, tc.payload, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Code != "material_required" {
				t.Errorf("code = %q, want material_required", body.Code)
			}
			if len(body.Details) != 1 || body.Details[0].Field != "summary_md" ||
				body.Details[0].Reason != "must not be blank" {
				t.Errorf("details = %+v", body.Details)
			}
		})
	}

	// Nothing moved: still in progress, lease intact, no material row.
	detail := getTaskHTTP(t, h, taskID)
	if detail.Status != domain.TaskInProgress || detail.Lease == nil || detail.Lease.LeaseID != leaseID {
		t.Errorf("task = status %d lease %+v, want in_progress with lease", detail.Status, detail.Lease)
	}
	if n := countReportMaterials(t, st); n != 0 {
		t.Errorf("report materials = %d, want 0", n)
	}
}

func TestReportTaskLeaseGuardHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	t.Run("wrong lease", func(t *testing.T) {
		taskID, _ := newInProgressHTTPTask(t, h, st, projectID)
		rec := postReport(t, h, taskID, "l_ffffffffffffffff", "报告")
		expectLeaseLost(t, rec)
	})

	t.Run("expired lease", func(t *testing.T) {
		detail := postTask(t, h, projectID, "过期卡", "# 正文\n", 5)
		leaseID := "l_expired000000000"
		forceTaskColumns(t, st, detail.ID, domain.TaskInProgress, 1, &leaseID, pastLease(t), nil)
		rec := postReport(t, h, detail.ID, leaseID, "报告")
		expectLeaseLost(t, rec)
	})

	t.Run("ready task", func(t *testing.T) {
		detail := postTask(t, h, projectID, "就绪卡", "# 正文\n", 5)
		leaseID := "l_ready00000000000"
		forceTaskColumns(t, st, detail.ID, domain.TaskReady, 0, nil, nil, nil)
		rec := postReport(t, h, detail.ID, leaseID, "报告")
		expectLeaseLost(t, rec)
	})

	t.Run("done task", func(t *testing.T) {
		detail := postTask(t, h, projectID, "完成卡", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskDone, 1, nil, nil, nil)
		rec := postReport(t, h, detail.ID, "l_done000000000000", "报告")
		expectLeaseLost(t, rec)
	})

	t.Run("missing task", func(t *testing.T) {
		rec := postReport(t, h, projectID+"-T9999", "l_any0000000000000", "报告")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "not_found" {
			t.Errorf("code = %q, want not_found", body.Code)
		}
	})

	t.Run("missing lease_id", func(t *testing.T) {
		taskID, _ := newInProgressHTTPTask(t, h, st, projectID)
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/report?id="+taskID,
			map[string]any{"summary_md": "报告"}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "invalid_argument" ||
			len(body.Details) != 1 || body.Details[0].Field != "lease_id" {
			t.Errorf("body = %+v", body)
		}
	})
}

func expectLeaseLost(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "lease_lost" {
		t.Errorf("code = %q, want lease_lost", body.Code)
	}
}

func TestReportTaskReplayHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)
	taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)

	if rec := postReport(t, h, taskID, leaseID, "第一次"); rec.Code != http.StatusOK {
		t.Fatalf("first report: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	rec := postReport(t, h, taskID, leaseID, "第二次")
	expectLeaseLost(t, rec)

	if n := countReportMaterials(t, st); n != 1 {
		t.Errorf("report materials = %d, want 1", n)
	}
}

func TestReportTaskVersionChainHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)
	taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)

	first := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "第一版"))
	firstMaterial := first.Report.MaterialID

	// Rework sends the card back to ready; the next claim issues a new lease.
	rec := postTaskDecision(t, h, taskID, first.Version, "rework", "补测试结果")
	if rec.Code != http.StatusOK {
		t.Fatalf("rework: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	claim := decodeClaim(t, doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP"}))
	if claim.Task.ID != taskID {
		t.Fatalf("claimed %s, want %s", claim.Task.ID, taskID)
	}

	second := decodeTaskDetail(t, postReport(t, h, taskID, claim.Lease.LeaseID, "第二版"))
	if second.Report == nil || second.Report.Version != 2 {
		t.Fatalf("report = %+v, want version 2", second.Report)
	}
	previous := previousMaterialID(t, st, second.Report.MaterialID)
	if previous == nil || *previous != materialPhysicalID(t, st, firstMaterial) {
		t.Errorf("previous_material_id = %v, want physical id of %s", previous, firstMaterial)
	}
}

func TestDecideTaskHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	t.Run("accept", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		reported := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "交付"))

		rec := postTaskDecision(t, h, taskID, reported.Version, "accept", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		detail := decodeTaskDetail(t, rec)
		if detail.Status != domain.TaskDone || detail.StatusName != "done" {
			t.Errorf("status = %d (%s), want done", detail.Status, detail.StatusName)
		}
		if detail.Reason != nil {
			t.Errorf("reason = %v, want null", *detail.Reason)
		}
		if detail.AttemptCount != 1 {
			t.Errorf("attempt_count = %d, want 1 (unchanged)", detail.AttemptCount)
		}
		if detail.Version != reported.Version+1 {
			t.Errorf("version = %d, want %d", detail.Version, reported.Version+1)
		}
	})

	t.Run("rework", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		reported := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "交付"))

		rec := postTaskDecision(t, h, taskID, reported.Version, "rework", "缺少回归测试")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		detail := decodeTaskDetail(t, rec)
		if detail.Status != domain.TaskReady || detail.StatusName != "ready" {
			t.Errorf("status = %d, want ready", detail.Status)
		}
		if detail.Reason == nil || *detail.Reason != "缺少回归测试" {
			t.Errorf("reason = %v", detail.Reason)
		}

		// Ready again and claimable: the next claim takes this card.
		claim := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP", "task_id": taskID})
		if claim.Code != http.StatusOK {
			t.Fatalf("claim after rework: status = %d (body %s)", claim.Code, claim.Body.String())
		}
		if decodeClaim(t, claim).Task.ID != taskID {
			t.Error("claim returned a different card")
		}
	})

	t.Run("retry", func(t *testing.T) {
		detail := postTask(t, h, projectID, "失败卡", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskFailed, 2, nil, nil, strPtr("第一次失败"))

		rec := postTaskDecision(t, h, detail.ID, detail.Version, "retry", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		task := decodeTaskDetail(t, rec)
		if task.Status != domain.TaskReady || task.AttemptCount != 0 || task.Reason != nil {
			t.Errorf("task = status %d attempt %d reason %v, want ready/0/null",
				task.Status, task.AttemptCount, task.Reason)
		}

		claim := doJSON(t, h, http.MethodPost, "/v1/tasks/claim", map[string]any{"product": "APP", "task_id": detail.ID})
		if claim.Code != http.StatusOK {
			t.Fatalf("claim after retry: status = %d", claim.Code)
		}
		if got := decodeClaim(t, claim).Task.Attempt; got != 1 {
			t.Errorf("attempt after claim = %d, want 1", got)
		}
	})

	t.Run("abandon", func(t *testing.T) {
		detail := postTask(t, h, projectID, "放弃卡", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskFailed, 3, nil, nil, nil)

		rec := postTaskDecision(t, h, detail.ID, detail.Version, "abandon", "环境不可用")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		task := decodeTaskDetail(t, rec)
		if task.Status != domain.TaskCancelled || task.StatusName != "cancelled" {
			t.Errorf("status = %d, want cancelled", task.Status)
		}
		if task.Reason == nil || *task.Reason != "环境不可用" {
			t.Errorf("reason = %v", task.Reason)
		}
	})
}

func strPtr(value string) *string { return &value }

func TestDecideTaskGuardHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	t.Run("accept on failed", func(t *testing.T) {
		detail := postTask(t, h, projectID, "失败", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskFailed, 1, nil, nil, nil)
		expectInvalidStateCode(t, postTaskDecision(t, h, detail.ID, detail.Version, "accept", ""))
	})
	t.Run("rework on failed", func(t *testing.T) {
		detail := postTask(t, h, projectID, "失败2", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskFailed, 1, nil, nil, nil)
		expectInvalidStateCode(t, postTaskDecision(t, h, detail.ID, detail.Version, "rework", "理由"))
	})
	t.Run("retry on pending_acceptance", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		reported := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "交付"))
		expectInvalidStateCode(t, postTaskDecision(t, h, taskID, reported.Version, "retry", ""))
	})
	t.Run("abandon on pending_acceptance", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		reported := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "交付"))
		expectInvalidStateCode(t, postTaskDecision(t, h, taskID, reported.Version, "abandon", "理由"))
	})
	t.Run("accept on ready", func(t *testing.T) {
		detail := postTask(t, h, projectID, "就绪", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskReady, 0, nil, nil, nil)
		expectInvalidStateCode(t, postTaskDecision(t, h, detail.ID, detail.Version, "accept", ""))
	})
	t.Run("missing If-Match", func(t *testing.T) {
		detail := postTask(t, h, projectID, "缺头", "# 正文\n", 5)
		rec := doJSONHeaders(t, h, http.MethodPost, "/v1/tasks/decide?id="+detail.ID,
			map[string]any{"decision": "accept"}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if body := decodeError(t, rec); body.Details[0].Field != "If-Match" {
			t.Errorf("details = %+v", body.Details)
		}
	})
	t.Run("stale version", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		reported := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "交付"))
		rec := postTaskDecision(t, h, taskID, reported.Version+10, "accept", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
		if body := decodeError(t, rec); body.Code != "invalid_version" {
			t.Errorf("code = %q, want invalid_version", body.Code)
		}
	})
	t.Run("invalid decision", func(t *testing.T) {
		detail := postTask(t, h, projectID, "非法", "# 正文\n", 5)
		rec := postTaskDecision(t, h, detail.ID, detail.Version, "maybe", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		body := decodeError(t, rec)
		if len(body.Details) != 1 || body.Details[0].Field != "decision" {
			t.Errorf("details = %+v", body.Details)
		}
	})
	t.Run("missing reason", func(t *testing.T) {
		detail := postTask(t, h, projectID, "缺理由", "# 正文\n", 5)
		forceTaskColumns(t, st, detail.ID, domain.TaskFailed, 1, nil, nil, nil)
		rec := postTaskDecision(t, h, detail.ID, detail.Version, "abandon", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if body := decodeError(t, rec); body.Details[0].Field != "reason" {
			t.Errorf("details = %+v", body.Details)
		}
	})
}

func expectInvalidStateCode(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeError(t, rec); body.Code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", body.Code)
	}
}

// newDevelopingProject creates a tasks_draft project with two cards and forces
// the project into pending_done at version 7.
func newDevelopingProject(t *testing.T, h http.Handler, st *store.Store) (projectID string, cards [2]taskDetailResponse) {
	t.Helper()
	projectID = createTasksDraftHTTPProject(t, h)
	cards[0] = postTask(t, h, projectID, "第一张", "# 正文\n", 5)
	cards[1] = postTask(t, h, projectID, "第二张", "# 正文\n", 5)
	forceProjectState(t, st, projectID, domain.StatusPendingDone, 7, nil)
	return projectID, cards
}

func TestAcceptLastTaskAdvancesProjectHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	t.Run("remaining ready keeps pending_done", func(t *testing.T) {
		pid, cards := newDevelopingProject(t, h, st)
		forceTaskColumns(t, st, cards[0].ID, domain.TaskPendingAcceptance, 1, nil, nil, nil)
		forceTaskColumns(t, st, cards[1].ID, domain.TaskReady, 0, nil, nil, nil)

		rec := postTaskDecision(t, h, cards[0].ID, cards[0].Version, "accept", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("accept: status = %d (body %s)", rec.Code, rec.Body.String())
		}
		if project := getProjectHTTP(t, h, pid); project.Status != domain.StatusPendingDone {
			t.Errorf("project status = %d, want pending_done", project.Status)
		}
	})

	t.Run("last non-terminal card completes the project", func(t *testing.T) {
		pid, cards := newDevelopingProject(t, h, st)
		forceTaskColumns(t, st, cards[0].ID, domain.TaskDone, 1, nil, nil, nil)
		forceTaskColumns(t, st, cards[1].ID, domain.TaskPendingAcceptance, 1, nil, nil, nil)

		rec := postTaskDecision(t, h, cards[1].ID, cards[1].Version, "accept", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("accept: status = %d (body %s)", rec.Code, rec.Body.String())
		}
		project := getProjectHTTP(t, h, pid)
		if project.Status != domain.StatusCompleted || project.StatusName != "completed" {
			t.Errorf("project status = %d (%s), want completed", project.Status, project.StatusName)
		}
		if project.Version != 8 {
			t.Errorf("project version = %d, want 8", project.Version)
		}
	})

	_ = projectID
}

func TestAcceptWithOnlyTerminalTasksAdvancesProjectHTTP(t *testing.T) {
	h, st, _ := newTaskHTTPEnv(t)
	pid, cards := newDevelopingProject(t, h, st)
	forceTaskColumns(t, st, cards[0].ID, domain.TaskCancelled, 1, nil, nil, nil)
	forceTaskColumns(t, st, cards[1].ID, domain.TaskPendingAcceptance, 1, nil, nil, nil)

	rec := postTaskDecision(t, h, cards[1].ID, cards[1].Version, "accept", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if project := getProjectHTTP(t, h, pid); project.Status != domain.StatusCompleted {
		t.Errorf("project status = %d, want completed (cancelled is terminal)", project.Status)
	}
}

func TestCompletedProjectRejectsDecisionHTTP(t *testing.T) {
	h, st, _ := newTaskHTTPEnv(t)
	pid, cards := newDevelopingProject(t, h, st)
	forceTaskColumns(t, st, cards[0].ID, domain.TaskDone, 1, nil, nil, nil)
	forceTaskColumns(t, st, cards[1].ID, domain.TaskPendingAcceptance, 1, nil, nil, nil)
	if rec := postTaskDecision(t, h, cards[1].ID, cards[1].Version, "accept", ""); rec.Code != http.StatusOK {
		t.Fatalf("accept last card: status = %d", rec.Code)
	}

	project := getProjectHTTP(t, h, pid)
	if project.Status != domain.StatusCompleted {
		t.Fatalf("project status = %d, want completed", project.Status)
	}

	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			body := map[string]any{"decision": decision}
			if decision == "reject" {
				body["reason"] = "想返工"
			}
			rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+pid,
				body, map[string]string{"If-Match": strconv.Itoa(project.Version)})
			expectInvalidStateCode(t, rec)
		})
	}
}

func TestConcurrentReportAndDecisionHTTP(t *testing.T) {
	h, st, projectID := newTaskHTTPEnv(t)

	t.Run("two reports on one lease", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		payload, _ := json.Marshal(map[string]any{"lease_id": leaseID, "summary_md": "并发报告"})
		target := "/v1/tasks/report?id=" + taskID

		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i] = postRaw(h, target, payload, nil)
			}(i)
		}
		wg.Wait()

		ok, lost := 0, 0
		for _, code := range codes {
			switch code {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				lost++
			}
		}
		if ok != 1 || lost != 1 {
			t.Errorf("codes = %v, want exactly one 200 and one 409", codes)
		}
		if n := countReportMaterials(t, st); n != 1 {
			t.Errorf("report materials = %d, want 1", n)
		}
	})

	t.Run("two decisions on one version", func(t *testing.T) {
		taskID, leaseID := newInProgressHTTPTask(t, h, st, projectID)
		reported := decodeTaskDetail(t, postReport(t, h, taskID, leaseID, "并发验收"))
		payload, _ := json.Marshal(map[string]any{"decision": "accept"})
		headers := map[string]string{"If-Match": strconv.Itoa(reported.Version)}
		target := "/v1/tasks/decide?id=" + taskID

		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i] = postRaw(h, target, payload, headers)
			}(i)
		}
		wg.Wait()

		ok, version := 0, 0
		for _, code := range codes {
			switch code {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				version++
			}
		}
		if ok != 1 || version != 1 {
			t.Errorf("codes = %v, want exactly one 200 and one 409", codes)
		}
		if detail := getTaskHTTP(t, h, taskID); detail.Status != domain.TaskDone {
			t.Errorf("final status = %d, want done", detail.Status)
		}
	})
}

// postRaw issues one request without testing.T so it is safe from a goroutine.
func postRaw(h http.Handler, target string, payload []byte, headers map[string]string) int {
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderActor, "alice")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}
