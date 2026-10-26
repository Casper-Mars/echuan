package api

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/Casper-Mars/echuan/internal/platform"
)

// Middleware wraps a handler with an additional behaviour.
type Middleware func(http.Handler) http.Handler

// DefaultRequestTimeout bounds the handling time of a single request.
const DefaultRequestTimeout = 30 * time.Second

// Chain applies middleware so that mw[0] is the outermost layer:
// Recover → RequestID → AccessLog → Timeout → handler.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// Recover converts panics into a 500 response and logs the stack trace.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is a deliberate abort signal; let the
				// server handle it as usual.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.Error("panic recovered",
					"request_id", RequestIDFromContext(r.Context()),
					"actor", actorOrUnknown(r),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
				)
				writeError(w, r, http.StatusInternalServerError, "internal", "internal server error", nil)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestID ensures every request carries an X-Request-Id: an inbound value is
// reused, otherwise a new one is generated. The id is echoed in the response
// header and stored in the request context.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strings.TrimSpace(r.Header.Get(HeaderRequestID))
			if id == "" {
				id = platform.NewRequestID()
			}
			w.Header().Set(HeaderRequestID, id)
			// Keep the request header in sync so that middleware outside this
			// layer (Recover) can report the id as well.
			r.Header.Set(HeaderRequestID, id)

			ctx := context.WithValue(r.Context(), requestIDKey, id)
			if actor := strings.TrimSpace(r.Header.Get(HeaderActor)); actor != "" {
				ctx = context.WithValue(ctx, actorKey, actor)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AccessLog emits one structured log line per request with the fields defined
// in tech design §7.4.
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w}
			start := time.Now()

			slot := &subjectSlot{}
			r = r.WithContext(context.WithValue(r.Context(), subjectSlotKey, slot))

			next.ServeHTTP(rec, r)

			attrs := []any{
				"request_id", RequestIDFromContext(r.Context()),
				"actor", actorOrUnknown(r),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.Status(),
				"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
			}
			if subject := slot.get(); subject != "" {
				attrs = append(attrs, "subject", subject)
			}
			logger.Info("request", attrs...)
		})
	}
}

// Timeout bounds the request lifetime. When the handler exceeds d the client
// receives a 503 with the unified error body and any late writes are dropped.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			req := r.WithContext(ctx)
			tw := &timeoutWriter{w: w, req: req, hdr: make(http.Header)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() {
					// The handler runs in its own goroutine, so a panic here
					// would otherwise crash the process instead of reaching
					// the Recover middleware (which sits on the calling
					// goroutine). Convert it into the unified 500 body.
					if rec := recover(); rec != nil {
						tw.fail(http.StatusInternalServerError, "internal", "internal server error")
					}
				}()
				next.ServeHTTP(tw, req)
			}()

			select {
			case <-done:
			case <-ctx.Done():
				tw.fail(http.StatusServiceUnavailable, "timeout", "request timed out")
			}
		})
	}
}

// timeoutWriter buffers the response headers of the handler goroutine until it
// commits them, so a timeout response never races with the handler's own
// header writes. Writes that happen after the request timed out are dropped.
type timeoutWriter struct {
	w        http.ResponseWriter
	req      *http.Request
	hdr      http.Header
	mu       sync.Mutex
	timedOut bool
	wrote    bool
}

func (t *timeoutWriter) Header() http.Header { return t.hdr }

// commit copies the buffered headers onto the real writer.
func (t *timeoutWriter) commit() {
	dst := t.w.Header()
	for k, vv := range t.hdr {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func (t *timeoutWriter) WriteHeader(status int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut || t.wrote {
		return
	}
	t.wrote = true
	t.commit()
	t.w.WriteHeader(status)
}

func (t *timeoutWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !t.wrote {
		t.wrote = true
		t.commit()
	}
	return t.w.Write(b)
}

// fail commits a terminal response and blocks any later handler writes.
func (t *timeoutWriter) fail(status int, code, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timedOut {
		return
	}
	t.timedOut = true
	if t.wrote {
		// The handler already committed a response; nothing left to do.
		return
	}
	writeError(t.w, t.req, status, code, message, nil)
}

func actorOrUnknown(r *http.Request) string {
	if actor := ActorFromContext(r.Context()); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(r.Header.Get(HeaderActor)); actor != "" {
		return actor
	}
	return "unknown"
}
