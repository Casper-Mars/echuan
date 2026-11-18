package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	cfg    Config
	source V3ReviewSource
	cards  CardStore
	msg    Messenger
	logger *slog.Logger
	now    func() time.Time
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
	return &V3Poller{cfg: cfg, source: source, cards: cards, msg: msg, logger: logger, now: now}
}

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
		body, err := card.Render(subject, nil, &card.WorkItem{
			ProjectID: review.ProjectID, Worker: review.WorkerID,
			Attempt: review.Attempt, MaxAttempts: review.MaxAttempts,
		}, card.Options{})
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
	}

	for id, record := range open {
		if alive[id] {
			continue
		}
		subject := card.Subject{Kind: card.KindWorkItem, ID: id, State: card.StateSubmitted, Title: id}
		body, err := card.RenderClosed(subject, "已处理", card.Options{})
		if err == nil {
			_ = p.msg.PatchCard(ctx, record.MessageID, string(body))
		}
		_ = p.cards.Close(ctx, card.KindWorkItem, id, p.now())
	}
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
