package card

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// goldenCase covers every card shape: the three review gates, the acceptance
// and failure-verdict cards, the degenerated link sets (none / partially
// imported), a long title full of markdown syntax, and the terminal card.
type goldenCase struct {
	name    string
	subject Subject
	project *Project
	item    *WorkItem
	opts    Options
	closed  bool
	status  string
}

// longTitle is over 60 runes and carries every markdown character the escape
// table knows about plus a line break.
const longTitle = "验收：[库存] 接口 *重写* _兼容层_ ~迁移~ `脚本` \\ 与\n第二行标题（含括号）"

func goldenCases() []goldenCase {
	return []goldenCase{
		{
			name: "project_requirement_review",
			subject: Subject{
				Kind: KindProject, ID: "APP-12", State: StateRequirementReview,
				Title: "供应链看板改版", Age: 6 * time.Hour,
			},
			project: &Project{ProductID: "APP", Version: 3, MaterialID: "m001abcdef", MaterialVersion: 3, MaterialKind: "requirement"},
		},
		{
			name: "project_spec_review_stale",
			subject: Subject{
				Kind: KindProject, ID: "APP-13", State: StateSpecReview,
				Title: "库存预警", Age: 80 * time.Hour, Stale: true,
				Reason: "缺少回滚方案",
			},
			project: &Project{ProductID: "APP", Version: 5, MaterialID: "m002deadbe", MaterialVersion: 2, MaterialKind: "spec"},
		},
		{
			name: "project_tasks_review_no_doc",
			subject: Subject{
				Kind: KindProject, ID: "APP-14", State: StateTasksReview,
				Title: "对账任务拆分", Age: 25 * time.Minute,
			},
			project: &Project{ProductID: "APP", Version: 1},
		},
		{
			name: "work_item_submitted",
			subject: Subject{
				Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
				Title: "补齐库存接口", ProjectName: "驿传", Age: 2 * time.Hour,
			},
			item: &WorkItem{ProjectID: "echuan-15", Worker: "dev-1", Attempt: 2, MaxAttempts: 3, ReportID: "r0099aabb"},
			opts: Options{Links: []Link{
				{MaterialID: "m001", Title: "方案-v2.md", URL: "https://example.feishu.cn/docx/aaa"},
				{MaterialID: "m002", Title: "接口清单.xlsx", URL: "https://example.feishu.cn/docx/bbb"},
				{MaterialID: "m003", Title: "交付说明.md", URL: "https://example.feishu.cn/docx/ccc"},
			}},
		},
		{
			name: "work_item_no_links",
			subject: Subject{
				Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
				Title: "补齐库存接口", ProjectName: "驿传", Age: 5 * time.Minute,
			},
			item: &WorkItem{ProjectID: "echuan-15", Worker: "dev-1", Attempt: 1, MaxAttempts: 3},
		},
		{
			name: "work_item_partial_links",
			subject: Subject{
				Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
				Title: "补齐库存接口", ProjectName: "驿传", Age: 3 * time.Hour,
			},
			item: &WorkItem{ProjectID: "echuan-15", Worker: "dev-1", Attempt: 1, MaxAttempts: 3, ReportID: "r0099aabb"},
			opts: Options{Links: []Link{
				{MaterialID: "m001", Title: "方案-v2.md", URL: "https://example.feishu.cn/docx/aaa"},
				{MaterialID: "m002", Title: "接口清单.xlsx"},
				{MaterialID: "m003", Title: "交付说明.md", URL: "https://example.feishu.cn/docx/ccc"},
			}},
		},
		{
			name: "work_item_long_title",
			subject: Subject{
				Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
				Title: longTitle, ProjectName: "驿传", Age: 90 * time.Minute,
			},
			item: &WorkItem{ProjectID: "echuan-15", Worker: "dev-2", Attempt: 1, MaxAttempts: 3},
			opts: Options{Links: []Link{
				{MaterialID: "m101", Title: "验收[报告] *终版*.md", URL: "https://example.feishu.cn/docx/ddd"},
				{MaterialID: "m102", Title: "补充_说明_(v2).md", URL: "https://example.feishu.cn/docx/eee"},
				{MaterialID: "m103"},
			}},
		},
		{
			name: "work_item_failed",
			subject: Subject{
				Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateFailed,
				Title: "对接旧 ERP", ProjectName: "驿传", Age: 30 * time.Hour, Reason: "依赖接口 502",
			},
			item: &WorkItem{ProjectID: "echuan-15", Worker: "dev-2", Attempt: 3, MaxAttempts: 3},
		},
		{
			name: "work_item_closed",
			subject: Subject{
				Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
				Title: "补齐库存接口", ProjectName: "驿传",
			},
			item:   &WorkItem{ProjectID: "echuan-15", Worker: "dev-1", Attempt: 2, MaxAttempts: 3, ReportID: "r0099aabb"},
			closed: true,
			status: "已处理",
			opts: Options{Links: []Link{
				{MaterialID: "m001", Title: "方案-v2.md", URL: "https://example.feishu.cn/docx/aaa"},
				{MaterialID: "m002", Title: "接口清单.xlsx"},
			}},
		},
	}
}

func renderCase(t *testing.T, tc goldenCase) []byte {
	t.Helper()
	var (
		raw []byte
		err error
	)
	if tc.closed {
		raw, err = RenderClosed(tc.subject, tc.item, tc.status, tc.opts)
	} else {
		raw, err = Render(tc.subject, tc.project, tc.item, tc.opts)
	}
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return raw
}

func TestRenderGolden(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			raw := renderCase(t, tc)
			path := filepath.Join("testdata", tc.name+".json")
			if *update {
				if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update): %v", err)
			}
			if string(raw) != strings.TrimRight(string(want), "\n") {
				t.Errorf("card mismatch for %s\ngot:  %s\nwant: %s", tc.name, raw, want)
			}
		})
	}
}

// TestGoldenCardsAreValidJSON guards against a golden file that was written but
// never parsed by Feishu.
func TestGoldenCardsAreValidJSON(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tc.name+".json"))
			if err != nil {
				t.Skip("golden missing")
			}
			var v map[string]any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("golden is not valid JSON: %v", err)
			}
			if v["schema"] != "2.0" {
				t.Errorf("schema = %v, want 2.0", v["schema"])
			}
		})
	}
}

// decode turns a rendered card into a generic map so the assertions can walk
// the JSON the way Feishu does, instead of grepping the encoded bytes.
func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	return doc
}

func headerOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	header, ok := doc["header"].(map[string]any)
	if !ok {
		t.Fatalf("card has no header: %v", doc)
	}
	return header
}

func headerField(t *testing.T, doc map[string]any, key string) string {
	t.Helper()
	node, ok := headerOf(t, doc)[key].(map[string]any)
	if !ok {
		return ""
	}
	content, _ := node["content"].(string)
	return content
}

func markdownElements(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	body, ok := doc["body"].(map[string]any)
	if !ok {
		t.Fatalf("card has no body: %v", doc)
	}
	elements, _ := body["elements"].([]any)
	out := make([]map[string]any, 0, len(elements))
	for _, element := range elements {
		typed, ok := element.(map[string]any)
		if !ok || typed["tag"] != "markdown" {
			continue
		}
		out = append(out, typed)
	}
	return out
}

func bodyMarkdown(t *testing.T, doc map[string]any) string {
	t.Helper()
	marks := markdownElements(t, doc)
	if len(marks) == 0 {
		t.Fatalf("card has no markdown element: %v", doc)
	}
	content, _ := marks[0]["content"].(string)
	return content
}

// TestRenderHeaderCarriesTaskTitle pins the rework: the header shows the task
// title verbatim, the subtitle shows "<project> · <card type>", and no raw
// business id appears anywhere in the header subtree.
func TestRenderHeaderCarriesTaskTitle(t *testing.T) {
	raw := renderCase(t, goldenCases()[3]) // work_item_submitted
	doc := decode(t, raw)

	if got := headerField(t, doc, "title"); got != "补齐库存接口" {
		t.Errorf("header.title = %q, want the task title", got)
	}
	if got := headerField(t, doc, "subtitle"); got != "驿传 · 交付验收" {
		t.Errorf("header.subtitle = %q, want 驿传 · 交付验收", got)
	}
	headerJSON, err := json.Marshal(headerOf(t, doc))
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	if strings.Contains(string(headerJSON), "t_") || strings.Contains(string(headerJSON), "r_") {
		t.Errorf("header carries a raw business id: %s", headerJSON)
	}
}

// TestRenderHeaderWithoutProjectName keeps the subtitle meaningful when the
// project name is unknown: only the card type remains.
func TestRenderHeaderWithoutProjectName(t *testing.T) {
	raw, err := Render(Subject{
		Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
		Title: "补齐库存接口", Age: time.Minute,
	}, nil, &WorkItem{Worker: "dev-1", Attempt: 1, MaxAttempts: 3}, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	doc := decode(t, raw)
	if got := headerField(t, doc, "subtitle"); got != "交付验收" {
		t.Errorf("header.subtitle = %q, want 交付验收", got)
	}
	if got := headerField(t, doc, "title"); got != "补齐库存接口" {
		t.Errorf("header.title = %q", got)
	}
}

// TestRenderHeaderFallsBackToProjectName is the second header fallback: an
// untitled task shows the project name, never the task id (AC21).
func TestRenderHeaderFallsBackToProjectName(t *testing.T) {
	raw, err := Render(Subject{
		Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
		ProjectName: "驿传", Age: time.Minute,
	}, nil, nil, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := headerField(t, decode(t, raw), "title"); got != "驿传" {
		t.Errorf("header.title = %q, want the project name", got)
	}
}

// TestRenderLongTitleIsVerbatim keeps the server from truncating: the client
// ellipsizes the fourth line, the payload keeps every character (AC14/AC26).
func TestRenderLongTitleIsVerbatim(t *testing.T) {
	raw := renderCase(t, goldenCases()[6]) // work_item_long_title
	doc := decode(t, raw)

	if got := headerField(t, doc, "title"); got != longTitle {
		t.Errorf("header.title changed:\n got %q\nwant %q", got, longTitle)
	}
	headerJSON, err := json.Marshal(headerOf(t, doc))
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	if strings.Contains(string(headerJSON), "…") {
		t.Errorf("header was truncated: %s", headerJSON)
	}
	if strings.Contains(string(headerJSON), `\u0026`) || strings.Contains(string(headerJSON), "&#") {
		t.Errorf("header title was entity-escaped; it is plain_text: %s", headerJSON)
	}
}

// TestRenderLinksEscapeMarkdown pins the entity table: the link text must not
// be parsed as markdown, and the URL must be left alone (AC15).
func TestRenderLinksEscapeMarkdown(t *testing.T) {
	got := renderLinks([]Link{
		{MaterialID: "m1", Title: "[方案] *v2* _草稿_ ~x~ `y` \\ & <b>", URL: "https://example.feishu.cn/docx/a_b?x=1&y=2"},
		{MaterialID: "m2", Title: "第一行\n第二行", URL: "https://example.feishu.cn/docx/c"},
		{MaterialID: "m3"},
	})
	want := "[&#91;方案&#93; &#42;v2&#42; &#95;草稿&#95; &sim;x&sim; &#96;y&#96; &#92; &amp; &lt;b&gt;]" +
		"(https://example.feishu.cn/docx/a_b?x=1&y=2)\n" +
		"[第一行 第二行](https://example.feishu.cn/docx/c)\n" +
		"m3"
	if got != want {
		t.Errorf("renderLinks:\n got %q\nwant %q", got, want)
	}
}

// TestRenderLinksOrderAndDegradation checks one line per attachment, in the
// given order, with an empty URL degrading to plain text without any copy.
func TestRenderLinksOrderAndDegradation(t *testing.T) {
	lines := strings.Split(renderLinks([]Link{
		{MaterialID: "m1", Title: "a.md", URL: "https://u/1"},
		{MaterialID: "m2", Title: "b.md"},
		{MaterialID: "m3", Title: "c.md", URL: "https://u/3"},
	}), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3: %v", len(lines), lines)
	}
	if lines[0] != "[a.md](https://u/1)" || lines[2] != "[c.md](https://u/3)" {
		t.Errorf("links out of order: %v", lines)
	}
	if lines[1] != "b.md" {
		t.Errorf("failed row = %q, want the bare file name", lines[1])
	}
	if strings.Contains(renderLinks([]Link{{MaterialID: "m2", Title: "b.md"}}), "](") {
		t.Error("failed row still renders as a link")
	}
}

// TestRenderNoLinkRowsWithoutAttachments covers the empty set: no link block,
// no placeholder copy (AC16).
func TestRenderNoLinkRowsWithoutAttachments(t *testing.T) {
	raw := renderCase(t, goldenCases()[4]) // work_item_no_links
	doc := decode(t, raw)
	if body := bodyMarkdown(t, doc); strings.Contains(body, "](") {
		t.Errorf("card without attachments renders link rows: %q", body)
	}
	if strings.Contains(string(raw), "看全文") || strings.Contains(string(raw), "open_doc") {
		t.Errorf("card still carries the doc button: %s", raw)
	}
}

// TestRenderKeepsFormWhenEveryImportFailed covers the worst case: all rows are
// plain text and the approval form is untouched (AC9).
func TestRenderKeepsFormWhenEveryImportFailed(t *testing.T) {
	raw, err := Render(Subject{
		Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
		Title: "补齐库存接口", ProjectName: "驿传", Age: 2 * time.Hour,
	}, nil, &WorkItem{Worker: "dev-1", Attempt: 1, MaxAttempts: 3}, Options{Links: []Link{
		{MaterialID: "m1", Title: "a.md"},
		{MaterialID: "m2", Title: "b.md"},
	}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"name":"gate_form"`, `"name":"approve"`, `"name":"reject"`, "a.md", "b.md"} {
		if !strings.Contains(body, want) {
			t.Errorf("card lost %s: %s", want, body)
		}
	}
	if strings.Contains(body, "](") {
		t.Errorf("failed rows rendered as links: %s", body)
	}
	for _, banned := range []string{"失败", "无链接", "暂无", "生成中"} {
		if strings.Contains(bodyMarkdown(t, decode(t, raw)), banned) {
			t.Errorf("degraded card carries copy %q", banned)
		}
	}
}

// TestRenderClosedCardShape pins the terminal card: task title in the header,
// the outcome plus the link rows in the body, the id footer, no form.
func TestRenderClosedCardShape(t *testing.T) {
	raw := renderCase(t, goldenCases()[8]) // work_item_closed
	doc := decode(t, raw)

	if got := headerField(t, doc, "title"); got != "补齐库存接口" {
		t.Errorf("closed header.title = %q, want the task title", got)
	}
	if got := headerField(t, doc, "subtitle"); got != "驿传 · 交付验收" {
		t.Errorf("closed header.subtitle = %q", got)
	}
	if template, _ := headerOf(t, doc)["template"].(string); template != "grey" {
		t.Errorf("closed header.template = %q, want grey", template)
	}
	body := bodyMarkdown(t, doc)
	if !strings.HasPrefix(body, "处理结果：已处理") {
		t.Errorf("closed body does not start with the outcome: %q", body)
	}
	if !strings.Contains(body, "[方案-v2.md](https://example.feishu.cn/docx/aaa)") || !strings.Contains(body, "\n接口清单.xlsx") {
		t.Errorf("closed body lost the link rows: %q", body)
	}
	if strings.Contains(string(raw), `"gate_form"`) {
		t.Error("closed card must not carry the form")
	}
}

// TestRenderClosedCardWithoutTitleFallsBackToCardType: no title anywhere means
// the card type, never the task id (AC25).
func TestRenderClosedCardWithoutTitleFallsBackToCardType(t *testing.T) {
	raw, err := RenderClosed(Subject{Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted}, nil, "已处理", Options{})
	if err != nil {
		t.Fatalf("RenderClosed: %v", err)
	}
	doc := decode(t, raw)
	if got := headerField(t, doc, "title"); got != "交付验收" {
		t.Errorf("closed header.title = %q, want 交付验收", got)
	}
	if got := headerField(t, doc, "subtitle"); got != "交付验收" {
		t.Errorf("closed header.subtitle = %q, want 交付验收", got)
	}
}

// TestRenderFooterCarriesIDsAtNotationSize: the raw ids live only in the last
// markdown element and that element is the notation-sized footer (AC24).
func TestRenderFooterCarriesIDsAtNotationSize(t *testing.T) {
	raw := renderCase(t, goldenCases()[3]) // work_item_submitted
	doc := decode(t, raw)
	marks := markdownElements(t, doc)
	last := marks[len(marks)-1]

	if size, _ := last["text_size"].(string); size != "notation" {
		t.Errorf("footer text_size = %q, want notation", size)
	}
	footer, _ := last["content"].(string)
	if !strings.Contains(footer, "任务 t_01M84QA8E3S4ZT47MD3P76RSF4") || !strings.Contains(footer, "报告 r0099aabb") {
		t.Errorf("footer lost the ids: %q", footer)
	}
	for _, mark := range marks[:len(marks)-1] {
		content, _ := mark["content"].(string)
		if strings.Contains(content, "t_") || strings.Contains(content, "r_") {
			t.Errorf("raw id outside the footer: %q", content)
		}
	}
}

// TestRenderBodyFirstLineIsStatus pins the body's first line: wait + worker +
// attempt, no bold title row (AC23).
func TestRenderBodyFirstLineIsStatus(t *testing.T) {
	raw := renderCase(t, goldenCases()[3]) // work_item_submitted
	first := strings.SplitN(bodyMarkdown(t, decode(t, raw)), "\n", 2)[0]
	if first != "已等待 2 小时 · dev-1 第 2/3 次提交" {
		t.Errorf("first body line = %q", first)
	}
	if strings.HasPrefix(first, "**") {
		t.Errorf("body still starts with a bold title row: %q", first)
	}
}

func TestRenderWithoutWorkerStillExplainsAttempt(t *testing.T) {
	raw, err := Render(Subject{
		Kind: KindWorkItem, ID: "t_01M84QA8E3S4ZT47MD3P76RSF4", State: StateSubmitted,
		Title: "补齐库存接口", Age: 30 * time.Minute,
	}, nil, &WorkItem{Attempt: 1, MaxAttempts: 3}, Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	first := strings.SplitN(bodyMarkdown(t, decode(t, raw)), "\n", 2)[0]
	if first != "已等待 30 分钟 · 第 1/3 次提交" {
		t.Errorf("first body line = %q", first)
	}
}

func TestRenderFailedCardIsOrangeAndRejectOnly(t *testing.T) {
	raw := renderCase(t, goldenCases()[7]) // work_item_failed
	doc := decode(t, raw)
	if template, _ := headerOf(t, doc)["template"].(string); template != "orange" {
		t.Errorf("failed card template = %q, want orange", template)
	}
	body := string(raw)
	if !strings.Contains(body, `"name":"reject"`) || strings.Contains(body, `"name":"approve"`) {
		t.Errorf("failed card buttons wrong: %s", body)
	}
	if !strings.Contains(bodyMarkdown(t, doc), "失败原因：依赖接口 502") {
		t.Errorf("failed card lost the reason: %s", body)
	}
}

func TestRenderUnsupportedState(t *testing.T) {
	_, err := Render(Subject{Kind: KindProject, ID: "APP-1", State: "completed"}, nil, nil, Options{})
	if err == nil {
		t.Fatal("Render(completed) = nil error, want unsupported state")
	}
	if _, err := RenderClosed(Subject{Kind: KindWorkItem, ID: "t_1", State: "done"}, nil, "已处理", Options{}); err == nil {
		t.Fatal("RenderClosed(done) = nil error, want unsupported state")
	}
}

func TestValidateNames(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{"valid", `{"elements":[{"name":"a"},{"name":"b"}]}`, ""},
		{"duplicate", `{"elements":[{"name":"a"},{"name":"a"}]}`, "duplicate component names"},
		{"empty", `{"elements":[{"name":""}]}`, "empty name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNames([]byte(tc.raw))
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("validateNames() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("validateNames() = %v, want %q", err, tc.wantSub)
			}
		})
	}
}

func TestHumanAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "不到 1 分钟"},
		{90 * time.Minute, "1 小时"},
		{80 * time.Hour, "3 天"},
	}
	for _, tc := range tests {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
