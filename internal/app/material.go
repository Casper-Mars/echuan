package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/store"
)

// MaterialService serves the material read surface: one object stream for the
// download endpoint and the version chain behind a project pointer.
//
// It never loads a whole object into memory: Download hands the HTTP layer an
// open file that http.ServeContent streams, so a material_max_bytes-sized body
// costs a constant amount of memory.
type MaterialService struct {
	store     *store.Store
	materials *material.Writer
}

// NewMaterialService builds the material service on top of an open store and
// the writer that owns data_dir/objects.
func NewMaterialService(st *store.Store, materials *material.Writer) *MaterialService {
	return &MaterialService{store: st, materials: materials}
}

// Download is one material version plus an open handle on its bytes. The caller
// owns Content and must close it.
type Download struct {
	Material domain.Material
	Content  io.ReadSeekCloser
}

// Download resolves materialID and opens its object file.
//
// The path comes from the materials row but is re-checked against
// data_dir/objects before opening (see material.Writer.Open), so a corrupted
// row cannot read outside the store.
func (s *MaterialService) Download(ctx context.Context, materialID string) (Download, error) {
	id := strings.TrimSpace(materialID)
	if id == "" {
		return Download{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	row, err := store.GetMaterialByMaterialID(ctx, s.store.Read(), id)
	if err != nil {
		if store.IsNotFound(err) {
			return Download{}, &domain.NotFoundError{Resource: "material", Key: id}
		}
		return Download{}, fmt.Errorf("read material %s: %w", id, err)
	}

	mapped, err := materialFromRow(row)
	if err != nil {
		return Download{}, err
	}
	content, err := s.materials.Open(row)
	if err != nil {
		return Download{}, err
	}
	return Download{Material: mapped, Content: content}, nil
}

// MaterialVersion is one link of a chain. PreviousMaterialID is the business id
// of the preceding version ("" for the root), never the physical id: physical
// ids are an internal detail of the store.
type MaterialVersion struct {
	Material           domain.Material
	PreviousMaterialID string
}

// MaterialChain is the ordered history of one kind for one project.
// CurrentMaterialID is empty when the project has no material of that kind.
type MaterialChain struct {
	Versions          []MaterialVersion
	CurrentMaterialID string
}

// Chain returns the version history of one material kind for a project.
//
// The chain starts at the project pointer for kind (requirement_material_id or
// spec_material_id) and follows previous_material_id back to the root; it is
// never derived from kind plus timestamps. A broken link surfaces as
// store.ErrChainBroken, which the HTTP layer answers with 500 and a log line:
// the data is corrupt and pretending otherwise would hide it.
func (s *MaterialService) Chain(ctx context.Context, projectID string, kind int) (MaterialChain, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return MaterialChain{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}
	switch kind {
	case domain.MaterialKindRequirement, domain.MaterialKindSpec:
	default:
		return MaterialChain{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "kind", Reason: "must be 1 (requirement) or 2 (spec)"},
		}}
	}

	row, err := store.GetProjectByProjectID(ctx, s.store.Read(), projectID)
	if err != nil {
		if store.IsNotFound(err) {
			return MaterialChain{}, &domain.NotFoundError{Resource: "project", Key: projectID}
		}
		return MaterialChain{}, fmt.Errorf("read project %s: %w", projectID, err)
	}

	var head *int64
	switch kind {
	case domain.MaterialKindRequirement:
		head = row.RequirementMaterialID
	case domain.MaterialKindSpec:
		head = row.SpecMaterialID
	}

	chain := MaterialChain{Versions: []MaterialVersion{}}
	if head == nil {
		return chain, nil
	}

	rows, err := store.ListChainByHead(ctx, s.store.Read(), *head)
	if err != nil {
		return MaterialChain{}, fmt.Errorf("material chain of project %s kind %d: %w", projectID, kind, err)
	}

	for i, link := range rows {
		mapped, err := materialFromRow(link)
		if err != nil {
			return MaterialChain{}, err
		}
		previous := ""
		if i > 0 {
			previous = rows[i-1].MaterialID
		}
		chain.Versions = append(chain.Versions, MaterialVersion{
			Material:           mapped,
			PreviousMaterialID: previous,
		})
	}
	chain.CurrentMaterialID = rows[len(rows)-1].MaterialID
	return chain, nil
}
