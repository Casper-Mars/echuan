package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

type SpecDocument struct {
	ID                 string
	ProjectID          string
	Current            domain.Material
	PreviousMaterialID string
	BodyMD             string
	Versions           []MaterialVersion
}

func (s *ProjectService) ListSpecs(ctx context.Context, projectID string) ([]SpecDocument, error) {
	p, err := store.GetProjectByProjectID(ctx, s.store.Read(), projectID)
	if err != nil {
		return nil, specLookupError(err, "project", projectID)
	}
	docs, err := store.ListSpecDocuments(ctx, s.store.Read(), p.ID)
	if err != nil {
		return nil, err
	}
	out := make([]SpecDocument, 0, len(docs))
	for _, d := range docs {
		m, err := store.GetMaterialByID(ctx, s.store.Read(), d.MaterialID)
		if err != nil {
			return nil, err
		}
		mapped, err := materialFromRow(m)
		if err != nil {
			return nil, err
		}
		previous := ""
		if m.PreviousMaterialID != nil {
			prev, err := store.GetMaterialByID(ctx, s.store.Read(), *m.PreviousMaterialID)
			if err != nil {
				return nil, err
			}
			previous = prev.MaterialID
		}
		out = append(out, SpecDocument{ID: d.SpecID, ProjectID: projectID, Current: mapped, PreviousMaterialID: previous})
	}
	return out, nil
}

func (s *ProjectService) GetSpec(ctx context.Context, specID, materialID string) (SpecDocument, error) {
	d, err := store.GetSpecDocument(ctx, s.store.Read(), specID)
	if err != nil {
		return SpecDocument{}, specLookupError(err, "spec", specID)
	}
	p, err := store.GetProjectByID(ctx, s.store.Read(), d.ProjectID)
	if err != nil {
		return SpecDocument{}, err
	}
	chain, err := store.ListChainByHead(ctx, s.store.Read(), d.MaterialID)
	if err != nil {
		return SpecDocument{}, err
	}
	out := SpecDocument{ID: specID, ProjectID: p.ProjectID, Versions: []MaterialVersion{}}
	previous := ""
	found := false
	for _, m := range chain {
		mapped, err := materialFromRow(m)
		if err != nil {
			return SpecDocument{}, err
		}
		out.Versions = append(out.Versions, MaterialVersion{Material: mapped, PreviousMaterialID: previous})
		previous = m.MaterialID
		if (materialID == "" && m.ID == d.MaterialID) || materialID == m.MaterialID {
			body, err := s.materials.Read(m)
			if err != nil {
				return SpecDocument{}, err
			}
			out.Current = mapped
			out.BodyMD = string(body)
			found = true
		}
	}
	if !found {
		return SpecDocument{}, &domain.NotFoundError{Resource: "spec version", Key: materialID}
	}
	return out, nil
}

func specLookupError(err error, resource, key string) error {
	if store.IsNotFound(err) {
		return &domain.NotFoundError{Resource: resource, Key: key}
	}
	return err
}

// SaveSpec keeps independent chains and the legacy execution snapshot atomic.
func (s *ProjectService) SaveSpec(ctx context.Context, projectID, specID, actor string, title *string, body string, expected int, legacy bool) (SpecSubmission, string, error) {
	if specID == "" && strings.TrimSpace(projectID) == "" {
		return SpecSubmission{}, "", &domain.ValidationError{Fields: []domain.FieldError{{Field: "id", Reason: "is required"}}}
	}
	bytes, fields := s.validateRequirementBody("body_md", body)
	if len(fields) > 0 {
		return SpecSubmission{}, "", &domain.ValidationError{Fields: fields}
	}
	normalized, fields := validateSpecTitle(title)
	if len(fields) > 0 {
		return SpecSubmission{}, "", &domain.ValidationError{Fields: fields}
	}
	actor = actorOrDefault(actor)
	var out SpecSubmission
	var written []material.Stored
	savedID := ""
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		var doc store.SpecDocument
		if specID != "" {
			var err error
			doc, err = store.GetSpecDocument(ctx, tx, specID)
			if err != nil {
				return specLookupError(err, "spec", specID)
			}
			p, err := store.GetProjectByID(ctx, tx, doc.ProjectID)
			if err != nil {
				return err
			}
			projectID = p.ProjectID
		}
		p, err := store.GetProjectByProjectID(ctx, tx, strings.TrimSpace(projectID))
		if err != nil {
			return specLookupError(err, "project", projectID)
		}
		if p.Status != domain.StatusSpecDraft && p.Status != domain.StatusSpecReview {
			return &domain.InvalidStateError{Reason: "specs can only be changed in spec_draft or spec_review"}
		}
		docs, err := store.ListSpecDocuments(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if legacy && len(docs) > 0 {
			doc = docs[0]
		}
		var head *int64
		if doc.ID != 0 {
			head = &doc.MaterialID
			current, err := store.GetMaterialByID(ctx, tx, doc.MaterialID)
			if err != nil {
				return err
			}
			if !legacy && current.Version != expected {
				return &domain.InvalidVersionError{Current: current.Version, Expected: expected}
			}
		}
		titleValue := normalized
		if titleValue == "" {
			titleValue = p.Title
		}
		m, err := s.materials.Write(ctx, tx, material.WriteInput{Kind: domain.MaterialKindSpec, Title: titleValue, Body: bytes, UploadedBy: actor, PreviousMaterialID: head})
		if err != nil {
			return materialWriteError("body_md", err)
		}
		written = append(written, m)
		updated := formatTimestamp(s.nextTimestamp(p.UpdateTime))
		if doc.ID == 0 {
			seq, err := store.NextSeqNo(ctx, tx, "project_specs", "project_id", p.ID)
			if err != nil {
				return err
			}
			savedID = fmt.Sprintf("%s-S%04d", p.ProjectID, seq)
			err = store.InsertSpecDocument(ctx, tx, store.SpecDocument{SpecID: savedID, ProjectID: p.ID, SeqNo: seq, MaterialID: m.Material.ID, CreateTime: updated, UpdateTime: updated})
			if err != nil {
				return err
			}
		} else {
			savedID = doc.SpecID
			if err := store.MoveSpecDocument(ctx, tx, doc.ID, m.Material.ID, updated); err != nil {
				return err
			}
		}
		docs, err = store.ListSpecDocuments(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		snapshot := m
		if len(docs) > 1 {
			var bundle strings.Builder
			fmt.Fprintf(&bundle, "# %s · 技术方案包\n\n", p.Title)
			for _, d := range docs {
				entry, err := store.GetMaterialByID(ctx, tx, d.MaterialID)
				if err != nil {
					return err
				}
				content, err := s.materials.Read(entry)
				if err != nil {
					return err
				}
				fmt.Fprintf(&bundle, "## %s · %s（第 %d 版）\n\n", d.SpecID, entry.Title, entry.Version)
				bundle.Write(content)
				bundle.WriteString("\n\n---\n\n")
				if int64(bundle.Len()) > s.materials.MaxBytes() {
					return &domain.TooLargeError{Field: "body_md", Limit: s.materials.MaxBytes(), Actual: int64(bundle.Len())}
				}
			}
			snapshot, err = s.materials.Write(ctx, tx, material.WriteInput{Kind: domain.MaterialKindSpec, Title: p.Title + " · 技术方案包", Body: []byte(bundle.String()), UploadedBy: actor, PreviousMaterialID: p.SpecMaterialID})
			if err != nil {
				return materialWriteError("body_md", err)
			}
			written = append(written, snapshot)
		}
		previous := ""
		if p.SpecMaterialID != nil {
			old, err := store.GetMaterialByID(ctx, tx, *p.SpecMaterialID)
			if err != nil {
				return err
			}
			previous = old.MaterialID
		}
		if err := store.MoveSpecPointer(ctx, tx, p.ID, snapshot.Material.ID, actor, updated); err != nil {
			return err
		}
		fresh, err := store.GetProjectByID(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		project, err := projectFromRow(fresh)
		if err != nil {
			return err
		}
		mapped, err := materialFromRow(snapshot.Material)
		if err != nil {
			return err
		}
		out = SpecSubmission{Project: project, Spec: mapped, PreviousMaterialID: previous}
		return nil
	})
	if err != nil {
		for _, m := range written {
			if m.NewObject {
				_ = s.materials.RemoveNewObject(m)
			}
		}
		return SpecSubmission{}, "", err
	}
	return out, savedID, nil
}
