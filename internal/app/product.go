package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Product validation limits (tech design §4.2).
const (
	maxProductNameLen = 200
	maxGitURLLen      = 500
	maxDescriptionLen = 2000
	maxLicenseLen     = 64

	maxListLimit = 200
)

// productIDPattern is the product_id short code: one leading letter followed by
// letters, digits, underscores or hyphens.
var productIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// CreateProductInput is the use-case input for POST /v1/products.
type CreateProductInput struct {
	ProductID   string
	Name        string
	GitURL      string
	Description string
	License     string
}

// UpdateProductInput is the use-case input for PATCH /v1/products. Nil means
// "field not supplied"; ProductID is only accepted so that it can be rejected
// with a precise field error.
type UpdateProductInput struct {
	ProductID   *string
	Name        *string
	GitURL      *string
	Description *string
	License     *string
}

// ProductPage is one page of products plus the physical id of the last item
// when more rows exist (0 otherwise). The API layer turns NextID into the
// opaque next_cursor value.
type ProductPage struct {
	Items  []domain.Product
	NextID int64
}

// ProductService implements the product use cases. It owns validation,
// business rules and transaction boundaries; SQL stays in the store package.
type ProductService struct {
	store *store.Store
	now   func() time.Time
}

// NewProductService builds the product service on top of an open store.
func NewProductService(st *store.Store) *ProductService {
	return &ProductService{store: st, now: func() time.Time { return time.Now().UTC() }}
}

// CreateProduct inserts a product. Duplicate product_id is detected by the
// UNIQUE constraint (never by a prior SELECT) and reported as a conflict.
func (s *ProductService) CreateProduct(ctx context.Context, in CreateProductInput) (domain.Product, error) {
	normalized, fields := validateCreateProduct(in)
	if len(fields) > 0 {
		return domain.Product{}, &domain.ValidationError{Fields: fields}
	}

	now := s.timestamp()
	ts := formatTimestamp(now)

	var out domain.Product
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		id, err := store.CreateProduct(ctx, tx, store.NewProduct{
			ProductID:   normalized.ProductID,
			Name:        normalized.Name,
			GitURL:      normalized.GitURL,
			Description: optionalString(normalized.Description),
			License:     optionalString(normalized.License),
			CreateTime:  ts,
			UpdateTime:  ts,
		})
		if err != nil {
			if errors.Is(err, store.ErrUniqueViolation) {
				return &domain.ConflictError{Reason: fmt.Sprintf("product %q already exists", normalized.ProductID)}
			}
			return err
		}

		row, err := store.GetProductByID(ctx, tx, id)
		if err != nil {
			return err
		}
		out, err = productFromRow(row)
		return err
	})
	if err != nil {
		return domain.Product{}, err
	}
	return out, nil
}

// GetProduct returns one product by business id.
func (s *ProductService) GetProduct(ctx context.Context, productID string) (domain.Product, error) {
	if strings.TrimSpace(productID) == "" {
		return domain.Product{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	row, err := store.GetProductByProductID(ctx, s.store.Read(), productID)
	if err != nil {
		if store.IsNotFound(err) {
			return domain.Product{}, &domain.NotFoundError{Resource: "product", Key: productID}
		}
		return domain.Product{}, err
	}
	return productFromRow(row)
}

// ListProducts returns a stable page ordered by physical id. It reads
// limit+1 rows so the caller knows whether a next page exists without a
// second query.
func (s *ProductService) ListProducts(ctx context.Context, afterID int64, limit int) (ProductPage, error) {
	if limit < 1 || limit > maxListLimit {
		return ProductPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "limit", Reason: fmt.Sprintf("must be between 1 and %d", maxListLimit)},
		}}
	}
	if afterID < 0 {
		return ProductPage{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "cursor", Reason: "is invalid"},
		}}
	}

	rows, err := store.ListProducts(ctx, s.store.Read(), afterID, limit+1)
	if err != nil {
		return ProductPage{}, err
	}

	page := ProductPage{Items: make([]domain.Product, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			page.NextID = page.Items[limit-1].ID
			break
		}
		product, err := productFromRow(row)
		if err != nil {
			return ProductPage{}, err
		}
		page.Items = append(page.Items, product)
	}
	return page, nil
}

// UpdateProduct patches the mutable fields of one product. It always bumps
// update_time strictly after the previous value and never touches create_time.
func (s *ProductService) UpdateProduct(ctx context.Context, productID string, in UpdateProductInput) (domain.Product, error) {
	if strings.TrimSpace(productID) == "" {
		return domain.Product{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}
	normalized, fields := validateUpdateProduct(in)
	if len(fields) > 0 {
		return domain.Product{}, &domain.ValidationError{Fields: fields}
	}

	var out domain.Product
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProductByProductID(ctx, tx, productID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "product", Key: productID}
			}
			return err
		}

		oldUpdate, err := parseTimestamp(row.UpdateTime)
		if err != nil {
			return err
		}
		// Strictly increasing update_time: two patches in the same millisecond
		// still produce different timestamps, so clients can order writes by
		// update_time.
		next := s.timestamp()
		if !next.After(oldUpdate) {
			next = oldUpdate.Add(time.Millisecond)
		}

		update := store.ProductUpdate{
			Name:        row.Name,
			GitURL:      row.GitURL,
			Description: row.Description,
			License:     row.License,
			UpdateTime:  formatTimestamp(next),
		}
		if normalized.Name != nil {
			update.Name = *normalized.Name
		}
		if normalized.GitURL != nil {
			update.GitURL = *normalized.GitURL
		}
		if normalized.Description != nil {
			update.Description = optionalString(*normalized.Description)
		}
		if normalized.License != nil {
			update.License = optionalString(*normalized.License)
		}

		if err := store.UpdateProduct(ctx, tx, row.ID, update); err != nil {
			return err
		}

		fresh, err := store.GetProductByID(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		out, err = productFromRow(fresh)
		return err
	})
	if err != nil {
		return domain.Product{}, err
	}
	return out, nil
}

// DeleteProduct removes a product. The "still has projects" check runs in the
// same write transaction as the DELETE, so a project cannot slip in between.
func (s *ProductService) DeleteProduct(ctx context.Context, productID string) error {
	if strings.TrimSpace(productID) == "" {
		return &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	return s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProductByProductID(ctx, tx, productID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "product", Key: productID}
			}
			return err
		}

		projects, err := store.CountProjectsByProduct(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if projects > 0 {
			return &domain.ConflictError{Reason: fmt.Sprintf(
				"product %q still has %d project(s)", productID, projects)}
		}
		return store.DeleteProduct(ctx, tx, row.ID)
	})
}

// validateCreateProduct normalises and validates a create request.
func validateCreateProduct(in CreateProductInput) (CreateProductInput, []domain.FieldError) {
	var fields []domain.FieldError

	if in.ProductID == "" {
		fields = append(fields, domain.FieldError{Field: "product_id", Reason: "is required"})
	} else if !productIDPattern.MatchString(in.ProductID) {
		fields = append(fields, domain.FieldError{
			Field:  "product_id",
			Reason: "must match ^[A-Za-z][A-Za-z0-9_-]{0,31}$",
		})
	}

	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		fields = append(fields, domain.FieldError{Field: "name", Reason: "is required"})
	case utf8.RuneCountInString(in.Name) > maxProductNameLen:
		fields = append(fields, domain.FieldError{
			Field:  "name",
			Reason: fmt.Sprintf("must be at most %d characters", maxProductNameLen),
		})
	}

	in.GitURL = strings.TrimSpace(in.GitURL)
	switch {
	case in.GitURL == "":
		fields = append(fields, domain.FieldError{Field: "git_url", Reason: "is required"})
	case utf8.RuneCountInString(in.GitURL) > maxGitURLLen:
		fields = append(fields, domain.FieldError{
			Field:  "git_url",
			Reason: fmt.Sprintf("must be at most %d characters", maxGitURLLen),
		})
	}

	if utf8.RuneCountInString(in.Description) > maxDescriptionLen {
		fields = append(fields, domain.FieldError{
			Field:  "description",
			Reason: fmt.Sprintf("must be at most %d characters", maxDescriptionLen),
		})
	}

	in.License = strings.TrimSpace(in.License)
	if utf8.RuneCountInString(in.License) > maxLicenseLen {
		fields = append(fields, domain.FieldError{
			Field:  "license",
			Reason: fmt.Sprintf("must be at most %d characters", maxLicenseLen),
		})
	}

	return in, fields
}

// validateUpdateProduct normalises and validates a patch. It rejects the
// immutable product_id and an empty patch.
func validateUpdateProduct(in UpdateProductInput) (UpdateProductInput, []domain.FieldError) {
	var fields []domain.FieldError

	if in.ProductID != nil {
		fields = append(fields, domain.FieldError{Field: "product_id", Reason: "is immutable"})
	}

	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		switch {
		case trimmed == "":
			fields = append(fields, domain.FieldError{Field: "name", Reason: "is required"})
		case utf8.RuneCountInString(trimmed) > maxProductNameLen:
			fields = append(fields, domain.FieldError{
				Field:  "name",
				Reason: fmt.Sprintf("must be at most %d characters", maxProductNameLen),
			})
		default:
			in.Name = &trimmed
		}
	}

	if in.GitURL != nil {
		trimmed := strings.TrimSpace(*in.GitURL)
		switch {
		case trimmed == "":
			fields = append(fields, domain.FieldError{Field: "git_url", Reason: "is required"})
		case utf8.RuneCountInString(trimmed) > maxGitURLLen:
			fields = append(fields, domain.FieldError{
				Field:  "git_url",
				Reason: fmt.Sprintf("must be at most %d characters", maxGitURLLen),
			})
		default:
			in.GitURL = &trimmed
		}
	}

	if in.Description != nil && utf8.RuneCountInString(*in.Description) > maxDescriptionLen {
		fields = append(fields, domain.FieldError{
			Field:  "description",
			Reason: fmt.Sprintf("must be at most %d characters", maxDescriptionLen),
		})
	}

	if in.License != nil {
		trimmed := strings.TrimSpace(*in.License)
		if utf8.RuneCountInString(trimmed) > maxLicenseLen {
			fields = append(fields, domain.FieldError{
				Field:  "license",
				Reason: fmt.Sprintf("must be at most %d characters", maxLicenseLen),
			})
		} else {
			in.License = &trimmed
		}
	}

	if in.ProductID == nil && in.Name == nil && in.GitURL == nil && in.Description == nil && in.License == nil {
		fields = append(fields, domain.FieldError{
			Field:  "body",
			Reason: "at least one of name, git_url, description, license is required",
		})
	}

	return in, fields
}

// productFromRow converts the store read model into the domain entity.
func productFromRow(row store.Product) (domain.Product, error) {
	createTime, err := parseTimestamp(row.CreateTime)
	if err != nil {
		return domain.Product{}, err
	}
	updateTime, err := parseTimestamp(row.UpdateTime)
	if err != nil {
		return domain.Product{}, err
	}
	return domain.Product{
		ID:           row.ID,
		ProductID:    row.ProductID,
		Name:         row.Name,
		GitURL:       row.GitURL,
		Description:  stringValue(row.Description),
		License:      stringValue(row.License),
		ProjectCount: row.ProjectCount,
		CreateTime:   createTime,
		UpdateTime:   updateTime,
	}, nil
}

// timestamp returns the current time truncated to milliseconds, which is the
// precision persisted in create_time/update_time.
func (s *ProductService) timestamp() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// formatTimestamp renders a persisted timestamp as UTC RFC3339.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTimestamp parses a persisted RFC3339 timestamp.
func parseTimestamp(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("app: parse timestamp %q: %w", value, err)
	}
	return t.UTC(), nil
}

// optionalString maps an empty optional field to SQL NULL.
func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// stringValue maps a nullable column to its zero value.
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
