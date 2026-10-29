package api

import (
	"net/http"
	"strings"

	"github.com/Casper-Mars/echuan/internal/app"
)

type specDocumentResponse struct {
	SpecID    string                    `json:"spec_id"`
	ProjectID string                    `json:"project_id"`
	Title     string                    `json:"title"`
	Version   int                       `json:"version"`
	Material  materialVersionResponse   `json:"material"`
	BodyMD    string                    `json:"body_md,omitempty"`
	Versions  []materialVersionResponse `json:"versions,omitempty"`
}

func toSpecDocumentResponse(d app.SpecDocument) specDocumentResponse {
	r := specDocumentResponse{SpecID: d.ID, ProjectID: d.ProjectID, Title: d.Current.Title, Version: d.Current.Version, Material: toMaterialVersionResponse(d.Current, nil), BodyMD: d.BodyMD}
	if d.PreviousMaterialID != "" {
		r.Material.PreviousMaterialID = &d.PreviousMaterialID
	}
	for _, v := range d.Versions {
		var previous *string
		if v.PreviousMaterialID != "" {
			p := v.PreviousMaterialID
			previous = &p
		}
		r.Versions = append(r.Versions, toMaterialVersionResponse(v.Material, previous))
		if v.Material.MaterialID == d.Current.MaterialID {
			r.Material = toMaterialVersionResponse(v.Material, previous)
		}
	}
	return r
}

func (h *projectsHandler) listOrGetSpec(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id != "" {
		d, err := h.service.GetSpec(r.Context(), id, r.URL.Query().Get("material_id"))
		if err != nil {
			writeDomainError(w, r, h.logger, err)
			return
		}
		writeJSON(w, http.StatusOK, toSpecDocumentResponse(d))
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("project_id", "is required"))
		return
	}
	docs, err := h.service.ListSpecs(r.Context(), projectID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	items := make([]specDocumentResponse, 0, len(docs))
	for _, d := range docs {
		items = append(items, toSpecDocumentResponse(d))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []specDocumentResponse `json:"items"`
	}{items})
}

func (h *projectsHandler) saveSpecDocument(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID string  `json:"project_id"`
		Title     *string `json:"title"`
		BodyMD    string  `json:"body_md"`
	}
	if err := decodeJSONBodyLimit(w, r, &req, h.bodyLimit()); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	if req.Title == nil {
		writeDomainError(w, r, h.logger, invalidField("title", "is required"))
		return
	}
	id := ""
	expected := 0
	if r.Method == http.MethodPatch {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeDomainError(w, r, h.logger, invalidField("id", "is required"))
			return
		}
		var err error
		expected, err = ifMatchVersion(r)
		if err != nil {
			writeDomainError(w, r, h.logger, err)
			return
		}
		if req.ProjectID != "" {
			writeDomainError(w, r, h.logger, invalidField("project_id", "is not allowed"))
			return
		}
	} else if strings.TrimSpace(req.ProjectID) == "" {
		writeDomainError(w, r, h.logger, invalidField("project_id", "is required"))
		return
	}
	WithSubject(r.Context(), id)
	_, saved, err := h.service.SaveSpec(r.Context(), req.ProjectID, id, ActorFromContext(r.Context()), req.Title, req.BodyMD, expected, false)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	d, err := h.service.GetSpec(r.Context(), saved, "")
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, toSpecDocumentResponse(d))
}
