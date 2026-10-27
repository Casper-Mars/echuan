package store

import (
	"context"
	"fmt"
)

// listInboxQuery is the single statement behind GET /v1/inbox (tech design
// §5.6): the union of every human-blocked project and task, oldest first.
//
// It is deliberately one UNION ALL subquery instead of two queries: the LIMIT
// must apply to the merged candidate set, otherwise the "50 oldest pending
// items" would silently become "the oldest 50 projects plus the oldest 50
// tasks". The status literals mirror domain.ProjectStatusSpecReview /
// PlanReview / DeliveryReview (1/3/5) and domain.TaskStatusPendingAcceptance /
// TaskStatusFailed (3/5); they are spelled out here because the dependency
// runs domain ← store and store must not import domain.
//
// Ordering uses julianday() rather than the raw text column: update_time is
// stored as RFC3339Nano, whose fractional part is variable length, so a plain
// string ORDER BY would sort "…:00.500Z" after "…:00Z" even though .5s is
// earlier. kind/id break ties deterministically.
const listInboxQuery = `SELECT kind, id, state, title, update_time, reason
FROM (
  SELECT 'project' AS kind, p.project_id AS id, p.status AS state,
         p.title AS title, p.update_time AS update_time, p.reason AS reason
    FROM projects p
   WHERE p.status IN (1, 3, 5)
  UNION ALL
  SELECT 'task' AS kind, t.task_id AS id, t.status AS state,
         t.title AS title, t.update_time AS update_time, t.reason AS reason
    FROM tasks t
   WHERE t.status IN (3, 5)
)
ORDER BY julianday(update_time) ASC, kind ASC, id ASC
LIMIT ?`

// InboxRow is the read model of one pending inbox item.
//
// Kind is "project" or "task"; ID is the business id (APP-1 / APP-1-T0001),
// never the physical row id. UpdateTime is kept as the stored RFC3339Nano
// text: the API returns it verbatim and the age is derived from it in the
// inbox package, so no precision is lost between the query and the response.
type InboxRow struct {
	Kind       string  `db:"kind"`
	ID         string  `db:"id"`
	State      int     `db:"state"`
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
