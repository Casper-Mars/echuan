package app

import (
	"context"

	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
)

const (
	// maxSpecTitleLen bounds a spec title in characters. It matches the project
	// title bound so a spec written under a project name always fits.
	maxSpecTitleLen = 200
)

// SpecSubmission is the outcome of one POST /v1/projects/specs.
//
// Project is the row as it looks after the pointer move (its Version is the
// bumped one). Spec is the freshly stored material version. PreviousMaterialID
// is the business id of the version this one chains onto ("" for the first).
type SpecSubmission struct {
	Project            domain.Project
	Spec               domain.Material
	PreviousMaterialID string
}

// SubmitSpec appends one spec version (kind=2) to a project and moves the
// project's spec pointer (tech design §4.2, §5.7).
//
// The whole write is one transaction: read the project → status guard → read
// the current chain head → store the object and its row → move
// spec_material_id + version + update_time + last_actor → read the result back.
// The chain head is read inside that transaction, and every write goes through
// the store's single write connection, so concurrent submissions queue instead
// of forking the chain: each one sees the previous head and takes head+1.
//
// status and reason are deliberately untouched. Submitting a spec does not move
// the review gate (decide does), and a rejection reason stays readable until
// the next approval, which is what lets a reviewer compare the new version
// against the objection that produced it (tech design §3.3).
//
// title is a pointer to tell "not sent" apart from "sent but blank": an absent
// title falls back to the project title, a blank or oversized one is rejected.
func (s *ProjectService) SubmitSpec(ctx context.Context, projectID, actor string, title *string, body string) (SpecSubmission, error) {
	result, _, err := s.SaveSpec(ctx, projectID, "", actor, title, body, 0, true)
	return result, err
}

// validateSpecTitle normalises the optional spec title.
//
// nil means "the field was absent" and selects the project title as fallback.
// A present but blank title is a client mistake and is reported instead of
// silently replaced, so a caller cannot make the stored title look empty.
func validateSpecTitle(title *string) (string, []domain.FieldError) {
	if title == nil {
		return "", nil
	}
	trimmed := strings.TrimSpace(*title)
	switch {
	case trimmed == "":
		return "", []domain.FieldError{{Field: "title", Reason: "must not be blank"}}
	case utf8.RuneCountInString(trimmed) > maxSpecTitleLen:
		return "", []domain.FieldError{{
			Field:  "title",
			Reason: fmt.Sprintf("must be at most %d characters", maxSpecTitleLen),
		}}
	}
	return trimmed, nil
}
