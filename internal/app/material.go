package app

import (
	"context"
	"database/sql"
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

// CreateMaterial stores one material version and returns its row. The object
// bytes and the materials row are written in one transaction (file first, row
// second); a failed transaction removes the freshly written object.
func (s *MaterialService) CreateMaterial(ctx context.Context, kind int, title string, body []byte, uploadedBy string) (store.Material, error) {
	var stored material.Stored
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		stored, err = s.materials.Write(ctx, tx, material.WriteInput{
			Kind:       kind,
			Title:      title,
			Body:       body,
			UploadedBy: uploadedBy,
		})
		return err
	})
	if err != nil {
		if stored.NewObject {
			_ = s.materials.RemoveNewObject(stored)
		}
		return store.Material{}, err
	}
	return stored.Material, nil
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

