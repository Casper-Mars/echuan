package app

import (
	"context"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// TestDispatchCarriesAgentPreset pins the V3 routing: a claim hands the worker
// the preset that should run the card, resolved from the platform mapping.
func TestDispatchCarriesAgentPreset(t *testing.T) {
	f := newPhaseFixture(t)
	routing := platform.DefaultAgentPresetByKind()
	f.claims.SetAgentPresetDispatch(true, routing)
	f.workers.SetAgentPresetDispatch(true, routing)

	detail := f.createProject(t, "预设路由项目", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID
	state := f.env.projectState(t, projectID)
	f.env.setProjectState(t, projectID, domain.StatusTasksDraft, state.Version, nil)
	created, err := f.work.CreateWorkItem(context.Background(), CreateWorkItemInput{
		ProjectID: projectID, Kind: "dev", Title: "开发卡", BodyMD: "# 开发\n", Priority: 1,
	}, "human:cto")
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	// Manual cards land blocked; queue it so the claim path can pick it up.
	if _, err := f.env.store.Write().ExecContext(context.Background(),
		`UPDATE work_items SET state = ? WHERE work_id = ?`, "queued", created.Item.WorkID); err != nil {
		t.Fatalf("queue dev card: %v", err)
	}

	// The heartbeat path dispatches directly, so it must carry the same field.
	_, dispatch := f.register(t, "dev")
	if dispatch == nil {
		t.Fatal("no dispatch for the dev worker")
	}
	if dispatch.AgentPreset != routing["dev"] {
		t.Fatalf("dispatch.AgentPreset = %q, want %q", dispatch.AgentPreset, routing["dev"])
	}
}

// TestDispatchAgentPresetDisabled keeps the escape hatch honest: with routing
// off the field stays empty and old workers see the pre-V3 payload.
func TestDispatchAgentPresetDisabled(t *testing.T) {
	f := newPhaseFixture(t)
	f.claims.SetAgentPresetDispatch(false, nil)
	f.workers.SetAgentPresetDispatch(false, nil)

	detail := f.createProject(t, "关闭路由项目", "# 需求\n\n正文\n")
	projectID := detail.Project.ProjectID
	state := f.env.projectState(t, projectID)
	f.env.setProjectState(t, projectID, domain.StatusTasksDraft, state.Version, nil)
	created, err := f.work.CreateWorkItem(context.Background(), CreateWorkItemInput{
		ProjectID: projectID, Kind: "dev", Title: "开发卡", BodyMD: "# 开发\n", Priority: 1,
	}, "human:cto")
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := f.env.store.Write().ExecContext(context.Background(),
		`UPDATE work_items SET state = ? WHERE work_id = ?`, "queued", created.Item.WorkID); err != nil {
		t.Fatalf("queue dev card: %v", err)
	}

	_, dispatch := f.register(t, "dev")
	if dispatch == nil {
		t.Fatal("no dispatch for the dev worker")
	}
	if dispatch.AgentPreset != "" {
		t.Fatalf("dispatch.AgentPreset = %q, want empty when routing is disabled", dispatch.AgentPreset)
	}
}
