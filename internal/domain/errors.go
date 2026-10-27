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
	// ErrInvalidState reports a request that the resource's current status does
	// not accept (tech design §4.7: 409 invalid_state). Task-06 reuses it for
	// the remaining project state transitions.
	ErrInvalidState = errors.New("invalid state")
	// ErrInvalidVersion reports a conditional write whose If-Match version no
	// longer matches the persisted one (tech design §4.7: 409 invalid_version).
	// It is distinct from ErrConflict so the client can tell "re-read and
	// retry" apart from a duplicate-key style conflict.
	ErrInvalidVersion = errors.New("invalid version")
	// ErrTooLarge reports a body above a configured limit (tech design §4.7:
	// 413 too_large). Both the HTTP body reader and the material writer raise
	// it, and both must produce the same response.
	ErrTooLarge = errors.New("too large")
	// ErrLeaseLost reports a heartbeat (or, from Task-11 on, a report) that
	// no longer owns the task: the lease expired, was reclaimed, or the lease
	// id belongs to another worker (tech design §4.4: 409 lease_lost).
	ErrLeaseLost = errors.New("lease lost")
	// ErrMaterialRequired reports a report submission whose content is blank
	// (tech design §4.6 / §4.7: 422 material_required). It is the project's
	// first 422: the request is syntactically valid but carries no artifact.
	ErrMaterialRequired = errors.New("material required")
	// ErrWorkerUnknown reports a heartbeat that named a worker id the registry
	// does not know (tech design v0.6 §4.1: 404 worker_unknown). A worker id is
	// only ever minted by a first registration, so an unknown id means the row
	// was pruned or the client is holding a stale id.
	ErrWorkerUnknown = errors.New("worker unknown")
	// ErrWorkerBusy reports a dispatch that would give one worker a second card
	// or one card a second worker (tech design v0.6 §4.3: 409 worker_busy). The
	// database constraints behind it (ux_workers_current_task and the explicit
	// pre-check in store.BindTask) are the authority, not a read-then-write.
	ErrWorkerBusy = errors.New("worker busy")
)

// FieldError is one field-level validation failure. It is rendered as an item
// of the "details" array in the error body.
type FieldError struct {
	Field  string
	Reason string
}

// ValidationError carries every field that failed validation.
//
// Message is an optional human-readable summary of a cross-field failure that
// cannot be expressed as a single field (a dependency cycle, for example).
// When empty the HTTP layer renders the generic "invalid argument" message;
// when set it becomes body.message, so the caller still sees the diagnostic
// while details keeps pointing at the offending field.
type ValidationError struct {
	Fields  []FieldError
	Message string
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

// InvalidStateError reports an action rejected by the current status, as
// opposed to a ConflictError which reports a remaining child object. Both map
// to HTTP 409, but they carry different codes: invalid_state tells the caller
// to re-read the state before deciding whether the request is retryable.
type InvalidStateError struct {
	Reason string
}

// Error implements error.
func (e *InvalidStateError) Error() string { return e.Reason }

// Unwrap reports ErrInvalidState.
func (e *InvalidStateError) Unwrap() error { return ErrInvalidState }

// InvalidVersionError reports a conditional write whose expected version no
// longer matches the persisted row. Current is the version the client must
// read back before retrying.
type InvalidVersionError struct {
	Current  int
	Expected int
}

// Error implements error. The current version is part of the message so the
// client can retry without a second round trip.
func (e *InvalidVersionError) Error() string {
	return fmt.Sprintf("version mismatch: expected %d, current version is %d", e.Expected, e.Current)
}

// Unwrap reports ErrInvalidVersion.
func (e *InvalidVersionError) Unwrap() error { return ErrInvalidVersion }

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

// TooLargeError reports a body that exceeded a limit. Field names the request
// member that carried it ("body" when the request body reader hit its cap,
// "body_md" when the material writer aborted). Actual is 0 when the size is not
// known, as is the case for the HTTP body cap.
type TooLargeError struct {
	Field  string
	Limit  int64
	Actual int64
}

// Error implements error.
func (e *TooLargeError) Error() string {
	if e.Actual > 0 {
		return fmt.Sprintf("%s exceeds %d bytes (got %d bytes)", e.Field, e.Limit, e.Actual)
	}
	return fmt.Sprintf("%s exceeds %d bytes", e.Field, e.Limit)
}

// Unwrap reports ErrTooLarge.
func (e *TooLargeError) Unwrap() error { return ErrTooLarge }

// LeaseLostError reports that the caller no longer holds the lease of an
// in-progress task. Reason names which condition failed so a worker can tell
// "my lease expired" from "another worker owns this card" without guessing.
type LeaseLostError struct {
	TaskID string
	Reason string
}

// Error implements error.
func (e *LeaseLostError) Error() string {
	return fmt.Sprintf("lease for task %q is lost: %s", e.TaskID, e.Reason)
}

// Unwrap reports ErrLeaseLost so errors.Is works for callers that only care
// about the class of failure.
func (e *LeaseLostError) Unwrap() error { return ErrLeaseLost }

// MaterialRequiredError reports a request that parsed correctly but supplied
// no usable content for a material. Field names the missing member and Reason
// explains what was wrong with it; the HTTP layer renders both as details.
type MaterialRequiredError struct {
	Field  string
	Reason string
}

// Error implements error.
func (e *MaterialRequiredError) Error() string {
	if e.Field == "" {
		return ErrMaterialRequired.Error()
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// Unwrap reports ErrMaterialRequired so errors.Is works for callers that only
// care about the class of failure.
func (e *MaterialRequiredError) Unwrap() error { return ErrMaterialRequired }

// WorkerUnknownError names the worker id the registry could not find.
type WorkerUnknownError struct {
	WorkerID string
}

// Error implements error.
func (e *WorkerUnknownError) Error() string {
	return fmt.Sprintf("worker %q is unknown", e.WorkerID)
}

// Unwrap reports ErrWorkerUnknown so errors.Is works for callers that only care
// about the class of failure.
func (e *WorkerUnknownError) Unwrap() error { return ErrWorkerUnknown }

// WorkerBusyError reports a dispatch that could not be bound: the worker is
// already holding another card, or the chosen card is already owned by another
// worker. TaskID may be empty when only the worker side is known.
type WorkerBusyError struct {
	WorkerID string
	TaskID   string
	Reason   string
}

// Error implements error.
func (e *WorkerBusyError) Error() string {
	switch {
	case e.Reason != "":
		return fmt.Sprintf("worker %q is busy: %s", e.WorkerID, e.Reason)
	case e.TaskID != "":
		return fmt.Sprintf("worker %q is busy with task %q", e.WorkerID, e.TaskID)
	default:
		return fmt.Sprintf("worker %q is busy", e.WorkerID)
	}
}

// Unwrap reports ErrWorkerBusy so errors.Is works for callers that only care
// about the class of failure.
func (e *WorkerBusyError) Unwrap() error { return ErrWorkerBusy }
