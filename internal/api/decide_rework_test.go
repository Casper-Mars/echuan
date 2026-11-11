package api

// HTTP contract of the requirement-gate reject: the 1→0 transition and the
// rework card it enqueues are one transaction, and a stale If-Match cannot add
// a second card.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Casper-Mars/echuan/internal/store"
)

// decodeWorkItemList decodes one GET /v1/work-items page.
func decodeWorkItemList(t *testing.T, rec *httptest.ResponseRecorder) workItemListResponse {
	t.Helper()
	var page workItemListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode work-item list %q: %v", rec.Body.String(), err)
	}
	return page
}

// requireReworkCardCount asserts the project owns exactly one requirement card,
// straight from the database.
func requireReworkCardCount(t *testing.T, st *store.Store, projectID string, want int) {
	t.Helper()
	var count int
	if err := st.Read().QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM work_items w
		  JOIN projects p ON p.id = w.project_id
		 WHERE p.project_id = ? AND w.kind = 'requirement'`, projectID).Scan(&count); err != nil {
		t.Fatalf("count requirement cards: %v", err)
	}
	if count != want {
		t.Fatalf("requirement cards in db = %d, want %d", count, want)
	}
}

// TestRequirementRejectHTTPEnqueuesReworkCard is AC1, AC2 and AC3 over HTTP.
func TestRequirementRejectHTTPEnqueuesReworkCard(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	created := doJSON(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "登录改版",
		"requirement_md": "# 需求\n\n正文\n",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create project: status = %d (body %s)", created.Code, created.Body.String())
	}
	project := decodeProjectDetail(t, created)
	projectID := project.ProjectID
	if project.Status != 1 {
		t.Fatalf("create status = %d, want requirement_review", project.Status)
	}
	requireReworkCardCount(t, st, projectID, 0)

	rec := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
		map[string]any{"decision": "reject", "reason": "需求缺少验收标准"},
		map[string]string{"If-Match": strconv.Itoa(project.Version)})
	if rec.Code != http.StatusOK {
		t.Fatalf("reject: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	rejected := decodeProjectFromResponse(t, rec)
	if rejected.Status != 0 {
		t.Errorf("status = %d, want 0 (requirement_draft)", rejected.Status)
	}
	if rejected.StatusName != "requirement_draft" {
		t.Errorf("status_name = %q, want requirement_draft", rejected.StatusName)
	}
	if rejected.Reason == nil || *rejected.Reason != "需求缺少验收标准" {
		t.Errorf("reason = %v, want the objection", rejected.Reason)
	}

	// AC2: exactly one queued requirement card, visible through the list API
	// and confirmed in the database.
	listed := doJSON(t, h, http.MethodGet, "/v1/work-items?project_id="+projectID+"&kind=requirement", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list work items: status = %d (body %s)", listed.Code, listed.Body.String())
	}
	page := decodeWorkItemList(t, listed)
	if len(page.Items) != 1 {
		t.Fatalf("requirement items = %d, want exactly 1 (%+v)", len(page.Items), page.Items)
	}
	card := page.Items[0]
	if card.Kind != "requirement" {
		t.Errorf("kind = %q, want requirement", card.Kind)
	}
	if card.State != "queued" {
		t.Errorf("state = %q, want queued", card.State)
	}
	if card.Priority != 990 {
		t.Errorf("priority = %d, want 990", card.Priority)
	}
	if card.Title != "需求撰写：登录改版" {
		t.Errorf("title = %q, want 需求撰写：登录改版", card.Title)
	}
	if card.ProjectID != projectID {
		t.Errorf("project_id = %q, want %s", card.ProjectID, projectID)
	}
	requireReworkCardCount(t, st, projectID, 1)

	// The detail body counts the rework card too.
	detail := decodeProjectDetail(t, doJSON(t, h, http.MethodGet, "/v1/projects?id="+projectID, nil))
	if detail.WorkItemTotal != 1 || detail.WorkItemDone != 0 {
		t.Errorf("work_item_total/done = %d/%d, want 1/0", detail.WorkItemTotal, detail.WorkItemDone)
	}

	// AC3: replaying the consumed If-Match is a version conflict and adds no
	// card.
	replay := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
		map[string]any{"decision": "reject", "reason": "需求缺少验收标准"},
		map[string]string{"If-Match": strconv.Itoa(project.Version)})
	if replay.Code != http.StatusConflict {
		t.Fatalf("replayed reject: status = %d, want 409 (body %s)", replay.Code, replay.Body.String())
	}
	if body := decodeError(t, replay); body.Code != "invalid_version" {
		t.Errorf("error code = %q, want invalid_version", body.Code)
	}
	requireReworkCardCount(t, st, projectID, 1)

	// A replay with the fresh version is refused by the state machine, still
	// without a second card.
	again := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
		map[string]any{"decision": "reject", "reason": "再次驳回"},
		map[string]string{"If-Match": strconv.Itoa(rejected.Version)})
	if again.Code != http.StatusConflict {
		t.Fatalf("second reject: status = %d, want 409 (body %s)", again.Code, again.Body.String())
	}
	if body := decodeError(t, again); body.Code != "invalid_state" {
		t.Errorf("error code = %q, want invalid_state", body.Code)
	}
	requireReworkCardCount(t, st, projectID, 1)

	relisted := doJSON(t, h, http.MethodGet, "/v1/work-items?project_id="+projectID+"&kind=requirement", nil)
	if relisted.Code != http.StatusOK {
		t.Fatalf("relist work items: status = %d (body %s)", relisted.Code, relisted.Body.String())
	}
	if items := decodeWorkItemList(t, relisted).Items; len(items) != 1 {
		t.Errorf("requirement items after replays = %d, want 1", len(items))
	}
}
