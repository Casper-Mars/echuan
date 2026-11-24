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

// MaterialRef is one attachment of a task, as the acceptance card needs it
// (tech design v0.2 §7.2).
type MaterialRef struct {
	MaterialID string
	Title      string
	Kind       string
	Role       string
	// SourceMaterialID is the material this one describes (a brief points at
	// its artifact version); empty means "no source".
	SourceMaterialID string
}

// TaskAttachmentSource returns every attachment of one task in the order the
// task detail attachment area shows it. Callers must not re-sort.
type TaskAttachmentSource interface {
	TaskAttachments(ctx context.Context, taskID string) ([]MaterialRef, error)
}

// TaskMeta is the "which task is this" projection the terminal card needs.
type TaskMeta struct {
	ProjectID   string
	ProjectName string
	TaskTitle   string
}

// TaskMetaSource resolves TaskMeta for the close path when the in-process
// cache is empty (e.g. after a restart).
type TaskMetaSource interface {
	TaskMeta(ctx context.Context, taskID string) (TaskMeta, error)
}

// TaskMaterialSource resolves the artifact a task's acceptance card links to:
// the latest output material of that task.
//
// It is the pre-§7.2 shape and is kept as the fallback: the poller prefers a
// TaskAttachmentSource (the wiring still passes the app service here) and only
// falls back to a one-row projection when the source cannot list attachments.
type TaskMaterialSource interface {
	LatestOutputMaterialID(ctx context.Context, taskID string) (string, error)
}

// Messenger is the outbound card channel.
type Messenger interface {
	SendCard(ctx context.Context, chatID, cardJSON string) (string, error)
	PatchCard(ctx context.Context, messageID, cardJSON string) error
}
