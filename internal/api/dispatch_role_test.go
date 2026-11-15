package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestDispatchLeavesRoleToWorker pins the routing boundary: the platform sends
// the work-item kind and nothing else about roles. Which preset runs a card is
// the worker's (kit) decision; re-adding a preset field here would re-couple the
// platform to the agent-side preset vocabulary.
func TestDispatchLeavesRoleToWorker(t *testing.T) {
	e := newBreakdownHTTPEnv(t)

	plan := map[string]any{"plan": map[string]any{"items": []any{
		map[string]any{"seq_no": 1, "project_id": e.projectID, "title": "实现登录",
			"body_md": "# 实现登录\n", "priority": 10},
	}}}
	rec := doJSON(t, e.handler, http.MethodPost, "/v1/work-items/report", e.reportBody(plan))
	if rec.Code != http.StatusOK {
		t.Fatalf("report breakdown = %d (body %s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/review",
		map[string]any{"project_id": e.projectID, "decision": "approve"})
	if rec.Code != http.StatusOK {
		t.Fatalf("review approve = %d (body %s)", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e.handler, http.MethodPost, "/v1/work-items/claim",
		map[string]any{"worker_id": e.workerID})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim = %d (body %s)", rec.Code, rec.Body.String())
	}

	var body map[string]map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	dispatch, ok := body["dispatch"]
	if !ok {
		t.Fatalf("response has no dispatch: %s", rec.Body.String())
	}
	if _, ok := dispatch["agent_preset"]; ok {
		t.Fatalf("dispatch carries agent_preset, want the field gone: %s", rec.Body.String())
	}
	var kind string
	if err := json.Unmarshal(dispatch["kind"], &kind); err != nil || kind != "dev" {
		t.Fatalf("dispatch.kind = %q (err %v), want dev", kind, err)
	}
}
