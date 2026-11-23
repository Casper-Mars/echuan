package api

import (
	"log/slog"
	"net/http"

	"github.com/Casper-Mars/echuan/internal/platform"
)

// Handler serves the health and version endpoints.
type Handler struct {
	logger *slog.Logger
	checks []HealthChecker
}

// NewHandler builds a Handler. checks are evaluated by /readyz in order.
func NewHandler(logger *slog.Logger, checks []HealthChecker) *Handler {
	return &Handler{logger: logger, checks: checks}
}

type versionResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
}

// version reports the build identity injected at link time.
func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{
		Version:   platform.Version,
		Commit:    platform.Commit,
		GoVersion: platform.GoVersion(),
	})
}
