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
//
// The three "what is this" fields are deliberately separate: the task title is
// the header, the project name is the subtitle prefix, and the raw business id
// (t_… / r_…) is only ever printed on the auxiliary footer line. A header must
// never show the id — that was the rejection this layout answers.
type Subject struct {
	Kind        string
	ID          string
	State       string
	Title       string
	ProjectName string
	Age         time.Duration
	Stale       bool
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

// Link is one attachment row of the acceptance card.
type Link struct {
	// MaterialID is only used for logging and de-duplication, never rendered.
	MaterialID string
	// Title is the unescaped display name (title, or the material id when the
	// title is empty).
	Title string
	// URL is the imported document. Empty degrades this one row to plain text
	// (import still running / failed / switched off).
	URL string
}

// Options are the per-render knobs resolved from configuration.
type Options struct {
	// Links is the current-version attachment set, in Web attachment order.
	Links []Link
	// StaleHours is only used to explain the wait in the subtitle.
	StaleHours int
}

// Render builds the card for an open gate.
func Render(s Subject, p *Project, w *WorkItem, opts Options) ([]byte, error) {
	gate, err := resolveGate(s, p, w)
	if err != nil {
		return nil, err
	}

	if s.Kind == KindProject {
		return renderProjectGate(s, gate)
	}

	body := gate.body
	if links := renderLinks(opts.Links); links != "" {
		body += "\n" + links
	}

	return marshal(card{
		Schema: "2.0",
		Config: cardConfig{UpdateMulti: true},
		Header: cardHeader{
			Title:    plainText(headerTitle(s, gate)),
			Subtitle: subtitle(s, gate),
			Template: templateOf(s),
		},
		Body: cardBody{Elements: []any{
			markdownElement{Tag: "markdown", Content: body},
			gate.form(),
			auxiliaryElement(s, w),
		}},
	})
}

// renderProjectGate keeps the review-gate cards as they were: they run no
// material import path and are not part of the acceptance-card rework, so only
// their document button went away with Options.DocURL.
func renderProjectGate(s Subject, gate gateLayout) ([]byte, error) {
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
		Body: cardBody{Elements: []any{
			markdownElement{Tag: "markdown", Content: gate.body},
			gate.form(),
		}},
	})
}

// RenderClosed rebuilds a card after the gate is gone: the form is dropped and
// the current-version links plus the outcome remain.
func RenderClosed(s Subject, w *WorkItem, statusLabel string, opts Options) ([]byte, error) {
	gate, err := resolveGate(s, nil, nil)
	if err != nil {
		return nil, err
	}

	body := "处理结果：" + statusLabel
	if links := renderLinks(opts.Links); links != "" {
		body += "\n" + links
	}

	return marshal(card{
		Schema: "2.0",
		Config: cardConfig{UpdateMulti: true},
		Header: cardHeader{
			Title:    plainText(headerTitle(s, gate)),
			Subtitle: subtitle(s, gate),
			Template: "grey",
		},
		Body: cardBody{Elements: []any{
			markdownElement{Tag: "markdown", Content: body},
			auxiliaryElement(s, w),
		}},
	})
}

// headerTitle is the "what is this task" line: the task title verbatim, then
// the project name, then the card type. It never falls back to Subject.ID — a
// header showing t_… is exactly what the rework removed.
func headerTitle(s Subject, gate gateLayout) string {
	if s.Title != "" {
		return s.Title
	}
	if s.ProjectName != "" {
		return s.ProjectName
	}
	return gate.label
}

// subtitle is "<project> · <card type>", or just the card type when the project
// name is unknown.
func subtitle(s Subject, gate gateLayout) *text {
	content := gate.label
	if s.ProjectName != "" {
		content = s.ProjectName + " · " + gate.label
	}
	sub := plainText(content)
	return &sub
}

// templateOf picks the semantic header colour: blue for "needs a human", orange
// for the failure verdict, grey for a closed card. The Stale → red branch is
// kept as-is (V3 never sets Stale).
func templateOf(s Subject) string {
	if s.Stale {
		return "red"
	}
	if s.State == StateFailed {
		return "orange"
	}
	return "blue"
}

// auxiliaryElement is the footer: the raw ids stay available for the operator,
// but at notation size (12px, grey) so they do not compete with the task title.
func auxiliaryElement(s Subject, w *WorkItem) markdownElement {
	content := "任务 " + s.ID
	if w != nil && w.ReportID != "" {
		content += " · 报告 " + w.ReportID
	}
	return markdownElement{
		Tag:      "markdown",
		Content:  "<font color='grey'>" + content + "</font>",
		TextSize: "notation",
	}
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

// form renders the approval form of the gate.
func (g gateLayout) form() formElement {
	return formElement{
		Tag:  "form",
		Name: "gate_form",
		Elements: []any{
			inputElement{
				Tag:         "input",
				Name:        "reason",
				Required:    g.reasonRequired,
				Placeholder: plainText("打回理由（通过可留空）"),
			},
			columnSetElement{
				Tag:     "column_set",
				Columns: g.buttonColumns(),
			},
		},
	}
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
			body := statusLine(s.Age, w, "提交")
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
			body := statusLine(s.Age, w, "失败")
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

// statusLine is the body's first line: how long the item has been waiting and
// who submitted which attempt. No raw ids here — those live in the footer.
func statusLine(age time.Duration, w *WorkItem, verb string) string {
	parts := []string{"已等待 " + humanAge(age)}
	if w == nil {
		return strings.Join(parts, " · ")
	}
	switch {
	case w.Worker != "":
		parts = append(parts, w.Worker+" "+attemptPhrase(w, verb))
	case w.MaxAttempts > 0:
		parts = append(parts, attemptPhrase(w, verb))
	}
	return strings.Join(parts, " · ")
}

func attemptPhrase(w *WorkItem, verb string) string {
	if w.MaxAttempts > 0 {
		return fmt.Sprintf("第 %d/%d 次%s", w.Attempt, w.MaxAttempts, verb)
	}
	return verb
}

func materialLine(p *Project, kind string) string {
	if p == nil || p.MaterialID == "" {
		return "材料：尚未提交"
	}
	return fmt.Sprintf("材料 %s v%d · 项目 v%d", shortID(p.MaterialID), p.MaterialVersion, p.Version)
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
