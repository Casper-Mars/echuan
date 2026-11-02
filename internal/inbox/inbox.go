// Package inbox derives the human inbox: which reviews and task decisions are
// waiting, how long they have been waiting and whether that is already stale.
//
// The package is pure: it performs no I/O, reads no clock of its own and writes
// nothing. Everything it needs (the rows, the current time and the stale
// threshold) is passed in, which makes the derivation testable without a
// database or a multi-day sleep. Fetching the rows lives in internal/store and
// wiring lives in internal/app.
package inbox

import (
	"fmt"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// Item kinds as they appear on the wire and in the UNION query.
const (
	// KindProject is a project waiting for a human review gate.
	KindProject = "project"
	// KindTask is a task waiting for acceptance or a failed task awaiting a
	// retry/abandon verdict.
	KindTask = "task"
)

// Item is one pending inbox row as read from the database.
//
// UpdateTime is the stored RFC3339/RFC3339Nano text. Keeping it as text lets
// the API return the persisted value unchanged; Derive parses it only to
// compute the age.
type Item struct {
	Kind       string
	ID         string
	State      int
	Title      string
	UpdateTime string
	Reason     *string
}

// Entry is one item of the inbox after derivation.
//
// Age is floored to whole seconds and clamped at zero, so a row written by a
// clock slightly ahead of the reader never produces a negative age. Stale
// means Age reached the configured review_stale_hours threshold; the flag is
// computed per request and never persisted.
type Entry struct {
	Kind       string
	ID         string
	State      int
	StateName  string
	Title      string
	UpdateTime string
	Age        time.Duration
	Stale      bool
	Reason     *string
}

// AgeSeconds renders Age as whole seconds, which is the wire field
// age_seconds.
func (e Entry) AgeSeconds() int64 { return int64(e.Age / time.Second) }

// Derive computes the age and stale flag of every item.
//
// Input order is preserved: the store already returns the rows sorted by dwell
// time (oldest first), and re-sorting here would duplicate that rule. now is
// the single clock reading of the request; staleAfter is
// cfg.ReviewStaleHours expressed as a duration.
//
// A row whose update_time does not parse is an error rather than a silently
// wrong age: every writer in the service stores UTC RFC3339Nano, so a parse
// failure means the database was modified outside the service.
func Derive(items []Item, now time.Time, staleAfter time.Duration) ([]Entry, error) {
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		updateTime, err := time.Parse(time.RFC3339Nano, item.UpdateTime)
		if err != nil {
			return nil, fmt.Errorf("inbox: parse update_time %q of %s %s: %w",
				item.UpdateTime, item.Kind, item.ID, err)
		}

		age := now.Sub(updateTime)
		if age < 0 {
			age = 0
		}
		// Truncate rounds toward zero; age is non-negative here, so it is the
		// floor the contract asks for (age_seconds = floor(now - update_time)).
		age = age.Truncate(time.Second)

		entries = append(entries, Entry{
			Kind:       item.Kind,
			ID:         item.ID,
			State:      item.State,
			StateName:  stateName(item.Kind, item.State),
			Title:      item.Title,
			UpdateTime: item.UpdateTime,
			Age:        age,
			Stale:      age >= staleAfter,
			Reason:     item.Reason,
		})
	}
	return entries, nil
}

// stateName picks the vocabulary that belongs to the kind. Both name tables
// live in domain; an unknown kind yields the empty string instead of a
// fabricated name.
func stateName(kind string, state int) string {
	switch kind {
	case KindProject:
		return domain.StatusName(state)
	case KindTask:
		return domain.TaskStatusName(state)
	default:
		return ""
	}
}
