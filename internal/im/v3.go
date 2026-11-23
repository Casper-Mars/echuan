package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Casper-Mars/echuan/internal/im/card"
)

// V3Review is one task waiting for a human gate.
type V3Review struct {
	TaskID      string
	ProjectID   string
	Kind        string
	Title       string
	Status      string
	Reason      string
	WorkerID    string
	Attempt     int
	MaxAttempts int
	UpdateTime  string
}

// V3ReviewSource returns the tasks waiting for review.
type V3ReviewSource interface {
	PendingReviews(ctx context.Context, limit int) ([]V3Review, error)
}

// V3Poller turns in_review tasks into Feishu cards. One card per task; the
// card carries approve/reject buttons and the callback resolves the task via
// im_card(message_id).
type V3Poller struct {
	cfg       Config
	source    V3ReviewSource
	cards     CardStore
	msg       Messenger
	docs      DocProvider
	materials TaskMaterialSource
	logger    *slog.Logger
	now       func() time.Time

	mu         sync.Mutex
	docFilling map[string]bool
	fillWG     sync.WaitGroup
}

// NewV3Poller builds the V3 poller.
func NewV3Poller(cfg Config, source V3ReviewSource, cards CardStore, msg Messenger, logger *slog.Logger, now func() time.Time) *V3Poller {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.MaxCards <= 0 {
		cfg.MaxCards = 10
	}
	return &V3Poller{cfg: cfg, source: source, cards: cards, msg: msg, logger: logger, now: now,
		docFilling: map[string]bool{}}
}

// SetDocs attaches the material → document importer. Without it, or when the
// importer is switched off, cards carry no "看全文" button.
func (p *V3Poller) SetDocs(docs DocProvider) { p.docs = docs }

// SetMaterials attaches the resolver for a task's latest output material.
func (p *V3Poller) SetMaterials(materials TaskMaterialSource) { p.materials = materials }

// Run polls until ctx is cancelled.
func (p *V3Poller) Run(ctx context.Context) {
	p.tick(ctx)
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *V3Poller) tick(ctx context.Context) {
	pending, err := p.source.PendingReviews(ctx, p.cfg.MaxCards)
	if err != nil {
		p.logger.Warn("im v3: list pending reviews", "error", err)
		return
	}
	if len(p.cfg.AllowedChatIDs) == 0 {
		p.logger.Warn("im v3: no allowed chat ids configured")
		return
	}
	chatID := p.cfg.AllowedChatIDs[0]

	records, err := p.cards.List(ctx)
	if err != nil {
		p.logger.Warn("im v3: list cards", "error", err)
		return
	}
	open := map[string]CardRecord{}
	for _, record := range records {
		if record.SubjectKind == card.KindWorkItem && !record.Closed {
			open[record.SubjectID] = record
		}
	}

	alive := map[string]bool{}
	for _, review := range pending {
		alive[review.TaskID] = true
		fingerprint := reviewFingerprint(review)
		now := p.now()
		if record, ok := open[review.TaskID]; ok && record.Fingerprint == fingerprint {
			continue
		}

		subject := card.Subject{
			Kind: card.KindWorkItem, ID: review.TaskID, State: card.StateSubmitted,
			Title: fmt.Sprintf("%s · %s", review.ProjectID, review.Title),
			Age:   ageOf(review.UpdateTime, now), Reason: review.Reason,
		}
		item := card.WorkItem{
			ProjectID: review.ProjectID, Worker: review.WorkerID,
			Attempt: review.Attempt, MaxAttempts: review.MaxAttempts,
		}
		// The doc link always points at this task's latest output material, so a
		// re-report or a new artifact version yields a different document.
		materialID := p.latestOutputMaterial(ctx, review.TaskID)
		docURL := p.cachedDoc(ctx, materialID)
		renderWithDoc := func(url string) ([]byte, error) {
			return card.Render(subject, nil, &item, card.Options{DocURL: url})
		}
		body, err := renderWithDoc(docURL)
		if err != nil {
			p.logger.Warn("im v3: render card", "task", review.TaskID, "error", err)
			continue
		}

		if record, ok := open[review.TaskID]; ok {
			if err := p.msg.PatchCard(ctx, record.MessageID, string(body)); err != nil {
				p.logger.Warn("im v3: patch card", "task", review.TaskID, "error", err)
				continue
			}
			record.Fingerprint = fingerprint
			record.RenderedAt = now
			_ = p.cards.Put(ctx, record)
			p.fillDocLater(ctx, subject, materialID, record.MessageID, docURL, renderWithDoc)
			continue
		}

		messageID, err := p.msg.SendCard(ctx, chatID, string(body))
		if err != nil {
			p.logger.Warn("im v3: send card", "task", review.TaskID, "error", err)
			continue
		}
		if err := p.cards.Put(ctx, CardRecord{
			SubjectKind: card.KindWorkItem, SubjectID: review.TaskID, ChatID: chatID,
			MessageID: messageID, Fingerprint: fingerprint, Version: 0,
			SentAt: now, RenderedAt: now,
		}); err != nil {
			p.logger.Warn("im v3: store card", "task", review.TaskID, "error", err)
		}
		p.fillDocLater(ctx, subject, materialID, messageID, docURL, renderWithDoc)
	}

	for id, record := range open {
		if alive[id] {
			continue
		}
		subject := card.Subject{Kind: card.KindWorkItem, ID: id, State: card.StateSubmitted, Title: id}
		materialID := p.latestOutputMaterial(ctx, id)
		docURL := p.cachedDoc(ctx, materialID)
		renderWithDoc := func(url string) ([]byte, error) {
			return card.RenderClosed(subject, "已处理", card.Options{DocURL: url})
		}
		body, err := renderWithDoc(docURL)
		if err == nil {
			_ = p.msg.PatchCard(ctx, record.MessageID, string(body))
			p.fillDocLater(ctx, subject, materialID, record.MessageID, docURL, renderWithDoc)
		}
		_ = p.cards.Close(ctx, card.KindWorkItem, id, p.now())
	}
}

// latestOutputMaterial resolves the material a task's acceptance card links to.
// A task without an output material simply renders without the button.
func (p *V3Poller) latestOutputMaterial(ctx context.Context, taskID string) string {
	if p.materials == nil || taskID == "" {
		return ""
	}
	materialID, err := p.materials.LatestOutputMaterialID(ctx, taskID)
	if err != nil {
		p.logger.Debug("im doc material resolve skipped", "task", taskID, "error", err)
		return ""
	}
	return materialID
}

// cachedDoc returns the already imported document URL, or "" when the document
// is unknown, failed earlier, or the feature is switched off.
func (p *V3Poller) cachedDoc(ctx context.Context, materialID string) string {
	if p.docs == nil || materialID == "" {
		return ""
	}
	return p.docs.Cached(ctx, materialID)
}

// fillDocLater imports the material in the background and patches the card once
// the document URL is known. The card is sent first so the three-call import
// never delays the review; on failure the card keeps its summary and its gate
// form and simply never gains the button.
func (p *V3Poller) fillDocLater(ctx context.Context, subject card.Subject, materialID, messageID, docURL string, render func(url string) ([]byte, error)) {
	if p.docs == nil || materialID == "" || messageID == "" || docURL != "" {
		return
	}

	p.mu.Lock()
	if p.docFilling[messageID] {
		p.mu.Unlock()
		return
	}
	p.docFilling[messageID] = true
	p.mu.Unlock()

	p.fillWG.Add(1)
	go func() {
		defer p.fillWG.Done()
		defer func() {
			p.mu.Lock()
			delete(p.docFilling, messageID)
			p.mu.Unlock()
		}()
		defer func() {
			if rec := recover(); rec != nil {
				p.logger.Error("im doc fill panicked", "panic", rec)
			}
		}()

		url, err := p.docs.Ensure(ctx, materialID, subject.Title)
		if err != nil {
			p.logger.Warn("im doc import failed", "subject", subject.ID, "material", materialID, "error", err)
			return
		}
		raw, err := render(url)
		if err != nil {
			p.logger.Warn("im rerender with doc failed", "subject", subject.ID, "error", err)
			return
		}
		if err := p.msg.PatchCard(ctx, messageID, string(raw)); err != nil {
			p.logger.Warn("im patch with doc failed", "subject", subject.ID, "error", err)
			return
		}
		p.logger.Info("im card updated with doc", "subject", subject.ID, "url", url)
	}()
}

func reviewFingerprint(review V3Review) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s",
		review.TaskID, review.Status, review.UpdateTime, review.Reason)))
	return hex.EncodeToString(sum[:])[:32]
}

func ageOf(update string, now time.Time) time.Duration {
	t, err := time.Parse(time.RFC3339Nano, update)
	if err != nil {
		return 0
	}
	return now.Sub(t)
}
