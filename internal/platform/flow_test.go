package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echuan.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// markerLine returns the 1-based line holding marker. Fixtures put a trailing
// "# @<marker>" comment on the line the rule is expected to point at, so the
// tests can assert "<path>:<line>:" precisely without hard-coding line numbers.
func markerLine(t *testing.T, body, marker string) int {
	t.Helper()
	for i, line := range strings.Split(body, "\n") {
		if strings.Contains(line, marker) {
			return i + 1
		}
	}
	t.Fatalf("marker %q not found in fixture:\n%s", marker, body)
	return 0
}

// TestFlowValidationRules covers every startup rule R1–R17 at least once and
// asserts the failure is reported as "<path>:<line>: <reason>" on the offending
// line (§8.6 / AC9).
func TestFlowValidationRules(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		rule   string
		marker string
		want   string
	}{
		{
			name:   "R1 empty stage table",
			yaml:   "flow:\n  stages: []   # @m\n",
			rule:   "R1",
			marker: "@m",
			want:   "flow.stages: must declare at least one stage",
		},
		{
			name: "R2 id is required",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - name: requirement   # @m\n" +
				"      material: requirement\n" +
				"      derive: { mode: none }\n",
			rule:   "R2",
			marker: "@m",
			want:   "flow.stages[1].id: is required",
		},
		{
			name: "R2 id must be positive",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 0   # @m\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { mode: none }\n",
			rule:   "R2",
			marker: "@m",
			want:   "must be a positive integer, got 0",
		},
		{
			name: "R2 duplicate id",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { mode: none }\n" +
				"    - id: 1   # @m\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      derive: { mode: none }\n",
			rule:   "R2",
			marker: "@m",
			want:   "duplicate id 1",
		},
		{
			name: "R3 name is required",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5   # @m\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R3",
			marker: "@m",
			want:   "flow.stages[1].name: is required",
		},
		{
			name: "R3 name must not be blank",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: \"   \"   # @m\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R3",
			marker: "@m",
			want:   "must not be blank",
		},
		{
			name: "R3 name too long",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: \"abcdefghijklmnopqrstuvwxyz0123456\"   # @m\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R3",
			marker: "@m",
			want:   "must be at most 32 characters, got 33",
		},
		{
			name: "R3 duplicate name",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n" +
				"    - id: 6\n" +
				"      name: review   # @m\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R3",
			marker: "@m",
			want:   "duplicate stage name \"review\"",
		},
		{
			name: "R4 label must not be blank",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      label: \"\"   # @m\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R4",
			marker: "@m",
			want:   "flow.stages[1].label: must not be blank",
		},
		{
			name: "R4 label collides with a stage name",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      label: 需求\n" +
				"      material: requirement\n" +
				"      derive: { mode: none }\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      label: requirement   # @m\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R4",
			marker: "@m",
			want:   "is already used by flow.stages[1] as a stage name or label",
		},
		{
			name: "R5 material must be a known category",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: spec   # @m\n" +
				"      derive: { mode: none }\n",
			rule:   "R5",
			marker: "@m",
			want:   "must be one of requirement, design, plan, report, context, other",
		},
		{
			name: "R5 material is required",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5   # @m\n" +
				"      name: review\n" +
				"      derive: { mode: none }\n",
			rule:   "R5",
			marker: "@m",
			want:   "got \"\"",
		},
		{
			name: "R6 mode must be known",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { to: review, mode: chain }   # @m\n",
			rule:   "R6",
			marker: "@m",
			want:   "derive.mode: must be one of none, single, list, fanout",
		},
		{
			name: "R6 list requires source artifact",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 3\n" +
				"      name: breakdown\n" +
				"      material: plan\n" +
				"      derive: { to: dev, mode: list, format: json }   # @m\n" +
				"    - id: 4\n" +
				"      name: dev\n" +
				"      material: report\n" +
				"      open_cards: multiple\n" +
				"      derive: { mode: none }\n",
			rule:   "R6",
			marker: "@m",
			want:   "derive.source: must be \"artifact\"",
		},
		{
			name: "R6 list format must be json or yaml",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 3\n" +
				"      name: breakdown\n" +
				"      material: plan\n" +
				"      derive: { to: dev, mode: list, source: artifact, format: xml }   # @m\n" +
				"    - id: 4\n" +
				"      name: dev\n" +
				"      material: report\n" +
				"      open_cards: multiple\n" +
				"      derive: { mode: none }\n",
			rule:   "R6",
			marker: "@m",
			want:   "derive.format: must be one of json, yaml",
		},
		{
			name: "R6 fanout needs items",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive:\n" +
				"        to: review\n" +
				"        mode: fanout\n" +
				"        items: []   # @m\n",
			rule:   "R6",
			marker: "@m",
			want:   "derive.items: must list at least one entry",
		},
		{
			name: "R6 fanout item keys are unique",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 2\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      derive:\n" +
				"        to: review\n" +
				"        mode: fanout\n" +
				"        items:\n" +
				"          - { key: security, title: 安全评审 }\n" +
				"          - { key: security, title: 性能评审 }   # @m\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      open_cards: multiple\n" +
				"      derive: { mode: none }\n",
			rule:   "R6",
			marker: "@m",
			want:   "duplicate entry key \"security\"",
		},
		{
			name: "R6 items only make sense for fanout",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { to: design, mode: single, items: [{ key: a }] }   # @m\n" +
				"    - id: 2\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      task: \"\"\n" +
				"      derive: { mode: none }\n",
			rule:   "R6",
			marker: "@m",
			want:   "derive.items: only allowed when mode is fanout",
		},
		{
			name: "R7 derive.to is required",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { mode: single }   # @m\n",
			rule:   "R7",
			marker: "@m",
			want:   "derive.to: is required when mode is single",
		},
		{
			name: "R7 derive.to must exist",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { to: nowhere, mode: single }   # @m\n",
			rule:   "R7",
			marker: "@m",
			want:   "unknown stage \"nowhere\"; available stages: review",
		},
		{
			name: "R7 derive.to must not point at itself",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { to: review, mode: single }   # @m\n",
			rule:   "R7",
			marker: "@m",
			want:   "must not point back at the stage itself",
		},
		{
			name: "R7 derive.to is rejected when nothing is derived",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { to: design, mode: none }   # @m\n",
			rule:   "R7",
			marker: "@m",
			want:   "derive.to: must not be set when mode is none",
		},
		{
			name: "R8 derivation cycle",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: alpha\n" +
				"      material: report\n" +
				"      task: \"\"\n" +
				"      derive: { to: beta, mode: single }\n" +
				"    - id: 6\n" +
				"      name: beta\n" +
				"      material: report\n" +
				"      task: \"\"\n" +
				"      derive: { to: alpha, mode: single }   # @m\n",
			rule:   "R8",
			marker: "@m",
			want:   "derivation cycle alpha -> beta -> alpha",
		},
		{
			name: "R9 multi-card target must allow parallel cards",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 3\n" +
				"      name: breakdown\n" +
				"      material: plan\n" +
				"      derive: { to: dev, mode: list, source: artifact, format: json }   # @m\n" +
				"    - id: 4\n" +
				"      name: dev\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R9",
			marker: "@m",
			want:   "must set open_cards: multiple",
		},
		{
			name: "R10 title template variable whitelist",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { to: design, mode: single }\n" +
				"    - id: 2\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      title: 方案：{上游摘要}   # @m\n" +
				"      task: \"\"\n" +
				"      derive: { mode: none }\n",
			rule:   "R10",
			marker: "@m",
			want:   "unknown template variable {上游摘要}",
		},
		{
			name: "R10 entry variables need a fanout target",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { to: design, mode: single }\n" +
				"    - id: 2\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      title: 方案：{条目标题}   # @m\n" +
				"      task: \"\"\n" +
				"      derive: { mode: none }\n",
			rule:   "R10",
			marker: "@m",
			want:   "可用变量: {上游卡号} {上游卡标题} {项目名} {产品名} {环节名}",
		},
		{
			name: "R11 unused template is rejected",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      title: 评审   # @m\n" +
				"      derive: { mode: none }\n",
			rule:   "R11",
			marker: "@m",
			want:   "is not the target of any derive.to",
		},
		{
			name: "R12 at most one closing stage",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 4\n" +
				"      name: dev\n" +
				"      material: report\n" +
				"      closing: true\n" +
				"      derive: { mode: none }\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      closing: true   # @m\n" +
				"      derive: { mode: none }\n",
			rule:   "R12",
			marker: "@m",
			want:   "at most one stage may set closing: true",
		},

		{
			name: "R14 unknown flow field",
			yaml: "flow:\n" +
				"  unknown_key: 1   # @m\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R14",
			marker: "@m",
			want:   "unknown field \"unknown_key\"",
		},
		{
			name: "R14 unknown stage field",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      descripton: \"\"   # @m\n" +
				"      derive: { mode: none }\n",
			rule:   "R14",
			marker: "@m",
			want:   "unknown field \"descripton\"",
		},
		{
			name: "R14 unknown derive field",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 5\n" +
				"      name: review\n" +
				"      material: report\n" +
				"      derive: { to: review2, mode: single, priority: 1 }   # @m\n" +
				"    - id: 6\n" +
				"      name: review2\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R14",
			marker: "@m",
			want:   "derive: unknown field \"priority\"",
		},
		{
			name: "R15 built-in ids keep their meaning",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1   # @m\n" +
				"      name: safety_review\n" +
				"      material: report\n" +
				"      derive: { mode: none }\n",
			rule:   "R15",
			marker: "@m",
			want:   "id 1 is reserved for the built-in stage \"requirement\"",
		},
		{
			// AC5: a stage description is no longer a task description, so a config
			// that only ever wrote description refuses to start instead of quietly
			// deriving cards with an empty body.
			name: "R16 single-derived stage must declare a task template",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { auto: true, to: design, mode: single }\n" +
				"    - id: 2   # @m\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      description: 把需求展开成技术方案\n" +
				"      derive: { mode: none }\n",
			rule:   "R16",
			marker: "@m",
			want:   "declares no task template, so the derived card would carry no task description",
		},
		{
			name: "R17 task template variable whitelist",
			yaml: "flow:\n" +
				"  stages:\n" +
				"    - id: 1\n" +
				"      name: requirement\n" +
				"      material: requirement\n" +
				"      derive: { to: design, mode: single }\n" +
				"    - id: 2\n" +
				"      name: design\n" +
				"      material: design\n" +
				"      task: 针对 {上游卡号} 做{未知变量}   # @m\n" +
				"      derive: { mode: none }\n",
			rule:   "R17",
			marker: "@m",
			want:   "unknown template variable {未知变量}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load: expected %s error, got nil", tc.rule)
			}
			line := markerLine(t, tc.yaml, tc.marker)
			prefix := fmt.Sprintf("%s:%d: ", path, line)
			if !strings.HasPrefix(err.Error(), prefix) {
				t.Fatalf("error = %q, want prefix %q", err.Error(), prefix)
			}
			if !strings.Contains(err.Error(), "("+tc.rule+")") {
				t.Errorf("error = %q, want it to name %s", err.Error(), tc.rule)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestMissingTaskNamesStageAndEscapeHatch pins the operator-facing contract of
// §7.3 item 2: the message names the offending stage (twice: the stage and the
// edge that derives it), points at its line, offers the task: "" escape hatch
// and says that description is no longer a fallback.
func TestMissingTaskNamesStageAndEscapeHatch(t *testing.T) {
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
		"      description: 把需求展开成技术方案\n" +
		"      derive: { mode: none }\n"
	path := writeConfig(t, body)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load: expected R16, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"stage \"design\"",
		"flow.stages[1] (requirement)",
		"write task: \"\" to keep it empty on purpose",
		"description 现在只表示阶段描述，不再作为任务说明兜底",
		"(R16)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}
	// The line is the stage's own line (the "- id: 2" entry), not the deriving
	// stage's: that is the line the operator has to edit.
	if !strings.HasPrefix(msg, path+":7: ") {
		t.Errorf("error = %q, want it to start with %q", msg, path+":7: ")
	}
}

// TestRemovedFlowKeysAreIgnored is the card's AC on the removed keys: writing
// flow.auto and flow.default_description must not change anything — no error, no
// warning, no fingerprint move, no effect on the task-description chain. They are
// not configuration any more, so a v1 file that still carries them behaves like a
// v2 file that does not.
func TestRemovedFlowKeysAreIgnored(t *testing.T) {
	clearEnv(t)

	const stagesOnly = "flow:\n" +
		"  stages:\n" +
		"    - id: 1\n" +
		"      name: requirement\n" +
		"      material: requirement\n" +
		"      derive: { auto: true, to: design, mode: single }\n" +
		"    - id: 2\n" +
		"      name: design\n" +
		"      material: design\n" +
		"      title: \"技术方案\"\n" +
		"      task: 针对 {上游卡号} 出方案\n" +
		"      derive: { mode: none }\n"

	const withRemovedKeys = "flow:\n" +
		"  auto: true\n" +
		"  default_description: 请按上游交付物完成本环节工作\n" +
		"  stages:\n" +
		"    - id: 1\n" +
		"      name: requirement\n" +
		"      material: requirement\n" +
		"      derive: { auto: true, to: design, mode: single }\n" +
		"    - id: 2\n" +
		"      name: design\n" +
		"      material: design\n" +
		"      title: \"技术方案\"\n" +
		"      task: 针对 {上游卡号} 出方案\n" +
		"      derive: { mode: none }\n"

	plain, err := Load(writeConfig(t, stagesOnly))
	if err != nil {
		t.Fatalf("Load stages-only: %v", err)
	}
	legacy, err := Load(writeConfig(t, withRemovedKeys))
	if err != nil {
		t.Fatalf("Load with removed keys: %v (the removed keys must be accepted and ignored)", err)
	}

	if got, want := legacy.Flow.Fingerprint(), plain.Flow.Fingerprint(); got != want {
		t.Errorf("fingerprint with removed keys = %q, want %q (they must not enter the digest)", got, want)
	}
	if warnings := legacy.Flow.SwitchWarnings(); len(warnings) != 0 {
		t.Errorf("SwitchWarnings() = %v, want none", warnings)
	}
	if got, want := strings.Join(legacy.Flow.Names(), ","), strings.Join(plain.Flow.Names(), ","); got != want {
		t.Errorf("stages = %s, want %s", got, want)
	}

	// The removed flow-level fallback must not fill in a task description: the
	// chain is item body → stage task → empty.
	for name, flow := range map[string]FlowConfig{"stages-only": plain.Flow, "with-removed-keys": legacy.Flow} {
		design, ok := flow.ByName("design")
		if !ok {
			t.Fatalf("%s: stage design not found", name)
		}
		got, source := flow.ResolveDescription(&design, DeriveSingle, nil)
		if got != "针对 {上游卡号} 出方案" || source != DescriptionFromStage {
			t.Errorf("%s: ResolveDescription = (%q, %q), want the stage task template", name, got, source)
		}
	}

	// Other misspelled flow keys are still R14 errors, and the whitelist names
	// stages only.
	typo := "flow:\n" +
		"  auto: true\n" +
		"  unknown_key: 1\n" +
		"  stages:\n" +
		"    - id: 5\n" +
		"      name: review\n" +
		"      material: report\n" +
		"      derive: { mode: none }\n"
	_, err = Load(writeConfig(t, typo))
	if err == nil {
		t.Fatal("Load with a misspelled flow key: expected R14, got nil")
	}
	for _, want := range []string{"unknown field \"unknown_key\"", "(allowed: stages)", "(R14)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestEntryStageDescriptionIsAllowed covers AC7: the stage description is no
// longer a template, so R11 has nothing to say about the entry stage. Before the
// hard cut a description on a stage nobody derives was a startup error.
func TestEntryStageDescriptionIsAllowed(t *testing.T) {
	clearEnv(t)

	body := "flow:\n" +
		"  stages:\n" +
		"    - id: 1\n" +
		"      name: requirement\n" +
		"      material: requirement\n" +
		"      description: 人工建卡入口，不自动派生下游\n" +
		"      derive: { auto: true, to: design, mode: single }\n" +
		"    - id: 2\n" +
		"      name: design\n" +
		"      material: design\n" +
		"      description: 把需求展开成技术方案\n" +
		"      task: 针对 {上游卡号} 出方案\n" +
		"      derive: { mode: none }\n"
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	requirement, ok := cfg.Flow.ByName("requirement")
	if !ok {
		t.Fatal("stage requirement not found")
	}
	if requirement.Description != "人工建卡入口，不自动派生下游" {
		t.Errorf("requirement.Description = %q, want the stage description verbatim", requirement.Description)
	}
	if requirement.Task != nil {
		t.Errorf("requirement.Task = %v, want unset", *requirement.Task)
	}
}

// TestDescriptionIsNotATemplate pins the field split of D2: braces in a stage
// description are not template variables (so they are not validated and not
// rendered), and writing both fields keeps them apart.
func TestDescriptionIsNotATemplate(t *testing.T) {
	clearEnv(t)

	body := "flow:\n" +
		"  stages:\n" +
		"    - id: 1\n" +
		"      name: requirement\n" +
		"      material: requirement\n" +
		"      description: 入口（上游写 {不存在的变量} 也不报错）\n" +
		"      derive: { auto: true, to: design, mode: single }\n" +
		"    - id: 2\n" +
		"      name: design\n" +
		"      material: design\n" +
		"      description: 阶段描述：{上游卡号} 不渲染\n" +
		"      task: 任务说明：{上游卡号}\n" +
		"      derive: { mode: none }\n"
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	design, ok := cfg.Flow.ByName("design")
	if !ok {
		t.Fatal("stage design not found")
	}
	if design.Description != "阶段描述：{上游卡号} 不渲染" {
		t.Errorf("design.Description = %q, want the description verbatim", design.Description)
	}
	if design.Task == nil || *design.Task != "任务说明：{上游卡号}" {
		t.Fatalf("design.Task = %v, want the task template", design.Task)
	}
	// AC6: the card body comes from task, never from the stage description.
	got, source := cfg.Flow.ResolveDescription(&design, DeriveSingle, nil)
	if got != "任务说明：{上游卡号}" || source != DescriptionFromStage {
		t.Errorf("ResolveDescription = (%q, %q), want the task template from the stage", got, source)
	}
}

// TestTaskOnUntargetedStageIsRejected is R11 after D4: the rule still guards
// templates, it just looks at task instead of description.
func TestTaskOnUntargetedStageIsRejected(t *testing.T) {
	clearEnv(t)

	body := "flow:\n" +
		"  stages:\n" +
		"    - id: 5\n" +
		"      name: review\n" +
		"      material: report\n" +
		"      task: 评审 {上游卡号}\n" +
		"      derive: { mode: none }\n"
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("Load: expected R11, got nil")
	}
	for _, want := range []string{"flow.stages[1].task", "is not the target of any derive.to", "(R11)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestSampleConfigsLoad covers the operator-facing part of the hard cut: the two
// sample files shipped in configs/ must start on the new binary and resolve to
// the same effective flow as the built-in default (so "copy the sample" and
// "delete the flow section" stay equivalent).
func TestSampleConfigsLoad(t *testing.T) {
	clearEnv(t)
	builtin := DefaultFlowProfile()

	for _, name := range []string{"echuan.yaml", "echuan.container.yaml"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(filepath.Join("..", "..", "configs", name))
			if err != nil {
				t.Fatalf("Load configs/%s: %v", name, err)
			}
			flow := cfg.Flow
			if flow.Source == BuiltinFlowSource {
				t.Fatalf("configs/%s fell back to the built-in flow", name)
			}
			if got, want := flow.Fingerprint(), builtin.Fingerprint(); got != want {
				t.Errorf("fingerprint = %q, want %q (the sample must match the built-in flow)", got, want)
			}
			names := strings.Join(flow.Names(), ",")
			if names != strings.Join(builtin.Names(), ",") {
				t.Errorf("stages = %s, want %s", names, strings.Join(builtin.Names(), ","))
			}
			for _, stage := range flow.Stages {
				if stage.Description == "" {
					t.Errorf("stage %s has no stage description", stage.Name)
				}
			}
			if warnings := flow.SwitchWarnings(); len(warnings) != 0 {
				t.Errorf("SwitchWarnings() = %v, want none for a complete v2 sample", warnings)
			}
		})
	}
}
