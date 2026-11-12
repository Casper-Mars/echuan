package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Execer is the write half of a database handle. Both *sql.DB and *sql.Tx
// satisfy it, so the same statement can run inside or outside a transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// IMCardRow is one card projection row.
//
// Fingerprint is the hash the poller compares; Version is the project version
// captured when the card was rendered (used for the decision If-Match).
// SentAt and RenderedAt are RFC3339Nano UTC text, matching every other
// timestamp column in this schema.
type IMCardRow struct {
	SubjectKind string  `db:"subject_kind"`
	SubjectID   string  `db:"subject_id"`
	ChatID      string  `db:"chat_id"`
	MessageID   string  `db:"message_id"`
	Fingerprint string  `db:"fingerprint"`
	Version     int     `db:"version"`
	SentAt      string  `db:"sent_at"`
	RenderedAt  string  `db:"rendered_at"`
	ClosedAt    *string `db:"closed_at"`
}

const listIMCardsQuery = `SELECT subject_kind, subject_id, chat_id, message_id,
       fingerprint, version, sent_at, rendered_at, closed_at
  FROM im_card`

// ListIMCards returns every card projection, open and closed.
func ListIMCards(ctx context.Context, q Querier) ([]IMCardRow, error) {
	rows, err := All[IMCardRow](ctx, q, listIMCardsQuery)
	if err != nil {
		return nil, fmt.Errorf("store: list im cards: %w", err)
	}
	return rows, nil
}

const upsertIMCardQuery = `INSERT INTO im_card
  (subject_kind, subject_id, chat_id, message_id, fingerprint, version, sent_at, rendered_at, closed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(subject_kind, subject_id) DO UPDATE SET
  chat_id = excluded.chat_id,
  message_id = excluded.message_id,
  fingerprint = excluded.fingerprint,
  version = excluded.version,
  sent_at = excluded.sent_at,
  rendered_at = excluded.rendered_at,
  closed_at = excluded.closed_at`

// UpsertIMCard inserts or replaces one projection row.
func UpsertIMCard(ctx context.Context, e Execer, row IMCardRow) error {
	if _, err := e.ExecContext(ctx, upsertIMCardQuery,
		row.SubjectKind, row.SubjectID, row.ChatID, row.MessageID,
		row.Fingerprint, row.Version, row.SentAt, row.RenderedAt, row.ClosedAt,
	); err != nil {
		return fmt.Errorf("store: upsert im card %s/%s: %w", row.SubjectKind, row.SubjectID, err)
	}
	return nil
}

const closeIMCardQuery = `UPDATE im_card SET closed_at = ? WHERE subject_kind = ? AND subject_id = ?`

// CloseIMCard marks a projection as terminal.
func CloseIMCard(ctx context.Context, e Execer, kind, id, at string) error {
	if _, err := e.ExecContext(ctx, closeIMCardQuery, at, kind, id); err != nil {
		return fmt.Errorf("store: close im card %s/%s: %w", kind, id, err)
	}
	return nil
}

const getIMCardByMessageQuery = `SELECT subject_kind, subject_id, chat_id, message_id,
       fingerprint, version, sent_at, rendered_at, closed_at
  FROM im_card WHERE message_id = ?`

// GetIMCardByMessageID resolves the card a callback came from. The callback
// only carries a message id, so this is the link back to the subject.
func GetIMCardByMessageID(ctx context.Context, q Querier, messageID string) (IMCardRow, error) {
	row, err := One[IMCardRow](ctx, q, getIMCardByMessageQuery, messageID)
	if err != nil {
		return IMCardRow{}, fmt.Errorf("store: get im card by message %s: %w", messageID, err)
	}
	return row, nil
}
