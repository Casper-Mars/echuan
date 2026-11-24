// Package v3 is the V3 application layer: it validates requests, maps
// persistence errors to HTTP-ready statuses, and composes the store calls that
// make up one use case.
package v3

import (
	"errors"
	"fmt"

	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// Error is an application error with an HTTP status attached. The API layer
// only has to read Status and Message; the persistence layer never knows about
// HTTP.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// StatusOf returns the HTTP status carried by err, or 500 when err is not an
// application error.
func StatusOf(err error) int {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	var noWork *NoWorkError
	if errors.As(err, &noWork) {
		return 204
	}
	return 500
}

func invalid(format string, args ...any) *Error {
	return &Error{Status: 400, Message: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) *Error {
	return &Error{Status: 404, Message: fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) *Error {
	return &Error{Status: 409, Message: fmt.Sprintf(format, args...)}
}

// workerNotRegistered is the single message both claim checks answer with: the
// pre-check (before taking a card) and the transaction's own guard (a row that
// vanished between the two). The worker_id is the recovery instruction — a
// client that never sent a heartbeat is one heartbeat away from claiming.
func workerNotRegistered(workerID string) *Error {
	return conflict("worker %q 未注册，请先发一次心跳", workerID)
}

// workerBusy is the answer to a claim from a worker that still holds a card.
// One worker, one lease (ux_tasks_worker_lease): the second claim must not
// surface as a 500 from the unique index (issue #165 D-1), and the recovery is
// report (or wait for the lease to expire), never a blind retry.
func workerBusy(workerID string) *Error {
	return conflict("worker %q 已持卡（busy），同一时间只能持有一张卡；请先上报交付或等租约过期后再 claim", workerID)
}

// NoWorkError is returned by Claim when the queue is empty. It carries 204 so
// the HTTP layer can answer "no content" without a body.
type NoWorkError struct{}

func (*NoWorkError) Error() string { return "no ready task" }

// IsNoWork reports whether err means "nothing to claim".
func IsNoWork(err error) bool {
	var target *NoWorkError
	return errors.As(err, &target)
}

// Translate maps a store/v3 sentinel to an application error. Unknown errors
// pass through unchanged and become 500 at the boundary.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, storev3.ErrNoWork):
		return &NoWorkError{}
	case errors.Is(err, storev3.ErrProjectNotFound), errors.Is(err, storev3.ErrProductNotFound),
		errors.Is(err, storev3.ErrTaskNotFound), errors.Is(err, storev3.ErrMaterialNotFound),
		errors.Is(err, storev3.ErrWorkerNotFound):
		return notFound("%s", err.Error())
	case errors.Is(err, storedb.ErrUniqueViolation):
		return conflict("%s", err.Error())
	case errors.Is(err, storev3.ErrProjectClosed),
		errors.Is(err, storev3.ErrProjectNotEmpty),
		errors.Is(err, storev3.ErrInvalidTransition),
		errors.Is(err, storev3.ErrLeaseLost),
		errors.Is(err, storev3.ErrTaskHasDependents),
		errors.Is(err, storev3.ErrInvalidAttachment):
		return conflict("%s", err.Error())
	default:
		return err
	}
}
