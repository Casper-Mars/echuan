package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Querier is the read side shared by *sql.DB and *sql.Tx, so the scan helpers
// work the same inside and outside a transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// One scans the first row of query into T. It returns sql.ErrNoRows when the
// query returns nothing.
//
// T must be a struct. Columns map to fields by the `db` tag, falling back to
// an exact (case-insensitive) field name. Columns without a matching field are
// discarded, which keeps `SELECT *` forward compatible. Nullable columns use
// pointer fields (*string, *int64, …) or database/sql Null* types.
func One[T any](ctx context.Context, q Querier, query string, args ...any) (T, error) {
	var zero T
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return zero, err
	}
	items, err := scanRows[T](rows)
	if err != nil {
		return zero, err
	}
	if len(items) == 0 {
		return zero, sql.ErrNoRows
	}
	return items[0], nil
}

// All scans every row of query into T. It returns an empty slice (never nil)
// when the query matches no rows.
func All[T any](ctx context.Context, q Querier, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanRows[T](rows)
}

// fieldPlan is one struct field selected for a result column.
type fieldPlan struct {
	index []int
}

// scanRows maps columns to struct fields and scans every row.
func scanRows[T any](rows *sql.Rows) ([]T, error) {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("store: read columns: %w", err)
	}

	plans, err := planColumns(reflect.TypeOf((*T)(nil)).Elem(), columns)
	if err != nil {
		return nil, err
	}

	items := make([]T, 0, 8)
	for rows.Next() {
		var item T
		itemValue := reflect.ValueOf(&item).Elem()
		targets := make([]any, len(columns))
		pointers := make([]reflect.Value, len(columns))

		for i := range columns {
			plan := plans[i]
			if plan == nil {
				targets[i] = new(any)
				continue
			}
			field := itemValue.FieldByIndex(plan.index)
			ptr := reflect.New(field.Type())
			targets[i] = ptr.Interface()
			pointers[i] = ptr
		}

		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("store: scan row: %w", err)
		}
		for i, ptr := range pointers {
			if ptr.IsValid() {
				itemValue.FieldByIndex(plans[i].index).Set(ptr.Elem())
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate rows: %w", err)
	}
	return items, nil
}

// planColumns resolves every result column to a struct field. A nil entry
// means "discard this column".
func planColumns(t reflect.Type, columns []string) ([]*fieldPlan, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("store: scan target %s is not a struct", t)
	}

	fields := make(map[string]fieldPlan, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		if f.Anonymous {
			continue
		}
		name := f.Tag.Get("db")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		key := strings.ToLower(name)
		if _, dup := fields[key]; dup {
			return nil, fmt.Errorf("store: duplicate struct field for column %q", name)
		}
		fields[key] = fieldPlan{index: f.Index}
	}

	plans := make([]*fieldPlan, len(columns))
	for i, column := range columns {
		if plan, ok := fields[strings.ToLower(column)]; ok {
			plan := plan
			plans[i] = &plan
		}
	}
	return plans, nil
}

// IsNotFound reports whether err means "no row matched".
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
