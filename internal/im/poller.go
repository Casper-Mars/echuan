package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Casper-Mars/echuan/internal/im/card"
)

// cardTTL is how long a Feishu card stays patchable. After this the message can
// neither be updated nor clicked, so the poller sends a new one.
const cardTTL = 14 * 24 * time.Hour

// patchThrottle is the minimum gap between two patches of the same card.
const patchThrottle = 10 * time.Second

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

// Messenger is the outbound card channel.
type Messenger interface {
	SendCard(ctx context.Context, chatID, cardJSON string) (string, error)
	PatchCard(ctx context.Context, messageID, cardJSON string) error
}

// Poller turns the inbox into cards. One instance runs per process; it is not
// safe for concurrent use.
type Poller struct {
	cfg      Config
	inbox    InboxSource
	subjects SubjectSource
	store    CardStore
	msg      Messenger
	docs     DocProvider
	logger   *slog.Logger
	now      func() time.Time

	lastPatch map[string]time.Time

	mu         sync.Mutex
	docFilling map[string]bool
}

// NewPoller builds a poller. now is injectable so tests can move the clock.
func NewPoller(cfg Config, inbox InboxSource, subjects SubjectSource, store CardStore, msg Messenger, logger *slog.Logger, now func() time.Time) *Poller {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	cfg.PollInterval = interval
	if cfg.MaxCards <= 0 {
		cfg.MaxCards = 10
	}
	return &Poller{
		cfg:        cfg,
		inbox:      inbox,
		subjects:   subjects,
		store:      store,
		msg:        msg,
		logger:     logger,
		now:        now,
		lastPatch:  map[string]time.Time{},
		docFilling: map[string]bool{},
	}
}

// SetDocs attaches the document importer. Without it cards simply carry no
// "read full text" button.
func (p *Poller) SetDocs(docs DocProvider) {
	p.docs = docs
}

// Run polls until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	// One pass immediately so a restart does not wait a full interval.
	if err := p.Tick(ctx); err != nil && ctx.Err() == nil {
		p.logger.Error("im poller tick failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Tick(ctx); err != nil && ctx.Err() == nil {
				p.logger.Error("im poller tick failed", "error", err)
			}
		}
	}
}

// Tick runs one poll/diff/render pass.
func (p *Poller) Tick(ctx context.Context) error {
	entries, err := p.inbox.Pending(ctx, 200)
	if err != nil {
		return fmt.Errorf("read inbox: %w", err)
	}
	records, err := p.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list cards: %w", err)
	}

	recordsByKey := make(map[string]CardRecord, len(records))
	for _, rec := range records {
		recordsByKey[key(rec.SubjectKind, rec.SubjectID)] = rec
	}

	present := make(map[string]struct{}, len(entries))
	openCount := 0
	for _, rec := range records {
		if !rec.Closed {
			openCount++
		}
	}

	for _, entry := range entries {
		k := key(entry.Kind, entry.ID)
		present[k] = struct{}{}
		rec, known := recordsByKey[k]

		fp := fingerprint(entry)
		// An expired card may still have an identical fingerprint: it can no
		// longer be patched, so it has to be resent.
		expired := known && !rec.Closed && p.now().Sub(rec.SentAt) > cardTTL
		if known && !rec.Closed && rec.Fingerprint == fp && !expired {
			continue
		}

		quiet := p.inQuietHours(p.now())
		if !known && quiet {
			// New cards stay silent during quiet hours; existing ones keep
			// updating so the user is not left with a stale card.
			continue
		}
		if !known && openCount >= p.cfg.MaxCards {
			p.logger.Warn("im card cap reached, skipping", "subject", entry.ID, "cap", p.cfg.MaxCards)
			continue
		}

		subject := card.Subject{
			Kind:   entry.Kind,
			ID:     entry.ID,
			State:  entry.State,
			Title:  entry.Title,
			Age:    entry.Age,
			Stale:  entry.Stale,
			Reason: entry.Reason,
		}

		var (
			project *card.Project
			item    *card.WorkItem
		)
		if entry.Kind == card.KindProject {
			meta, err := p.subjects.ProjectGate(ctx, entry)
			if err != nil {
				p.logger.Warn("im resolve project failed", "subject", entry.ID, "error", err)
				continue
			}
			project = &meta
		} else {
			meta, err := p.subjects.WorkItemGate(ctx, entry)
			if err != nil {
				p.logger.Warn("im resolve work item failed", "subject", entry.ID, "error", err)
				continue
			}
			item = &meta
		}

		materialID := ""
		if project != nil {
			materialID = project.MaterialID
		}
		if item != nil {
			materialID = item.ReportID
		}
		docURL := ""
		if p.docs != nil && materialID != "" {
			docURL = p.docs.Cached(ctx, materialID)
		}

		raw, err := card.Render(subject, project, item, card.Options{DocURL: docURL})
		if err != nil {
			p.logger.Warn("im render card failed", "subject", entry.ID, "error", err)
			continue
		}

		now := p.now()
		version := 0
		if project != nil {
			version = project.Version
		}

		switch {
		case !known:
			messageID, err := p.send(ctx, entry, string(raw), version, now)
			if err != nil {
				continue
			}
			openCount++
			p.fillDocLater(ctx, subject, project, item, materialID, messageID, docURL)
		case rec.Closed:
			// The item came back (reopened). Send a fresh card rather than
			// patching a terminal one.
			messageID, err := p.send(ctx, entry, string(raw), version, now)
			if err != nil {
				continue
			}
			openCount++
			p.fillDocLater(ctx, subject, project, item, materialID, messageID, docURL)
		case now.Sub(rec.SentAt) > cardTTL:
			p.logger.Info("im card expired, resending", "subject", entry.ID, "sent_at", rec.SentAt)
			messageID, err := p.send(ctx, entry, string(raw), version, now)
			if err != nil {
				continue
			}
			p.fillDocLater(ctx, subject, project, item, materialID, messageID, docURL)
		default:
			if last, ok := p.lastPatch[k]; ok && now.Sub(last) < patchThrottle {
				continue
			}
			if err := p.msg.PatchCard(ctx, rec.MessageID, string(raw)); err != nil {
				p.logger.Warn("im patch card failed", "subject", entry.ID, "error", err)
				continue
			}
			p.lastPatch[k] = now
			rec.Fingerprint = fp
			rec.Version = version
			rec.RenderedAt = now
			if err := p.store.Put(ctx, rec); err != nil {
				p.logger.Warn("im record card failed", "subject", entry.ID, "error", err)
			}
		}
	}

	return p.closeGone(ctx, records, present)
}

// closeGone renders a terminal card for every projection whose inbox row is no
// longer pending.
func (p *Poller) closeGone(ctx context.Context, records []CardRecord, present map[string]struct{}) error {
	now := p.now()
	for _, rec := range records {
		if rec.Closed {
			continue
		}
		if _, ok := present[key(rec.SubjectKind, rec.SubjectID)]; ok {
			continue
		}

		subject := card.Subject{Kind: rec.SubjectKind, ID: rec.SubjectID, State: ""}
		// State is unknown here: derive a layout from the kind only.
		subject.State = fallbackState(rec.SubjectKind)

		raw, err := card.RenderClosed(subject, "已处理", card.Options{})
		if err != nil {
			p.logger.Warn("im render closed failed", "subject", rec.SubjectID, "error", err)
			continue
		}
		if err := p.msg.PatchCard(ctx, rec.MessageID, string(raw)); err != nil {
			p.logger.Warn("im patch closed failed", "subject", rec.SubjectID, "error", err)
			continue
		}
		if err := p.store.Close(ctx, rec.SubjectKind, rec.SubjectID, now); err != nil {
			p.logger.Warn("im close record failed", "subject", rec.SubjectID, "error", err)
		}
	}
	return nil
}

func (p *Poller) send(ctx context.Context, entry InboxEntry, raw string, version int, now time.Time) (string, error) {
	chatID := ""
	if len(p.cfg.AllowedChatIDs) > 0 {
		chatID = p.cfg.AllowedChatIDs[0]
	}
	if chatID == "" {
		return "", fmt.Errorf("no chat id configured")
	}
	messageID, err := p.msg.SendCard(ctx, chatID, raw)
	if err != nil {
		p.logger.Warn("im send card failed", "subject", entry.ID, "error", err)
		return "", err
	}
	rec := CardRecord{
		SubjectKind: entry.Kind,
		SubjectID:   entry.ID,
		ChatID:      chatID,
		MessageID:   messageID,
		Fingerprint: fingerprint(entry),
		Version:     version,
		SentAt:      now,
		RenderedAt:  now,
	}
	if err := p.store.Put(ctx, rec); err != nil {
		p.logger.Warn("im record card failed", "subject", entry.ID, "error", err)
	}
	p.logger.Info("im card sent", "subject", entry.ID, "message_id", messageID)
	return messageID, nil
}

// fillDocLater imports the material in the background and patches the card once
// the document URL is known. The card is sent first so the user is never
// blocked by a three-call import.
func (p *Poller) fillDocLater(ctx context.Context, subject card.Subject, project *card.Project, item *card.WorkItem, materialID, messageID, docURL string) {
	if p.docs == nil || docURL != "" || materialID == "" || messageID == "" {
		return
	}

	p.mu.Lock()
	if p.docFilling[messageID] {
		p.mu.Unlock()
		return
	}
	p.docFilling[messageID] = true
	p.mu.Unlock()

	go func() {
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
			p.logger.Warn("im doc import failed", "subject", subject.ID, "error", err)
			return
		}
		raw, err := card.Render(subject, project, item, card.Options{DocURL: url})
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

// inQuietHours reports whether now falls inside the configured window. A
// window that wraps midnight (23:00-08:00) is supported.
func (p *Poller) inQuietHours(now time.Time) bool {
	start, end, ok := parseWindow(p.cfg.QuietHours)
	if !ok {
		return false
	}
	local := now.In(time.Local)
	mins := local.Hour()*60 + local.Minute()
	if start <= end {
		return mins >= start && mins < end
	}
	return mins >= start || mins < end
}

func parseWindow(raw string) (int, int, bool) {
	start, end, ok := strings.Cut(strings.TrimSpace(raw), "-")
	if !ok {
		return 0, 0, false
	}
	s, err := parseClock(start)
	if err != nil {
		return 0, 0, false
	}
	e, err := parseClock(end)
	if err != nil {
		return 0, 0, false
	}
	return s, e, true
}

func parseClock(raw string) (int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("bad clock %q", raw)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("clock out of range %q", raw)
	}
	return h*60 + m, nil
}

func key(kind, id string) string { return kind + "/" + id }

// fingerprint is the change detector: it only has to be stable within one
// process and cheap to compute.
func fingerprint(e InboxEntry) string {
	h := sha256.Sum256([]byte(e.State + "|" + e.UpdateTime + "|" + e.Title))
	return hex.EncodeToString(h[:8])
}

// fallbackState keeps RenderClosed usable when the original state is gone.
func fallbackState(kind string) string {
	if kind == card.KindWorkItem {
		return card.StateSubmitted
	}
	return card.StateRequirementReview
}

// sortedKeys is used by tests to keep failure output deterministic.
func sortedKeys(m map[string]CardRecord) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
