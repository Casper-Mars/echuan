// Package api contains the HTTP layer: routing, middleware, encoding and the
// unified error contract.
package api

import (
	"context"
	"encoding/json"
	"net/http"
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
)

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
// request. Handlers added by later tasks set it via WithSubject; until then it
// is empty and omitted from access logs.
func SubjectFromContext(ctx context.Context) string {
	subject, _ := ctx.Value(subjectKey).(string)
	return subject
}

// WithSubject returns a context carrying subject for access logging.
func WithSubject(ctx context.Context, subject string) context.Context {
	if subject == "" {
		return ctx
	}
	return context.WithValue(ctx, subjectKey, subject)
}

// ErrorBody is the unified error response body (§4.1).
type ErrorBody struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Details   []string `json:"details"`
	RequestID string   `json:"request_id"`
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
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details []string) {
	if headerWritten(w) {
		return
	}
	if details == nil {
		details = []string{}
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
