package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// submitSpecRequest is the POST /v1/projects/specs body.
//
// Title is a pointer on purpose: an absent title falls back to the project
// title, while a present-but-blank one is a 400 (see app.SubmitSpec). The
// decoder rejects every other member, so typos cannot be silently ignored.
type submitSpecRequest struct {
	Title  *string `json:"title"`
	BodyMD string  `json:"body_md"`
}

// specSubmitResponse is the wire form of one spec submission: the project state
// after the pointer move plus the metadata of the version just stored. The spec
// block is a chain link, so previous_material_id is null for v1.
type specSubmitResponse struct {
	ProjectID      string                  `json:"project_id"`
	Status         int                     `json:"status"`
	StatusName     string                  `json:"status_name"`
	ProjectVersion int                     `json:"project_version"`
	Spec           materialVersionResponse `json:"spec"`
}

// submitSpec handles POST /v1/projects/specs?id=APP-1.
func (h *projectsHandler) submitSpec(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	var req submitSpecRequest
	if verr := decodeJSONBodyLimit(w, r, &req, h.bodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.SubmitSpec(r.Context(), projectID, ActorFromContext(r.Context()), req.Title, req.BodyMD)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logSpecSubmission(r, result)
	writeJSON(w, http.StatusOK, toSpecSubmitResponse(result))
}

// logSpecSubmission records one submission with the fields §7 asks for:
// subject, version, material id and size.
func (h *projectsHandler) logSpecSubmission(r *http.Request, result app.SpecSubmission) {
	h.logger.Info("spec submitted",
		"request_id", RequestIDFromContext(r.Context()),
		"subject", result.Project.ProjectID,
		"spec_version", result.Spec.Version,
		"material_id", result.Spec.MaterialID,
		"size", result.Spec.Size,
		"project_version", result.Project.Version,
		"actor", result.Spec.UploadedBy,
	)
}

// toSpecSubmitResponse maps the submission to the wire DTO.
func toSpecSubmitResponse(result app.SpecSubmission) specSubmitResponse {
	var previous *string
	if result.PreviousMaterialID != "" {
		value := result.PreviousMaterialID
		previous = &value
	}
	return specSubmitResponse{
		ProjectID:      result.Project.ProjectID,
		Status:         result.Project.Status,
		StatusName:     domain.StatusName(result.Project.Status),
		ProjectVersion: result.Project.Version,
		Spec:           toMaterialVersionResponse(result.Spec, previous),
	}
}

// toMaterialVersionResponse maps one chain link to the wire DTO.
func toMaterialVersionResponse(m domain.Material, previous *string) materialVersionResponse {
	return materialVersionResponse{
		MaterialID:         m.MaterialID,
		Kind:               m.Kind,
		Version:            m.Version,
		Title:              m.Title,
		SHA256:             m.SHA256,
		Size:               m.Size,
		UploadedBy:         m.UploadedBy,
		PreviousMaterialID: previous,
		CreateTime:         m.CreateTime.UTC().Format(time.RFC3339Nano),
	}
}
