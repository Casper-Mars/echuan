package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

const (
	// maxSpecTitleLen bounds a spec title in characters. It matches the project
	// title bound so a spec written under a project name always fits.
	maxSpecTitleLen = 200
	// specExcerptRunes is how much of the current spec body the detail endpoint
	// previews. It counts runes, not bytes, so a Chinese or emoji-heavy body is
	// never cut in the middle of a UTF-8 sequence.
	specExcerptRunes = 200
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
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return SpecSubmission{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	bodyBytes, fields := s.validateRequirementBody("body_md", body)
	if len(fields) > 0 {
		return SpecSubmission{}, &domain.ValidationError{Fields: fields}
	}
	normalizedTitle, titleFields := validateSpecTitle(title)
	if len(titleFields) > 0 {
		return SpecSubmission{}, &domain.ValidationError{Fields: titleFields}
	}

	uploadedBy := actorOrDefault(actor)

	var (
		out     SpecSubmission
		written material.Stored
	)

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}
		// A spec is only meaningful while it is being reviewed or while the CTO
		// is still planning: past plan_review the breakdown was built on the
		// approved spec.
		if row.Status != domain.StatusSpecReview && row.Status != domain.StatusPlanning {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q is in status %s; a spec can only be submitted in spec_review or planning",
				projectID, domain.StatusName(row.Status))}
		}

		titleValue := row.Title
		if normalizedTitle != "" {
			titleValue = normalizedTitle
		}

		// The head's business id is what the response reports as
		// previous_material_id; the physical id only travels into the writer.
		previousMaterialID := ""
		if row.SpecMaterialID != nil {
			head, err := store.GetMaterialByID(ctx, tx, *row.SpecMaterialID)
			if err != nil {
				return err
			}
			previousMaterialID = head.MaterialID
		}

		// material.Write owns the object file, the dedup check and the row; it
		// derives version = head+1 from the pointer read above, so a caller
		// cannot write a version that skips or repeats.
		written, err = s.materials.Write(ctx, tx, material.WriteInput{
			Kind:               domain.MaterialKindSpec,
			Title:              titleValue,
			Body:               bodyBytes,
			UploadedBy:         uploadedBy,
			PreviousMaterialID: row.SpecMaterialID,
		})
		if err != nil {
			return materialWriteError("body_md", err)
		}

		if err := store.MoveSpecPointer(ctx, tx, row.ID, written.Material.ID,
			uploadedBy, formatTimestamp(s.nextTimestamp(row.UpdateTime))); err != nil {
			return err
		}

		fresh, err := store.GetProjectByID(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		project, err := projectFromRow(fresh)
		if err != nil {
			return err
		}
		spec, err := materialFromRow(written.Material)
		if err != nil {
			return err
		}
		out = SpecSubmission{
			Project:            project,
			Spec:               spec,
			PreviousMaterialID: previousMaterialID,
		}
		return nil
	})
	if err != nil {
		// The row died with the rolled-back transaction; the object file did
		// not. Drop it only when this request created it (same policy as
		// CreateProject/WriteRequirement).
		if written.NewObject {
			_ = s.materials.RemoveNewObject(written)
		}
		return SpecSubmission{}, err
	}
	return out, nil
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

// excerptRunes returns the first limit runes of s.
//
// It walks the rune boundaries instead of converting the whole body to
// []rune, so a material_max_bytes-sized body costs no extra allocation and the
// cut can never land inside a multi-byte character.
func excerptRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}
