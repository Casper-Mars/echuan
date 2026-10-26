package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors shared by the application and HTTP layers.
//
// Use cases return one of the typed errors below; the HTTP layer is the only
// place that translates them into status codes and the unified error body.
var (
	// ErrInvalidArgument reports a request that failed validation.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrNotFound reports a resource that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict reports a request that conflicts with the current state.
	ErrConflict = errors.New("conflict")
)

// FieldError is one field-level validation failure. It is rendered as an item
// of the "details" array in the error body.
type FieldError struct {
	Field  string
	Reason string
}

// ValidationError carries every field that failed validation.
type ValidationError struct {
	Fields []FieldError
}

// Error implements error.
func (e *ValidationError) Error() string { return ErrInvalidArgument.Error() }

// Unwrap reports ErrInvalidArgument so errors.Is works for callers that only
// care about the class of failure.
func (e *ValidationError) Unwrap() error { return ErrInvalidArgument }

// ConflictError describes a state conflict such as a duplicate product_id or
// deleting a product that still owns projects.
type ConflictError struct {
	Reason string
}

// Error implements error.
func (e *ConflictError) Error() string { return e.Reason }

// Unwrap reports ErrConflict.
func (e *ConflictError) Unwrap() error { return ErrConflict }

// NotFoundError names the resource (and optionally its key) that was missing.
type NotFoundError struct {
	Resource string
	Key      string
}

// Error implements error.
func (e *NotFoundError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s not found", e.Resource)
	}
	return fmt.Sprintf("%s %q not found", e.Resource, e.Key)
}

// Unwrap reports ErrNotFound.
func (e *NotFoundError) Unwrap() error { return ErrNotFound }
