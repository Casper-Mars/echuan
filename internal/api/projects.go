package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// defaultProjectListLimit is applied when ?limit= is absent.
const defaultProjectListLimit = 50

// extraBodyBytes leaves room for JSON syntax (field names, the title, escaping)
// on top of material_max_bytes, so a requirement just under the material limit
// is not rejected by the body reader first.
const extraBodyBytes = 1 << 20

// projectsHandler serves /v1/projects and /v1/projects/requirement.
type projectsHandler struct {
	service *app.ProjectService
	logger  *slog.Logger
}

func newProjectsHandler(service *app.ProjectService, logger *slog.Logger) *projectsHandler {
	return &projectsHandler{service: service, logger: logger}
}

// createProjectRequest is the POST /v1/projects body.
type createProjectRequest struct {
	ProductID     string `json:"product_id"`
	Title         string `json:"title"`
	RequirementMD string `json:"requirement_md"`
}

// updateProjectRequest is the PATCH body. Status and product_id are decoded so
// that attempting to change them yields a precise field error instead of a
// generic "unknown field"; every other member is rejected by the decoder.
type updateProjectRequest struct {
	Title     *string `json:"title"`
	Status    *int    `json:"status"`
	ProductID *string `json:"product_id"`
}

// writeRequirementRequest is the POST /v1/projects/requirement body.
type writeRequirementRequest struct {
	BodyMD string `json:"body_md"`
}

// taskStatsResponse summarises tasks; by_status is only filled in by the
// detail endpoint and is omitted when empty.
type taskStatsResponse struct {
	Total    int64            `json:"total"`
	Done     int64            `json:"done"`
	ByStatus map[string]int64 `json:"by_status,omitempty"`
}

// projectResponse is the wire representation of a project.
type projectResponse struct {
	ID         string            `json:"id"`
	ProjectID  string            `json:"project_id"`
	ProductID  string            `json:"product_id"`
	SeqNo      int               `json:"seq_no"`
	Title      string            `json:"title"`
	Status     int               `json:"status"`
	StatusName string            `json:"status_name"`
	Reason     *string           `json:"reason"`
	Version    int               `json:"version"`
	TaskStats  taskStatsResponse `json:"task_stats"`
	CreateTime string            `json:"create_time"`
	UpdateTime string            `json:"update_time"`
}

// materialResponse is the metadata view of one material version; the bytes are
// served by /v1/materials (Task-05).
type materialResponse struct {
	MaterialID string `json:"material_id"`
	Kind       int    `json:"kind"`
	Version    int    `json:"version"`
	Title      string `json:"title"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	UploadedBy string `json:"uploaded_by"`
	CreateTime string `json:"create_time"`
}

// projectDetailResponse adds the requirement body, the current spec and the
// per-status task histogram to the list representation.
type projectDetailResponse struct {
	projectResponse
	Requirement   *materialResponse `json:"requirement"`
	RequirementMD string            `json:"requirement_md"`
	Spec          *materialResponse `json:"spec"`
}

// projectListResponse is the list envelope; next_cursor is omitted on the last
// page.
type projectListResponse struct {
	Items      []projectResponse `json:"items"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

// createProject handles POST /v1/projects.
func (h *projectsHandler) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if verr := decodeJSONBodyLimit(w, r, &req, h.bodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.CreateProject(r.Context(), app.CreateProjectInput{
		ProductID:     req.ProductID,
		Title:         req.Title,
		RequirementMD: req.RequirementMD,
		UploadedBy:    ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), detail.Project.ProjectID)
	w.Header().Set("Location", "/v1/projects?id="+url.QueryEscape(detail.Project.ProjectID))
	writeJSON(w, http.StatusCreated, toProjectDetailResponse(detail))
}

// listOrGetProject handles GET /v1/projects: ?id= selects the detail form,
// otherwise the collection is listed.
func (h *projectsHandler) listOrGetProject(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("id") {
		h.getProject(w, r, query.Get("id"))
		return
	}
	h.listProjects(w, r)
}

// getProject handles GET /v1/projects?id=APP-1.
func (h *projectsHandler) getProject(w http.ResponseWriter, r *http.Request, projectID string) {
	if strings.TrimSpace(projectID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	detail, err := h.service.GetProject(r.Context(), projectID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectDetailResponse(detail))
}

// listProjects handles GET /v1/projects?product_id=&status=&limit=&cursor=.
func (h *projectsHandler) listProjects(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultProjectListLimit
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("limit", "must be an integer"))
			return
		}
		limit = parsed
	}

	var afterID int64
	if query.Has("cursor") {
		id, err := decodeCursor(query.Get("cursor"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("cursor", "is invalid"))
			return
		}
		afterID = id
	}

	var status *int
	if query.Has("status") {
		parsed, err := strconv.Atoi(query.Get("status"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("status", "must be an integer"))
			return
		}
		status = &parsed
	}

	page, err := h.service.ListProjects(r.Context(), query.Get("product_id"), status, afterID, limit)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]projectResponse, 0, len(page.Items))
	for _, project := range page.Items {
		items = append(items, toProjectResponse(project))
	}
	response := projectListResponse{Items: items}
	if page.NextID != 0 {
		next := encodeCursor(page.NextID)
		response.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, response)
}

// updateProject handles PATCH /v1/projects?id=APP-1.
func (h *projectsHandler) updateProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("id")
	if strings.TrimSpace(projectID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	var req updateProjectRequest
	if verr := decodeJSONBody(w, r, &req); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	project, err := h.service.UpdateProject(r.Context(), projectID, app.UpdateProjectInput{
		Title:     req.Title,
		Status:    req.Status,
		ProductID: req.ProductID,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectResponse(project))
}

// deleteProject handles DELETE /v1/projects?id=APP-1.
func (h *projectsHandler) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("id")
	if strings.TrimSpace(projectID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	if err := h.service.DeleteProject(r.Context(), projectID); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeRequirement handles POST /v1/projects/requirement?id=APP-1.
func (h *projectsHandler) writeRequirement(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("id")
	if strings.TrimSpace(projectID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	var req writeRequirementRequest
	if verr := decodeJSONBodyLimit(w, r, &req, h.bodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.WriteRequirement(r.Context(), projectID, app.WriteRequirementInput{
		BodyMD:     req.BodyMD,
		UploadedBy: ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toProjectDetailResponse(detail))
}

// bodyLimit sizes the request body reader: one material plus JSON syntax.
func (h *projectsHandler) bodyLimit() int64 {
	return h.service.MaxMaterialBytes() + extraBodyBytes
}

// toProjectResponse maps the domain entity to the list DTO.
func toProjectResponse(p domain.Project) projectResponse {
	var reason *string
	if p.Reason != "" {
		value := p.Reason
		reason = &value
	}
	return projectResponse{
		ID:         p.ProjectID,
		ProjectID:  p.ProjectID,
		ProductID:  p.ProductID,
		SeqNo:      p.SeqNo,
		Title:      p.Title,
		Status:     p.Status,
		StatusName: domain.StatusName(p.Status),
		Reason:     reason,
		Version:    p.Version,
		TaskStats: taskStatsResponse{
			Total:    p.TaskStats.Total,
			Done:     p.TaskStats.Done,
			ByStatus: statusHistogramResponse(p.TaskStats.ByStatus),
		},
		CreateTime: p.CreateTime.UTC().Format(time.RFC3339Nano),
		UpdateTime: p.UpdateTime.UTC().Format(time.RFC3339Nano),
	}
}

// toMaterialResponse maps one material version to the wire DTO.
func toMaterialResponse(m domain.Material) materialResponse {
	return materialResponse{
		MaterialID: m.MaterialID,
		Kind:       m.Kind,
		Version:    m.Version,
		Title:      m.Title,
		SHA256:     m.SHA256,
		Size:       m.Size,
		UploadedBy: m.UploadedBy,
		CreateTime: m.CreateTime.UTC().Format(time.RFC3339Nano),
	}
}

// toProjectDetailResponse maps the detail aggregate to the wire DTO.
func toProjectDetailResponse(detail domain.ProjectDetail) projectDetailResponse {
	response := projectDetailResponse{
		projectResponse: toProjectResponse(detail.Project),
		RequirementMD:   detail.RequirementMD,
	}
	if detail.Requirement != nil {
		requirement := toMaterialResponse(*detail.Requirement)
		response.Requirement = &requirement
	}
	if detail.Spec != nil {
		spec := toMaterialResponse(*detail.Spec)
		response.Spec = &spec
	}
	return response
}

// statusHistogramResponse renders the task histogram with string keys, the way
// JSON object keys must be shaped.
func statusHistogramResponse(histogram map[int]int64) map[string]int64 {
	if len(histogram) == 0 {
		return nil
	}
	out := make(map[string]int64, len(histogram))
	for status, count := range histogram {
		out[strconv.Itoa(status)] = count
	}
	return out
}
