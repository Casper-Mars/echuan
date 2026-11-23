package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// materialsHandler serves the two material read endpoints:
//
//	GET /v1/materials?id=<material_id>              one object as a stream
//	GET /v1/projects/materials?id=<project_id>&kind=  one version chain
type materialsHandler struct {
	service *app.MaterialService
	logger  *slog.Logger
}

func newMaterialsHandler(service *app.MaterialService, logger *slog.Logger) *materialsHandler {
	return &materialsHandler{service: service, logger: logger}
}

// materialVersionResponse is one link of a chain. PreviousMaterialID is
// rendered as null for the first version, matching the wire contract.
func (h *materialsHandler) download(w http.ResponseWriter, r *http.Request) {
	materialID := strings.TrimSpace(r.URL.Query().Get("id"))
	if materialID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), materialID)

	dl, err := h.service.Download(r.Context(), materialID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	defer func() {
		if cerr := dl.Content.Close(); cerr != nil {
			h.logger.Warn("close material object",
				"request_id", RequestIDFromContext(r.Context()),
				"subject", dl.Material.MaterialID,
				"error", cerr.Error(),
			)
		}
	}()

	w.Header().Set("Content-Type", materialContentType(dl.Material))
	w.Header().Set("Content-Length", strconv.FormatInt(dl.Material.Size, 10))
	w.Header().Set("ETag", `"`+dl.Material.SHA256+`"`)
	// Content addressing: the bytes behind an id never change, so clients may
	// cache a material for a year without revalidating.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Material-Id", dl.Material.MaterialID)
	w.Header().Set("X-Material-Version", strconv.Itoa(dl.Material.Version))
	w.Header().Set("Content-Disposition", contentDisposition(dl.Material))

	// ServeContent streams the file, answers HEAD without a body and supports
	// Range requests. A zero modtime keeps the response headers limited to the
	// ones the contract names; the ETag carries the version identity anyway.
	http.ServeContent(w, r, "", time.Time{}, dl.Content)
}

// chain handles GET /v1/projects/materials?id=<project_id>&kind=<1|2>.
// the helper keeps the decision in one place for future binary attachments.
func materialContentType(domain.Material) string {
	return "text/markdown; charset=utf-8"
}

// contentDisposition renders an inline disposition whose file name is the
// material title.
//
// Titles are user input, so the ASCII file name replaces quotes, backslashes,
// separators and control characters; when the title is not ASCII (Chinese
// titles are the norm here) the ASCII fallback is material-<id>.md and the
// original title travels in filename*, which is where RFC 6266 puts non-ASCII.
func contentDisposition(m domain.Material) string {
	fallback := strings.Trim(strings.TrimSpace(headerSafeFilename(m.Title)), "-.")
	if fallback == "" {
		// A fully non-ASCII title (Chinese titles are the norm here) leaves
		// nothing usable for the quoted form; the id is a stable stand-in and
		// filename* still carries the real title.
		fallback = "material-" + m.MaterialID
	}
	return fmt.Sprintf(`inline; filename="%s.md"; filename*=UTF-8''%s.md`,
		fallback, url.PathEscape(m.Title))
}

// headerSafeFilename keeps printable ASCII, replacing everything that would
// break the quoted-string syntax of a header value.
func headerSafeFilename(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == '"' || r == '\\' || r == '/' || r < 0x20 || r == 0x7f:
			b.WriteByte('-')
		case r > 0x7e:
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
