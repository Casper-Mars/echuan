package app

import (
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// invalidField builds a one-field validation error.
func invalidField(field, reason string) *domain.ValidationError {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Reason: reason}}}
}

// decideReason resolves the reason column of one project transition: a submit
// or an internal advance clears it, an approve clears it, a completion clears it
// (the closing record lives in gate_decisions, not in reason), and any other
// verdict stores the trimmed reason.
func decideReason(action string, decision domain.Decision, reason string) *string {
	switch {
	case action == domain.ProjectActionSubmit:
		return nil
	case action == domain.ProjectActionAddFirstTask,
		action == domain.ProjectActionReviewTasks,
		action == domain.ProjectActionComplete:
		return nil
	case action == domain.ProjectActionDecide && decision == domain.DecisionApprove:
		return nil
	case strings.TrimSpace(reason) == "":
		return nil
	default:
		value := strings.TrimSpace(reason)
		return &value
	}
}

// timestampAt truncates a clock reading to milliseconds, the precision of the
// persisted create_time/update_time columns.
func timestampAt(now time.Time) time.Time {
	return now.UTC().Truncate(time.Millisecond)
}

// nextTimestampAfter returns a timestamp strictly after previous, so two
// writes in the same millisecond still order deterministically.
func nextTimestampAfter(now time.Time, previous string) time.Time {
	next := timestampAt(now)
	old, err := parseTimestamp(previous)
	if err != nil {
		return next
	}
	if !next.After(old) {
		return old.Add(time.Millisecond)
	}
	return next
}
