package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

const (
	gateHumanActor = "human:web"
	gateAgentActor = "agent:echuan"
)

// TestDecisionEndpointsRejectAgents pins the actor gate: the three decision
// endpoints answer 403 agent_forbidden for agent actors and keep working for
// humans (acceptance 5).
func TestDecisionEndpointsRejectAgents(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	created := doJSONHeaders(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "门禁权限",
		"requirement_md": "# 需求\n\n正文\n",
	}, map[string]string{HeaderActor: gateHumanActor})
	if created.Code != http.StatusCreated {
		t.Fatalf("create project = %d (body %s)", created.Code, created.Body.String())
	}
	detail := decodeProjectDetail(t, created)
	projectID := detail.ProjectID
	requirementID := detail.Requirement.MaterialID

	submitted := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id="+projectID, nil,
		map[string]string{HeaderActor: gateHumanActor})
	if submitted.Code != http.StatusOK {
		t.Fatalf("submit = %d (body %s)", submitted.Code, submitted.Body.String())
	}
	version := decodeProjectFromResponse(t, submitted).Version

	// ---- agent is rejected before any state is read or written -----------
	for _, target := range []string{
		"/v1/projects/decide?id=" + projectID,
		"/v1/work-items/decide",
		"/v1/work-items/review",
	} {
		body := map[string]any{"decision": "approve"}
		headers := map[string]string{HeaderActor: gateAgentActor}
		if target == "/v1/projects/decide?id="+projectID {
			headers["If-Match"] = strconv.Itoa(version)
		}
		if target == "/v1/work-items/review" {
			body["project_id"] = projectID
		}
		rec := doJSONHeaders(t, h, http.MethodPost, target, body, headers)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as agent = %d, want 403 (body %s)", target, rec.Code, rec.Body.String())
		}
		if body := decodeError(t, rec); body.Code != "agent_forbidden" {
			t.Errorf("%s code = %q, want agent_forbidden", target, body.Code)
		}
	}

	// Nothing was recorded while the agent was refused.
	var count int
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM gate_decisions`).Scan(&count); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	if count != 0 {
		t.Fatalf("gate decisions after agent requests = %d, want 0", count)
	}

	// ---- human verdict succeeds and returns material_id ------------------
	approved := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
		map[string]any{"decision": "approve"},
		map[string]string{"If-Match": strconv.Itoa(version), HeaderActor: gateHumanActor})
	if approved.Code != http.StatusOK {
		t.Fatalf("human decide = %d (body %s)", approved.Code, approved.Body.String())
	}
	var decision projectDecisionResponse
	if err := json.Unmarshal(approved.Body.Bytes(), &decision); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if decision.MaterialID == nil || *decision.MaterialID != requirementID {
		t.Fatalf("material_id = %v, want %s", decision.MaterialID, requirementID)
	}
	if decision.ProjectID != projectID {
		t.Errorf("project_id = %q, want %s", decision.ProjectID, projectID)
	}

	// ---- single-card decision: agent 403, human 200 ----------------------
	physicalProject := physicalProjectID(t, st, projectID)
	requirementPhysical := materialPhysicalID(t, st, requirementID)
	insertSubmittedCard(t, st, physicalProject, "w_api_decide", 20, requirementPhysical)

	// The board list must expose the reviewed result material so the UI can
	// show its version without a second detail call.
	listed := doJSONHeaders(t, h, http.MethodGet,
		"/v1/work-items?project_id="+projectID+"&state=submitted", nil, nil)
	var board struct {
		Items []struct {
			WorkID           string  `json:"work_id"`
			ResultMaterialID *string `json:"result_material_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &board); err != nil {
		t.Fatalf("decode board: %v", err)
	}
	if len(board.Items) != 1 || board.Items[0].ResultMaterialID == nil ||
		*board.Items[0].ResultMaterialID != requirementID {
		t.Fatalf("board result_material_id = %+v, want %s", board.Items, requirementID)
	}

	agentCard := doJSONHeaders(t, h, http.MethodPost, "/v1/work-items/decide",
		map[string]any{"work_id": "w_api_decide", "decision": "approve"},
		map[string]string{HeaderActor: gateAgentActor})
	if agentCard.Code != http.StatusForbidden {
		t.Fatalf("work-item decide as agent = %d, want 403", agentCard.Code)
	}

	cardDecision := doJSONHeaders(t, h, http.MethodPost, "/v1/work-items/decide",
		map[string]any{"work_id": "w_api_decide", "decision": "approve"},
		map[string]string{HeaderActor: gateHumanActor})
	if cardDecision.Code != http.StatusOK {
		t.Fatalf("human work-item decide = %d (body %s)", cardDecision.Code, cardDecision.Body.String())
	}
	var card workItemDecisionResponse
	if err := json.Unmarshal(cardDecision.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode work-item decision: %v", err)
	}
	if card.MaterialID == nil || *card.MaterialID != requirementID {
		t.Errorf("work-item material_id = %v, want %s", card.MaterialID, requirementID)
	}

	// ---- plan review: agent 403, human 200 -------------------------------
	// Park the project on tasks_review with a blocked dev card and an
	// accepted breakdown card carrying the plan material.
	if _, err := st.Write().ExecContext(context.Background(), `
		UPDATE projects SET status = ?, version = version + 1 WHERE id = ?`,
		domain.StatusTasksReview, physicalProject); err != nil {
		t.Fatalf("park project: %v", err)
	}
	insertCard(t, st, physicalProject, "w_api_plan", "breakdown", store.WorkItemStateAccepted, 1, &requirementPhysical)
	insertCard(t, st, physicalProject, "w_api_blocked", "dev", store.WorkItemStateBlocked, 2, nil)

	agentReview := doJSONHeaders(t, h, http.MethodPost, "/v1/work-items/review",
		map[string]any{"project_id": projectID, "decision": "approve"},
		map[string]string{HeaderActor: gateAgentActor})
	if agentReview.Code != http.StatusForbidden {
		t.Fatalf("review as agent = %d, want 403", agentReview.Code)
	}

	humanReview := doJSONHeaders(t, h, http.MethodPost, "/v1/work-items/review",
		map[string]any{"project_id": projectID, "decision": "approve"},
		map[string]string{HeaderActor: gateHumanActor})
	if humanReview.Code != http.StatusOK {
		t.Fatalf("human review = %d (body %s)", humanReview.Code, humanReview.Body.String())
	}
}

// TestGateDecisionHistoryHTTP covers the list endpoint shape, paging, filters
// and 404 (acceptance 4).
func TestGateDecisionHistoryHTTP(t *testing.T) {
	h, st := newProjectTestServer(t)
	seedProduct(t, st, "APP")

	created := doJSONHeaders(t, h, http.MethodPost, "/v1/projects", map[string]any{
		"product_id":     "APP",
		"title":          "审批历史",
		"requirement_md": "# 需求\n\n正文\n",
	}, map[string]string{HeaderActor: gateHumanActor})
	detail := decodeProjectDetail(t, created)
	projectID := detail.ProjectID
	requirementID := detail.Requirement.MaterialID

	submitted := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/submit?id="+projectID, nil,
		map[string]string{HeaderActor: gateHumanActor})
	version := decodeProjectFromResponse(t, submitted).Version
	decided := doJSONHeaders(t, h, http.MethodPost, "/v1/projects/decide?id="+projectID,
		map[string]any{"decision": "reject", "reason": "缺少验收标准"},
		map[string]string{"If-Match": strconv.Itoa(version), HeaderActor: gateHumanActor})
	if decided.Code != http.StatusOK {
		t.Fatalf("decide = %d (body %s)", decided.Code, decided.Body.String())
	}

	physicalProject := physicalProjectID(t, st, projectID)
	requirementPhysical := materialPhysicalID(t, st, requirementID)
	insertSubmittedCard(t, st, physicalProject, "w_api_history", 30, requirementPhysical)
	card := doJSONHeaders(t, h, http.MethodPost, "/v1/work-items/decide",
		map[string]any{"work_id": "w_api_history", "decision": "approve", "reason": "验收通过"},
		map[string]string{HeaderActor: gateHumanActor})
	if card.Code != http.StatusOK {
		t.Fatalf("work-item decide = %d (body %s)", card.Code, card.Body.String())
	}

	rec := doJSONHeaders(t, h, http.MethodGet, "/v1/gate-decisions?project_id="+projectID+"&limit=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("history = %d (body %s)", rec.Code, rec.Body.String())
	}
	var page gateDecisionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page = %+v, want one item and next_cursor", page)
	}
	first := page.Items[0]
	if first.SubjectType != store.GateSubjectWorkItem || first.SubjectID != "w_api_history" {
		t.Errorf("first item subject = %s/%s, want work_item/w_api_history", first.SubjectType, first.SubjectID)
	}
	if first.MaterialID == nil || *first.MaterialID != requirementID {
		t.Errorf("first item material_id = %v, want %s", first.MaterialID, requirementID)
	}
	if first.MaterialVersion == nil || *first.MaterialVersion != 1 {
		t.Errorf("first item material_version = %v, want 1", first.MaterialVersion)
	}
	if first.DecidedBy != gateHumanActor {
		t.Errorf("decided_by = %q, want %s", first.DecidedBy, gateHumanActor)
	}

	second := doJSONHeaders(t, h, http.MethodGet,
		"/v1/gate-decisions?project_id="+projectID+"&limit=1&cursor="+*page.NextCursor, nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second page = %d (body %s)", second.Code, second.Body.String())
	}
	var page2 gateDecisionListResponse
	if err := json.Unmarshal(second.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(page2.Items) != 1 || page2.NextCursor != nil {
		t.Fatalf("second page = %+v, want the last item", page2)
	}
	if page2.Items[0].SubjectType != store.GateSubjectProject || page2.Items[0].Gate != "requirement" {
		t.Errorf("second item = %+v, want the project requirement verdict", page2.Items[0])
	}
	if page2.Items[0].Reason == nil || *page2.Items[0].Reason != "缺少验收标准" {
		t.Errorf("reject reason = %v", page2.Items[0].Reason)
	}

	filtered := doJSONHeaders(t, h, http.MethodGet,
		"/v1/gate-decisions?project_id="+projectID+"&subject_type=project", nil, nil)
	var fpage gateDecisionListResponse
	if err := json.Unmarshal(filtered.Body.Bytes(), &fpage); err != nil {
		t.Fatalf("decode filtered page: %v", err)
	}
	if len(fpage.Items) != 1 || fpage.Items[0].SubjectType != store.GateSubjectProject {
		t.Errorf("subject_type filter = %+v, want only the project row", fpage.Items)
	}

	if rec := doJSONHeaders(t, h, http.MethodGet, "/v1/gate-decisions", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("missing project_id = %d, want 400", rec.Code)
	}
	if rec := doJSONHeaders(t, h, http.MethodGet, "/v1/gate-decisions?project_id=NOPE", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
	if rec := doJSONHeaders(t, h, http.MethodGet, "/v1/gate-decisions?project_id="+projectID+"&limit=0", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("limit 0 = %d, want 400", rec.Code)
	}
}

func physicalProjectID(t *testing.T, st *store.Store, projectID string) int64 {
	t.Helper()
	var id int64
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT id FROM projects WHERE project_id = ?`, projectID).Scan(&id); err != nil {
		t.Fatalf("physical project id: %v", err)
	}
	return id
}

func materialPhysicalID(t *testing.T, st *store.Store, materialID string) int64 {
	t.Helper()
	var id int64
	if err := st.Read().QueryRowContext(context.Background(),
		`SELECT id FROM materials WHERE material_id = ?`, materialID).Scan(&id); err != nil {
		t.Fatalf("physical material id: %v", err)
	}
	return id
}

func insertSubmittedCard(t *testing.T, st *store.Store, projectID int64, workID string, seq int, materialID int64) {
	t.Helper()
	insertCard(t, st, projectID, workID, "dev", store.WorkItemStateSubmitted, seq, &materialID)
}

func insertCard(t *testing.T, st *store.Store, projectID int64, workID, kind, state string, seq int, materialID *int64) {
	t.Helper()
	if _, err := st.Write().ExecContext(context.Background(), `
		INSERT INTO work_items (work_id, project_id, kind, seq_no, title, body_md, state,
		                        attempt, max_attempts, result_material_id, version, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, '# 卡片', ?, 1, 3, ?, 2, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		workID, projectID, kind, seq, "卡片 "+workID, state, materialID); err != nil {
		t.Fatalf("insert card %s: %v", workID, err)
	}
}
