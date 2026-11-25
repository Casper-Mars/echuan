package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// DeriveMode is how a stage derives the cards that follow it (D3″).
//
// It is a graph edge declaration, not a chain: every stage says on its own who
// it hands work to and how many cards it creates.
type DeriveMode string

const (
	// DeriveNone does not derive anything: the stage is a chain tail or purely
	// manual.
	DeriveNone DeriveMode = "none"
	// DeriveSingle derives exactly one card with an empty entry.
	DeriveSingle DeriveMode = "single"
	// DeriveList reads the entry list out of the stage's latest output
	// artifact (the plan JSON/YAML a breakdown card produces).
	DeriveList DeriveMode = "list"
	// DeriveFanout derives the entries enumerated in the configuration.
	DeriveFanout DeriveMode = "fanout"
)

// valid reports whether m is a declared derivation mode (R6).
func (m DeriveMode) valid() bool {
	switch m {
	case DeriveNone, DeriveSingle, DeriveList, DeriveFanout:
		return true
	}
	return false
}

// OpenCards is the per-project cap on unfinished cards of one stage (D4″).
type OpenCards string

const (
	// OpenCardsSingle allows at most one unfinished card per project.
	OpenCardsSingle OpenCards = "single"
	// OpenCardsMultiple allows any number of unfinished cards per project.
	OpenCardsMultiple OpenCards = "multiple"
)

// MaterialNames are the material categories a stage may declare (R5). The
// values match enum.MaterialKind, which is what lands in materials.kind.
var MaterialNames = []string{"requirement", "design", "plan", "report", "context", "other"}

// BuiltinNames maps the ids the existing database already uses to the stage
// name that id must keep (R15). Renaming them would silently reinterpret every
// historical card.
var BuiltinNames = map[int]string{
	1: "requirement",
	2: "design",
	3: "breakdown",
	4: "dev",
}

// CardTemplateVars are the card-level template variables every template may
// use (D5″).
var CardTemplateVars = []string{"上游卡号", "上游卡标题", "项目名", "产品名", "环节名"}

// ItemTemplateVars are the entry-level template variables that only apply to
// list/fanout derivations (D5″).
var ItemTemplateVars = []string{"条目标题", "条目key", "条目序号"}

// DescriptionSource names the level a derived card description came from
// (§8.4).
type DescriptionSource string

const (
	// DescriptionFromItem means the entry body (the artifact field for list,
	// the configured body for fanout) supplied the text.
	DescriptionFromItem DescriptionSource = "item"
	// DescriptionFromStage means the target stage's task template.
	DescriptionFromStage DescriptionSource = "stage"
	// DescriptionEmpty means nothing matched: the card is created with an empty
	// description. For single derivations R16 forbids this unless the operator
	// wrote task: "" on purpose.
	DescriptionEmpty DescriptionSource = "empty"
)

// FlowConfig is the resolved flow: the stage table plus the derivation policy
// shared by every stage.
//
// There is deliberately no flow-level switch any more (D1): whether an approval
// derives the next stage is decided by that stage's own Derive.Auto, so a
// configuration cannot be "on globally, off here" or the other way round.
type FlowConfig struct {
	// Stages is the ordered stage table. The order is the board and task
	// picker order; ids are what lands in tasks.kind.
	Stages []Stage
	// Source names where the flow came from: BuiltinFlowSource when the config
	// file has no flow section, otherwise the config file path.
	Source string
}

// Stage is one configured stage.
type Stage struct {
	// ID is the integer stored in tasks.kind. Positive, unique, never reused.
	ID int
	// Name is the public identifier: kit preset keys, HTTP kind values and
	// audit rule strings all use it. Unique within the flow.
	Name string
	// Label is the display name. Empty means "use Name".
	Label string
	// Material is the material category the stage produces (R5).
	Material string
	// OpenCards is the per-project cap on unfinished cards of this stage.
	OpenCards OpenCards
	// Closing marks the stage whose completion opens the project merge PR.
	Closing bool
	// Title is the card title template. Empty means "use the entry title".
	Title string
	// Description is the human-readable stage description (D2): it is what
	// GET /v1/flow and the stage-list tool show, and it never lands in a card.
	// It is not a template, so it is not rendered and R17 does not look at it.
	Description string
	// Task is the derived card's task-description template (D2). nil means "not
	// configured" (fall through to the flow default); a pointer to "" is an
	// explicit empty task description, which R16 accepts as "empty on purpose".
	Task *string
	// Derive declares what an approval of a card in this stage creates.
	Derive Derive
}

// LabelOrName returns the display name, falling back to the identifier.
func (s Stage) LabelOrName() string {
	if strings.TrimSpace(s.Label) != "" {
		return s.Label
	}
	return s.Name
}

// Derive is one stage's derivation rule.
type Derive struct {
	// Auto is the only switch deciding whether an approval of a card in this
	// stage derives the next one (D1). It defaults to false: writing to/mode
	// without auto means "configured, still off" (R-C).
	Auto bool
	// To is the target stage name. Empty when Mode is DeriveNone.
	To string
	// Mode is none, single, list or fanout.
	Mode DeriveMode
	// Source is where a list derivation reads its entries from. Only
	// "artifact" is supported.
	Source string
	// Format is the artifact format of a list derivation: json or yaml.
	Format string
	// Items are the entries of a fanout derivation.
	Items []DeriveItem
}

// DeriveItem is one entry a fanout derivation turns into a card.
type DeriveItem struct {
	// Key identifies the entry among its siblings (not a stage name).
	Key string
	// Title is the card title, or empty to use the target stage template.
	Title string
	// BodyMD is the card description, or empty to fall back to the stage /
	// flow default.
	BodyMD string
	// Priority is an optional ordering hint passed through to the engine.
	Priority int
	// DependsOn are the sibling entry keys this entry depends on.
	DependsOn []string
}

// String renders the derivation rule. It is used by the startup log line and
// by the flow fingerprint, so it must stay deterministic.
func (d Derive) String() string {
	parts := []string{fmt.Sprintf("auto=%t", d.Auto)}
	if d.Mode == "" || d.Mode == DeriveNone {
		return strings.Join(append(parts, string(DeriveNone)), " ")
	}
	parts = append(parts, "to="+d.To, "mode="+string(d.Mode))
	if d.Source != "" {
		parts = append(parts, "source="+d.Source)
	}
	if d.Format != "" {
		parts = append(parts, "format="+d.Format)
	}
	for _, item := range d.Items {
		parts = append(parts, fmt.Sprintf("item=%s:%s:%s:%d:%s",
			item.Key, item.Title, item.BodyMD, item.Priority, strings.Join(item.DependsOn, ",")))
	}
	return strings.Join(parts, " ")
}

// ByName returns the stage with the given name.
func (f FlowConfig) ByName(name string) (Stage, bool) {
	for _, s := range f.Stages {
		if s.Name == name {
			return s, true
		}
	}
	return Stage{}, false
}

// ByID returns the stage with the given id (tasks.kind).
func (f FlowConfig) ByID(id int) (Stage, bool) {
	for _, s := range f.Stages {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}

// Names returns the stage names in configured order. It feeds the "available
// stages" half of every error message.
func (f FlowConfig) Names() []string {
	out := make([]string, 0, len(f.Stages))
	for _, s := range f.Stages {
		out = append(out, s.Name)
	}
	return out
}

// IDs returns the configured stage ids in order. The startup database sentinel
// compares it against SELECT DISTINCT kind FROM tasks.
func (f FlowConfig) IDs() []int {
	out := make([]int, 0, len(f.Stages))
	for _, s := range f.Stages {
		out = append(out, s.ID)
	}
	return out
}

// Label returns the display name of a stage name, falling back to the name.
func (f FlowConfig) Label(name string) string {
	if s, ok := f.ByName(name); ok {
		return s.LabelOrName()
	}
	return name
}

// FingerprintScheme is the version of the fingerprint input string. It is part
// of that string, so a scheme change can never collide with a value computed by
// an older binary (D6). Scheme 2 is the per-stage switch + task template layout.
const FingerprintScheme = 2

// Fingerprint is the 12-character digest of the effective flow, used by the
// startup log and by the derivation event detail to record which configuration
// produced a card. It is content-only (no timestamps), so two loads of the same
// configuration always agree (§8.7).
//
// The digest covers, for every stage, id|name|label|material|open_cards|closing|
// title|task|derive (scheme 2). Nothing else: the stage description is display
// text that changes no card, and the removed flow-level keys (auto,
// default_description) are not configuration any more, so they must not move the
// digest either — a file that still carries them fingerprints like one that does
// not. An unset task is encoded differently from an explicit "", because the two
// mean different things (R16).
func (f FlowConfig) Fingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scheme=%d\n", FingerprintScheme)
	for _, s := range f.Stages {
		fmt.Fprintf(&b, "%d|%s|%s|%s|%s|%t|%s|%s|%s\n",
			s.ID, s.Name, s.Label, s.Material, s.OpenCards, s.Closing,
			s.Title, encodeOptional(s.Task), s.Derive)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:12]
}

// encodeOptional makes "not configured" and "explicit empty" distinguishable in
// the fingerprint input string.
func encodeOptional(v *string) string {
	if v == nil {
		return "<unset>"
	}
	return "<" + *v + ">"
}

// ChainSummary renders one line per stage for the startup log, for example
// "1:requirement(需求) -> 2:design(方案) (single, auto=on)" (§8.1 step 6).
//
// Every deriving stage prints its switch next to its mode: with the switch
// defaulting to off, "to/mode but no auto" is the one misconfiguration the
// startup log has to make visible (R-C).
func (f FlowConfig) ChainSummary() []string {
	out := make([]string, 0, len(f.Stages))
	for _, s := range f.Stages {
		line := fmt.Sprintf("%d:%s(%s)", s.ID, s.Name, s.LabelOrName())
		if s.Derive.Mode == "" || s.Derive.Mode == DeriveNone {
			line += " (" + string(DeriveNone) + ")"
		} else if dst, ok := f.ByName(s.Derive.To); ok {
			line += fmt.Sprintf(" -> %d:%s(%s) (%s, auto=%s)",
				dst.ID, dst.Name, dst.LabelOrName(), s.Derive.Mode, switchLabel(s.Derive.Auto))
		} else {
			line += fmt.Sprintf(" -> %s (%s, auto=%s)", s.Derive.To, s.Derive.Mode, switchLabel(s.Derive.Auto))
		}
		out = append(out, line)
	}
	return out
}

// switchLabel renders the derive switch the way the startup log spells it.
func switchLabel(auto bool) string {
	if auto {
		return "on"
	}
	return "off"
}

// SwitchWarning is one W1 finding (§10.2): a stage whose derive switch is on
// while its derive mode creates no card at all, so the switch silently does
// nothing.
type SwitchWarning struct {
	// Stage is the stage name the operator has to fix.
	Stage string
	// Mode is the configured derive mode, kept so the log line can name it.
	Mode DeriveMode
}

// SwitchWarnings reports, in configured order, every stage with derive.auto on
// and derive.mode none (§10.2 W1). A half-migrated configuration must still
// start (B1), so this is a warning list and never a validation error. One entry
// per offending stage.
func (f FlowConfig) SwitchWarnings() []SwitchWarning {
	out := make([]SwitchWarning, 0)
	for _, s := range f.Stages {
		if s.Derive.Auto && (s.Derive.Mode == "" || s.Derive.Mode == DeriveNone) {
			out = append(out, SwitchWarning{Stage: s.Name, Mode: DeriveNone})
		}
	}
	return out
}

// ResolveDescription picks the template a derived card's task description is
// rendered from, together with the level it came from (§8.4 / D3).
//
// The rules, in order:
//
//  1. a list derivation takes the entry body and never falls back: when the
//     upstream agent left body_md empty the card keeps an empty description;
//  2. a fanout derivation takes the configured entry body when it is non-empty;
//  3. then the target stage's task template;
//  4. otherwise the task description is empty.
//
// There is no flow-level fallback: default_description was removed together with
// the master switch and is not read at all (see parseFlowSection). The stage's
// Description (the human-readable one) is never a candidate either: it is display
// text, not a template (D2). R16 guarantees a single-derived stage never reaches
// step 4 unless the operator wrote task: "" on purpose.
//
// mode is the *upstream* stage's Derive.Mode (the derivation that creates the
// card), item is the entry being turned into a card and may be nil for single
// derivations. dst may be nil when the target stage is unknown; the caller then
// gets an empty description (there is no flow-level fallback any more).
func (f FlowConfig) ResolveDescription(dst *Stage, mode DeriveMode, item *DeriveItem) (string, DescriptionSource) {
	hasBody := item != nil && strings.TrimSpace(item.BodyMD) != ""

	switch mode {
	case DeriveList:
		if hasBody {
			return item.BodyMD, DescriptionFromItem
		}
		return "", DescriptionEmpty
	case DeriveNone:
		return "", DescriptionEmpty
	}

	if hasBody {
		return item.BodyMD, DescriptionFromItem
	}
	if dst != nil && dst.Task != nil {
		return *dst.Task, DescriptionFromStage
	}
	return "", DescriptionEmpty
}
