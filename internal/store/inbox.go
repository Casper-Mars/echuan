package store

import (
	"context"
	"fmt"
)

// listInboxQuery is the single statement behind GET /v1/inbox (tech design
// §5.6): the union of every human-blocked project and work item, oldest first.
//
// It is deliberately one UNION ALL subquery instead of two queries: the LIMIT
// must apply to the merged candidate set, otherwise the "50 oldest pending
// items" would silently become "the oldest 50 projects plus the oldest 50 work
// items". The status literals mirror the three project review gates
// (requirement_review / spec_review / tasks_review = 1/3/5); the work-item
// states 'submitted' (pending acceptance) and 'failed' are the two states a
// human has to look at. They are spelled out here because the dependency runs
// domain ← store and store must not import domain.
//
// Stage cards (kind requirement/design/breakdown) are excluded while they are
// submitted: reporting one of them advances its project into the matching
// review gate in the same transaction, so the project row already carries that
// pending decision. Listing the card too would show one decision twice — and an
// approval taken on the card would not even move the gate. The card is settled
// by the gate itself (approve accepts it, reject requeues it), so the human
// never needs to see it. A failed stage card stays visible: the gate never
// opened for it, so nothing else would report that the producing stage died.
//
// state is rendered as text for both branches: a project's integer status is
// cast, a work item's state is already text. One read model therefore carries
// both without a union-unfriendly type split.
//
// Ordering uses julianday() rather than the raw text column: update_time is
// stored as RFC3339Nano, whose fractional part is variable length, so a plain
// string ORDER BY would sort "…:00.500Z" before "…:00Z" even though .5s is
// later. kind/id break ties deterministically.
const listInboxQuery = `SELECT kind, id, state, title, update_time, reason
FROM (
  SELECT 'project' AS kind, p.project_id AS id, CAST(p.status AS TEXT) AS state,
         p.title AS title, p.update_time AS update_time, p.reason AS reason
    FROM projects p
   WHERE p.status IN (1, 3, 5)
  UNION ALL
  SELECT 'work_item' AS kind, w.work_id AS id, w.state AS state,
         w.title AS title, w.update_time AS update_time, w.reason AS reason
    FROM work_items w
   WHERE w.state IN ('submitted', 'failed')
     AND NOT (w.state = 'submitted'
              AND w.kind IN ('requirement', 'design', 'breakdown'))
)
ORDER BY julianday(update_time) ASC, kind ASC, id ASC
LIMIT ?`

// InboxRow is the read model of one pending inbox item.
//
// Kind is "project" or "work_item"; ID is the business id (APP-1 / APP-1-T0001),
// never the physical row id. State is the raw stored value as text — a project
// status number or a work-item state name — and the inbox layer interprets it.
// UpdateTime is kept as the stored RFC3339Nano text: the API returns it
// verbatim and the age is derived from it in the inbox package, so no precision
// is lost between the query and the response.
type InboxRow struct {
	Kind       string  `db:"kind"`
	ID         string  `db:"id"`
	State      string  `db:"state"`
	Title      string  `db:"title"`
	UpdateTime string  `db:"update_time"`
	Reason     *string `db:"reason"`
}

// ListInbox returns up to limit pending items ordered by dwell time,
// longest first (oldest update_time first), then kind and business id.
//
// The result is always freshly read from the database: no snapshot, cache or
// materialised table exists for the inbox.
func ListInbox(ctx context.Context, q Querier, limit int) ([]InboxRow, error) {
	rows, err := All[InboxRow](ctx, q, listInboxQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list inbox: %w", err)
	}
	return rows, nil
}
