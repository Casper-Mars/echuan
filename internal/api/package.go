package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// downloadPackage handles GET/HEAD /v1/projects/package?id=APP-1.
//
// The zip is built on demand and streamed from memory; nothing is stored, so
// repeated downloads always reflect the current requirement and spec versions.
// Content-Disposition carries an ASCII fallback plus the RFC 5987 filename*
// with the Chinese archive name.
func (h *projectsHandler) downloadPackage(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	pkg, err := h.service.ProjectPackage(r.Context(), projectID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.Itoa(len(pkg.Data)))
	w.Header().Set("Content-Disposition", packageContentDisposition(projectID))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(pkg.Data); err != nil {
		h.logger.Warn("write project package",
			"request_id", RequestIDFromContext(r.Context()),
			"subject", projectID,
			"error", err.Error(),
		)
		return
	}
	h.logger.Info("project package downloaded",
		"request_id", RequestIDFromContext(r.Context()),
		"subject", projectID,
		"entries", pkg.Entries,
		"bytes", len(pkg.Data),
	)
}

// packageContentDisposition renders the attachment header. The quoted form is
// pure ASCII (the project id), while the real Chinese file name travels in
// filename* where RFC 6266 puts non-ASCII.
func packageContentDisposition(projectID string) string {
	fallback := headerSafeFilename(projectID + "-package.zip")
	if fallback == "" {
		fallback = "package.zip"
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		fallback, url.PathEscape(projectID+"-资料包.zip"))
}
