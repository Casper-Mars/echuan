package im

import (
	"context"
	"time"

	"github.com/Casper-Mars/echuan/internal/im/card"
)

// InboxEntry is the projection of one pending inbox row the poller needs.
type InboxEntry struct {
	Kind       string
	ID         string
	State      string
	Title      string
	UpdateTime string
	Age        time.Duration
	Stale      bool
	Reason     string
}

// InboxSource returns the items waiting for a human.
type InboxSource interface {
	Pending(ctx context.Context, limit int) ([]InboxEntry, error)
}

// SubjectSource resolves the metadata a card needs.
type SubjectSource interface {
	ProjectGate(ctx context.Context, entry InboxEntry) (card.Project, error)
	WorkItemGate(ctx context.Context, entry InboxEntry) (card.WorkItem, error)
}

// CardRecord is one row of the card projection store.
type CardRecord struct {
	SubjectKind string
	SubjectID   string
	ChatID      string
	MessageID   string
	Fingerprint string
	Version     int
	SentAt      time.Time
	RenderedAt  time.Time
	Closed      bool
}

// CardStore persists card projections.
type CardStore interface {
	List(ctx context.Context) ([]CardRecord, error)
	Put(ctx context.Context, rec CardRecord) error
	Close(ctx context.Context, kind, id string, at time.Time) error
}

// DocProvider resolves the Feishu document holding a material's full text.
type DocProvider interface {
	Cached(ctx context.Context, materialID string) string
	Ensure(ctx context.Context, materialID, title string) (string, error)
}

// TaskMaterialSource resolves the artifact a task's acceptance card links to:
// the latest output material of that task.
type TaskMaterialSource interface {
	LatestOutputMaterialID(ctx context.Context, taskID string) (string, error)
}

// Messenger is the outbound card channel.
type Messenger interface {
	SendCard(ctx context.Context, chatID, cardJSON string) (string, error)
	PatchCard(ctx context.Context, messageID, cardJSON string) error
}
