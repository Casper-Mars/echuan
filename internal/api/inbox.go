package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
)

// defaultInboxLimit is applied when GET /v1/inbox has no ?limit=.
const defaultInboxLimit = 50

// inboxHandler serves GET /v1/inbox.
type inboxHandler struct {
	service *app.InboxService
	logger  *slog.Logger
}

func newInboxHandler(service *app.InboxService, logger *slog.Logger) *inboxHandler {
	return &inboxHandler{service: service, logger: logger}
}

// inboxItemResponse is the wire representation of one pending item (§5.6).
//
// reason is a pointer so "no reason" is JSON null instead of an empty string;
// update_time is the stored RFC3339 value, unchanged.
type inboxItemResponse struct {
	Kind       string  `json:"kind"`
	ID         string  `json:"id"`
	State      int     `json:"state"`
	StateName  string  `json:"state_name"`
	Title      string  `json:"title"`
	UpdateTime string  `json:"update_time"`
	AgeSeconds int64   `json:"age_seconds"`
	Stale      bool    `json:"stale"`
	Reason     *string `json:"reason"`
}

// inboxResponse is the 200 body of GET /v1/inbox.
//
// items is built with make(…, 0, n) so an empty inbox encodes as [] rather
// than null; count is the number of returned items, not the number of pending
// items in the database.
type inboxResponse struct {
	Items            []inboxItemResponse `json:"items"`
	Count            int                 `json:"count"`
	ReviewStaleHours int                 `json:"review_stale_hours"`
	GeneratedAt      string              `json:"generated_at"`
}

// list handles GET /v1/inbox?limit=.
//
// limit is the only query parameter: absent means 50, anything outside 1..200
// (or not an integer) is 400 invalid_argument with details.field=limit. There
// is no cursor: the inbox is a "what is pending right now" view, not a
// browsable collection.
func (h *inboxHandler) list(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	query := r.URL.Query()

	limit := defaultInboxLimit
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("limit", "must be an integer"))
			return
		}
		limit = parsed
	}

	result, err := h.service.Inbox(r.Context(), limit)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]inboxItemResponse, 0, len(result.Items))
	staleCount := 0
	for _, entry := range result.Items {
		if entry.Stale {
			staleCount++
		}
		items = append(items, inboxItemResponse{
			Kind:       entry.Kind,
			ID:         entry.ID,
			State:      entry.State,
			StateName:  entry.StateName,
			Title:      entry.Title,
			UpdateTime: entry.UpdateTime,
			AgeSeconds: entry.AgeSeconds(),
			Stale:      entry.Stale,
			Reason:     entry.Reason,
		})
	}

	writeJSON(w, http.StatusOK, inboxResponse{
		Items:            items,
		Count:            len(items),
		ReviewStaleHours: result.ReviewStaleHours,
		GeneratedAt:      result.GeneratedAt.UTC().Format(time.RFC3339Nano),
	})

	h.logger.Info("inbox listed",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"count", len(items),
		"limit", limit,
		"stale_count", staleCount,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}
