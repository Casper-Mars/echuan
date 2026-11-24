package im

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/im/card"
)

// fakeV3Source is the full §7.2 source: pending rows, the attachment
// projection and the task meta fallback.
type fakeV3Source struct {
	items       []V3Review
	attachments map[string][]MaterialRef
	attachErr   error
	metas       map[string]TaskMeta
	metaErr     error
}

func (f *fakeV3Source) PendingReviews(context.Context, int) ([]V3Review, error) {
	return f.items, nil
}

func (f *fakeV3Source) TaskAttachments(_ context.Context, taskID string) ([]MaterialRef, error) {
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	return f.attachments[taskID], nil
}

func (f *fakeV3Source) TaskMeta(_ context.Context, taskID string) (TaskMeta, error) {
	if f.metaErr != nil {
		return TaskMeta{}, f.metaErr
	}
	return f.metas[taskID], nil
}

// legacyV3Source is a source from before §7.2: it can only name the latest
// output material.
type legacyV3Source struct {
	items    []V3Review
	material map[string]string
}

func (f *legacyV3Source) PendingReviews(context.Context, int) ([]V3Review, error) {
	return f.items, nil
}

func (f *legacyV3Source) LatestOutputMaterialID(_ context.Context, taskID string) (string, error) {
	return f.material[taskID], nil
}

type fakeCardStore struct {
	rows   map[string]CardRecord
	closed map[string]bool
}

func newFakeCardStore() *fakeCardStore {
	return &fakeCardStore{rows: map[string]CardRecord{}, closed: map[string]bool{}}
}

func (f *fakeCardStore) List(context.Context) ([]CardRecord, error) {
	out := make([]CardRecord, 0, len(f.rows))
	for _, row := range f.rows {
		row.Closed = f.closed[row.SubjectID]
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeCardStore) Put(_ context.Context, rec CardRecord) error {
	f.rows[rec.SubjectID] = rec
	return nil
}

func (f *fakeCardStore) Close(_ context.Context, _, id string, _ time.Time) error {
	f.closed[id] = true
	return nil
}

type fakeV3Messenger struct {
	sent        []string
	patched     []string
	patchBodies []string
}

func (f *fakeV3Messenger) SendCard(_ context.Context, _, cardJSON string) (string, error) {
	f.sent = append(f.sent, cardJSON)
	return "om_1", nil
}

func (f *fakeV3Messenger) PatchCard(_ context.Context, messageID, cardJSON string) error {
	f.patched = append(f.patched, messageID)
	f.patchBodies = append(f.patchBodies, cardJSON)
	return nil
}

// fakeDocProvider mimics the Docs service: Ensure imports once and remembers
// the URL, so a second lookup is a cache hit.
type fakeDocProvider struct {
	cached    map[string]string
	failFor   map[string]error
	ensureErr error
	ensureAll []MaterialRef
	imports   int
	// onEnsure runs before the import result is returned, so a test can move
	// the attachment set while the import is "in flight".
	onEnsure func(materialID string)
}

func newFakeDocProvider() *fakeDocProvider {
	return &fakeDocProvider{cached: map[string]string{}, failFor: map[string]error{}}
}

func (f *fakeDocProvider) Cached(_ context.Context, materialID string) string {
	return f.cached[materialID]
}

func (f *fakeDocProvider) Ensure(_ context.Context, materialID, docName string) (string, error) {
	f.imports++
	f.ensureAll = append(f.ensureAll, MaterialRef{MaterialID: materialID, Title: docName})
	if f.onEnsure != nil {
		f.onEnsure(materialID)
	}
	if err := f.failFor[materialID]; err != nil {
		return "", err
	}
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	url := "https://example.feishu.cn/docx/" + materialID
	f.cached[materialID] = url
	return url, nil
}

func newV3Poller(t *testing.T, source V3ReviewSource, cards CardStore, messenger Messenger, logger *slog.Logger) *V3Poller {
	t.Helper()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	return NewV3Poller(Config{
		Enabled: true, AllowedChatIDs: []string{"oc_1"}, PollInterval: time.Minute, MaxCards: 10,
	}, source, cards, messenger, logger, func() time.Time { return now })
}

func review(taskTitle string) V3Review {
	return V3Review{
		TaskID: "t_01M84QA8E3S4ZT47MD3P76RSF4", ProjectID: "echuan-15",
		ProjectName: "驿传", TaskTitle: taskTitle, Kind: "dev",
		Status: "in_review", Attempt: 1, MaxAttempts: 3, WorkerID: "dev-1",
		UpdateTime: "2026-11-18T11:55:00.000Z",
	}
}

// threeAttachments is a brief plus two bodies: three rows, in Web order.
func threeAttachments() []MaterialRef {
	return []MaterialRef{
		{MaterialID: "m_brief", Title: "需求简报.md", Kind: "context", Role: "input"},
		{MaterialID: "m_plan", Title: "实施计划.md", Kind: "plan", Role: "output"},
		{MaterialID: "m_report", Title: "验收报告.md", Kind: "report", Role: "output"},
	}
}

func decodeCard(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode card: %v\n%s", err, raw)
	}
	return doc
}

func cardHeaderField(t *testing.T, doc map[string]any, key string) string {
	t.Helper()
	header, ok := doc["header"].(map[string]any)
	if !ok {
		t.Fatalf("card has no header: %v", doc)
	}
	node, ok := header[key].(map[string]any)
	if !ok {
		return ""
	}
	content, _ := node["content"].(string)
	return content
}

// cardMarkdown returns every markdown element of the card body, in order.
func cardMarkdown(t *testing.T, doc map[string]any) []map[string]any {
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

func cardBodyText(t *testing.T, doc map[string]any) string {
	t.Helper()
	marks := cardMarkdown(t, doc)
	if len(marks) == 0 {
		t.Fatalf("card has no markdown body: %v", doc)
	}
	content, _ := marks[0]["content"].(string)
	return content
}

// AC1/AC3/AC4: every current-version attachment becomes one link row, in the
// same order as the Web attachment area, with no doc button and no "看全文".
func TestV3PollerRendersOneLinePerAttachment(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, nil)
	docs := newFakeDocProvider()
	for _, ref := range threeAttachments() {
		docs.cached[ref.MaterialID] = "https://example.feishu.cn/docx/" + ref.MaterialID
	}
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	body := cardBodyText(t, decodeCard(t, messenger.sent[0]))
	lines := strings.Split(body, "\n")
	if len(lines) != 4 {
		t.Fatalf("body lines = %d, want status + 3 links:\n%s", len(lines), body)
	}
	want := []string{
		"已等待 5 分钟 · dev-1 第 1/3 次提交",
		"1. [需求简报.md](https://example.feishu.cn/docx/m_brief)",
		"2. [实施计划.md](https://example.feishu.cn/docx/m_plan)",
		"3. [验收报告.md](https://example.feishu.cn/docx/m_report)",
	}
	for i, line := range want {
		if lines[i] != line {
			t.Errorf("line %d = %q, want %q", i, lines[i], line)
		}
	}
	if got := strings.Count(body, "]("); got != 3 {
		t.Errorf("link rows = %d, want 3", got)
	}
	if strings.Contains(messenger.sent[0], "open_doc") || strings.Contains(messenger.sent[0], "看全文") {
		t.Errorf("card still carries the doc button: %s", messenger.sent[0])
	}
}

// AC21/AC22/AC24: header title is the task title verbatim, the subtitle is
// "<project> · 交付验收", and the raw task id only shows up in the notation
// footer.
func TestV3PollerHeaderShowsTaskTitleOnly(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, nil)
	docs := newFakeDocProvider()
	for _, ref := range threeAttachments() {
		docs.cached[ref.MaterialID] = "https://example.feishu.cn/docx/" + ref.MaterialID
	}
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	doc := decodeCard(t, messenger.sent[0])
	if got := cardHeaderField(t, doc, "title"); got != "补齐库存接口" {
		t.Errorf("header.title = %q, want the task title", got)
	}
	if got := cardHeaderField(t, doc, "subtitle"); got != "驿传 · 交付验收" {
		t.Errorf("header.subtitle = %q", got)
	}
	header, _ := json.Marshal(doc["header"])
	if strings.Contains(string(header), "t_") || strings.Contains(string(header), "r_") {
		t.Errorf("header carries a raw business id: %s", header)
	}

	marks := cardMarkdown(t, doc)
	footer, _ := marks[len(marks)-1]["content"].(string)
	if size, _ := marks[len(marks)-1]["text_size"].(string); size != "notation" {
		t.Errorf("footer text_size = %q, want notation", size)
	}
	if !strings.Contains(footer, "任务 t_01M84QA8E3S4ZT47MD3P76RSF4") {
		t.Errorf("footer lost the task id: %q", footer)
	}
	if strings.Contains(cardBodyText(t, doc), "t_") {
		t.Errorf("task id leaked into the body: %q", cardBodyText(t, doc))
	}
}

// AC7/AC8/AC9: one failing import degrades exactly that row to its file name;
// the other rows keep their links and the form / buttons stay.
func TestV3PollerPartialImportFailureDegradesOneRow(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, nil)
	docs := newFakeDocProvider()
	docs.failFor["m_plan"] = errors.New("feishu: tenant token code=99991663 msg=invalid app_secret")
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.patchBodies) == 0 {
		t.Fatal("no patch after the successful imports")
	}
	patched := decodeCard(t, messenger.patchBodies[len(messenger.patchBodies)-1])
	body := cardBodyText(t, patched)
	if !strings.Contains(body, "[需求简报.md](https://example.feishu.cn/docx/m_brief)") {
		t.Errorf("brief row lost its link: %q", body)
	}
	if !strings.Contains(body, "\n2. 实施计划.md") {
		t.Errorf("failed row is not plain text: %q", body)
	}
	if strings.Contains(body, "实施计划.md](") {
		t.Errorf("failed row still renders as a link: %q", body)
	}
	raw := messenger.patchBodies[len(messenger.patchBodies)-1]
	for _, want := range []string{`"name":"gate_form"`, `"name":"approve"`, `"name":"reject"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("card lost %s after a failed import: %s", want, raw)
		}
	}
	for _, banned := range []string{"失败", "无链接", "暂无", "生成中"} {
		if strings.Contains(body, banned) {
			t.Errorf("degraded row carries copy %q: %q", banned, body)
		}
	}
}

// AC11: five ticks against a permanently failing import leave the counter at
// two; afterwards the only line about that material is the attempt-cap skip.
func TestV3PollerAttemptCapStopsAfterTwoTicks(t *testing.T) {
	buf, logger := logBuffer()
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, logger)
	docs := newFakeDocProvider()
	docs.ensureErr = errors.New("feishu: network unreachable")
	poller.SetDocs(docs)

	for i := 0; i < 5; i++ {
		poller.tick(context.Background())
		poller.fillWG.Wait()
	}

	poller.mu.Lock()
	attempts := poller.states["t_01M84QA8E3S4ZT47MD3P76RSF4"].attempts["m_brief"]
	poller.mu.Unlock()
	if attempts != maxImportAttempts {
		t.Errorf("attempts = %d, want %d", attempts, maxImportAttempts)
	}
	if got := strings.Count(buf.String(), "result=failed"); got != 3*maxImportAttempts {
		t.Errorf("`im doc import attempt` failures = %d, want %d:\n%s", got, 3*maxImportAttempts, buf.String())
	}
	if !strings.Contains(buf.String(), "im doc fill skipped") || !strings.Contains(buf.String(), "reason=attempt_cap") {
		t.Errorf("missing the attempt-cap skip line:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("attempt cap logged as an error:\n%s", buf.String())
	}
}

// AC18/AC19: the first frame has no import yet; once the document exists the
// card is patched and the message id does not change.
func TestV3PollerFillPatchesSameMessageID(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, nil)
	docs := newFakeDocProvider()
	docs.cached["m_brief"] = "https://example.feishu.cn/docx/m_brief"
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	if strings.Contains(messenger.sent[0], "docx/m_plan") {
		t.Errorf("first frame already carried the pending link: %s", messenger.sent[0])
	}
	if len(messenger.patched) == 0 {
		t.Fatal("no patch after the imports succeeded")
	}
	for i, id := range messenger.patched {
		if id != "om_1" {
			t.Errorf("patch %d went to message %q, want om_1", i, id)
		}
	}
	if len(messenger.sent) != 1 {
		t.Errorf("a second card was sent instead of patching: %d", len(messenger.sent))
	}
}

// AC13: a stable attachment set renders byte-identical bodies and the second
// tick patches nothing.
func TestV3PollerStableSetDoesNotRepatch(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	cards := newFakeCardStore()
	poller := newV3Poller(t, source, cards, messenger, nil)
	docs := newFakeDocProvider()
	for _, ref := range threeAttachments() {
		docs.cached[ref.MaterialID] = "https://example.feishu.cn/docx/" + ref.MaterialID
	}
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()
	sentBody := messenger.sent[0]

	poller.mu.Lock()
	firstBody := poller.states["t_01M84QA8E3S4ZT47MD3P76RSF4"].lastBody
	poller.mu.Unlock()

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 || len(messenger.patched) != 0 {
		t.Fatalf("second tick sent=%d patched=%d, want 1/0", len(messenger.sent), len(messenger.patched))
	}
	poller.mu.Lock()
	secondBody := poller.states["t_01M84QA8E3S4ZT47MD3P76RSF4"].lastBody
	poller.mu.Unlock()
	if firstBody != secondBody {
		t.Errorf("body changed between two stable ticks:\n%q\n%q", firstBody, secondBody)
	}
	if firstBody != sentBody {
		t.Errorf("remembered body differs from the sent one:\n%q\n%q", firstBody, sentBody)
	}
	if fingerprint := cards.rows["t_01M84QA8E3S4ZT47MD3P76RSF4"].Fingerprint; fingerprint == "" {
		t.Error("card record lost its fingerprint")
	}
}

// AC12: a new version replaces the old row within one tick, and the same
// message is patched.
func TestV3PollerNewVersionDropsOldRow(t *testing.T) {
	source := &fakeV3Source{
		items: []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": {
			{MaterialID: "m_v1", Title: "验收报告-v1.md", Kind: "report", Role: "output"},
		}},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, nil)
	docs := newFakeDocProvider()
	docs.cached["m_v1"] = "https://example.feishu.cn/docx/m_v1"
	docs.cached["m_v2"] = "https://example.feishu.cn/docx/m_v2"
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()
	if !strings.Contains(messenger.sent[0], "m_v1") {
		t.Fatalf("first card lacks v1: %s", messenger.sent[0])
	}

	source.attachments["t_01M84QA8E3S4ZT47MD3P76RSF4"] = []MaterialRef{
		{MaterialID: "m_v2", Title: "验收报告-v2.md", Kind: "report", Role: "output"},
	}
	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.patched) != 1 || messenger.patched[0] != "om_1" {
		t.Fatalf("patched = %v, want the same message", messenger.patched)
	}
	body := cardBodyText(t, decodeCard(t, messenger.patchBodies[0]))
	if strings.Contains(body, "m_v1") || strings.Contains(body, "验收报告-v1.md") {
		t.Errorf("old version row survived: %q", body)
	}
	if !strings.Contains(body, "[验收报告-v2.md](https://example.feishu.cn/docx/m_v2)") {
		t.Errorf("new version row missing: %q", body)
	}
}

// AC17/AC25: the terminal card keeps the task title in the header, shows the
// outcome, the links and the notation footer, and drops the form.
func TestV3PollerClosedCardUsesTaskTitle(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	cards := newFakeCardStore()
	poller := newV3Poller(t, source, cards, messenger, nil)
	docs := newFakeDocProvider()
	for _, ref := range threeAttachments() {
		docs.cached[ref.MaterialID] = "https://example.feishu.cn/docx/" + ref.MaterialID
	}
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	source.items = nil
	poller.tick(context.Background())
	poller.fillWG.Wait()

	if !cards.closed["t_01M84QA8E3S4ZT47MD3P76RSF4"] {
		t.Fatal("card was not closed after the task left in_review")
	}
	if len(messenger.patchBodies) != 1 {
		t.Fatalf("closed patch bodies = %d, want 1", len(messenger.patchBodies))
	}
	doc := decodeCard(t, messenger.patchBodies[0])
	if got := cardHeaderField(t, doc, "title"); got != "补齐库存接口" {
		t.Errorf("closed header.title = %q, want the task title, not the task id", got)
	}
	if got := cardHeaderField(t, doc, "subtitle"); got != "驿传 · 交付验收" {
		t.Errorf("closed header.subtitle = %q", got)
	}
	body := cardBodyText(t, doc)
	if !strings.HasPrefix(body, "处理结果：已处理") {
		t.Errorf("closed body does not start with the outcome: %q", body)
	}
	if got := strings.Count(body, "]("); got != 3 {
		t.Errorf("closed card link rows = %d, want 3: %q", got, body)
	}
	marks := cardMarkdown(t, doc)
	if size, _ := marks[len(marks)-1]["text_size"].(string); size != "notation" {
		t.Errorf("closed footer text_size = %q", size)
	}
	if strings.Contains(messenger.patchBodies[0], `"gate_form"`) {
		t.Errorf("closed card still carries the form: %s", messenger.patchBodies[0])
	}
}

// AC25 fallback: without a cached title and without TaskMeta the header shows
// the card type and the miss is logged — never the raw task id.
func TestV3PollerCloseWithoutMetaFallsBackToCardType(t *testing.T) {
	buf, logger := logBuffer()
	source := &fakeV3Source{metaErr: errors.New("task not found")}
	cards := newFakeCardStore()
	cards.rows["t_01M84QA8E3S4ZT47MD3P76RSF4"] = CardRecord{
		SubjectKind: card.KindWorkItem, SubjectID: "t_01M84QA8E3S4ZT47MD3P76RSF4",
		ChatID: "oc_1", MessageID: "om_7", Fingerprint: "fp", SentAt: time.Now(),
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, cards, messenger, logger)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if !strings.Contains(buf.String(), "im card close meta missing") {
		t.Errorf("missing `im card close meta missing`:\n%s", buf.String())
	}
	if len(messenger.patchBodies) != 1 {
		t.Fatalf("closed patch bodies = %d, want 1", len(messenger.patchBodies))
	}
	doc := decodeCard(t, messenger.patchBodies[0])
	if got := cardHeaderField(t, doc, "title"); got != "交付验收" {
		t.Errorf("closed header.title = %q, want the card type", got)
	}
}

// §10.1: one `im card rendered` line carries every field the runbook greps for.
func TestV3PollerLogsCardRenderedFields(t *testing.T) {
	buf, logger := logBuffer()
	longTitle := strings.Repeat("很长的任务标题", 12) // 84 runes > 60
	source := &fakeV3Source{
		items:       []V3Review{review(longTitle)},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, logger)
	docs := newFakeDocProvider()
	docs.cached["m_brief"] = "https://example.feishu.cn/docx/m_brief"
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	logged := buf.String()
	for _, want := range []string{
		"im card rendered", "task=t_01M84QA8E3S4ZT47MD3P76RSF4", "message_id=om_1",
		"action=send", "set_size=3", "links_total=1", "links_missing=2",
		"links_missing_ids=m_plan,m_report", "fingerprint=", "title_len=84", "card_bytes=",
		"im card title long",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log missing %q:\n%s", want, logged)
		}
	}
}

// An unreadable attachment projection skips the task instead of sending a card
// without its files.
func TestV3PollerSkipsTaskWhenAttachmentsFail(t *testing.T) {
	buf, logger := logBuffer()
	source := &fakeV3Source{items: []V3Review{review("补齐库存接口")}, attachErr: errors.New("db is locked")}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, logger)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 0 {
		t.Errorf("sent %d cards despite an attachment error", len(messenger.sent))
	}
	if !strings.Contains(buf.String(), "im attachments resolve failed") {
		t.Errorf("missing `im attachments resolve failed`:\n%s", buf.String())
	}
}

// With the importer switched off every row is plain text, the form stays, and
// the reason is logged once per card.
func TestV3PollerDocsDisabledDegradesAllRows(t *testing.T) {
	buf, logger := logBuffer()
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, logger)

	poller.tick(context.Background())
	poller.fillWG.Wait()
	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	body := cardBodyText(t, decodeCard(t, messenger.sent[0]))
	if strings.Contains(body, "](") {
		t.Errorf("rows carry links without an importer: %q", body)
	}
	if !strings.Contains(messenger.sent[0], `"name":"gate_form"`) {
		t.Errorf("form lost: %s", messenger.sent[0])
	}
	if got := strings.Count(buf.String(), "reason=docs_disabled"); got != 1 {
		t.Errorf("`im doc fill skipped` docs_disabled logged %d times, want 1:\n%s", got, buf.String())
	}
	if len(messenger.patched) != 0 {
		t.Errorf("patched %d times with the importer off", len(messenger.patched))
	}
}

// A fill that finishes after the task left in_review must not paint the open
// card body over the terminal card.
func TestV3PollerLateFillDoesNotReopenClosedCard(t *testing.T) {
	source := &fakeV3Source{
		items:       []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": threeAttachments()},
	}
	messenger := &fakeV3Messenger{}
	cards := newFakeCardStore()
	poller := newV3Poller(t, source, cards, messenger, nil)
	docs := newFakeDocProvider()
	entered := make(chan struct{})
	release := make(chan struct{})
	docs.onEnsure = func(materialID string) {
		if materialID != "m_brief" {
			return
		}
		close(entered)
		<-release
	}
	poller.SetDocs(docs)

	poller.tick(context.Background())
	<-entered

	source.items = nil
	poller.tick(context.Background())
	if len(messenger.patchBodies) != 1 {
		t.Fatalf("closed patch bodies = %d, want 1", len(messenger.patchBodies))
	}

	close(release)
	poller.fillWG.Wait()

	if len(messenger.patchBodies) != 1 {
		t.Fatalf("late fill patched the closed card: %d bodies", len(messenger.patchBodies))
	}
	body := cardBodyText(t, decodeCard(t, messenger.patchBodies[0]))
	if !strings.HasPrefix(body, "处理结果：已处理") {
		t.Errorf("closed card body changed: %q", body)
	}
}

// A version replaced while its import is running must not surface on the card:
// the URL stays cached for other cards, this one logs not_in_set.
func TestV3PollerDropsFilledLinkWhenVersionReplaced(t *testing.T) {
	buf, logger := logBuffer()
	source := &fakeV3Source{
		items: []V3Review{review("补齐库存接口")},
		attachments: map[string][]MaterialRef{"t_01M84QA8E3S4ZT47MD3P76RSF4": {
			{MaterialID: "m_v1", Title: "验收报告-v1.md", Kind: "report", Role: "output"},
		}},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, logger)
	docs := newFakeDocProvider()
	docs.onEnsure = func(string) {
		source.attachments["t_01M84QA8E3S4ZT47MD3P76RSF4"] = []MaterialRef{
			{MaterialID: "m_v2", Title: "验收报告-v2.md", Kind: "report", Role: "output"},
		}
	}
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if !strings.Contains(buf.String(), "im doc fill skipped") || !strings.Contains(buf.String(), "reason=not_in_set") {
		t.Errorf("missing the not_in_set skip:\n%s", buf.String())
	}
	for _, body := range messenger.patchBodies {
		if strings.Contains(body, "m_v1") {
			t.Errorf("a replaced version reached the card: %s", body)
		}
	}
}

// The legacy latest-output source still renders one row: partial wiring must
// not blank the card out.
func TestV3PollerLegacyMaterialSourceRendersOneRow(t *testing.T) {
	source := &legacyV3Source{
		items:    []V3Review{review("补齐库存接口")},
		material: map[string]string{"t_01M84QA8E3S4ZT47MD3P76RSF4": "m_only"},
	}
	messenger := &fakeV3Messenger{}
	poller := newV3Poller(t, source, newFakeCardStore(), messenger, nil)
	poller.SetMaterials(source)
	docs := newFakeDocProvider()
	docs.cached["m_only"] = "https://example.feishu.cn/docx/m_only"
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if strings.Count(cardBodyText(t, decodeCard(t, messenger.sent[0])), "](") != 1 {
		t.Errorf("legacy source rows wrong: %s", messenger.sent[0])
	}
}
