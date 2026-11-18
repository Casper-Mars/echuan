package im

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Casper-Mars/echuan/internal/store"
)

// V3Store adapts the store's im_card / im_action_log tables to the IM layer.
type V3Store struct {
	st *store.Store
}

// NewV3Store returns the adapter.
func NewV3Store(st *store.Store) *V3Store { return &V3Store{st: st} }

func (s *V3Store) List(ctx context.Context) ([]CardRecord, error) {
	rows, err := store.ListIMCards(ctx, s.st.Read())
	if err != nil {
		return nil, err
	}
	out := make([]CardRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, toCardRecord(row))
	}
	return out, nil
}

func (s *V3Store) Put(ctx context.Context, rec CardRecord) error {
	return store.UpsertIMCard(ctx, s.st.Write(), store.IMCardRow{
		SubjectKind: rec.SubjectKind, SubjectID: rec.SubjectID, ChatID: rec.ChatID,
		MessageID: rec.MessageID, Fingerprint: rec.Fingerprint, Version: rec.Version,
		SentAt: formatIMTime(rec.SentAt), RenderedAt: formatIMTime(rec.RenderedAt),
	})
}

func (s *V3Store) Close(ctx context.Context, kind, id string, at time.Time) error {
	return store.CloseIMCard(ctx, s.st.Write(), kind, id, formatIMTime(at))
}

func (s *V3Store) CardByMessage(ctx context.Context, messageID string) (CardRecord, error) {
	row, err := store.GetIMCardByMessageID(ctx, s.st.Read(), messageID)
	if err != nil {
		return CardRecord{}, err
	}
	return toCardRecord(row), nil
}

func (s *V3Store) ActionResult(ctx context.Context, idemKey string) (string, bool, error) {
	result, err := store.GetIMActionResult(ctx, s.st.Read(), idemKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return result, true, nil
}

func (s *V3Store) SaveAction(ctx context.Context, idemKey, subject, action, result string, at time.Time) error {
	return store.PutIMActionResult(ctx, s.st.Write(), idemKey, subject, action, result, formatIMTime(at))
}

func toCardRecord(row store.IMCardRow) CardRecord {
	rec := CardRecord{
		SubjectKind: row.SubjectKind, SubjectID: row.SubjectID, ChatID: row.ChatID,
		MessageID: row.MessageID, Fingerprint: row.Fingerprint, Version: row.Version,
		Closed: row.ClosedAt != nil,
	}
	if t, err := time.Parse(time.RFC3339Nano, row.SentAt); err == nil {
		rec.SentAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, row.RenderedAt); err == nil {
		rec.RenderedAt = t
	}
	return rec
}

func formatIMTime(t time.Time) string {
	if t.IsZero() {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}
