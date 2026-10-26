package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	sqlite "modernc.org/sqlite"
)

// ErrUniqueViolation reports that a write was rejected by a UNIQUE
// constraint. Callers translate it into a domain-level conflict; the check
// relies on the database constraint instead of a read-then-insert race.
var ErrUniqueViolation = errors.New("store: unique constraint violation")

// productColumns selects a product together with its project count.
//
// The count is a correlated subquery so that listing N products stays one
// statement (no N+1) and the number is always read in the same snapshot as the
// product row.
const productColumns = `p.id, p.product_id, p.name, p.git_url, p.description, p.license,
	(SELECT COUNT(*) FROM projects WHERE projects.product_id = p.id) AS project_count,
	p.create_time, p.update_time`

// Product is the read model of one products row.
type Product struct {
	ID           int64   `db:"id"`
	ProductID    string  `db:"product_id"`
	Name         string  `db:"name"`
	GitURL       string  `db:"git_url"`
	Description  *string `db:"description"`
	License      *string `db:"license"`
	ProjectCount int64   `db:"project_count"`
	CreateTime   string  `db:"create_time"`
	UpdateTime   string  `db:"update_time"`
}

// NewProduct is the write model for INSERT.
type NewProduct struct {
	ProductID   string
	Name        string
	GitURL      string
	Description *string
	License     *string
	CreateTime  string
	UpdateTime  string
}

// ProductUpdate is the write model for a full-row UPDATE. Optional fields are
// nil to store SQL NULL.
type ProductUpdate struct {
	Name        string
	GitURL      string
	Description *string
	License     *string
	UpdateTime  string
}

// GetProductByID returns the product with the given physical id.
func GetProductByID(ctx context.Context, q Querier, id int64) (Product, error) {
	return One[Product](ctx, q,
		`SELECT `+productColumns+` FROM products p WHERE p.id = ?`, id)
}

// GetProductByProductID returns the product with the given business id.
func GetProductByProductID(ctx context.Context, q Querier, productID string) (Product, error) {
	return One[Product](ctx, q,
		`SELECT `+productColumns+` FROM products p WHERE p.product_id = ?`, productID)
}

// ListProducts returns up to limit products with id > afterID ordered by id
// ascending. afterID 0 starts from the beginning.
func ListProducts(ctx context.Context, q Querier, afterID int64, limit int) ([]Product, error) {
	return All[Product](ctx, q,
		`SELECT `+productColumns+` FROM products p
		 WHERE p.id > ?
		 ORDER BY p.id ASC
		 LIMIT ?`, afterID, limit)
}

// CreateProduct inserts a product and returns its physical id.
//
// A duplicate product_id is reported as ErrUniqueViolation through the UNIQUE
// constraint; the write is never guarded by a prior SELECT.
func CreateProduct(ctx context.Context, tx *sql.Tx, p NewProduct) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO products (product_id, name, git_url, description, license, create_time, update_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ProductID, p.Name, p.GitURL, p.Description, p.License, p.CreateTime, p.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %v", ErrUniqueViolation, err)
		}
		return 0, fmt.Errorf("store: insert product: %w", err)
	}
	return res.LastInsertId()
}

// UpdateProduct replaces the mutable columns of one product.
func UpdateProduct(ctx context.Context, tx *sql.Tx, id int64, p ProductUpdate) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE products
		   SET name = ?, git_url = ?, description = ?, license = ?, update_time = ?
		 WHERE id = ?`,
		p.Name, p.GitURL, p.Description, p.License, p.UpdateTime, id)
	if err != nil {
		return fmt.Errorf("store: update product %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update product %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update product %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// DeleteProduct removes one product by physical id.
func DeleteProduct(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete product %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete product %d: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: delete product %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

// CountProjectsByProduct counts the projects owned by a product (physical id).
//
// It is meant to run inside the same transaction as the DELETE so that no
// project can be attached between the check and the removal.
func CountProjectsByProduct(ctx context.Context, q Querier, productID int64) (int64, error) {
	row, err := One[struct {
		Count int64 `db:"count"`
	}](ctx, q, `SELECT COUNT(*) AS count FROM projects WHERE product_id = ?`, productID)
	if err != nil {
		return 0, fmt.Errorf("store: count projects for product %d: %w", productID, err)
	}
	return row.Count, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE (or primary key)
// constraint failure. Extended result codes 2067/1555 are checked first; the
// message fallback keeps a plain-code driver from silently turning a
// duplicate into a 500.
func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case 19, 1555, 2067: // SQLITE_CONSTRAINT, _PRIMARYKEY, _UNIQUE
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "constraint failed: products.product_id")
}
