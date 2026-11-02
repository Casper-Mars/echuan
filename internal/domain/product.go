package domain

import "time"

// Product is the outermost container of the platform: one product owns many
// projects.
//
// ID is the physical row id. It is internal to the service: it is never
// exposed in responses and only surfaces inside the opaque list cursor.
type Product struct {
	ID           int64
	ProductID    string
	Name         string
	GitURL       string
	Description  string
	License      string
	ProjectCount int64
	CreateTime   time.Time
	UpdateTime   time.Time
}
