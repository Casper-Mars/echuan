package platform

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxNameRunes is the length limit R3 and R4 put on name and label.
const maxNameRunes = 32

// validate runs every startup rule R1–R17 on the parsed flow (§8.6). Every
// violation is a configError, so the startup output always contains
// "<path>:<line>: <reason>".
//
// R13 is retired (D4): there is no master switch any more, so validation always
// runs. A flow whose per-stage switches are all off still has to be valid — a
// switch decides what a running process derives, never whether the file parses.
func (d *flowDoc) validate() error {
	path := d.path

	// R1: the flow must declare at least one stage.
	if len(d.stages) == 0 {
		return configErrf(path, d.stagesLine, "flow.stages: must declare at least one stage (R1)")
	}

	byName := make(map[string]*stageDoc, len(d.stages))
	tokens := make(map[string]int, len(d.stages)*2) // name and label share one namespace (R4)
	ids := make(map[int]int, len(d.stages))

	for i := range d.stages {
		s := &d.stages[i]
		field := fmt.Sprintf("flow.stages[%d]", s.index)

		// R2: positive, unique id.
		if !s.idSet {
			return configErrf(path, s.line, "%s.id: is required (R2)", field)
		}
		if s.id <= 0 {
			return configErrf(path, s.idLine, "%s.id: must be a positive integer, got %d (R2)", field, s.id)
		}
		if prev, ok := ids[s.id]; ok {
			return configErrf(path, s.idLine,
				"%s.id: duplicate id %d, already used by flow.stages[%d] (R2)", field, s.id, prev)
		}
		ids[s.id] = s.index

		// R3: name is required, trimmed 1–32 characters, no control characters,
		// unique.
		if !s.nameSet {
			return configErrf(path, s.line, "%s.name: is required (R3)", field)
		}
		name := strings.TrimSpace(s.name)
		if name == "" {
			return configErrf(path, s.nameLine, "%s.name: must not be blank (R3)", field)
		}
		if utf8.RuneCountInString(name) > maxNameRunes {
			return configErrf(path, s.nameLine,
				"%s.name: must be at most %d characters, got %d (R3)",
				field, maxNameRunes, utf8.RuneCountInString(name))
		}
		if strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return configErrf(path, s.nameLine, "%s.name: must not contain control characters (R3)", field)
		}
		if prev, ok := byName[name]; ok {
			return configErrf(path, s.nameLine,
				"%s.name: duplicate stage name %q, already used by flow.stages[%d] (R3)", field, name, prev.index)
		}
		byName[name] = s
		if prev, ok := tokens[name]; ok {
			return configErrf(path, s.nameLine,
				"%s.name: %q is already used as a label by flow.stages[%d]; name and label share one namespace (R4)",
				field, name, prev)
		}
		tokens[name] = s.index

		// R4: label is optional but, when given, trimmed 1–32 characters; the
		// union of names and labels must be unique so name→id resolution stays
		// unambiguous.
		if s.labelSet {
			label := strings.TrimSpace(s.label)
			if label == "" {
				return configErrf(path, s.labelLine, "%s.label: must not be blank (R4)", field)
			}
			if utf8.RuneCountInString(label) > maxNameRunes {
				return configErrf(path, s.labelLine,
					"%s.label: must be at most %d characters, got %d (R4)",
					field, maxNameRunes, utf8.RuneCountInString(label))
			}
			if label != name {
				if prev, ok := tokens[label]; ok {
					return configErrf(path, s.labelLine,
						"%s.label: %q is already used by flow.stages[%d] as a stage name or label; name and label share one namespace (R4)",
						field, label, prev)
				}
				tokens[label] = s.index
			}
		}

		// R5: material category.
		if !isMaterialName(s.material) {
			return configErrf(path, lineOf(s.materialLine, s.line),
				"%s.material: must be one of %s, got %q (R5)",
				field, strings.Join(MaterialNames, ", "), s.material)
		}

		// R15: ids 1–4 keep their built-in meaning.
		if builtin, ok := BuiltinNames[s.id]; ok && name != builtin {
			return configErrf(path, s.idLine,
				"%s.id: id %d is reserved for the built-in stage %q; a stage with id %d must be named %q (R15)",
				field, s.id, builtin, s.id, builtin)
		}
	}

	// R6, R7, R12 per stage: derivation shape and the closing flag.
	closing := 0
	for i := range d.stages {
		s := &d.stages[i]
		field := fmt.Sprintf("flow.stages[%d]", s.index)
		mode := DeriveNone

		if s.derive != nil {
			mode = s.derive.mode
			if mode == "" {
				mode = DeriveNone
			}
			// R6: mode and its companion fields.
			if s.derive.modeSet && !mode.valid() {
				return configErrf(path, s.derive.modeLine,
					"%s.derive.mode: must be one of none, single, list, fanout, got %q (R6)", field, mode)
			}
			switch mode {
			case DeriveList:
				if s.derive.source != "artifact" {
					return configErrf(path, lineOf(s.derive.sourceLine, s.derive.line),
						"%s.derive.source: must be \"artifact\" when mode is list, got %q (R6)", field, s.derive.source)
				}
				if s.derive.format != "json" && s.derive.format != "yaml" {
					return configErrf(path, lineOf(s.derive.formatLine, s.derive.line),
						"%s.derive.format: must be one of json, yaml when mode is list, got %q (R6)", field, s.derive.format)
				}
			case DeriveFanout:
				if len(s.derive.items) == 0 {
					return configErrf(path, lineOf(s.derive.itemsLine, s.derive.line),
						"%s.derive.items: must list at least one entry when mode is fanout (R6)", field)
				}
				keys := make(map[string]int, len(s.derive.items))
				for j := range s.derive.items {
					item := &s.derive.items[j]
					key := strings.TrimSpace(item.key)
					if key == "" {
						return configErrf(path, lineOf(item.keyLine, item.line),
							"%s.derive.items[%d].key: is required (R6)", field, item.index)
					}
					if prev, ok := keys[key]; ok {
						return configErrf(path, lineOf(item.keyLine, item.line),
							"%s.derive.items[%d].key: duplicate entry key %q, already used by items[%d] (R6)",
							field, item.index, key, prev)
					}
					keys[key] = item.index
				}
			default:
				if len(s.derive.items) > 0 {
					return configErrf(path, lineOf(s.derive.itemsLine, s.derive.line),
						"%s.derive.items: only allowed when mode is fanout (R6)", field)
				}
			}
		}

		// R7: the target must exist, must not be the stage itself and must be
		// declared exactly when something is derived.
		to := ""
		toLine := s.line
		if s.derive != nil {
			to = strings.TrimSpace(s.derive.to)
			toLine = lineOf(s.derive.toLine, s.derive.line)
		}
		switch {
		case mode == DeriveNone:
			if to != "" {
				return configErrf(path, toLine,
					"%s.derive.to: must not be set when mode is none (R7)", field)
			}
		case to == "":
			return configErrf(path, toLine,
				"%s.derive.to: is required when mode is %s (R7)", field, mode)
		case to == strings.TrimSpace(s.name):
			return configErrf(path, toLine,
				"%s.derive.to: must not point back at the stage itself (R7)", field)
		case byName[to] == nil:
			return configErrf(path, toLine,
				"%s.derive.to: unknown stage %q; available stages: %s (R7)",
				field, to, strings.Join(sortedNames(d.stages), ", "))
		}

		// R12: at most one stage closes the project.
		if s.closing {
			closing++
			if closing > 1 {
				return configErrf(path, s.closingLine,
					"%s.closing: at most one stage may set closing: true (R12)", field)
			}
		}
	}

	// R8: the derivation graph has no cycle.
	if err := d.checkCycles(); err != nil {
		return err
	}

	// R9: a stage that may be derived into several cards must accept them.
	for i := range d.stages {
		s := &d.stages[i]
		if s.derive == nil {
			continue
		}
		if s.derive.mode != DeriveList && s.derive.mode != DeriveFanout {
			continue
		}
		dst, ok := byName[strings.TrimSpace(s.derive.to)]
		if !ok {
			continue
		}
		if dst.effectiveOpenCards() != OpenCardsMultiple {
			return configErrf(path, lineOf(s.derive.toLine, s.derive.line),
				"flow.stages[%d].derive.to: target stage %q must set open_cards: multiple when derived by a %s stage (R9)",
				s.index, dst.name, s.derive.mode)
		}
	}

	// R16: every stage that a single derivation targets must say where its card
	// task description comes from (D4). The stage description does not count: it
	// is display text now, so a config that only ever wrote description fails here
	// instead of silently producing empty cards (§7.3 item 2).
	if d.defaultDescription == nil {
		for i := range d.stages {
			s := &d.stages[i]
			if s.derive == nil || s.derive.mode != DeriveSingle {
				continue
			}
			dst, ok := byName[strings.TrimSpace(s.derive.to)]
			if !ok || dst.task != nil {
				continue
			}
			return configErrf(path, dst.line,
				"flow.stages[%d]: stage %q is derived automatically by flow.stages[%d] (%s) but declares no task template and flow.default_description is unset, so the derived card would carry no task description; add task to stage %q (write task: \"\" to keep it empty on purpose) or set flow.default_description (R16)。注：description 现在只表示阶段描述，不再作为任务说明兜底。",
				dst.index, dst.name, s.index, s.name, dst.name)
		}
	}

	// R10 / R17: template variables are limited to the documented set.
	fanoutTargets := make(map[string]bool, len(d.stages))
	for i := range d.stages {
		if d.stages[i].derive != nil && d.stages[i].derive.mode == DeriveFanout {
			fanoutTargets[strings.TrimSpace(d.stages[i].derive.to)] = true
		}
	}
	for i := range d.stages {
		s := &d.stages[i]
		field := fmt.Sprintf("flow.stages[%d]", s.index)
		name := strings.TrimSpace(s.name)
		allowItem := fanoutTargets[name]
		if s.title != "" {
			if err := checkTemplateVars(path, lineOf(s.titleLine, s.line), field+".title", s.title, allowItem, "R10"); err != nil {
				return err
			}
		}
		// D2: the stage description is not a template, so it is not checked here
		// and braces in it render verbatim (R-D).
		if s.task != nil && *s.task != "" {
			if err := checkTemplateVars(path, lineOf(s.taskLine, s.line), field+".task", *s.task, allowItem, "R17"); err != nil {
				return err
			}
		}
	}
	if d.defaultDescription != nil && *d.defaultDescription != "" {
		if err := checkTemplateVars(path, lineOf(d.defaultLine, d.line), "flow.default_description", *d.defaultDescription, true, "R17"); err != nil {
			return err
		}
	}

	// R11: a template on a stage nobody derives is silently dead.
	targeted := make(map[string]bool, len(d.stages))
	for i := range d.stages {
		s := &d.stages[i]
		if s.derive == nil || s.derive.mode == "" || s.derive.mode == DeriveNone {
			continue
		}
		targeted[strings.TrimSpace(s.derive.to)] = true
	}
	for i := range d.stages {
		s := &d.stages[i]
		if targeted[strings.TrimSpace(s.name)] {
			continue
		}
		if s.title != "" {
			return configErrf(path, lineOf(s.titleLine, s.line),
				"flow.stages[%d].title: stage %q is not the target of any derive.to, so the template is never rendered; point a derive.to at it or drop the template (R11)",
				s.index, s.name)
		}
		// D2/D4: only templates count. A stage description is always meaningful
		// (it is shown by GET /v1/flow), so it is deliberately not checked (AC7).
		if s.task != nil {
			return configErrf(path, lineOf(s.taskLine, s.line),
				"flow.stages[%d].task: stage %q is not the target of any derive.to, so the template is never rendered; point a derive.to at it or drop the template (R11)",
				s.index, s.name)
		}
	}

	return nil
}

// effectiveOpenCards returns the open_cards policy with the documented default.
func (s *stageDoc) effectiveOpenCards() OpenCards {
	if s.openCardsSet {
		return s.openCards
	}
	return OpenCardsSingle
}

// checkCycles runs the R8 depth-first search over the derivation graph.
func (d *flowDoc) checkCycles() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := make(map[string]int, len(d.stages))
	byName := make(map[string]*stageDoc, len(d.stages))
	for i := range d.stages {
		byName[strings.TrimSpace(d.stages[i].name)] = &d.stages[i]
	}
	var stack []string

	// visit walks the graph and reports a cycle on the edge that closes it, so
	// the line in the message is the derive.to the operator has to change.
	var visit func(name string) error
	visit = func(name string) error {
		s := byName[name]
		if s == nil || state[name] == black {
			return nil
		}
		if s.derive == nil || s.derive.mode == "" || s.derive.mode == DeriveNone {
			state[name] = black
			return nil
		}
		target := strings.TrimSpace(s.derive.to)
		if state[target] == gray {
			start := 0
			for i, v := range stack {
				if v == target {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), name, target)
			return configErrf(d.path, lineOf(s.derive.toLine, s.line),
				"flow.stages[%d].derive.to: derivation cycle %s (R8)",
				s.index, strings.Join(cycle, " -> "))
		}
		state[name] = gray
		stack = append(stack, name)
		if err := visit(target); err != nil {
			return err
		}
		stack = stack[:len(stack)-1]
		state[name] = black
		return nil
	}

	for i := range d.stages {
		if err := visit(strings.TrimSpace(d.stages[i].name)); err != nil {
			return err
		}
	}
	return nil
}

// checkTemplateVars rejects any template placeholder outside the documented
// whitelist (R10 / R17) and lists the variables that are available.
func checkTemplateVars(path string, line int, field, value string, allowItem bool, rule string) error {
	allowed := make(map[string]bool, len(CardTemplateVars)+len(ItemTemplateVars))
	for _, v := range CardTemplateVars {
		allowed[v] = true
	}
	if allowItem {
		for _, v := range ItemTemplateVars {
			allowed[v] = true
		}
	}

	rest := value
	for {
		open := strings.Index(rest, "{")
		if open < 0 {
			return nil
		}
		closing := strings.Index(rest[open:], "}")
		if closing < 0 {
			return configErrf(path, line,
				"%s: unclosed template variable in %q; 可用变量: %s (%s)",
				field, value, templateVarList(allowItem), rule)
		}
		token := rest[open+1 : open+closing]
		if !allowed[token] {
			return configErrf(path, line,
				"%s: unknown template variable {%s} in %q; 可用变量: %s (%s)",
				field, token, value, templateVarList(allowItem), rule)
		}
		rest = rest[open+closing+1:]
	}
}

// templateVarList renders the available variables for an error message.
func templateVarList(allowItem bool) string {
	out := make([]string, 0, len(CardTemplateVars)+len(ItemTemplateVars))
	for _, v := range CardTemplateVars {
		out = append(out, "{"+v+"}")
	}
	if allowItem {
		for _, v := range ItemTemplateVars {
			out = append(out, "{"+v+"}")
		}
	}
	return strings.Join(out, " ")
}

// sortedNames returns the stage names in configured order for error messages.
func sortedNames(stages []stageDoc) []string {
	out := make([]string, 0, len(stages))
	for i := range stages {
		out = append(out, strings.TrimSpace(stages[i].name))
	}
	return out
}

// isMaterialName reports whether v is a declared material category (R5).
func isMaterialName(v string) bool {
	for _, m := range MaterialNames {
		if m == v {
			return true
		}
	}
	return false
}

// lineOf falls back to the fallback line when the field was not written.
func lineOf(line, fallback int) int {
	if line > 0 {
		return line
	}
	return fallback
}
