// Package api contains the HTTP layer: routing, middleware, encoding and the
// unified error contract.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// HeaderRequestID is the header carrying the request correlation id.
const HeaderRequestID = "X-Request-Id"

// HeaderActor is the header declaring the caller identity (§4.1). It is a
// declaration, not an authentication mechanism.
const HeaderActor = "X-Actor"

type ctxKey int

const (
	requestIDKey ctxKey = iota
	actorKey
	subjectKey
	subjectSlotKey
)

// subjectSlot lets a handler publish the business subject of the current
// request to the surrounding access-log middleware. The request context is
// immutable, so the slot pointer is the only way the outer middleware can
// observe a value set inside the handler; the mutex also makes the read safe
// when the handler goroutine outlives a timed-out request.
type subjectSlot struct {
	mu    sync.RWMutex
	value string
}

func (s *subjectSlot) set(value string) {
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
}

func (s *subjectSlot) get() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// RequestIDFromContext returns the request id attached by the RequestID
// middleware, or "" when absent.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// ActorFromContext returns the identity declared through X-Actor, or "".
func ActorFromContext(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey).(string)
	return actor
}

// SubjectFromContext returns the business subject associated with the
// request (for products, the product_id). Handlers set it through WithSubject;
// the access log reads the same value through its middleware slot.
func SubjectFromContext(ctx context.Context) string {
	subject, _ := ctx.Value(subjectKey).(string)
	return subject
}

// WithSubject returns a context carrying subject for access logging. When the
// context was enriched by AccessLog, the value is also published to the log
// middleware's slot.
func WithSubject(ctx context.Context, subject string) context.Context {
	if subject == "" {
		return ctx
	}
	if slot, ok := ctx.Value(subjectSlotKey).(*subjectSlot); ok {
		slot.set(subject)
	}
	return context.WithValue(ctx, subjectKey, subject)
}

// ErrorDetail is one item of the "details" array. Field-level validation
// failures always name the offending field so clients can highlight inputs.
type ErrorDetail struct {
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// ErrorBody is the unified error response body (§4.1).
type ErrorBody struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []ErrorDetail `json:"details"`
	RequestID string        `json:"request_id"`
}

// writeJSON encodes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		// Only reachable for programmer errors (unencodable types). Keep the
		// response well-formed instead of emitting a half-written body.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"internal","message":"response encoding failed","details":[],"request_id":""}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// writeError writes the unified error body and logs it when a logger is
// available on the request.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details []ErrorDetail) {
	if headerWritten(w) {
		return
	}
	if details == nil {
		details = []ErrorDetail{}
	}
	// The request id lives in the context for handlers, but outer middleware
	// (Recover) may hold a request created before RequestID ran, so fall back
	// to the header that RequestID also sets on the request.
	requestID := RequestIDFromContext(r.Context())
	if requestID == "" {
		requestID = r.Header.Get(HeaderRequestID)
	}
	writeJSON(w, status, ErrorBody{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: requestID,
	})
}

// writeDomainError is the single place where domain errors become HTTP status
// codes and the unified error body. Handlers must not map status codes
// themselves.
func writeDomainError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	var validation *domain.ValidationError
	var notFound *domain.NotFoundError
	var conflict *domain.ConflictError
	var invalidState *domain.InvalidStateError
	var invalidVersion *domain.InvalidVersionError
	var tooLarge *domain.TooLargeError

	switch {
	case errors.As(err, &invalidVersion):
		// A conditional write lost the race: the message carries the current
		// version so the client can re-read and retry without a probe call.
		writeError(w, r, http.StatusConflict, "invalid_version", invalidVersion.Error(), nil)
	case errors.As(err, &tooLarge):
		// Both the HTTP body cap and the material writer land here, so the 413
		// body and the warning are identical whichever layer noticed the size.
		if logger != nil {
			logger.Warn("request body too large",
				"request_id", RequestIDFromContext(r.Context()),
				"actor", actorOrUnknown(r),
				"method", r.Method,
				"path", r.URL.Path,
				"field", tooLarge.Field,
				"limit_bytes", tooLarge.Limit,
				"actual_bytes", tooLarge.Actual,
			)
		}
		writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "request body is too large",
			[]ErrorDetail{{Field: tooLarge.Field, Reason: fmt.Sprintf("exceeds %d bytes", tooLarge.Limit)}})
	case errors.As(err, &validation):
		details := make([]ErrorDetail, 0, len(validation.Fields))
		for _, field := range validation.Fields {
			details = append(details, ErrorDetail{Field: field.Field, Reason: field.Reason})
		}
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "invalid argument", details)
	case errors.As(err, &notFound):
		writeError(w, r, http.StatusNotFound, "not_found", notFound.Error(), nil)
	case errors.As(err, &conflict):
		writeError(w, r, http.StatusConflict, "conflict", conflict.Error(), nil)
	case errors.As(err, &invalidState):
		// Same status as a conflict, distinct code: the caller must re-read
		// the state before deciding whether the request is retryable (§4.7).
		writeError(w, r, http.StatusConflict, "invalid_state", invalidState.Error(), nil)
	default:
		logger.Error("request failed",
			"request_id", RequestIDFromContext(r.Context()),
			"actor", actorOrUnknown(r),
			"method", r.Method,
			"path", r.URL.Path,
			"error", err.Error(),
		)
		writeError(w, r, http.StatusInternalServerError, "internal", "internal server error", nil)
	}
}

// statusRecorder captures the response status for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.wroteHeader {
		return
	}
	s.status = status
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

// HeaderWritten reports whether the response header was already sent.
func (s *statusRecorder) HeaderWritten() bool { return s.wroteHeader }

// Status returns the recorded status, defaulting to 200.
func (s *statusRecorder) Status() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

// headerWritten reports whether the response was already committed.
func headerWritten(w http.ResponseWriter) bool {
	type headerWriter interface{ HeaderWritten() bool }
	if hw, ok := w.(headerWriter); ok {
		return hw.HeaderWritten()
	}
	return false
}
