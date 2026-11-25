package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// flowConfigYAML is the configured-flow fixture: the built-in chain with dev
// handing over to a new stage, whose task template names the upstream card. Every
// deriving stage spells out derive.auto: true and there is no flow-level switch.
const flowConfigYAML = `flow:
  stages:
    - { id: 1, name: requirement, label: 需求, material: requirement, description: 人工建卡入口, derive: { auto: true, to: design, mode: single } }
    - { id: 2, name: design, label: 方案, material: design, title: "技术方案", task: "", description: 把需求展开成技术方案, derive: { auto: true, to: breakdown, mode: single } }
    - { id: 3, name: breakdown, label: 拆解, material: plan, title: "任务拆分", task: "", description: 把方案拆成可独立验收的开发卡, derive: { auto: true, to: dev, mode: list, source: artifact, format: json } }
    - id: 4
      name: dev
      label: 开发
      material: report
      open_cards: multiple
      closing: true
      description: 实现与自测
      derive: { auto: true, to: safety_review, mode: single }
    - id: 5
      name: safety_review
      label: 安全评审
      material: report
      title: "安全评审：{上游卡标题}"
      description: 安全评审环节的阶段描述
      task: "针对 {上游卡号} 做安全评审"
      derive: { mode: none }
`

// newAPIServerWithFlow builds the router from a flow parsed by the real startup
// path, and returns the config path so a test can compare GET /v1/flow's source
// with it.
func newAPIServerWithFlow(t *testing.T, body string) (*apiServer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echuan.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := platform.Load(path)
	if err != nil {
		t.Fatalf("platform.Load: %v", err)
	}
	return newAPIServerWithConfig(t, cfg), path
}

// TestFlowEndpointMatchesConfig covers AC1/AC2 on the HTTP side: the effective
// stage table is served in configured order, field by field, together with the
// provenance of the configuration that produced it.
func TestFlowEndpointMatchesConfig(t *testing.T) {
	a, path := newAPIServerWithFlow(t, flowConfigYAML)

	resp, payload := a.do(t, http.MethodGet, "/v1/flow", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/flow = %d (%s)", resp.StatusCode, payload)
	}
	flow := decodeBody[v3FlowResponse](t, payload)

	if flow.Default {
		t.Error("default = true, want false for a configured flow")
	}
	if flow.Source != path {
		t.Errorf("source = %q, want %q", flow.Source, path)
	}
	if len(flow.Fingerprint) != 12 {
		t.Errorf("fingerprint = %q, want 12 characters", flow.Fingerprint)
	}

	// AC3/AC11 at the wire level: no flow-level "auto" any more, and every
	// stage carries a "task" key next to its "description".
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if _, ok := raw["auto"]; ok {
		t.Error(`top-level "auto" is still served; the switch is per stage now`)
	}
	rawStages, ok := raw["stages"].([]any)
	if !ok || len(rawStages) != 5 {
		t.Fatalf("payload stages = %v, want 5 objects", raw["stages"])
	}
	for i, rawStage := range rawStages {
		stage, ok := rawStage.(map[string]any)
		if !ok {
			t.Fatalf("stage %d = %v, want an object", i, rawStage)
		}
		if _, ok := stage["task"]; !ok {
			t.Errorf("stage %d (%v) has no \"task\" key", i, stage["name"])
		}
	}

	want := []struct {
		id        int
		name      string
		label     string
		material  string
		openCards string
		closing   bool
		title     string
		desc      string
		task      string
		mode      string
		to        string
	}{
		{1, "requirement", "需求", "requirement", "single", false, "", "人工建卡入口", "", "single", "design"},
		{2, "design", "方案", "design", "single", false, "技术方案", "把需求展开成技术方案", "", "single", "breakdown"},
		{3, "breakdown", "拆解", "plan", "single", false, "任务拆分", "把方案拆成可独立验收的开发卡", "", "list", "dev"},
		{4, "dev", "开发", "report", "multiple", true, "", "实现与自测", "", "single", "safety_review"},
		{5, "safety_review", "安全评审", "report", "single", false, "安全评审：{上游卡标题}", "安全评审环节的阶段描述", "针对 {上游卡号} 做安全评审", "none", ""},
	}
	if len(flow.Stages) != len(want) {
		t.Fatalf("stages = %d, want %d", len(flow.Stages), len(want))
	}
	for i, w := range want {
		got := flow.Stages[i]
		if got.ID != w.id || got.Name != w.name || got.Label != w.label || got.Material != w.material {
			t.Errorf("stage %d = id=%d name=%q label=%q material=%q, want %d/%s/%s/%s",
				i, got.ID, got.Name, got.Label, got.Material, w.id, w.name, w.label, w.material)
		}
		if got.OpenCards != w.openCards || got.Closing != w.closing {
			t.Errorf("stage %s open_cards=%q closing=%t, want %q/%t",
				got.Name, got.OpenCards, got.Closing, w.openCards, w.closing)
		}
		if got.Title != w.title || got.Description != w.desc || got.Task != w.task {
			t.Errorf("stage %s title=%q description=%q task=%q, want %q/%q/%q",
				got.Name, got.Title, got.Description, got.Task, w.title, w.desc, w.task)
		}
		if got.Derive.Mode != w.mode || got.Derive.To != w.to {
			t.Errorf("stage %s derive=mode=%q to=%q, want %q/%q",
				got.Name, got.Derive.Mode, got.Derive.To, w.mode, w.to)
		}
	}
}

// TestDefaultFlowEndpoint serves the built-in flow when the configuration has no
// flow section, and marks it as the default (the web picker's fallback).
func TestDefaultFlowEndpoint(t *testing.T) {
	a := newAPIServer(t)
	resp, payload := a.do(t, http.MethodGet, "/v1/flow", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/flow = %d (%s)", resp.StatusCode, payload)
	}
	flow := decodeBody[v3FlowResponse](t, payload)
	if !flow.Default || flow.Source != platform.BuiltinFlowSource {
		t.Errorf("default=%t source=%q, want the built-in default", flow.Default, flow.Source)
	}
	names := make([]string, 0, len(flow.Stages))
	for _, stage := range flow.Stages {
		names = append(names, stage.Name)
	}
	if strings.Join(names, ",") != "requirement,design,breakdown,dev" {
		t.Errorf("stage names = %v, want the built-in chain", names)
	}
}

// TestCreateTaskByStageSpelling covers §7.3's three accepted spellings and the
// outbound kind projection (§7.4): name, label and numeric id all land on the
// same stage id, and the response carries kind/kind_id/kind_label/material_kind.
func TestCreateTaskByStageSpelling(t *testing.T) {
	a := newAPIServerWithFlowServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "safety_review", "title": "安全评审卡",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create by name = %d (%s)", resp.StatusCode, payload)
	}
	byName := decodeBody[v3TaskResponse](t, payload)
	if byName.Kind != "safety_review" || byName.KindID != 5 || byName.KindLabel != "安全评审" || byName.MaterialKind != "report" {
		t.Errorf("create by name = kind=%q kind_id=%d kind_label=%q material_kind=%q",
			byName.Kind, byName.KindID, byName.KindLabel, byName.MaterialKind)
	}

	// A second single-open card of the same stage is a 409 by design; use a
	// fresh project for the label spelling.
	_, payload = a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p2"})
	project2 := decodeBody[v3ProjectResponse](t, payload)
	resp, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project2.ProjectID, "kind": "安全评审", "title": "按显示名建卡",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create by label = %d (%s)", resp.StatusCode, payload)
	}
	byLabel := decodeBody[v3TaskResponse](t, payload)
	if byLabel.Kind != "safety_review" || byLabel.KindID != 5 {
		t.Errorf("create by label = kind=%q kind_id=%d, want safety_review/5", byLabel.Kind, byLabel.KindID)
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "4", "title": "按数字 id 建卡",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create by id = %d (%s)", resp.StatusCode, payload)
	}
	byID := decodeBody[v3TaskResponse](t, payload)
	if byID.Kind != "dev" || byID.KindID != 4 {
		t.Errorf("create by id = kind=%q kind_id=%d, want dev/4", byID.Kind, byID.KindID)
	}
}

// TestUnknownStageNameListsAvailable covers the 400 of §7.1: a spelling the flow
// does not know answers with the usable names, not a bare "invalid kind".
func TestUnknownStageNameListsAvailable(t *testing.T) {
	a := newAPIServerWithFlowServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	resp, payload := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "safety_review!", "title": "x",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d (%s)", resp.StatusCode, payload)
	}
	body := decodeBody[ErrorBody](t, payload)
	if len(body.Details) == 0 || body.Details[0].Field != "kind" {
		t.Fatalf("details = %+v, want field kind", body.Details)
	}
	for _, want := range []string{"requirement(需求)", "safety_review(安全评审)"} {
		if !strings.Contains(string(payload), want) {
			t.Errorf("error body does not list %q: %s", want, payload)
		}
	}
}

// TestClaimAndHeartbeatByStageName covers AC5's platform half: the heartbeat
// declares the new stage by name, and claim leases the card by the same name.
func TestClaimAndHeartbeatByStageName(t *testing.T) {
	a := newAPIServerWithFlowServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)
	_, _ = a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
		"project_id": project.ProjectID, "kind": "safety_review", "title": "安全评审卡",
	})

	resp, payload := a.do(t, http.MethodPost, "/v1/workers/heartbeat", map[string]any{
		"worker_id": "w_sec", "kinds": []string{"safety_review"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("heartbeat = %d (%s)", resp.StatusCode, payload)
	}
	resp, payload = a.do(t, http.MethodGet, "/v1/workers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list workers = %d (%s)", resp.StatusCode, payload)
	}
	workers := decodeBody[v3WorkerListResponse](t, payload)
	if len(workers.Items) != 1 || len(workers.Items[0].Kinds) != 1 || workers.Items[0].Kinds[0] != "safety_review" {
		t.Fatalf("worker kinds = %+v, want [safety_review]", workers.Items)
	}

	resp, payload = a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
		"worker_id": "w_sec", "kind": "safety_review",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d (%s)", resp.StatusCode, payload)
	}
	envelope := decodeBody[v3ClaimResponse](t, payload)
	if envelope.Task.Kind != "safety_review" || envelope.Task.KindID != 5 || envelope.Task.KindLabel != "安全评审" {
		t.Errorf("claimed kind = %q/%d/%q, want safety_review/5/安全评审",
			envelope.Task.Kind, envelope.Task.KindID, envelope.Task.KindLabel)
	}
}

// TestUnknownKindRendersAsNumber covers §7.4's history rule: a card whose stage
// the configuration no longer has still lists, as kind(<id>).
func TestUnknownKindRendersAsNumber(t *testing.T) {
	a := newAPIServer(t)
	a.seedProduct(t, "APP")
	_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{"product_id": "APP", "name": "p"})
	project := decodeBody[v3ProjectResponse](t, payload)

	// Written through the store: the HTTP create path refuses stage ids the flow
	// does not know, which is exactly why such a row can only be history.
	if _, err := storev3.CreateTask(context.Background(), a.store, storev3.CreateTaskInput{
		ProjectID: project.ProjectID, TaskID: "t_legacy", Kind: enum.TaskKind(9),
		Title: "历史卡", MaxAttempts: 3, Now: "2026-11-18T00:00:00.000Z",
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	resp, payload := a.do(t, http.MethodGet, "/v1/tasks?id=t_legacy", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get task = %d (%s)", resp.StatusCode, payload)
	}
	task := decodeBody[v3TaskResponse](t, payload)
	if task.Kind != "kind(9)" || task.KindLabel != "kind(9)" || task.KindID != 9 || task.MaterialKind != "" {
		t.Errorf("legacy task = kind=%q kind_label=%q kind_id=%d material_kind=%q",
			task.Kind, task.KindLabel, task.KindID, task.MaterialKind)
	}
}

// newAPIServerWithFlowServer is the flow-configured server plus the config body
// this file always uses.
func newAPIServerWithFlowServer(t *testing.T) *apiServer {
	t.Helper()
	server, _ := newAPIServerWithFlow(t, flowConfigYAML)
	return server
}
