package app

import (
	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// materialFromRow maps one persisted material row onto the domain type.
// Kept after the v2 service layer removal: the material service still needs it.
func materialFromRow(row store.Material) (domain.Material, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return domain.Material{}, err
	}
	return domain.Material{
		MaterialID: row.MaterialID,
		Kind:       row.Kind,
		Version:    row.Version,
		Title:      row.Title,
		SHA256:     row.SHA256,
		Size:       row.Size,
		UploadedBy: row.UploadedBy,
		CreateTime: createTime,
	}, nil
}
