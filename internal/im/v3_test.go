package im

import (
	"context"
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
	sent    []string
	patched []string
}

func (f *fakeV3Messenger) SendCard(_ context.Context, _, cardJSON string) (string, error) {
	f.sent = append(f.sent, cardJSON)
	return "om_1", nil
}

func (f *fakeV3Messenger) PatchCard(_ context.Context, messageID, cardJSON string) error {
	f.patched = append(f.patched, messageID)
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
