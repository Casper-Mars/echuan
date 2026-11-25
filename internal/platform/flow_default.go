package platform

// BuiltinFlowSource is the FlowConfig.Source value used when the configuration
// has no flow section and the built-in default flow takes over.
const BuiltinFlowSource = "<builtin>"

// builtinFlowYAML is the built-in default flow: the exact flow the platform ran
// before the flow became configurable (§8.7), field for field.
//
// The stage names are deliberately requirement/design/breakdown/dev. They are
// the audit rule strings (AC7 needs "requirement->design" verbatim) and the
// keys kit's presetByKind already uses, so an existing agent configuration
// keeps working; label carries the Chinese display name the board has always
// shown.
//
// The explicit task: "" on design and breakdown is load-bearing (R-I). Without
// it both cards would fall back to flow.default_description, and AC4 requires
// their description_md to stay exactly empty. An explicit empty string also
// satisfies R16, which otherwise refuses to start when a single-derived stage
// has no task template at all.
//
// Every stage that derives the next one has to say derive.auto: true: there is
// no flow-level switch any more (D1), so a missing switch would silently break
// the built-in chain (R-B). The requirement stage's description is what makes it
// visible in the stage list that the entry is manual (D10).
//
// open_cards: multiple on dev reproduces "a project can run many cards in
// parallel"; closing: true marks the stage whose settlement opens the project
// merge PR.
const builtinFlowYAML = `stages:
  - id: 1
    name: requirement
    label: 需求
    material: requirement
    description: "人工建卡入口，不自动派生下游"
    derive: { auto: true, to: design, mode: single }
  - id: 2
    name: design
    label: 方案
    material: design
    title: "技术方案"
    description: "把需求展开成技术方案：目标与范围、接口与数据、风险与验证方式"
    task: ""
    derive: { auto: true, to: breakdown, mode: single }
  - id: 3
    name: breakdown
    label: 拆解
    material: plan
    title: "任务拆分"
    description: "把方案拆成可独立验收的开发卡"
    task: ""
    derive: { auto: true, to: dev, mode: list, source: artifact, format: json }
  - id: 4
    name: dev
    label: 开发
    material: report
    open_cards: multiple
    closing: true
    description: "实现与自测，交付物写入任务报告"
    derive: { mode: none }
`

// DefaultFlowProfile returns the built-in default flow. It runs the same parser
// and the same R1–R17 validation as a configured flow: the default is not a
// special case the rules may skip (R13).
//
// The returned value is re-parsed on every call, so callers own their copy.
func DefaultFlowProfile() FlowConfig {
	flow, err := parseFlowConfig(BuiltinFlowSource, []byte(builtinFlowYAML))
	if err != nil {
		// The built-in flow is a compile-time constant validated by the tests;
		// reaching this panic means the constant and the rules disagree, which
		// is a programming error, not an operator error.
		panic("platform: built-in default flow is invalid: " + err.Error())
	}
	return flow
}
