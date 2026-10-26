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
type materialVersionResponse struct {
	MaterialID         string  `json:"material_id"`
	Kind               int     `json:"kind"`
	Version            int     `json:"version"`
	Title              string  `json:"title"`
	SHA256             string  `json:"sha256"`
	Size               int64   `json:"size"`
	UploadedBy         string  `json:"uploaded_by"`
	PreviousMaterialID *string `json:"previous_material_id"`
	CreateTime         string  `json:"create_time"`
}

// materialChainResponse is the version-chain envelope. current_material_id is
// omitted when the project has no material of the requested kind.
type materialChainResponse struct {
	Items             []materialVersionResponse `json:"items"`
	CurrentMaterialID *string                   `json:"current_material_id,omitempty"`
	Count             int                       `json:"count"`
}

// download handles GET/HEAD /v1/materials?id=<material_id>.
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
func (h *materialsHandler) chain(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	projectID := strings.TrimSpace(query.Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	kind, verr := parseMaterialKind(query.Get("kind"))
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	chain, err := h.service.Chain(r.Context(), projectID, kind)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]materialVersionResponse, 0, len(chain.Versions))
	for _, version := range chain.Versions {
		var previous *string
		if version.PreviousMaterialID != "" {
			value := version.PreviousMaterialID
			previous = &value
		}
		items = append(items, materialVersionResponse{
			MaterialID:         version.Material.MaterialID,
			Kind:               version.Material.Kind,
			Version:            version.Material.Version,
			Title:              version.Material.Title,
			SHA256:             version.Material.SHA256,
			Size:               version.Material.Size,
			UploadedBy:         version.Material.UploadedBy,
			PreviousMaterialID: previous,
			CreateTime:         version.Material.CreateTime.UTC().Format(time.RFC3339Nano),
		})
	}

	response := materialChainResponse{Items: items, Count: len(items)}
	if chain.CurrentMaterialID != "" {
		current := chain.CurrentMaterialID
		response.CurrentMaterialID = &current
	}
	writeJSON(w, http.StatusOK, response)
}

// methodNotAllowed answers every method the two material routes do not serve.
// The mux would answer 405 on its own, but with a plain-text body; routing it
// here keeps the unified error envelope on every response.
func (h *materialsHandler) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

// parseMaterialKind validates ?kind=. Only requirement (1) and spec (2) have a
// chain endpoint today; a report chain belongs to a task, not a project.
func parseMaterialKind(raw string) (int, *domain.ValidationError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, invalidField("kind", "is required and must be 1 (requirement) or 2 (spec)")
	}
	kind, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalidField("kind", "must be 1 (requirement) or 2 (spec)")
	}
	switch kind {
	case domain.MaterialKindRequirement, domain.MaterialKindSpec:
		return kind, nil
	}
	return 0, invalidField("kind", "must be 1 (requirement) or 2 (spec)")
}

// materialContentType returns the media type of a stored object. Every material
// written today is markdown (requirement, spec, report), so the type is fixed;
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
