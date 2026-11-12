// Package card renders the Feishu interactive cards used by the bot.
//
// Rendering is a pure function of the inbox row plus the project/work-item
// metadata: no I/O, no clock. That keeps every layout rule testable with golden
// files instead of a live tenant.
package card

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Subject kinds, mirroring inbox.KindProject / inbox.KindWorkItem.
const (
	KindProject  = "project"
	KindWorkItem = "work_item"
)

// Gate states that produce a card. Project states come from the project state
// machine, work-item states from the task state machine.
const (
	StateRequirementReview = "requirement_review"
	StateSpecReview        = "spec_review"
	StateTasksReview       = "tasks_review"
	StateSubmitted         = "submitted"
	StateFailed            = "failed"
)

// Subject is the inbox row a card projects.
type Subject struct {
	Kind  string
	ID    string
	State string
	Title string
	Age   time.Duration
	Stale bool
	// Reason carries the last rejection comment, when the row has one.
	Reason string
}

// Project carries the metadata a gate card needs. Material fields are zero
// when the gate has no material yet.
type Project struct {
	ProductID       string
	Version         int
	MaterialID      string
	MaterialVersion int
	MaterialKind    string
}

// WorkItem carries the metadata an acceptance card needs.
type WorkItem struct {
	ProjectID   string
	Worker      string
	Attempt     int
	MaxAttempts int
	ReportID    string
}

// Options are the per-render knobs resolved from configuration.
type Options struct {
	// DocURL is the imported Feishu document. Empty means "not ready yet".
	DocURL string
	// StaleHours is only used to explain the wait in the subtitle.
	StaleHours int
}

// Render builds the card for an open gate.
func Render(s Subject, p *Project, w *WorkItem, opts Options) ([]byte, error) {
	gate, err := resolveGate(s, p, w)
	if err != nil {
		return nil, err
	}

	elements := []any{
		markdownElement{Tag: "markdown", Content: gate.body},
		formElement{
			Tag:  "form",
			Name: "gate_form",
			Elements: []any{
				inputElement{
					Tag:         "input",
					Name:        "reason",
					Required:    gate.reasonRequired,
					Placeholder: plainText("驳回理由（通过可留空）"),
				},
				columnSetElement{
					Tag:     "column_set",
					Columns: gate.buttonColumns(),
				},
			},
		},
	}

	if opts.DocURL != "" {
		// Card JSON 2.0 dropped the "action" container: buttons are top-level
		// elements now. Feishu rejects the whole card with 200861 otherwise.
		elements = append(elements, buttonElement{
			Tag:       "button",
			Name:      "open_doc",
			Type:      "default",
			Text:      plainText("看全文"),
			Behaviors: []behaviorElement{{Type: "open_url", DefaultURL: opts.DocURL}},
		})
	} else {
		elements = append(elements, markdownElement{Tag: "markdown", Content: "_文档生成中…_"})
	}

	template := "blue"
	if s.Stale {
		template = "red"
	}

	return marshal(card{
		Schema: "2.0",
		Config: cardConfig{UpdateMulti: true},
		Header: cardHeader{
			Title:    plainText(fmt.Sprintf("%s · %s · 已等 %s", s.ID, gate.label, humanAge(s.Age))),
			Template: template,
		},
		Body: cardBody{Elements: elements},
	})
}

// RenderClosed rebuilds a card after the gate is gone: the form is dropped and
// only the outcome remains.
func RenderClosed(s Subject, statusLabel string, opts Options) ([]byte, error) {
	gate, err := resolveGate(s, nil, nil)
	if err != nil {
		return nil, err
	}

	content := fmt.Sprintf("**%s**\n%s · 处理结果：%s", s.Title, gate.label, statusLabel)
	if opts.DocURL != "" {
		content += fmt.Sprintf("\n[看全文](%s)", opts.DocURL)
	}

	return marshal(card{
		Schema: "2.0",
		Config: cardConfig{UpdateMulti: true},
		Header: cardHeader{
			Title:    plainText(fmt.Sprintf("%s · %s", s.ID, gate.label)),
			Template: "grey",
		},
		Body: cardBody{Elements: []any{
			markdownElement{Tag: "markdown", Content: content},
		}},
	})
}

// gateLayout is the per-state copy. All differences between cards live here so
// the layout code above stays single-path.
type gateLayout struct {
	label          string
	approveLabel   string
	rejectLabel    string
	reasonRequired bool
	// rejectOnly is set for states where approve is not a legal transition, so
	// the card does not offer a button that can only fail.
	rejectOnly bool
	body       string
}

// buttonColumns renders the submit buttons for the gate.
func (g gateLayout) buttonColumns() []columnElement {
	if g.rejectOnly {
		return []columnElement{
			{Tag: "column", Width: "auto", Elements: []any{
				buttonElement{Tag: "button", Name: "reject", Type: "danger", FormActionType: "submit", Text: plainText(g.rejectLabel)},
			}},
		}
	}
	return []columnElement{
		{Tag: "column", Width: "auto", Elements: []any{
			buttonElement{Tag: "button", Name: "approve", Type: "primary", FormActionType: "submit", Text: plainText(g.approveLabel)},
		}},
		{Tag: "column", Width: "auto", Elements: []any{
			buttonElement{Tag: "button", Name: "reject", Type: "danger", FormActionType: "submit", Text: plainText(g.rejectLabel)},
		}},
	}
}

func resolveGate(s Subject, p *Project, w *WorkItem) (gateLayout, error) {
	switch s.Kind {
	case KindProject:
		var label, material string
		switch s.State {
		case StateRequirementReview:
			label = "需求评审"
			material = materialLine(p, "requirement")
		case StateSpecReview:
			label = "方案评审"
			material = materialLine(p, "spec")
		case StateTasksReview:
			label = "计划评审"
			material = materialLine(p, "plan")
		default:
			return gateLayout{}, fmt.Errorf("card: unsupported project state %q", s.State)
		}
		body := fmt.Sprintf("**%s**\n%s", s.Title, material)
		if s.Reason != "" {
			body += fmt.Sprintf("\n上次驳回：%s", truncate(s.Reason, 80))
		}
		if s.State == StateTasksReview {
			body += "\n_驳回必须填写理由_"
		}
		return gateLayout{
			label:          label,
			approveLabel:   "通过",
			rejectLabel:    "驳回",
			reasonRequired: s.State == StateTasksReview,
			body:           body,
		}, nil

	case KindWorkItem:
		switch s.State {
		case StateSubmitted:
			body := fmt.Sprintf("**%s**\n%s", s.Title, workItemLine(w))
			if s.Reason != "" {
				body += fmt.Sprintf("\n上次打回：%s", truncate(s.Reason, 80))
			}
			return gateLayout{
				label:        "交付验收",
				approveLabel: "验收通过",
				rejectLabel:  "打回",
				body:         body,
			}, nil
		case StateFailed:
			// Only reject is valid on a failed item: approve requires the
			// item to be submitted, so a second button would always 409.
			body := fmt.Sprintf("**%s**\n%s", s.Title, workItemLine(w))
			if s.Reason != "" {
				body += fmt.Sprintf("\n失败原因：%s", truncate(s.Reason, 80))
			}
			return gateLayout{
				label:          "失败裁决",
				rejectLabel:    "打回重做",
				rejectOnly:     true,
				reasonRequired: false,
				body:           body,
			}, nil
		default:
			return gateLayout{}, fmt.Errorf("card: unsupported work item state %q", s.State)
		}
	default:
		return gateLayout{}, fmt.Errorf("card: unsupported subject kind %q", s.Kind)
	}
}

func materialLine(p *Project, kind string) string {
	if p == nil || p.MaterialID == "" {
		return "材料：尚未提交"
	}
	return fmt.Sprintf("材料 %s v%d · 项目 v%d", shortID(p.MaterialID), p.MaterialVersion, p.Version)
}

func workItemLine(w *WorkItem) string {
	if w == nil {
		return "任务详情不可用"
	}
	parts := []string{fmt.Sprintf("worker：%s", defaultString(w.Worker, "未知"))}
	if w.MaxAttempts > 0 {
		parts = append(parts, fmt.Sprintf("尝试 %d/%d", w.Attempt, w.MaxAttempts))
	}
	if w.ReportID != "" {
		parts = append(parts, fmt.Sprintf("报告 %s", shortID(w.ReportID)))
	}
	return strings.Join(parts, " · ")
}

// humanAge renders the wait in the coarsest unit that still reads naturally.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "不到 1 分钟"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func defaultString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// marshal encodes the card and runs the structural self-check. Every card is
// validated before it can reach the wire: a missing or duplicated component
// name makes Feishu reject the whole card with 200530.
func marshal(c card) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("card: marshal: %w", err)
	}
	if err := validateNames(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
