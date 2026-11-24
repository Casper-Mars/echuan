package im

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/currentver"
	"github.com/Casper-Mars/echuan/internal/im/card"
)

const (
	// maxImportAttempts caps how often one material is imported for one card
	// (tech design v0.2 §7.4). The counter lives in process memory and is reset
	// when the card fingerprint changes.
	maxImportAttempts = 2
	// maxConcurrentFills bounds the background imports of the whole process.
	maxConcurrentFills = 2
	// longTitleRunes is the header length worth an `im card title long` line:
	// Feishu shows at most four title lines and lets the client ellipsize the
	// rest, so the server only observes, it never truncates.
	longTitleRunes = 60
)

// V3Review is one task waiting for a human gate.
type V3Review struct {
	TaskID string
	// ProjectID is the project business code; ProjectName is its display name.
	ProjectID   string
	ProjectName string
	// TaskTitle is the task title verbatim — the card header main title.
	TaskTitle   string
	Kind        string
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

// cardState is the in-process state of one delivered card (design §6.3). It
// lives only while the card is open: the document URLs themselves are in
// im_doc, the attempts are short-lived and not worth a migration.
type cardState struct {
	taskTitle   string
	projectName string
	item        card.WorkItem
	attempts    map[string]int
	lastBody    string
	fingerprint string
	filling     bool
	docsSkipped bool
	// closed stops a still-running fill from patching a card that has just been
	// turned into its terminal form.
	closed bool
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

	mu      sync.Mutex
	states  map[string]*cardState
	fillSem chan struct{}
	fillWG  sync.WaitGroup
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
	return &V3Poller{
		cfg: cfg, source: source, cards: cards, msg: msg, logger: logger, now: now,
		states:  map[string]*cardState{},
		fillSem: make(chan struct{}, maxConcurrentFills),
	}
}

// SetDocs attaches the material → document importer. Without it, or when the
// importer is switched off, every link row degrades to its bare file name.
func (p *V3Poller) SetDocs(docs DocProvider) { p.docs = docs }

// SetMaterials attaches the resolver for a task's latest output material. A
// source that can list all attachments (TaskAttachmentSource) takes priority;
// this one is the single-row fallback.
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

	alive := make(map[string]bool, len(pending))
	for _, review := range pending {
		alive[review.TaskID] = true
		now := p.now()

		refs, err := p.taskAttachments(ctx, review.TaskID)
		if err != nil {
			// The old card stays untouched; the next tick retries.
			p.logger.Warn("im attachments resolve failed", "task", review.TaskID, "error", err)
			continue
		}
		set := p.currentVersions(review.TaskID, refs)
		fingerprint := reviewFingerprint(review, set)

		state := p.stateFor(review.TaskID)
		subject := card.Subject{
			Kind: card.KindWorkItem, ID: review.TaskID, State: card.StateSubmitted,
			Title: review.TaskTitle, ProjectName: review.ProjectName,
			Age: ageOf(review.UpdateTime, now), Reason: review.Reason,
		}
		item := card.WorkItem{
			ProjectID: review.ProjectID, Worker: review.WorkerID,
			Attempt: review.Attempt, MaxAttempts: review.MaxAttempts,
		}
		links := p.linksOf(ctx, set)

		record, isOpen := open[review.TaskID]
		messageID := record.MessageID
		p.prepareState(state, review, item, fingerprint)

		if !isOpen || record.Fingerprint != fingerprint {
			body, err := card.Render(subject, nil, &item, card.Options{Links: links})
			if err != nil {
				p.logger.Warn("im v3: render card", "task", review.TaskID, "error", err)
				continue
			}
			if isOpen {
				if p.shouldPatch(state, body) {
					if err := p.msg.PatchCard(ctx, messageID, string(body)); err != nil {
						p.logger.Warn("im v3: patch card", "task", review.TaskID, "error", err)
						continue
					}
					p.rememberBody(state, body)
				}
				record.Fingerprint = fingerprint
				record.RenderedAt = now
				_ = p.cards.Put(ctx, record)
				p.logRendered("patch", subject, messageID, set, links, body, fingerprint)
			} else {
				messageID, err = p.msg.SendCard(ctx, chatID, string(body))
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
				p.rememberBody(state, body)
				p.logRendered("send", subject, messageID, set, links, body, fingerprint)
			}
		}

		// The fill runs even when the fingerprint did not change: a card that is
		// already on screen still owes its missing rows a retry (up to the cap).
		p.fillLater(ctx, review.TaskID, messageID, subject, item, links)
	}

	for id, record := range open {
		if alive[id] {
			continue
		}
		p.closeCard(ctx, id, record)
	}
}

// closeCard patches the terminal card and closes the record. The header keeps
// the task title: falling back to the raw task id here would bring back exactly
// the "a string of characters as title" problem the rework removed.
func (p *V3Poller) closeCard(ctx context.Context, id string, record CardRecord) {
	state := p.stateFor(id)
	p.markClosed(state)
	cachedTitle, cachedProject, item := p.snapshot(state)
	subject := card.Subject{
		Kind: card.KindWorkItem, ID: id, State: card.StateSubmitted,
		Title: cachedTitle, ProjectName: cachedProject,
	}

	if subject.Title == "" {
		meta, err := p.taskMeta(ctx, id)
		if err != nil || meta.TaskTitle == "" {
			// No title anywhere: the card type is the only honest header left.
			p.logger.Warn("im card close meta missing", "task", id, "error", err)
		} else {
			subject.Title = meta.TaskTitle
			if subject.ProjectName == "" {
				subject.ProjectName = meta.ProjectName
			}
		}
	}

	refs, err := p.taskAttachments(ctx, id)
	if err != nil {
		p.logger.Warn("im attachments resolve failed", "task", id, "error", err)
		refs = nil
	}
	set := p.currentVersions(id, refs)
	links := p.linksOf(ctx, set)

	body, err := card.RenderClosed(subject, &item, "已处理", card.Options{Links: links})
	if err == nil {
		if p.shouldPatch(state, body) {
			if err := p.msg.PatchCard(ctx, record.MessageID, string(body)); err != nil {
				p.logger.Warn("im v3: patch card", "task", id, "error", err)
			} else {
				p.rememberBody(state, body)
				p.logRendered("patch", subject, record.MessageID, set, links, body, p.fingerprintOf(state))
			}
		}
	} else {
		p.logger.Warn("im v3: render card", "task", id, "error", err)
	}

	if err := p.cards.Close(ctx, card.KindWorkItem, id, p.now()); err != nil {
		p.logger.Warn("im v3: close card", "task", id, "error", err)
	}
	p.forget(id)
}

// taskAttachments resolves the attachment projection of one task. The rich
// source wins; the latest-output resolver is the legacy fallback so a partial
// wiring still renders a single row instead of nothing.
func (p *V3Poller) taskAttachments(ctx context.Context, taskID string) ([]MaterialRef, error) {
	if source, ok := p.attachmentSource(); ok {
		return source.TaskAttachments(ctx, taskID)
	}
	if p.materials == nil || taskID == "" {
		return nil, nil
	}
	materialID, err := p.materials.LatestOutputMaterialID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if materialID == "" {
		return nil, nil
	}
	return []MaterialRef{{MaterialID: materialID, Role: "output"}}, nil
}

func (p *V3Poller) attachmentSource() (TaskAttachmentSource, bool) {
	if source, ok := p.materials.(TaskAttachmentSource); ok {
		return source, true
	}
	if source, ok := p.source.(TaskAttachmentSource); ok {
		return source, true
	}
	return nil, false
}

func (p *V3Poller) taskMeta(ctx context.Context, taskID string) (TaskMeta, error) {
	source, ok := p.source.(TaskMetaSource)
	if !ok {
		return TaskMeta{}, fmt.Errorf("im: source cannot resolve task meta")
	}
	return source.TaskMeta(ctx, taskID)
}

// currentVersions projects the attachment list onto the set the task detail
// shows (currentver rules) and reports refs the projection cannot judge.
func (p *V3Poller) currentVersions(taskID string, refs []MaterialRef) []currentver.Ref {
	in := make([]currentver.Ref, 0, len(refs))
	for _, ref := range refs {
		if ref.MaterialID == "" {
			p.logger.Warn("im currentver suspect ref", "task", taskID,
				"material", ref.MaterialID, "role", ref.Role, "kind", ref.Kind)
		}
		in = append(in, currentver.Ref{
			MaterialID: ref.MaterialID, Title: ref.Title, Kind: ref.Kind,
			Role: ref.Role, SourceMaterialID: ref.SourceMaterialID,
		})
	}
	return currentver.Current(in)
}

// linksOf pairs each current-version ref with the document already imported for
// it. A ref without a document keeps an empty URL and renders as plain text.
func (p *V3Poller) linksOf(ctx context.Context, set []currentver.Ref) []card.Link {
	links := make([]card.Link, 0, len(set))
	for _, ref := range set {
		links = append(links, card.Link{
			MaterialID: ref.MaterialID, Title: ref.Title,
			URL: p.cachedDoc(ctx, ref.MaterialID),
		})
	}
	return links
}

// cachedDoc returns the already imported document URL, or "" when the document
// is unknown, failed earlier, or the feature is switched off.
func (p *V3Poller) cachedDoc(ctx context.Context, materialID string) string {
	if p.docs == nil || materialID == "" {
		return ""
	}
	return p.docs.Cached(ctx, materialID)
}

// fillLater imports the missing rows of one card in the background and patches
// the same message once each document is known. One task per card, at most two
// imports at a time for the whole process.
func (p *V3Poller) fillLater(ctx context.Context, taskID, messageID string, subject card.Subject, item card.WorkItem, links []card.Link) {
	if messageID == "" || len(links) == 0 {
		return
	}
	state := p.stateFor(taskID)

	if p.docs == nil {
		p.noteDocsDisabled(taskID, state)
		return
	}

	p.mu.Lock()
	if state.filling {
		p.mu.Unlock()
		return
	}
	todo := make([]card.Link, 0, len(links))
	capped := make([]card.Link, 0, len(links))
	attempts := make(map[string]int, len(links))
	for _, link := range links {
		if link.URL != "" {
			continue
		}
		attempts[link.MaterialID] = state.attempts[link.MaterialID]
		if state.attempts[link.MaterialID] >= maxImportAttempts {
			capped = append(capped, link)
			continue
		}
		todo = append(todo, link)
	}
	state.filling = len(todo) > 0
	p.mu.Unlock()

	for _, link := range capped {
		p.logger.Info("im doc fill skipped", "task", taskID, "material", link.MaterialID,
			"attempt", attempts[link.MaterialID], "reason", "attempt_cap")
	}
	if len(todo) == 0 {
		return
	}

	p.fillWG.Add(1)
	go func() {
		defer p.fillWG.Done()
		defer func() {
			if rec := recover(); rec != nil {
				p.logger.Error("im doc fill panicked", "panic", rec)
			}
		}()
		defer func() {
			p.mu.Lock()
			state.filling = false
			p.mu.Unlock()
		}()

		select {
		case p.fillSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-p.fillSem }()

		p.fill(ctx, taskID, messageID, subject, item, todo, state)
	}()
}

func (p *V3Poller) noteDocsDisabled(taskID string, state *cardState) {
	p.mu.Lock()
	already := state.docsSkipped
	state.docsSkipped = true
	p.mu.Unlock()
	if already {
		return
	}
	p.logger.Info("im doc fill skipped", "task", taskID, "reason", "docs_disabled")
}

// fill imports the pending materials one by one, patching the card after each
// success. A failure only degrades its own row: it never blocks the card, the
// form or the other rows.
func (p *V3Poller) fill(ctx context.Context, taskID, messageID string, subject card.Subject, item card.WorkItem, todo []card.Link, state *cardState) {
	attemptsUsed := 0
	for _, link := range todo {
		if ctx.Err() != nil || p.isClosed(state) {
			return
		}
		p.mu.Lock()
		attempt := state.attempts[link.MaterialID] + 1
		if attempt > maxImportAttempts {
			p.mu.Unlock()
			p.logger.Info("im doc fill skipped", "task", taskID, "material", link.MaterialID,
				"attempt", maxImportAttempts, "reason", "attempt_cap")
			continue
		}
		state.attempts[link.MaterialID] = attempt
		p.mu.Unlock()
		attemptsUsed++

		docName := link.Title
		if docName == "" {
			docName = link.MaterialID
		}
		started := time.Now()
		_, err := p.docs.Ensure(ctx, link.MaterialID, docName)
		duration := time.Since(started).Milliseconds()
		if err != nil {
			// Two lines on purpose: `im doc import failed` is the message the
			// runbook greps for, `im doc import attempt` carries the retry count.
			p.logger.Warn("im doc import attempt", "task", taskID, "material", link.MaterialID,
				"material_title", link.Title, "attempt", attempt, "result", "failed",
				"duration_ms", duration, "error", err)
			p.logger.Warn("im doc import failed", "task", taskID, "material", link.MaterialID,
				"material_title", link.Title, "attempt", attempt, "error", err)
			continue
		}
		p.logger.Info("im doc import attempt", "task", taskID, "material", link.MaterialID,
			"material_title", link.Title, "attempt", attempt, "result", "ok", "duration_ms", duration)

		if err := p.patchAfterFill(ctx, taskID, link.MaterialID, messageID, subject, item, state); err != nil {
			p.logger.Warn("im patch with doc failed", "task", taskID, "error", err)
		}
	}

	set := p.currentVersions(taskID, p.attachmentsOrEmpty(ctx, taskID))
	links := p.linksOf(ctx, set)
	p.logger.Info("im doc fill finished", "task", taskID, "set_size", len(set),
		"links_total", countLinks(links), "links_missing", countMissing(links),
		"attempts_used", attemptsUsed)
}

// patchAfterFill rebuilds the card from the freshly cached documents and
// patches the very same message. The body is compared first so a no-op round
// costs no API call (idempotency, AC13).
func (p *V3Poller) patchAfterFill(ctx context.Context, taskID, materialID, messageID string, subject card.Subject, item card.WorkItem, state *cardState) error {
	if p.isClosed(state) {
		return nil
	}
	set := p.currentVersions(taskID, p.attachmentsOrEmpty(ctx, taskID))
	if !containsMaterial(set, materialID) {
		// The version was replaced while the import was running: the URL stays
		// in im_doc (another card may reuse it) but must not reach this card.
		p.logger.Info("im doc fill skipped", "task", taskID, "material", materialID, "reason", "not_in_set")
		return nil
	}
	links := p.linksOf(ctx, set)
	body, err := card.Render(subject, nil, &item, card.Options{Links: links})
	if err != nil {
		return err
	}
	if !p.shouldPatch(state, body) {
		return nil
	}
	if p.isClosed(state) {
		return nil
	}
	if err := p.msg.PatchCard(ctx, messageID, string(body)); err != nil {
		return err
	}
	p.rememberBody(state, body)
	p.logRendered("patch", subject, messageID, set, links, body, p.fingerprintOf(state))
	return nil
}

func (p *V3Poller) attachmentsOrEmpty(ctx context.Context, taskID string) []MaterialRef {
	refs, err := p.taskAttachments(ctx, taskID)
	if err != nil {
		p.logger.Warn("im attachments resolve failed", "task", taskID, "error", err)
		return nil
	}
	return refs
}

// prepareState refreshes the cached title/project and clears the import
// attempts whenever the fingerprint changed: a new attachment set starts from
// zero attempts (design §6.3).
func (p *V3Poller) prepareState(state *cardState, review V3Review, item card.WorkItem, fingerprint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state.taskTitle = review.TaskTitle
	state.projectName = review.ProjectName
	state.item = item
	if state.fingerprint != fingerprint {
		state.attempts = map[string]int{}
		state.fingerprint = fingerprint
	}
}

func (p *V3Poller) shouldPatch(state *cardState, body []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(body) != state.lastBody
}

func (p *V3Poller) rememberBody(state *cardState, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state.lastBody = string(body)
}

// snapshot reads the cached title / project / worker of one card. A close can
// race with a background fill, so every read of cardState goes through p.mu.
func (p *V3Poller) snapshot(state *cardState) (string, string, card.WorkItem) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return state.taskTitle, state.projectName, state.item
}

func (p *V3Poller) markClosed(state *cardState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state.closed = true
}

func (p *V3Poller) isClosed(state *cardState) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return state.closed
}

func (p *V3Poller) fingerprintOf(state *cardState) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return state.fingerprint
}

func (p *V3Poller) stateFor(taskID string) *cardState {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.states[taskID]
	if !ok {
		state = &cardState{attempts: map[string]int{}}
		p.states[taskID] = state
	}
	return state
}

func (p *V3Poller) forget(taskID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.states, taskID)
}

// logRendered is the single `im card rendered` line: it carries everything the
// runbook needs to answer "why is this card missing a link" (design §10.1).
func (p *V3Poller) logRendered(action string, subject card.Subject, messageID string, set []currentver.Ref, links []card.Link, body []byte, fingerprint string) {
	titleLen := utf8.RuneCountInString(subject.Title)
	missing := make([]string, 0, len(links))
	for _, link := range links {
		if link.URL == "" {
			missing = append(missing, link.MaterialID)
		}
	}
	p.logger.Info("im card rendered",
		"task", subject.ID, "message_id", messageID, "action", action,
		"set_size", len(set), "links_total", countLinks(links), "links_missing", len(missing),
		"links_missing_ids", strings.Join(missing, ","), "card_bytes", len(body),
		"fingerprint", fingerprint, "title_len", titleLen)
	if titleLen > longTitleRunes {
		p.logger.Info("im card title long", "task", subject.ID, "title_len", titleLen)
	}
}

func containsMaterial(set []currentver.Ref, materialID string) bool {
	for _, ref := range set {
		if ref.MaterialID == materialID {
			return true
		}
	}
	return false
}

func countLinks(links []card.Link) int {
	total := 0
	for _, link := range links {
		if link.URL != "" {
			total++
		}
	}
	return total
}

func countMissing(links []card.Link) int { return len(links) - countLinks(links) }

// reviewFingerprint is the "did anything the card shows change" digest. The
// attachment set is part of it, so re-delivering a new version patches the card
// within one tick and drops the old row (AC12).
func reviewFingerprint(review V3Review, set []currentver.Ref) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s", review.TaskID, review.Status, review.UpdateTime, review.Reason)
	for _, ref := range set {
		fmt.Fprintf(&b, "|%s,%s,%s,%s", ref.Role, ref.Kind, ref.MaterialID, ref.Title)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:32]
}

func ageOf(update string, now time.Time) time.Duration {
	t, err := time.Parse(time.RFC3339Nano, update)
	if err != nil {
		return 0
	}
	return now.Sub(t)
}
