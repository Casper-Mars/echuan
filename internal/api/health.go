package api

import (
	"context"
	"errors"
	"net/http"
)

// ErrPending marks a readiness aspect that is not implemented yet.
var ErrPending = errors.New("pending")

// HealthChecker is one named readiness aspect.
//
// Extension point for Task-02: replace PendingChecker with implementations
// that verify the SQLite database is writable and that the applied migration
// version matches the embedded one. Returning a non-ErrPending error makes
// /readyz report 503 so the process stops receiving traffic.
type HealthChecker interface {
	// Name is the key reported in the "checks" object.
	Name() string
	// Check reports nil when healthy, ErrPending when not yet available and
	// any other error when unhealthy.
	Check(ctx context.Context) error
}

// PendingChecker reports ErrPending for every check. It is the placeholder
// used until Task-02 wires the real database and migration probes.
type PendingChecker struct {
	name string
}

// NewPendingChecker returns a HealthChecker that always reports pending.
func NewPendingChecker(name string) HealthChecker {
	return PendingChecker{name: name}
}

// Name returns the check name.
func (c PendingChecker) Name() string { return c.name }

// Check always reports ErrPending.
func (c PendingChecker) Check(context.Context) error { return ErrPending }

type healthResponse struct {
	Status string `json:"status"`
}

type readinessResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// healthz reports process liveness without touching external dependencies.
func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// readyz reports readiness by running every registered checker.
func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string, len(h.checks))
	status := "ok"
	httpStatus := http.StatusOK

	for _, c := range h.checks {
		switch err := c.Check(r.Context()); {
		case err == nil:
			checks[c.Name()] = "ok"
		case errors.Is(err, ErrPending):
			checks[c.Name()] = "pending"
		default:
			checks[c.Name()] = "error"
			status = "unavailable"
			httpStatus = http.StatusServiceUnavailable
			h.logger.Error("readiness check failed",
				"request_id", RequestIDFromContext(r.Context()),
				"check", c.Name(),
				"error", err.Error(),
			)
		}
	}

	writeJSON(w, httpStatus, readinessResponse{Status: status, Checks: checks})
}
