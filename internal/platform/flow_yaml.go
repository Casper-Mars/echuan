package platform

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// configError is the shape every configuration problem is reported in:
// "<path>:<line>: <reason>". A startup failure therefore points at the exact
// line the operator has to fix (AC9).
type configError struct {
	// Path is the configuration file the problem was found in.
	Path string
	// Line is the 1-based line, or 0 when the problem is not tied to a line.
	Line int
	// Msg is the reason, without a trailing period.
	Msg string
}

// Error renders the "<path>:<line>: <reason>" form the startup output uses.
func (e *configError) Error() string {
	line := e.Line
	if line <= 0 {
		line = 1
	}
	return fmt.Sprintf("%s:%d: %s", e.Path, line, e.Msg)
}

// configErrf builds a configError.
func configErrf(path string, line int, format string, args ...any) error {
	return &configError{Path: path, Line: line, Msg: fmt.Sprintf(format, args...)}
}

// yamlLinePattern matches the line marker yaml.v3 embeds in its syntax errors
// ("yaml: line 7: did not find expected key"), so the marker can be rewritten
// into the "<path>:<line>:" form the rest of the configuration layer uses.
var yamlLinePattern = regexp.MustCompile("line (\\d+): ")

// yamlConfigError rewrites a yaml.v3 error into a configError, keeping the line
// it reported.
func yamlConfigError(path string, err error) error {
	msg := err.Error()
	line := 1
	if m := yamlLinePattern.FindStringSubmatch(msg); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			line = n
		}
	}
	msg = strings.TrimPrefix(msg, "yaml: ")
	msg = yamlLinePattern.ReplaceAllString(msg, "")
	return configErrf(path, line, "parse config: %s", strings.TrimSpace(msg))
}

// yamlRoot parses data and returns the root node of the document, or nil for an
// empty document.
func yamlRoot(path string, data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, yamlConfigError(path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if isNullNode(root) {
		return nil, nil
	}
	return root, nil
}

// isNullNode reports whether n is absent or an explicit YAML null.
func isNullNode(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

// yamlScalar returns the scalar value of n.
func yamlScalar(path string, n *yaml.Node, field string) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", configErrf(path, n.Line, "%s: expected a scalar value", field)
	}
	if n.Tag == "!!null" {
		return "", configErrf(path, n.Line, "%s: expected a scalar value, got null", field)
	}
	return n.Value, nil
}

// yamlString is yamlScalar for string fields.
func yamlString(path string, n *yaml.Node, field string) (string, error) {
	return yamlScalar(path, n, field)
}

// yamlInt parses an integer field.
func yamlInt(path string, n *yaml.Node, field string) (int, error) {
	raw, err := yamlScalar(path, n, field)
	if err != nil {
		return 0, err
	}
	v, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil {
		return 0, configErrf(path, n.Line, "%s: expected an integer, got %q", field, raw)
	}
	return v, nil
}

// yamlBool parses a boolean field.
func yamlBool(path string, n *yaml.Node, field string) (bool, error) {
	raw, err := yamlScalar(path, n, field)
	if err != nil {
		return false, err
	}
	v, convErr := strconv.ParseBool(strings.ToLower(strings.TrimSpace(raw)))
	if convErr != nil {
		return false, configErrf(path, n.Line, "%s: expected a boolean, got %q", field, raw)
	}
	return v, nil
}

// yamlStringList parses a sequence of strings.
func yamlStringList(path string, n *yaml.Node, field string) ([]string, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, configErrf(path, n.Line, "%s: expected a list", field)
	}
	out := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		v, err := yamlScalar(path, item, field)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// yamlPairs walks the key/value pairs of a mapping node.
func yamlPairs(path string, n *yaml.Node, field string) ([][2]*yaml.Node, error) {
	if n.Kind != yaml.MappingNode {
		return nil, configErrf(path, n.Line, "%s: expected a mapping", field)
	}
	out := make([][2]*yaml.Node, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
	}
	return out, nil
}

// ── flow section ─────────────────────────────────────────────────────────

// flowDoc is the flow section as written, with the line every field came from.
// Validation runs on this form so every violation can name a line; the plain
// model in flow.go is built afterwards.
type flowDoc struct {
	path string
	line int

	defaultDescription *string
	defaultLine        int

	stages     []stageDoc
	stagesLine int
}

// stageDoc is one configured stage with its source lines.
type stageDoc struct {
	index int
	line  int

	id     int
	idSet  bool
	idLine int

	name     string
	nameSet  bool
	nameLine int

	label     string
	labelSet  bool
	labelLine int

	material     string
	materialLine int

	openCards     OpenCards
	openCardsSet  bool
	openCardsLine int

	closing     bool
	closingLine int

	title     string
	titleLine int

	// description is the stage description: plain text, never a template.
	description     string
	descriptionLine int

	// task keeps the three states R16 needs: nil = not configured, pointer to ""
	// = empty on purpose, pointer to text = the template.
	task     *string
	taskLine int

	derive *deriveDoc
}

// deriveDoc is one stage's derive block with its source lines.
type deriveDoc struct {
	line int

	auto     bool
	autoSet  bool
	autoLine int

	to     string
	toSet  bool
	toLine int

	mode     DeriveMode
	modeSet  bool
	modeLine int

	source     string
	sourceLine int

	format     string
	formatLine int

	items     []itemDoc
	itemsLine int
}

// itemDoc is one fanout entry with its source lines.
type itemDoc struct {
	index int
	line  int

	key     string
	keySet  bool
	keyLine int

	title     string
	titleLine int

	bodyMD   string
	bodyLine int

	priority     int
	priorityLine int

	dependsOn     []string
	dependsOnLine int
}

// parseFlowConfig parses and validates one flow document. path is used both as
// the error path and as FlowConfig.Source, so a built-in flow passes its
// synthetic path here too.
func parseFlowConfig(path string, data []byte) (FlowConfig, error) {
	root, err := yamlRoot(path, data)
	if err != nil {
		return FlowConfig{}, err
	}
	if root == nil {
		return FlowConfig{}, configErrf(path, 1, "flow: the document is empty (R1)")
	}
	doc, err := parseFlowSection(path, root)
	if err != nil {
		return FlowConfig{}, err
	}
	if err := doc.validate(); err != nil {
		return FlowConfig{}, err
	}
	return doc.flowConfig(path), nil
}

// parseFlowSection reads the flow mapping. Unknown fields are rejected (R14);
// unknown fields outside the flow section are still ignored.
func parseFlowSection(path string, node *yaml.Node) (*flowDoc, error) {
	pairs, err := yamlPairs(path, node, "flow")
	if err != nil {
		return nil, err
	}
	doc := &flowDoc{path: path, line: node.Line}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		switch key.Value {
		case "auto":
			// D5: the master switch is gone. The field stays a hard error with a
			// dedicated message, so an operator carrying a v1 file over gets the
			// migration step instead of a bare "unknown field" (N3′ refuses to
			// read it: there is no compatibility path).
			return nil, configErrf(path, key.Line,
				"flow: \"auto\" was removed; move the switch into each stage as stages[].derive.auto (R14)")
		case "default_description":
			doc.defaultLine = key.Line
			empty := ""
			if isNullNode(value) {
				doc.defaultDescription = &empty
				continue
			}
			v, err := yamlString(path, value, "flow.default_description")
			if err != nil {
				return nil, err
			}
			doc.defaultDescription = &v
		case "stages":
			stages, err := parseStages(path, value)
			if err != nil {
				return nil, err
			}
			doc.stages, doc.stagesLine = stages, value.Line
		default:
			return nil, configErrf(path, key.Line,
				"flow: unknown field %q (allowed: default_description, stages) (R14)", key.Value)
		}
	}
	return doc, nil
}

// parseStages reads the ordered stage list.
func parseStages(path string, node *yaml.Node) ([]stageDoc, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, configErrf(path, node.Line, "flow.stages: expected a list")
	}
	out := make([]stageDoc, 0, len(node.Content))
	for i, item := range node.Content {
		stage, err := parseStage(path, item, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, stage)
	}
	return out, nil
}

// parseStage reads one stage entry.
func parseStage(path string, node *yaml.Node, index int) (stageDoc, error) {
	field := fmt.Sprintf("flow.stages[%d]", index)
	pairs, err := yamlPairs(path, node, field)
	if err != nil {
		return stageDoc{}, err
	}
	stage := stageDoc{index: index, line: node.Line}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		switch key.Value {
		case "id":
			v, err := yamlInt(path, value, field+".id")
			if err != nil {
				return stageDoc{}, err
			}
			stage.id, stage.idSet, stage.idLine = v, true, key.Line
		case "name":
			v, err := yamlString(path, value, field+".name")
			if err != nil {
				return stageDoc{}, err
			}
			stage.name, stage.nameSet, stage.nameLine = v, true, key.Line
		case "label":
			if isNullNode(value) {
				continue
			}
			v, err := yamlString(path, value, field+".label")
			if err != nil {
				return stageDoc{}, err
			}
			stage.label, stage.labelSet, stage.labelLine = v, true, key.Line
		case "material":
			v, err := yamlString(path, value, field+".material")
			if err != nil {
				return stageDoc{}, err
			}
			stage.material, stage.materialLine = v, key.Line
		case "open_cards":
			v, err := yamlString(path, value, field+".open_cards")
			if err != nil {
				return stageDoc{}, err
			}
			stage.openCards, stage.openCardsSet, stage.openCardsLine = OpenCards(v), true, key.Line
		case "closing":
			v, err := yamlBool(path, value, field+".closing")
			if err != nil {
				return stageDoc{}, err
			}
			stage.closing, stage.closingLine = v, key.Line
		case "title":
			if isNullNode(value) {
				continue
			}
			v, err := yamlString(path, value, field+".title")
			if err != nil {
				return stageDoc{}, err
			}
			stage.title, stage.titleLine = v, key.Line
		case "description":
			// D2: the stage description is plain text. null and "" mean the same
			// thing (no description), because nothing falls back to it any more.
			stage.descriptionLine = key.Line
			if isNullNode(value) {
				continue
			}
			v, err := yamlString(path, value, field+".description")
			if err != nil {
				return stageDoc{}, err
			}
			stage.description = v
		case "task":
			// D3: the three states matter. A missing key means "not configured"
			// (fall back to flow.default_description); an explicit "" is a
			// deliberate empty card body and satisfies R16.
			stage.taskLine = key.Line
			empty := ""
			if isNullNode(value) {
				stage.task = &empty
				continue
			}
			v, err := yamlString(path, value, field+".task")
			if err != nil {
				return stageDoc{}, err
			}
			stage.task = &v
		case "derive":
			derive, err := parseDerive(path, value, field)
			if err != nil {
				return stageDoc{}, err
			}
			stage.derive = derive
		default:
			return stageDoc{}, configErrf(path, key.Line,
				"%s: unknown field %q (allowed: id, name, label, material, open_cards, closing, title, description, task, derive) (R14)",
				field, key.Value)
		}
	}
	return stage, nil
}

// parseDerive reads one derive block.
func parseDerive(path string, node *yaml.Node, field string) (*deriveDoc, error) {
	pairs, err := yamlPairs(path, node, field+".derive")
	if err != nil {
		return nil, err
	}
	derive := &deriveDoc{line: node.Line}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		switch key.Value {
		case "auto":
			v, err := yamlBool(path, value, field+".derive.auto")
			if err != nil {
				return nil, err
			}
			derive.auto, derive.autoSet, derive.autoLine = v, true, key.Line
		case "to":
			v, err := yamlString(path, value, field+".derive.to")
			if err != nil {
				return nil, err
			}
			derive.to, derive.toSet, derive.toLine = v, true, key.Line
		case "mode":
			v, err := yamlString(path, value, field+".derive.mode")
			if err != nil {
				return nil, err
			}
			derive.mode, derive.modeSet, derive.modeLine = DeriveMode(v), true, key.Line
		case "source":
			v, err := yamlString(path, value, field+".derive.source")
			if err != nil {
				return nil, err
			}
			derive.source, derive.sourceLine = v, key.Line
		case "format":
			v, err := yamlString(path, value, field+".derive.format")
			if err != nil {
				return nil, err
			}
			derive.format, derive.formatLine = v, key.Line
		case "items":
			items, err := parseItems(path, value, field+".derive")
			if err != nil {
				return nil, err
			}
			derive.items, derive.itemsLine = items, value.Line
		default:
			return nil, configErrf(path, key.Line,
				"%s.derive: unknown field %q (allowed: auto, to, mode, source, format, items) (R14)", field, key.Value)
		}
	}
	return derive, nil
}

// parseItems reads the fanout entry list.
func parseItems(path string, node *yaml.Node, field string) ([]itemDoc, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, configErrf(path, node.Line, "%s.items: expected a list", field)
	}
	out := make([]itemDoc, 0, len(node.Content))
	for i, entry := range node.Content {
		item, err := parseItem(path, entry, field, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// parseItem reads one fanout entry.
func parseItem(path string, node *yaml.Node, field string, index int) (itemDoc, error) {
	name := fmt.Sprintf("%s.items[%d]", field, index)
	pairs, err := yamlPairs(path, node, name)
	if err != nil {
		return itemDoc{}, err
	}
	item := itemDoc{index: index, line: node.Line}
	for _, pair := range pairs {
		key, value := pair[0], pair[1]
		switch key.Value {
		case "key":
			v, err := yamlString(path, value, name+".key")
			if err != nil {
				return itemDoc{}, err
			}
			item.key, item.keySet, item.keyLine = v, true, key.Line
		case "title":
			v, err := yamlString(path, value, name+".title")
			if err != nil {
				return itemDoc{}, err
			}
			item.title, item.titleLine = v, key.Line
		case "body_md":
			v, err := yamlString(path, value, name+".body_md")
			if err != nil {
				return itemDoc{}, err
			}
			item.bodyMD, item.bodyLine = v, key.Line
		case "priority":
			v, err := yamlInt(path, value, name+".priority")
			if err != nil {
				return itemDoc{}, err
			}
			item.priority, item.priorityLine = v, key.Line
		case "depends_on":
			v, err := yamlStringList(path, value, name+".depends_on")
			if err != nil {
				return itemDoc{}, err
			}
			item.dependsOn, item.dependsOnLine = v, key.Line
		default:
			return itemDoc{}, configErrf(path, key.Line,
				"%s: unknown field %q (allowed: key, title, body_md, priority, depends_on) (R14)", name, key.Value)
		}
	}
	return item, nil
}

// flowConfig builds the plain model from the parsed document.
func (d *flowDoc) flowConfig(source string) FlowConfig {
	flow := FlowConfig{DefaultDescription: d.defaultDescription, Source: source}
	flow.Stages = make([]Stage, 0, len(d.stages))
	for i := range d.stages {
		flow.Stages = append(flow.Stages, d.stages[i].stage())
	}
	return flow
}

// stage builds the plain model of one stage.
func (s *stageDoc) stage() Stage {
	openCards := OpenCardsSingle
	if s.openCardsSet {
		openCards = s.openCards
	}
	out := Stage{
		ID:          s.id,
		Name:        strings.TrimSpace(s.name),
		Label:       strings.TrimSpace(s.label),
		Material:    s.material,
		OpenCards:   openCards,
		Closing:     s.closing,
		Title:       s.title,
		Description: s.description,
		Task:        s.task,
		Derive:      Derive{Mode: DeriveNone},
	}
	if s.derive != nil {
		out.Derive = Derive{
			Auto:   s.derive.auto,
			To:     strings.TrimSpace(s.derive.to),
			Mode:   s.derive.mode,
			Source: s.derive.source,
			Format: s.derive.format,
		}
		if out.Derive.Mode == "" {
			out.Derive.Mode = DeriveNone
		}
		for i := range s.derive.items {
			item := &s.derive.items[i]
			out.Derive.Items = append(out.Derive.Items, DeriveItem{
				Key:       strings.TrimSpace(item.key),
				Title:     item.title,
				BodyMD:    item.bodyMD,
				Priority:  item.priority,
				DependsOn: item.dependsOn,
			})
		}
	}
	return out
}
