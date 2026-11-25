package platform

import (
	"strings"
	"testing"
)

// TestDefaultFlowProfile pins the built-in default flow field by field: it is
// what "no flow section" must reproduce, including the explicit empty task
// templates that keep AC4 byte-for-byte compatible and the per-stage derive
// switches that keep the chain alive after the flow-level switch was removed.
func TestDefaultFlowProfile(t *testing.T) {
	flow := DefaultFlowProfile()

	if flow.Source != BuiltinFlowSource {
		t.Errorf("Source = %q, want %q", flow.Source, BuiltinFlowSource)
	}
	if flow.DefaultDescription != nil {
		t.Errorf("DefaultDescription = %q, want unset", *flow.DefaultDescription)
	}

	want := []struct {
		id          int
		name        string
		label       string
		material    string
		open        OpenCards
		closing     bool
		title       string
		description string
		taskSet     bool
		task        string
		mode        DeriveMode
		to          string
		auto        bool
	}{
		{1, "requirement", "需求", "requirement", OpenCardsSingle, false, "", "人工建卡入口，不自动派生下游", false, "", DeriveSingle, "design", true},
		{2, "design", "方案", "design", OpenCardsSingle, false, "技术方案", "把需求展开成技术方案：目标与范围、接口与数据、风险与验证方式", true, "", DeriveSingle, "breakdown", true},
		{3, "breakdown", "拆解", "plan", OpenCardsSingle, false, "任务拆分", "把方案拆成可独立验收的开发卡", true, "", DeriveList, "dev", true},
		{4, "dev", "开发", "report", OpenCardsMultiple, true, "", "实现与自测，交付物写入任务报告", false, "", DeriveNone, "", false},
	}
	if len(flow.Stages) != len(want) {
		t.Fatalf("len(Stages) = %d, want %d", len(flow.Stages), len(want))
	}
	for i, w := range want {
		got := flow.Stages[i]
		if got.ID != w.id || got.Name != w.name || got.Label != w.label || got.Material != w.material {
			t.Errorf("stage %d = id=%d name=%q label=%q material=%q, want id=%d name=%s label=%s material=%s",
				i, got.ID, got.Name, got.Label, got.Material, w.id, w.name, w.label, w.material)
		}
		if got.OpenCards != w.open {
			t.Errorf("stage %s open_cards = %q, want %q", got.Name, got.OpenCards, w.open)
		}
		if got.Closing != w.closing {
			t.Errorf("stage %s closing = %t, want %t", got.Name, got.Closing, w.closing)
		}
		if got.Title != w.title {
			t.Errorf("stage %s title = %q, want %q", got.Name, got.Title, w.title)
		}
		if got.Description != w.description {
			t.Errorf("stage %s description = %q, want %q", got.Name, got.Description, w.description)
		}
		if w.taskSet != (got.Task != nil) {
			t.Fatalf("stage %s task present = %t, want %t", got.Name, got.Task != nil, w.taskSet)
		}
		if w.taskSet && *got.Task != w.task {
			t.Errorf("stage %s task = %q, want %q", got.Name, *got.Task, w.task)
		}
		if got.Derive.Mode != w.mode || got.Derive.To != w.to || got.Derive.Auto != w.auto {
			t.Errorf("stage %s derive = mode=%q to=%q auto=%t, want mode=%q to=%q auto=%t",
				got.Name, got.Derive.Mode, got.Derive.To, got.Derive.Auto, w.mode, w.to, w.auto)
		}
	}

	// The list derivation of breakdown reads the plan artifact, which is what
	// the hard-coded flow of the previous release did.
	if d := flow.Stages[2].Derive; d.Source != "artifact" || d.Format != "json" {
		t.Errorf("breakdown derive = source=%q format=%q, want artifact/json", d.Source, d.Format)
	}
	if warnings := flow.SwitchWarnings(); len(warnings) != 0 {
		t.Errorf("SwitchWarnings() = %v, want none", warnings)
	}
}

// TestLoadWithoutFlowSectionUsesDefault covers AC4's precondition: deleting the
// flow section leaves the effective flow identical to the built-in one, and the
// fingerprint does not move between loads.
func TestLoadWithoutFlowSectionUsesDefault(t *testing.T) {
	clearEnv(t)

	body := "listen: \"127.0.0.1:8720\"\ndata_dir: \"/tmp/echuan-flow-default\"\n"
	path := writeConfig(t, body)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := DefaultFlowProfile()
	if cfg.Flow.Source != BuiltinFlowSource {
		t.Errorf("Flow.Source = %q, want %q", cfg.Flow.Source, BuiltinFlowSource)
	}
	if got, wantFP := cfg.Flow.Fingerprint(), want.Fingerprint(); got != wantFP {
		t.Errorf("Fingerprint = %q, want %q", got, wantFP)
	}
	if len(cfg.Flow.Stages) != len(want.Stages) {
		t.Fatalf("len(Stages) = %d, want %d", len(cfg.Flow.Stages), len(want.Stages))
	}
	for i := range want.Stages {
		got, exp := cfg.Flow.Stages[i], want.Stages[i]
		if got.ID != exp.ID || got.Name != exp.Name || got.Label != exp.Label ||
			got.Material != exp.Material || got.OpenCards != exp.OpenCards ||
			got.Closing != exp.Closing || got.Title != exp.Title ||
			got.Description != exp.Description ||
			got.Derive.Mode != exp.Derive.Mode || got.Derive.To != exp.Derive.To ||
			got.Derive.Auto != exp.Derive.Auto ||
			got.Derive.Source != exp.Derive.Source || got.Derive.Format != exp.Derive.Format {
			t.Errorf("stage %d = %+v, want %+v", i, got, exp)
		}
		if (got.Task == nil) != (exp.Task == nil) {
			t.Errorf("stage %d task presence differs", i)
		}
	}

	again, err := Load(path)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if again.Flow.Fingerprint() != cfg.Flow.Fingerprint() {
		t.Errorf("fingerprint is not stable: %q != %q", again.Flow.Fingerprint(), cfg.Flow.Fingerprint())
	}
}

// TestFlowFromConfigFile parses every supported field, including a fanout
// derivation with entries, priorities and sibling dependencies, and the two
// independent text fields (stage description vs task template).
func TestFlowFromConfigFile(t *testing.T) {
	clearEnv(t)

	body := `listen: "127.0.0.1:8720"
data_dir: "/tmp/echuan-flow-parse"
flow:
  default_description: "请按上游交付物完成本环节工作"
  stages:
    - id: 1
      name: requirement
      label: 需求
      material: requirement
      description: 人工建卡入口
      derive: { auto: true, to: design, mode: single }
    - id: 2
      name: design
      label: 方案
      material: design
      title: "技术方案：{上游卡标题}"
      description: 把需求展开成技术方案
      task: "请按 {上游卡标题} 出方案"
      derive:
        auto: true
        to: review
        mode: fanout
        items:
          - { key: security, title: "安全评审：{上游卡标题}", priority: 2 }
          - { key: perf, title: "性能评审：{上游卡标题}", depends_on: [security] }
    - id: 5
      name: review
      label: 评审
      material: report
      open_cards: multiple
      closing: true
      description: 安全与性能评审
      derive: { mode: none }
`
	path := writeConfig(t, body)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	flow := cfg.Flow
	if flow.Source != path {
		t.Errorf("Source = %q, want %q", flow.Source, path)
	}
	if flow.DefaultDescription == nil || *flow.DefaultDescription != "请按上游交付物完成本环节工作" {
		t.Errorf("DefaultDescription = %v", flow.DefaultDescription)
	}

	requirement, ok := flow.ByName("requirement")
	if !ok {
		t.Fatal("stage requirement not found")
	}
	if !requirement.Derive.Auto {
		t.Error("requirement derive.auto = false, want true")
	}
	if requirement.Description != "人工建卡入口" || requirement.Task != nil {
		t.Errorf("requirement description/task = %q/%v, want the stage description and no task", requirement.Description, requirement.Task)
	}

	design, ok := flow.ByName("design")
	if !ok {
		t.Fatal("stage design not found")
	}
	if design.ID != 2 || design.Label != "方案" || design.Material != "design" {
		t.Errorf("design = id=%d label=%q material=%q", design.ID, design.Label, design.Material)
	}
	if design.Derive.Mode != DeriveFanout || design.Derive.To != "review" || !design.Derive.Auto {
		t.Errorf("design derive = %+v", design.Derive)
	}
	if design.Description != "把需求展开成技术方案" {
		t.Errorf("design.Description = %q", design.Description)
	}
	if design.Task == nil || *design.Task != "请按 {上游卡标题} 出方案" {
		t.Errorf("design.Task = %v", design.Task)
	}
	if len(design.Derive.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(design.Derive.Items))
	}
	security, perf := design.Derive.Items[0], design.Derive.Items[1]
	if security.Key != "security" || security.Priority != 2 || security.Title != "安全评审：{上游卡标题}" {
		t.Errorf("items[0] = %+v", security)
	}
	if perf.Key != "perf" || len(perf.DependsOn) != 1 || perf.DependsOn[0] != "security" {
		t.Errorf("items[1] = %+v", perf)
	}

	review, ok := flow.ByID(5)
	if !ok {
		t.Fatal("stage 5 not found")
	}
	if review.OpenCards != OpenCardsMultiple || !review.Closing {
		t.Errorf("review = open_cards=%q closing=%t", review.OpenCards, review.Closing)
	}
	if review.Derive.Auto {
		t.Error("review derive.auto = true, want false (a chain tail needs no switch)")
	}
	if names := strings.Join(flow.Names(), ","); names != "requirement,design,review" {
		t.Errorf("Names() = %v", flow.Names())
	}
	if ids := flow.IDs(); len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 5 {
		t.Errorf("IDs() = %v", ids)
	}
	if got := flow.Label("design"); got != "方案" {
		t.Errorf("Label(design) = %q", got)
	}
	if got := flow.Label("review"); got != "评审" {
		t.Errorf("Label(review) = %q", got)
	}
	if got := flow.Label("missing"); got != "missing" {
		t.Errorf("Label(missing) = %q", got)
	}
}

// TestFlowFingerprint pins the fingerprint's properties (scheme 2): stable for
// identical content, different as soon as anything that changes a card changes,
// and blind to the stage description, which changes no card.
func TestFlowFingerprint(t *testing.T) {
	base := DefaultFlowProfile()
	if len(base.Fingerprint()) != 12 {
		t.Fatalf("Fingerprint() = %q, want 12 hex characters", base.Fingerprint())
	}
	if base.Fingerprint() != DefaultFlowProfile().Fingerprint() {
		t.Error("fingerprint is not deterministic")
	}

	changed := DefaultFlowProfile()
	changed.Stages[1].Title = "技术方案（改）"
	if changed.Fingerprint() == base.Fingerprint() {
		t.Error("changing a stage title must change the fingerprint")
	}

	renamed := DefaultFlowProfile()
	renamed.Stages[2].Label = "拆解（改）"
	if renamed.Fingerprint() == base.Fingerprint() {
		t.Error("changing a stage label must change the fingerprint")
	}

	// D6: the switch is part of the derive string, so flipping it must move the
	// digest even though nothing else changed.
	switchOff := DefaultFlowProfile()
	switchOff.Stages[0].Derive.Auto = false
	if switchOff.Fingerprint() == base.Fingerprint() {
		t.Error("flipping derive.auto must change the fingerprint")
	}

	explicitEmpty := DefaultFlowProfile()
	empty := ""
	explicitEmpty.Stages[0].Task = &empty
	if explicitEmpty.Fingerprint() == base.Fingerprint() {
		t.Error("setting an explicit empty task template must change the fingerprint")
	}

	described := DefaultFlowProfile()
	described.Stages[0].Description = "换一句阶段描述"
	if described.Fingerprint() != base.Fingerprint() {
		t.Error("the stage description is display text and must not enter the fingerprint")
	}
}

// TestFlowChainSummary pins the startup log rendering (§8.1 step 6): every
// deriving stage prints its own switch, because "to/mode but auto off" is the
// silent misconfiguration this release introduced (R-C).
func TestFlowChainSummary(t *testing.T) {
	got := DefaultFlowProfile().ChainSummary()
	want := []string{
		"1:requirement(需求) -> 2:design(方案) (single, auto=on)",
		"2:design(方案) -> 3:breakdown(拆解) (single, auto=on)",
		"3:breakdown(拆解) -> 4:dev(开发) (list, auto=on)",
		"4:dev(开发) (none)",
	}
	if len(got) != len(want) {
		t.Fatalf("ChainSummary() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ChainSummary()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	off := DefaultFlowProfile()
	off.Stages[0].Derive.Auto = false
	if line := off.ChainSummary()[0]; !strings.Contains(line, "auto=off") {
		t.Errorf("ChainSummary()[0] = %q, want it to show auto=off", line)
	}
}

// TestSwitchWarnings covers W1 (§10.2): a stage with the switch on and nothing
// to derive is a warning, one line per stage, and never an error.
func TestSwitchWarnings(t *testing.T) {
	clearEnv(t)

	body := "flow:\n" +
		"  stages:\n" +
		"    - id: 1\n" +
		"      name: requirement\n" +
		"      material: requirement\n" +
		"      derive: { auto: true, to: design, mode: single }\n" +
		"    - id: 2\n" +
		"      name: design\n" +
		"      material: design\n" +
		"      task: \"\"\n" +
		"      derive: { auto: true, to: breakdown, mode: single }\n" +
		"    - id: 3\n" +
		"      name: breakdown\n" +
		"      material: plan\n" +
		"      task: \"\"\n" +
		"      derive: { auto: true, mode: none }\n"
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Exactly one stage has the switch on with nothing to derive.
	warnings := cfg.Flow.SwitchWarnings()
	if len(warnings) != 1 {
		t.Fatalf("SwitchWarnings() = %v, want exactly one", warnings)
	}
	if warnings[0].Stage != "breakdown" || warnings[0].Mode != DeriveNone {
		t.Errorf("warning = %+v, want stage=breakdown mode=none", warnings[0])
	}

	// A switched-off stage is not a warning: that is the documented default.
	quiet := DefaultFlowProfile()
	quiet.Stages[0].Derive.Auto = false
	if warnings := quiet.SwitchWarnings(); len(warnings) != 0 {
		t.Errorf("SwitchWarnings() = %v, want none for a switched-off stage", warnings)
	}
}

// ptrStage returns a pointer to a copy of s, so table entries cannot share state.
func ptrStage(s Stage) *Stage { return &s }

// TestResolveDescription covers the three-level resolution of §8.4 and the one
// branch that must not fall back: a list entry with an empty body. The stage's
// human-readable Description is never a candidate (D2).
func TestResolveDescription(t *testing.T) {
	empty := ""
	stageTask := "针对 {上游卡号} 做安全评审"
	defaultDesc := "请按上游交付物完成本环节工作"
	withTask := func(task *string) *Stage {
		return ptrStage(Stage{Name: "design", Description: "阶段描述不进卡", Task: task})
	}

	tests := []struct {
		name       string
		flow       FlowConfig
		dst        *Stage
		mode       DeriveMode
		item       *DeriveItem
		want       string
		wantSource DescriptionSource
	}{
		{
			name:       "list entry body wins",
			flow:       FlowConfig{},
			dst:        withTask(&stageTask),
			mode:       DeriveList,
			item:       &DeriveItem{BodyMD: "实现登录页"},
			want:       "实现登录页",
			wantSource: DescriptionFromItem,
		},
		{
			name:       "list with an empty body stays empty and does not fall back",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(&stageTask),
			mode:       DeriveList,
			item:       &DeriveItem{BodyMD: ""},
			want:       "",
			wantSource: DescriptionEmpty,
		},
		{
			name:       "list without an entry stays empty",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(&stageTask),
			mode:       DeriveList,
			item:       nil,
			want:       "",
			wantSource: DescriptionEmpty,
		},
		{
			name:       "single falls back to the stage task template",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(&stageTask),
			mode:       DeriveSingle,
			item:       nil,
			want:       stageTask,
			wantSource: DescriptionFromStage,
		},
		{
			name:       "single without a stage task template falls back to the flow default",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(nil),
			mode:       DeriveSingle,
			item:       nil,
			want:       defaultDesc,
			wantSource: DescriptionFromDefault,
		},
		{
			name:       "explicit empty stage task template is honoured",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(&empty),
			mode:       DeriveSingle,
			item:       nil,
			want:       "",
			wantSource: DescriptionFromStage,
		},
		{
			name:       "fanout entry body wins over the stage task template",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(&stageTask),
			mode:       DeriveFanout,
			item:       &DeriveItem{BodyMD: "按安全基线评审"},
			want:       "按安全基线评审",
			wantSource: DescriptionFromItem,
		},
		{
			name:       "fanout with an empty body falls back to the stage task template",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        withTask(&stageTask),
			mode:       DeriveFanout,
			item:       &DeriveItem{},
			want:       stageTask,
			wantSource: DescriptionFromStage,
		},
		{
			name:       "nothing configured yields an empty task description",
			flow:       FlowConfig{},
			dst:        withTask(nil),
			mode:       DeriveSingle,
			item:       nil,
			want:       "",
			wantSource: DescriptionEmpty,
		},
		{
			name:       "a nil target still gets the flow default",
			flow:       FlowConfig{DefaultDescription: &defaultDesc},
			dst:        nil,
			mode:       DeriveSingle,
			item:       nil,
			want:       defaultDesc,
			wantSource: DescriptionFromDefault,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, source := tc.flow.ResolveDescription(tc.dst, tc.mode, tc.item)
			if got != tc.want {
				t.Errorf("description = %q, want %q", got, tc.want)
			}
			if source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}
}

// TestSingleDerivationAcceptsExplicitEmptyTask pins the R16 escape hatch:
// task: "" is a deliberate "no task description", not a missing one, and it is
// what keeps the default flow (and AC4) possible.
func TestSingleDerivationAcceptsExplicitEmptyTask(t *testing.T) {
	clearEnv(t)

	body := `flow:
  stages:
    - id: 1
      name: requirement
      material: requirement
      description: 人工建卡入口
      derive: { auto: true, to: design, mode: single }
    - id: 2
      name: design
      material: design
      description: 把需求展开成技术方案
      task: ""
      derive: { mode: none }
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	design, ok := cfg.Flow.ByName("design")
	if !ok {
		t.Fatal("stage design not found")
	}
	if design.Task == nil || *design.Task != "" {
		t.Fatalf("design.Task = %v, want an explicit empty string", design.Task)
	}
	if design.Description != "把需求展开成技术方案" {
		t.Errorf("design.Description = %q, want the stage description kept apart", design.Description)
	}
}

// TestFlowDefaultDescriptionSatisfiesR16 covers the other R16 branch: a flow
// level default is enough, no per-stage task template needed.
func TestFlowDefaultDescriptionSatisfiesR16(t *testing.T) {
	clearEnv(t)

	body := `flow:
  default_description: "请按上游交付物完成 {环节名} 的工作"
  stages:
    - id: 1
      name: requirement
      material: requirement
      derive: { auto: true, to: design, mode: single }
    - id: 2
      name: design
      material: design
      description: 把需求展开成技术方案
      derive: { mode: none }
`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	design, _ := cfg.Flow.ByName("design")
	got, source := cfg.Flow.ResolveDescription(&design, DeriveSingle, nil)
	if source != DescriptionFromDefault {
		t.Errorf("source = %q, want %q", source, DescriptionFromDefault)
	}
	if !strings.Contains(got, "{环节名}") {
		t.Errorf("description = %q, want the unrendered template", got)
	}
}
