package im

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type fakeV3Source struct {
	items []V3Review
}

func (f *fakeV3Source) PendingReviews(context.Context, int) ([]V3Review, error) {
	return f.items, nil
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

func TestV3PollerSendsAndDedupesByFingerprint(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	source := &fakeV3Source{items: []V3Review{{
		TaskID: "t_1", ProjectID: "APP-1", Kind: "dev", Title: "实现接口",
		Status: "in_review", Attempt: 1, MaxAttempts: 3,
		UpdateTime: "2026-11-18T11:55:00.000Z",
	}}}
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := NewV3Poller(Config{
		Enabled: true, AllowedChatIDs: []string{"oc_1"}, PollInterval: time.Minute, MaxCards: 10,
	}, source, cards, messenger, nil, func() time.Time { return now })

	poller.tick(context.Background())
	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	if len(cards.rows) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards.rows))
	}

	// Same review: fingerprint unchanged, no second message.
	poller.tick(context.Background())
	if len(messenger.sent) != 1 {
		t.Fatalf("sent after dedupe = %d, want 1", len(messenger.sent))
	}

	// Updated review: patch the existing card.
	source.items[0].UpdateTime = "2026-11-18T11:59:00.000Z"
	poller.tick(context.Background())
	if len(messenger.sent) != 1 || len(messenger.patched) != 1 {
		t.Fatalf("sent=%d patched=%d, want 1/1", len(messenger.sent), len(messenger.patched))
	}
}

func TestV3PollerClosesCardWhenTaskLeavesReview(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	source := &fakeV3Source{items: []V3Review{{
		TaskID: "t_1", ProjectID: "APP-1", Kind: "dev", Title: "实现接口",
		Status: "in_review", Attempt: 1, MaxAttempts: 3,
		UpdateTime: "2026-11-18T11:55:00.000Z",
	}}}
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := NewV3Poller(Config{
		Enabled: true, AllowedChatIDs: []string{"oc_1"}, PollInterval: time.Minute, MaxCards: 10,
	}, source, cards, messenger, nil, func() time.Time { return now })

	poller.tick(context.Background())
	source.items = nil
	poller.tick(context.Background())

	if !cards.closed["t_1"] {
		t.Fatal("card was not closed after the task left in_review")
	}
	if len(messenger.patched) != 1 {
		t.Fatalf("patched = %d, want a closed-card patch", len(messenger.patched))
	}
}

// fakeDocProvider mimics the Docs service: Ensure imports once and remembers
// the URL, so a second lookup is a cache hit.
type fakeDocProvider struct {
	cached    map[string]string
	ensureErr error
	imports   int
}

func newFakeDocProvider() *fakeDocProvider {
	return &fakeDocProvider{cached: map[string]string{}}
}

func (f *fakeDocProvider) Cached(_ context.Context, materialID string) string {
	return f.cached[materialID]
}

func (f *fakeDocProvider) Ensure(_ context.Context, materialID, _ string) (string, error) {
	f.imports++
	if f.ensureErr != nil {
		return "", f.ensureErr
	}
	url := "https://example.feishu.cn/docx/" + materialID
	f.cached[materialID] = url
	return url, nil
}

type fakeMaterialSource struct {
	materials map[string]string
	err       error
}

func (f *fakeMaterialSource) LatestOutputMaterialID(_ context.Context, taskID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.materials[taskID], nil
}

func newDocPoller(t *testing.T, source V3ReviewSource, cards CardStore, messenger Messenger, logger *slog.Logger) *V3Poller {
	t.Helper()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	return NewV3Poller(Config{
		Enabled: true, AllowedChatIDs: []string{"oc_1"}, PollInterval: time.Minute, MaxCards: 10,
	}, source, cards, messenger, logger, func() time.Time { return now })
}

func reviewFixture() *fakeV3Source {
	return &fakeV3Source{items: []V3Review{{
		TaskID: "t_1", ProjectID: "APP-1", Kind: "dev", Title: "实现接口",
		Status: "in_review", Attempt: 1, MaxAttempts: 3, WorkerID: "dev-1",
		UpdateTime: "2026-11-18T11:55:00.000Z",
	}}}
}

// AC1 + AC2: the acceptance card gains the "看全文" button after the import, and
// the same material is never imported twice.
func TestV3PollerAddsDocLinkOncePerMaterial(t *testing.T) {
	source := reviewFixture()
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := newDocPoller(t, source, cards, messenger, nil)
	poller.SetMaterials(&fakeMaterialSource{materials: map[string]string{"t_1": "m001"}})
	docs := newFakeDocProvider()
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	if strings.Contains(messenger.sent[0], "看全文") {
		t.Errorf("first render already carries the button: %s", messenger.sent[0])
	}
	if docs.imports != 1 {
		t.Fatalf("imports = %d, want 1", docs.imports)
	}
	if len(messenger.patchBodies) != 1 {
		t.Fatalf("patch bodies = %d, want 1 (doc filled in)", len(messenger.patchBodies))
	}
	filled := messenger.patchBodies[0]
	if !strings.Contains(filled, "看全文") || !strings.Contains(filled, "https://example.feishu.cn/docx/m001") {
		t.Errorf("filled card lacks the doc link: %s", filled)
	}
	if strings.Contains(filled, "文档生成中") {
		t.Errorf("filled card still carries the placeholder: %s", filled)
	}

	// The task is still in review and updates: the card is patched with the
	// cached URL instead of importing again.
	source.items[0].UpdateTime = "2026-11-18T11:59:00.000Z"
	poller.tick(context.Background())
	poller.fillWG.Wait()

	if docs.imports != 1 {
		t.Errorf("imports after second render = %d, want 1 (cache hit)", docs.imports)
	}
	if len(messenger.patchBodies) != 2 {
		t.Fatalf("patch bodies = %d, want 2", len(messenger.patchBodies))
	}
	updated := messenger.patchBodies[1]
	if !strings.Contains(updated, "https://example.feishu.cn/docx/m001") {
		t.Errorf("updated card lost the doc link: %s", updated)
	}
	if !strings.Contains(updated, "验收通过") || !strings.Contains(updated, `"name":"approve"`) {
		t.Errorf("updated card lost the approval buttons: %s", updated)
	}
}

// AC4: a failing import degrades to "no button, summary unchanged".
func TestV3PollerImportFailureKeepsSummaryAndApproval(t *testing.T) {
	buf, logger := logBuffer()
	source := reviewFixture()
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := newDocPoller(t, source, cards, messenger, logger)
	poller.SetMaterials(&fakeMaterialSource{materials: map[string]string{"t_1": "m001"}})
	docs := newFakeDocProvider()
	docs.ensureErr = errors.New("feishu: tenant token code=99991663 msg=invalid app_secret")
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	body := messenger.sent[0]
	if strings.Contains(body, "看全文") {
		t.Errorf("failed import must not add the button: %s", body)
	}
	if strings.Contains(body, "文档生成中") {
		t.Errorf("failed import must not render the placeholder: %s", body)
	}
	if !strings.Contains(body, "worker：dev-1") || !strings.Contains(body, "验收通过") || !strings.Contains(body, "打回") {
		t.Errorf("summary or approval buttons lost: %s", body)
	}
	if !strings.Contains(buf.String(), "im doc import failed") {
		t.Errorf("log missing `im doc import failed`:\n%s", buf.String())
	}
	if len(messenger.patchBodies) != 0 {
		t.Errorf("failed import patched the card %d times, want 0", len(messenger.patchBodies))
	}
}

// AC3: no doc provider at all (empty folder token) → no button, no placeholder.
func TestV3PollerWithoutDocProviderRendersNoPlaceholder(t *testing.T) {
	source := reviewFixture()
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := newDocPoller(t, source, cards, messenger, nil)
	poller.SetMaterials(&fakeMaterialSource{materials: map[string]string{"t_1": "m001"}})

	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(messenger.sent))
	}
	body := messenger.sent[0]
	if strings.Contains(body, "看全文") || strings.Contains(body, "文档生成中") || strings.Contains(body, "open_doc") {
		t.Errorf("card without a doc provider must carry no doc element: %s", body)
	}
	if !strings.Contains(body, "验收通过") {
		t.Errorf("approval buttons lost: %s", body)
	}
	if len(messenger.patchBodies) != 0 {
		t.Errorf("patched = %d, want 0", len(messenger.patchBodies))
	}
}

// AC5: the closed card keeps the doc link.
func TestV3PollerClosedCardCarriesDocLink(t *testing.T) {
	source := reviewFixture()
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := newDocPoller(t, source, cards, messenger, nil)
	poller.SetMaterials(&fakeMaterialSource{materials: map[string]string{"t_1": "m001"}})
	docs := newFakeDocProvider()
	docs.cached["m001"] = "https://example.feishu.cn/docx/m001"
	poller.SetDocs(docs)

	poller.tick(context.Background())
	poller.fillWG.Wait()
	if len(messenger.sent) != 1 || !strings.Contains(messenger.sent[0], "看全文") {
		t.Fatalf("open card lacks the cached doc link: %v", messenger.sent)
	}

	source.items = nil
	poller.tick(context.Background())
	if len(messenger.patchBodies) != 1 {
		t.Fatalf("closed patch bodies = %d, want 1", len(messenger.patchBodies))
	}
	closed := messenger.patchBodies[0]
	if !strings.Contains(closed, "看全文") || !strings.Contains(closed, "https://example.feishu.cn/docx/m001") {
		t.Errorf("closed card lost the doc link: %s", closed)
	}
	if !strings.Contains(closed, "处理结果：已处理") {
		t.Errorf("closed card lost the outcome line: %s", closed)
	}
}

// AC5 failure path: no document → outcome only, no placeholder.
func TestV3PollerClosedCardWithoutDocHasNoPlaceholder(t *testing.T) {
	source := reviewFixture()
	cards := newFakeCardStore()
	messenger := &fakeV3Messenger{}
	poller := newDocPoller(t, source, cards, messenger, nil)
	poller.SetMaterials(&fakeMaterialSource{materials: map[string]string{"t_1": "m001"}})

	poller.tick(context.Background())
	poller.fillWG.Wait()
	source.items = nil
	poller.tick(context.Background())
	poller.fillWG.Wait()

	if len(messenger.patchBodies) != 1 {
		t.Fatalf("closed patch bodies = %d, want 1", len(messenger.patchBodies))
	}
	closed := messenger.patchBodies[0]
	if strings.Contains(closed, "看全文") || strings.Contains(closed, "文档生成中") {
		t.Errorf("closed card without a doc must carry no doc element: %s", closed)
	}
	if !strings.Contains(closed, "处理结果：已处理") {
		t.Errorf("closed card lost the outcome line: %s", closed)
	}
}
