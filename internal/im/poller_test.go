package im

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/im/card"
)

type fakeInbox struct {
	entries []InboxEntry
	err     error
}

func (f *fakeInbox) Pending(context.Context, int) ([]InboxEntry, error) {
	return f.entries, f.err
}

type fakeSubjects struct{}

func (fakeSubjects) ProjectGate(context.Context, InboxEntry) (card.Project, error) {
	return card.Project{ProductID: "APP", Version: 3, MaterialID: "m001", MaterialVersion: 1, MaterialKind: "requirement"}, nil
}

func (fakeSubjects) WorkItemGate(context.Context, InboxEntry) (card.WorkItem, error) {
	return card.WorkItem{ProjectID: "APP-1", Worker: "dev-1", Attempt: 1, MaxAttempts: 3}, nil
}

type fakeStore struct {
	records map[string]CardRecord
	puts    int
	closes  int
}

func newFakeStore() *fakeStore { return &fakeStore{records: map[string]CardRecord{}} }

func (f *fakeStore) List(context.Context) ([]CardRecord, error) {
	out := make([]CardRecord, 0, len(f.records))
	for _, rec := range f.records {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeStore) Put(_ context.Context, rec CardRecord) error {
	f.records[key(rec.SubjectKind, rec.SubjectID)] = rec
	f.puts++
	return nil
}

func (f *fakeStore) Close(_ context.Context, kind, id string, at time.Time) error {
	rec := f.records[key(kind, id)]
	rec.Closed = true
	rec.RenderedAt = at
	f.records[key(kind, id)] = rec
	f.closes++
	return nil
}

type fakeMessenger struct {
	sends    []string
	patches  []string
	payloads []string
	nextID   int
}

func (f *fakeMessenger) SendCard(_ context.Context, _, cardJSON string) (string, error) {
	f.sends = append(f.sends, "msg")
	f.payloads = append(f.payloads, cardJSON)
	f.nextID++
	return "om_" + strings.Repeat("x", f.nextID), nil
}

func (f *fakeMessenger) PatchCard(_ context.Context, messageID, cardJSON string) error {
	f.patches = append(f.patches, messageID)
	f.payloads = append(f.payloads, cardJSON)
	return nil
}

func newTestPoller(t *testing.T, cfg Config, inbox InboxSource, store CardStore, msg Messenger, now time.Time) *Poller {
	t.Helper()
	if cfg.QuietHours == "" {
		cfg.QuietHours = "23:00-08:00"
	}
	if cfg.MaxCards == 0 {
		cfg.MaxCards = 10
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewPoller(cfg, inbox, fakeSubjects{}, store, msg, logger, func() time.Time { return now })
}

func projectEntry(id, state, updateTime string) InboxEntry {
	return InboxEntry{
		Kind: card.KindProject, ID: id, State: state,
		Title: "供应链看板改版", UpdateTime: updateTime,
		Age: 2 * time.Hour,
	}
}

var testNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)

func TestTickSendsOncePerChange(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z")}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}}, inbox, store, msg, testNow)

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(msg.sends))
	}

	// Same fingerprint: nothing to do.
	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.sends) != 1 || len(msg.patches) != 0 {
		t.Fatalf("second tick sends=%d patches=%d, want 1/0", len(msg.sends), len(msg.patches))
	}
}

func TestTickPatchesOnChange(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z")}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}}, inbox, store, msg, testNow)

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// Move the clock past the patch throttle and change the item.
	p.now = func() time.Time { return testNow.Add(time.Minute) }
	inbox.entries = []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T11:00:00Z")}

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(msg.patches))
	}
	if len(msg.sends) != 1 {
		t.Fatalf("sends = %d, want 1 (patch, not resend)", len(msg.sends))
	}
}

func TestTickClosesGoneItems(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z")}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}}, inbox, store, msg, testNow)

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	inbox.entries = nil

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.patches) != 1 {
		t.Fatalf("patches = %d, want 1 closed card", len(msg.patches))
	}
	if store.closes != 1 {
		t.Fatalf("closes = %d, want 1", store.closes)
	}
	if !store.records[key(card.KindProject, "APP-1")].Closed {
		t.Error("record must be marked closed")
	}
}

func TestTickResendsExpiredCard(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z")}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}}, inbox, store, msg, testNow)

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	rec := store.records[key(card.KindProject, "APP-1")]
	firstID := rec.MessageID
	rec.SentAt = testNow.Add(-15 * 24 * time.Hour)
	store.records[key(card.KindProject, "APP-1")] = rec

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.sends) != 2 {
		t.Fatalf("sends = %d, want 2 (resend after 14 days)", len(msg.sends))
	}
	if store.records[key(card.KindProject, "APP-1")].MessageID == firstID {
		t.Error("message id must change after resend")
	}
}

func TestTickQuietHoursSuppressesNewCardsOnly(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z")}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	quietNow := time.Date(2026, 1, 1, 23, 30, 0, 0, time.Local)
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}}, inbox, store, msg, quietNow)

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.sends) != 0 {
		t.Fatalf("sends = %d, want 0 during quiet hours", len(msg.sends))
	}

	// Seed an existing card, then change the item: updates must still go out.
	p.now = func() time.Time { return testNow }
	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.sends) != 1 {
		t.Fatalf("sends = %d, want 1 outside quiet hours", len(msg.sends))
	}
	inbox.entries = []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T11:00:00Z")}
	p.now = func() time.Time { return quietNow.Add(24 * time.Hour) }

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.patches) != 1 {
		t.Fatalf("patches = %d, want 1 (existing cards keep updating)", len(msg.patches))
	}
}

func TestTickRespectsCardCap(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{
		projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z"),
		projectEntry("APP-2", "requirement_review", "2026-01-01T10:00:00Z"),
	}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}, MaxCards: 1}, inbox, store, msg, testNow)

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.sends) != 1 {
		t.Fatalf("sends = %d, want 1 (cap)", len(msg.sends))
	}
}

func TestInQuietHoursWrapsMidnight(t *testing.T) {
	p := &Poller{cfg: Config{QuietHours: "23:00-08:00"}}
	cases := []struct {
		at   time.Time
		want bool
	}{
		{time.Date(2026, 1, 1, 23, 30, 0, 0, time.Local), true},
		{time.Date(2026, 1, 1, 3, 0, 0, 0, time.Local), true},
		{time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local), false},
		{time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local), false},
	}
	for _, tc := range cases {
		if got := p.inQuietHours(tc.at); got != tc.want {
			t.Errorf("inQuietHours(%s) = %v, want %v", tc.at, got, tc.want)
		}
	}
}

type fakeDocs struct {
	url string
}

func (f fakeDocs) Cached(context.Context, string) string { return f.url }
func (f fakeDocs) Ensure(context.Context, string, string) (string, error) {
	return f.url, nil
}

func TestTickLinksCachedDocument(t *testing.T) {
	ctx := context.Background()
	inbox := &fakeInbox{entries: []InboxEntry{projectEntry("APP-1", "requirement_review", "2026-01-01T10:00:00Z")}}
	store := newFakeStore()
	msg := &fakeMessenger{}
	p := newTestPoller(t, Config{AllowedChatIDs: []string{"oc_1"}}, inbox, store, msg, testNow)
	p.SetDocs(fakeDocs{url: "https://example.feishu.cn/docx/abc"})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(msg.payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(msg.payloads))
	}
	if !strings.Contains(msg.payloads[0], "看全文") {
		t.Error("card must carry the full-text button when a document exists")
	}
}
