package api

import (
	"errors"
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
//
// products backs the read-only repo_url derivation of a project response; it is
// optional (a router built without the product service answers repo_url=null).
type projectsHandler struct {
	service  *app.ProjectService
	products *app.ProductService
	logger   *slog.Logger
}

func newProjectsHandler(service *app.ProjectService, products *app.ProductService, logger *slog.Logger) *projectsHandler {
	return &projectsHandler{service: service, products: products, logger: logger}
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
	DevBranch *string `json:"dev_branch"`
	Status    *int    `json:"status"`
	ProductID *string `json:"product_id"`
}

// writeRequirementRequest is the POST /v1/projects/requirement body.
type writeRequirementRequest struct {
	BodyMD string `json:"body_md"`
}

// decideProjectRequest is the POST /v1/projects/decide body.
type decideProjectRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// projectResponse is the wire representation of a project.
//
// DevBranch and RepoURL are read-only derivations, not columns: DevBranch is the
// recorded branch only for the statuses that can own one (null before the tasks
// draft and for a cancelled project), and RepoURL is the product's git_url
// (null when the product has none). Neither is writable through this DTO.
type projectResponse struct {
	ID         string  `json:"id"`
	ProjectID  string  `json:"project_id"`
	ProductID  string  `json:"product_id"`
	SeqNo      int     `json:"seq_no"`
	Title      string  `json:"title"`
	DevBranch  *string `json:"dev_branch"`
	RepoURL    *string `json:"repo_url"`
	Status     int     `json:"status"`
	StatusName string  `json:"status_name"`
	Reason     *string `json:"reason"`
	LastActor  *string `json:"last_actor"`
	Version    int     `json:"version"`
	// TasksReviewReason and TasksReviewRejectedAt are the plan-batch
	// objection: both are null unless the last review was a reject.
	TasksReviewReason     *string          `json:"tasks_review_reason"`
	TasksReviewRejectedAt *string          `json:"tasks_review_rejected_at"`
	WorkItemTotal         int64            `json:"work_item_total"`
	WorkItemDone          int64            `json:"work_item_done"`
	WorkItemByState       map[string]int64 `json:"work_item_by_state,omitempty"`
	CreateTime            string           `json:"create_time"`
	UpdateTime            string           `json:"update_time"`
}

// materialResponse is the metadata view of one material version; the bytes are
// served by GET /v1/materials.
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

// projectDetailResponse adds the requirement/spec metadata and the per-status
// task histogram to the list representation. Bodies are not embedded: clients
// fetch them from GET /v1/materials using the material_id each version carries.
type projectDetailResponse struct {
	projectResponse
	Requirement *materialResponse `json:"requirement"`
	Spec        *materialResponse `json:"spec"`
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
	writeJSON(w, http.StatusCreated, h.projectDetailResponse(r, detail))
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
	writeJSON(w, http.StatusOK, h.projectDetailResponse(r, detail))
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
	// One resolver per response: N projects of one product cost one read.
	urls := h.repoURLs()
	for _, project := range page.Items {
		items = append(items, h.projectResponse(r, project, urls))
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
		DevBranch: req.DevBranch,
		Status:    req.Status,
		ProductID: req.ProductID,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, h.projectResponse(r, project, h.repoURLs()))
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
	writeJSON(w, http.StatusOK, h.projectDetailResponse(r, detail))
}

// bodyLimit sizes the request body reader: one material plus JSON syntax.
func (h *projectsHandler) bodyLimit() int64 {
	return h.service.MaxMaterialBytes() + extraBodyBytes
}

// submitProject handles POST /v1/projects/submit?id=APP-1: the draft enters
// its review gate (requirement_draft → requirement_review, or spec_draft →
// spec_review).
func (h *projectsHandler) submitProject(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	result, err := h.service.SubmitProject(r.Context(), projectID, ActorFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	h.logProjectTransition(r, domain.ProjectActionSubmit, "", result)
	writeJSON(w, http.StatusOK, h.projectResponse(r, *result.Project, h.repoURLs()))
}

// decideProject handles POST /v1/projects/decide?id=APP-1.
func (h *projectsHandler) decideProject(w http.ResponseWriter, r *http.Request) {
	if forbidAgentActor(w, r) {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	version, err := ifMatchVersion(r)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	var req decideProjectRequest
	if verr := decodeJSONBody(w, r, &req); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	decision, ok := domain.ParseDecision(strings.TrimSpace(req.Decision))
	if !ok {
		writeDomainError(w, r, h.logger, invalidField("decision", "must be approve or reject"))
		return
	}

	result, err := h.service.Decide(r.Context(), app.DecideInput{
		Kind:          app.DecideKindProject,
		ID:            projectID,
		Decision:      decision,
		Reason:        req.Reason,
		ExpectVersion: &version,
		Actor:         ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	h.logProjectTransition(r, domain.ProjectActionDecide, decision, result)
	writeJSON(w, http.StatusOK, projectDecisionResponse{
		projectResponse: h.projectResponse(r, *result.Project, h.repoURLs()),
		MaterialID:      materialRefID(result.Material),
	})
}

// cancelProject handles POST /v1/projects/cancel?id=APP-1. Cancelling is a
// final action, so no If-Match is required; the state machine still rejects
// terminal projects with 409 invalid_state.
func (h *projectsHandler) cancelProject(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	result, err := h.service.CancelProject(r.Context(), projectID, ActorFromContext(r.Context()), "")
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	h.logProjectTransition(r, domain.ProjectActionCancel, "", result)
	writeJSON(w, http.StatusOK, h.projectResponse(r, *result.Project, h.repoURLs()))
}

// completeProject handles POST /v1/projects/complete?id=APP-1: the only way a
// pending_done project becomes completed.
//
// The precondition set is the whole authorization story of the edge — a human
// actor (agents are refused), a project in pending_done with no unsettled card,
// and an If-Match version that still matches. Everything is re-checked inside
// the write transaction, so a concurrent card cannot slip between the check and
// the status write.
func (h *projectsHandler) completeProject(w http.ResponseWriter, r *http.Request) {
	if forbidAgentActor(w, r) {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	version, verr := ifMatchVersion(r)
	if verr != nil {
		// The version lives in a header here, so a missing or malformed one is
		// reported as a version error (400 invalid_version) instead of a body
		// field error (tech design §7.1).
		writePreconditionError(w, r, verr)
		return
	}

	result, err := h.service.CompleteProject(r.Context(), projectID, ActorFromContext(r.Context()), version)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	h.logProjectTransition(r, domain.ProjectActionComplete, "", result)
	writeJSON(w, http.StatusOK, h.projectResponse(r, *result.Project, h.repoURLs()))
}

// writePreconditionError renders a rejected If-Match header as 400
// invalid_version, keeping the offending field in details so the client can tell
// "header missing" from "header malformed".
func writePreconditionError(w http.ResponseWriter, r *http.Request, err error) {
	details := []ErrorDetail{}
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		for _, field := range validation.Fields {
			details = append(details, ErrorDetail{Field: field.Field, Reason: field.Reason})
		}
	}
	writeError(w, r, http.StatusBadRequest, "invalid_version",
		"If-Match must carry the current project version", details)
}

// logProjectTransition records one decision with the fields required by §7 of
// the tech design: subject, decision, from_status, to_status and version.
func (h *projectsHandler) logProjectTransition(r *http.Request, action string, decision domain.Decision, result app.DecideResult) {
	if result.Project == nil {
		return
	}
	attrs := []any{
		"request_id", RequestIDFromContext(r.Context()),
		"subject", result.Project.ProjectID,
		"action", action,
		"from_status", domain.StatusName(result.FromStatus),
		"to_status", domain.StatusName(result.ToStatus),
		"version", result.Project.Version,
	}
	if decision != "" {
		attrs = append(attrs, "decision", string(decision))
	}
	h.logger.Info("project decision", attrs...)
}

// ifMatchVersion parses the If-Match header as a positive integer version.
// Both a missing and a malformed header are 400 invalid_argument; only a
// well-formed version that no longer matches is 409 invalid_version.
func ifMatchVersion(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, invalidField("If-Match", "is required")
	}
	// Accept the quoted ETag form as well as the bare integer.
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	version, err := strconv.Atoi(raw)
	if err != nil || version < 1 {
		return 0, invalidField("If-Match", "must be a positive integer version")
	}
	return version, nil
}

// projectResponse maps one domain entity to the list DTO.
//
// The two read-only derivations are resolved here rather than in the store:
// dev_branch is a projection of the recorded column that depends on the status,
// and repo_url belongs to the owning product (the resolver memoises it).
func (h *projectsHandler) projectResponse(r *http.Request, p domain.Project, urls *repoURLResolver) projectResponse {
	var reason *string
	if p.Reason != "" {
		value := p.Reason
		reason = &value
	}
	return projectResponse{
		ID:                    p.ProjectID,
		ProjectID:             p.ProjectID,
		ProductID:             p.ProductID,
		SeqNo:                 p.SeqNo,
		Title:                 p.Title,
		DevBranch:             projectDevBranch(p),
		RepoURL:               urls.url(r, p.ProductID),
		Status:                p.Status,
		StatusName:            domain.StatusName(p.Status),
		Reason:                reason,
		LastActor:             nullableString(p.LastActor),
		Version:               p.Version,
		TasksReviewReason:     nullableString(p.TasksReviewReason),
		TasksReviewRejectedAt: nullableString(p.TasksReviewRejectedAt),
		WorkItemTotal:         p.WorkItemStats.Total,
		WorkItemDone:          p.WorkItemStats.Done,
		WorkItemByState:       p.WorkItemStats.ByState,
		CreateTime:            p.CreateTime.UTC().Format(time.RFC3339Nano),
		UpdateTime:            p.UpdateTime.UTC().Format(time.RFC3339Nano),
	}
}

// projectDevBranch projects projects.dev_branch onto the wire value: the
// recorded name for the statuses that can own a feature branch (tasks_draft 4
// through completed 7), null for 0–3 (the branch does not exist yet) and for
// cancelled 8 (the platform has no evidence a branch was ever created, and
// guessing one would show a name nobody can verify).
func projectDevBranch(p domain.Project) *string {
	switch p.Status {
	case domain.StatusTasksDraft, domain.StatusTasksReview,
		domain.StatusPendingDone, domain.StatusCompleted:
		return nullableString(p.DevBranch)
	}
	return nil
}

// repoURLResolver resolves the product git_url backing one project response.
//
// It is created per response and caches by product id, so listing N projects of
// the same product costs one read. A product that cannot be read (or has no
// git_url) yields null instead of failing the project response: repo_url is
// auxiliary information (tech design §7.2).
type repoURLResolver struct {
	products *app.ProductService
	logger   *slog.Logger
	cache    map[string]*string
}

// repoURLs returns a resolver bound to this handler's dependencies.
func (h *projectsHandler) repoURLs() *repoURLResolver {
	return &repoURLResolver{products: h.products, logger: h.logger, cache: map[string]*string{}}
}

// url returns the product's git_url, or nil when there is none to show.
func (u *repoURLResolver) url(r *http.Request, productID string) *string {
	if u.products == nil || productID == "" {
		return nil
	}
	if cached, ok := u.cache[productID]; ok {
		return cached
	}
	resolved := (*string)(nil)
	product, err := u.products.GetProduct(r.Context(), productID)
	switch {
	case err != nil:
		if u.logger != nil {
			u.logger.Warn("resolve project repo_url",
				"request_id", RequestIDFromContext(r.Context()),
				"product_id", productID,
				"error", err.Error(),
			)
		}
	case product.GitURL != "":
		value := product.GitURL
		resolved = &value
	}
	u.cache[productID] = resolved
	return resolved
}

// nullableString renders an empty domain string as JSON null.
func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
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

// projectDetailResponse maps the detail aggregate to the wire DTO.
func (h *projectsHandler) projectDetailResponse(r *http.Request, detail domain.ProjectDetail) projectDetailResponse {
	response := projectDetailResponse{
		projectResponse: h.projectResponse(r, detail.Project, h.repoURLs()),
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
