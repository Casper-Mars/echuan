package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Casper-Mars/echuan/internal/inbox"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// defaultReviewStaleHours mirrors platform.DefaultConfig().ReviewStaleHours so
// a caller that forgets to pass the configuration still produces a usable
// inbox instead of marking everything stale.
const defaultReviewStaleHours = 72

// InboxService answers "what should I do now" (tech design §5.6).
//
// It owns no cache and no snapshot: every Inbox call reads the database again.
// The stale threshold comes from configuration (review_stale_hours) and the
// clock is injectable, so tests can move time instead of waiting for it.
type InboxService struct {
	store      *store.Store
	logger     *slog.Logger
	clock      platform.Clock
	staleHours int
}

// NewInboxService builds the inbox service. A non-positive staleHours falls
// back to 72 so a misconfigured value never paints the whole inbox stale.
func NewInboxService(st *store.Store, logger *slog.Logger, staleHours int) *InboxService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if staleHours <= 0 {
		staleHours = defaultReviewStaleHours
	}
	return &InboxService{
		store:      st,
		logger:     logger,
		clock:      platform.RealClock{},
		staleHours: staleHours,
	}
}

// SetClock replaces the clock. It is meant for tests that need to move the wall
// clock; production always uses platform.RealClock.
func (s *InboxService) SetClock(clock platform.Clock) {
	if clock != nil {
		s.clock = clock
	}
}

// ReviewStaleHours returns the configured threshold the service judges with.
func (s *InboxService) ReviewStaleHours() int { return s.staleHours }

// InboxResult is one inbox response before it is mapped onto the wire DTO.
//
// GeneratedAt is the single clock reading used for every age in Items, so two
// entries never disagree about "now". ReviewStaleHours is echoed back so the
// UI can colour the list with the same threshold the server used.
type InboxResult struct {
	Items            []inbox.Entry
	ReviewStaleHours int
	GeneratedAt      time.Time
}

// Inbox reads the pending items and derives age/stale for each of them.
//
// limit must already be resolved by the caller (the API layer applies the
// default); the range check lives here so every entry point shares it. The
// query is one UNION with the LIMIT applied to the merged set — never one
// query per kind or per row.
func (s *InboxService) Inbox(ctx context.Context, limit int) (InboxResult, error) {
	if limit < 1 || limit > maxListLimit {
		return InboxResult{}, invalidField("limit", fmt.Sprintf("must be between 1 and %d", maxListLimit))
	}

	rows, err := store.ListInbox(ctx, s.store.Read(), limit)
	if err != nil {
		return InboxResult{}, err
	}

	now := s.clock.Now().UTC()
	items := make([]inbox.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, inbox.Item{
			Kind:       row.Kind,
			ID:         row.ID,
			State:      row.State,
			Title:      row.Title,
			UpdateTime: row.UpdateTime,
			Reason:     row.Reason,
		})
	}

	entries, err := inbox.Derive(items, now, time.Duration(s.staleHours)*time.Hour)
	if err != nil {
		return InboxResult{}, err
	}

	s.logger.Debug("inbox derived",
		"limit", limit,
		"count", len(entries),
		"review_stale_hours", s.staleHours,
	)

	return InboxResult{
		Items:            entries,
		ReviewStaleHours: s.staleHours,
		GeneratedAt:      now,
	}, nil
}
