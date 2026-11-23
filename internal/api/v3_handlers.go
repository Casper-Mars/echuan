package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/platform"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

type v3Handler struct {
	projects  *appv3.ProjectService
	tasks     *appv3.Service
	workers   *appv3.WorkerService
	materials *app.MaterialService
	logger    *slog.Logger
}

func newV3Handler(services V3Services, logger *slog.Logger) *v3Handler {
	return &v3Handler{
		projects:  services.Projects,
		tasks:     services.Tasks,
		workers:   services.Workers,
		materials: services.Materials,
		logger:    logger,
	}
}

// ── responses ───────────────────────────────────────────────────────────

type v3ProjectResponse struct {
	ProjectID  string             `json:"project_id"`
	ProductID  string             `json:"product_id"`
	SeqNo      int                `json:"seq_no"`
	Name       string             `json:"name"`
	ContextMD  string             `json:"context_md"`
	Status     enum.ProjectStatus `json:"status"`
	CreateTime string             `json:"create_time"`
	UpdateTime string             `json:"update_time"`
	ClosedAt   *string            `json:"closed_at,omitempty"`
}

type v3ProjectListResponse struct {
	Items       []v3ProjectResponse `json:"items"`
	NextAfterID int64               `json:"next_after_id"`
}

func v3ProjectDTO(p storev3.Project) v3ProjectResponse {
	return v3ProjectResponse{
		ProjectID:  p.ProjectID,
		ProductID:  p.ProductCode,
		SeqNo:      p.SeqNo,
		Name:       p.Name,
		ContextMD:  p.ContextMD,
		Status:     p.Status,
		CreateTime: p.CreateTime,
		UpdateTime: p.UpdateTime,
		ClosedAt:   p.ClosedAt,
	}
}

type v3TaskResponse struct {
	TaskID        string          `json:"task_id"`
	ProjectID     string          `json:"project_id"`
	ParentTaskID  *string         `json:"parent_task_id,omitempty"`
	Kind          enum.TaskKind   `json:"kind"`
	Title         string          `json:"title"`
	DescriptionMD string          `json:"description_md"`
	ContextMD     string          `json:"context_md"`
	Priority      int             `json:"priority"`
	Status        enum.TaskStatus `json:"status"`
	Attempt       int             `json:"attempt"`
	MaxAttempts   int             `json:"max_attempts"`
	LeaseID       *string         `json:"lease_id,omitempty"`
	LeaseUntil    *string         `json:"lease_until,omitempty"`
	WorkerID      *string         `json:"worker_id,omitempty"`
	Reason        *string         `json:"reason,omitempty"`
	CreateTime    string          `json:"create_time"`
	UpdateTime    string          `json:"update_time"`
}

func v3TaskDTO(t storev3.Task) v3TaskResponse {
	return v3TaskResponse{
		TaskID:        t.TaskID,
		ProjectID:     t.ProjectCode,
		ParentTaskID:  t.ParentCode,
		Kind:          t.Kind,
		Title:         t.Title,
		DescriptionMD: t.DescriptionMD,
		ContextMD:     t.ContextMD,
		Priority:      t.Priority,
		Status:        t.Status,
		Attempt:       t.Attempt,
		MaxAttempts:   t.MaxAttempts,
		LeaseID:       t.LeaseID,
		LeaseUntil:    t.LeaseUntil,
		WorkerID:      t.WorkerID,
		Reason:        t.Reason,
		CreateTime:    t.CreateTime,
		UpdateTime:    t.UpdateTime,
	}
}

type v3TaskListResponse struct {
	Items []v3TaskResponse `json:"items"`
}

type v3WorkerResponse struct {
	WorkerID string           `json:"worker_id"`
	Kinds    []string         `json:"kinds"`
	State    enum.WorkerState `json:"state"`
	// CurrentTaskID 是数据库自增行 id（历史字段，保留给潜在消费者）。
	CurrentTaskID *int64 `json:"current_task_id,omitempty"`
	// CurrentWorkItemID 是任务业务标识（`t_...`），与旧版 /v1/workers、
	// 任务响应与 claim 信封同一口径；空闲节点为 null（不用 omitempty，
	// 前端契约是 `string | null`，null 与缺省要分得开）。
	CurrentWorkItemID *string `json:"current_work_item_id"`
	CurrentLeaseID    *string `json:"current_lease_id,omitempty"`
	LastSeen          string  `json:"last_seen"`
}

type v3WorkerListResponse struct {
	Items []v3WorkerResponse `json:"items"`
}

type v3EventResponse struct {
	ID         int64      `json:"id"`
	Event      enum.Event `json:"event"`
	Actor      *string    `json:"actor,omitempty"`
	DetailJSON string     `json:"detail_json"`
	CreateTime string     `json:"create_time"`
}

type v3EventListResponse struct {
	Items []v3EventResponse `json:"items"`
}

type v3DecisionResponse struct {
	ID         int64         `json:"id"`
	TaskID     string        `json:"task_id"`
	Gate       enum.TaskKind `json:"gate"`
	MaterialID *int64        `json:"material_id,omitempty"`
	Decision   enum.Decision `json:"decision"`
	DecidedBy  string        `json:"decided_by"`
	Reason     *string       `json:"reason,omitempty"`
	CreateTime string        `json:"create_time"`
}

type v3DecisionListResponse struct {
	Items []v3DecisionResponse `json:"items"`
}

// ── errors ──────────────────────────────────────────────────────────────

func (h *v3Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if appv3.IsNoWork(err) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	status := appv3.StatusOf(err)
	code := "internal"
	switch status {
	case http.StatusBadRequest:
		code = "invalid_argument"
	case http.StatusNotFound:
		code = "not_found"
	case http.StatusConflict:
		code = "conflict"
	}
	if status == http.StatusInternalServerError && h.logger != nil {
		h.logger.Error("v3 request failed",
			"request_id", RequestIDFromContext(r.Context()),
			"method", r.Method, "path", r.URL.Path, "error", err)
	}
	writeError(w, r, status, code, err.Error(), nil)
}

func invalidEnum(w http.ResponseWriter, r *http.Request, field, raw string) {
	writeError(w, r, http.StatusBadRequest, "invalid_argument",
		fmt.Sprintf("invalid %s", field),
		[]ErrorDetail{{Field: field, Reason: fmt.Sprintf("unknown value %q", raw)}})
}

// ── projects ────────────────────────────────────────────────────────────

type v3CreateProjectRequest struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	ContextMD string `json:"context_md"`
}

func (h *v3Handler) createProject(w http.ResponseWriter, r *http.Request) {
	var body v3CreateProjectRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	project, err := h.projects.CreateProject(r.Context(), appv3.CreateProjectRequest{
		ProductID: body.ProductID,
		Name:      body.Name,
		ContextMD: body.ContextMD,
		Actor:     ActorFromContext(r.Context()),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/projects?id="+project.ProjectID)
	writeJSON(w, http.StatusCreated, v3ProjectDTO(project))
}

func (h *v3Handler) listOrGetProject(w http.ResponseWriter, r *http.Request) {
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		project, err := h.projects.GetProject(r.Context(), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v3ProjectDTO(project))
		return
	}

	var status *enum.ProjectStatus
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		parsed, err := enum.ParseProjectStatus(raw)
		if err != nil {
			invalidEnum(w, r, "status", raw)
			return
		}
		status = &parsed
	}
	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	projects, err := h.projects.ListProjects(r.Context(), appv3.ProjectFilter{
		Status: status, AfterID: afterID, Limit: limit,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]v3ProjectResponse, 0, len(projects))
	for _, project := range projects {
		items = append(items, v3ProjectDTO(project))
	}
	var next int64
	if len(projects) > 0 {
		next = projects[len(projects)-1].ID
	}
	writeJSON(w, http.StatusOK, v3ProjectListResponse{Items: items, NextAfterID: next})
}

type v3UpdateProjectRequest struct {
	Name      string `json:"name"`
	ContextMD string `json:"context_md"`
}

func (h *v3Handler) updateProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required",
			[]ErrorDetail{{Field: "id", Reason: "is required"}})
		return
	}
	var body v3UpdateProjectRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	project, err := h.projects.UpdateProject(r.Context(), appv3.UpdateProjectRequest{
		ProjectID: id, Name: body.Name, ContextMD: body.ContextMD,
		Actor: ActorFromContext(r.Context()),
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3ProjectDTO(project))
}

func (h *v3Handler) closeProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required",
			[]ErrorDetail{{Field: "id", Reason: "is required"}})
		return
	}
	force := r.URL.Query().Get("force")
	if err := h.projects.CloseProject(r.Context(), id, force == "1" || force == "true", ActorFromContext(r.Context())); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *v3Handler) reopenProject(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required",
			[]ErrorDetail{{Field: "id", Reason: "is required"}})
		return
	}
	if err := h.projects.ReopenProject(r.Context(), id, ActorFromContext(r.Context())); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── tasks ───────────────────────────────────────────────────────────────

type createTaskRequest struct {
	ProjectID     string   `json:"project_id"`
	TaskID        string   `json:"task_id"`
	ParentTaskID  string   `json:"parent_task_id"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	DescriptionMD string   `json:"description_md"`
	ContextMD     string   `json:"context_md"`
	Priority      int      `json:"priority"`
	MaxAttempts   int      `json:"max_attempts"`
	DependsOn     []string `json:"depends_on"`
}

func (h *v3Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var body createTaskRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	kind, err := enum.ParseTaskKind(body.Kind)
	if err != nil {
		invalidEnum(w, r, "kind", body.Kind)
		return
	}
	taskID := strings.TrimSpace(body.TaskID)
	if taskID == "" {
		ulid, err := platform.NewULID(time.Now())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		taskID = "t_" + ulid
	}
	task, err := h.tasks.CreateTask(r.Context(), appv3.CreateTaskRequest{
		ProjectID:     body.ProjectID,
		TaskID:        taskID,
		ParentTaskID:  body.ParentTaskID,
		Kind:          kind,
		Title:         body.Title,
		DescriptionMD: body.DescriptionMD,
		ContextMD:     body.ContextMD,
		Priority:      body.Priority,
		MaxAttempts:   body.MaxAttempts,
		DependsOn:     body.DependsOn,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/tasks?id="+task.TaskID)
	writeJSON(w, http.StatusCreated, v3TaskDTO(task))
}

func (h *v3Handler) listOrGetTask(w http.ResponseWriter, r *http.Request) {
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		task, err := h.tasks.GetTask(r.Context(), id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v3TaskDTO(task))
		return
	}

	filter := storev3.TaskFilter{ProjectID: strings.TrimSpace(r.URL.Query().Get("project_id"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("kind")); raw != "" {
		kind, err := enum.ParseTaskKind(raw)
		if err != nil {
			invalidEnum(w, r, "kind", raw)
			return
		}
		filter.Kind = &kind
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		status, err := enum.ParseTaskStatus(raw)
		if err != nil {
			invalidEnum(w, r, "status", raw)
			return
		}
		filter.Status = &status
	}
	filter.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))

	tasks, err := h.tasks.ListTasks(r.Context(), filter)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]v3TaskResponse, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, v3TaskDTO(task))
	}
	writeJSON(w, http.StatusOK, v3TaskListResponse{Items: items})
}

type updateTaskRequest struct {
	Title         string `json:"title"`
	DescriptionMD string `json:"description_md"`
	ContextMD     string `json:"context_md"`
	Priority      int    `json:"priority"`
}

func (h *v3Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required",
			[]ErrorDetail{{Field: "id", Reason: "is required"}})
		return
	}
	var body updateTaskRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	task, err := h.tasks.UpdateTask(r.Context(), appv3.UpdateTaskRequest{
		TaskID: id, Title: body.Title, DescriptionMD: body.DescriptionMD,
		ContextMD: body.ContextMD, Priority: body.Priority,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3TaskDTO(task))
}

func (h *v3Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required",
			[]ErrorDetail{{Field: "id", Reason: "is required"}})
		return
	}
	if err := h.tasks.DeleteTask(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type v3AttachRequest struct {
	ProjectID  string `json:"project_id"`
	TaskID     string `json:"task_id"`
	MaterialID string `json:"material_id"`
	Role       string `json:"role"`
	SortNo     int    `json:"sort_no"`
}

func (h *v3Handler) attach(w http.ResponseWriter, r *http.Request) {
	var body v3AttachRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	role, err := enum.ParseAttachmentRole(body.Role)
	if err != nil {
		invalidEnum(w, r, "role", body.Role)
		return
	}
	attachment, err := h.tasks.Attach(r.Context(), appv3.AttachRequest{
		ProjectID: body.ProjectID, TaskID: body.TaskID,
		MaterialID: body.MaterialID, Role: role, SortNo: body.SortNo,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, attachment)
}

type v3ClaimResponse struct {
	Task             v3TaskResponse    `json:"task"`
	Project          v3ClaimProject    `json:"project"`
	Product          v3ClaimProduct    `json:"product"`
	Inputs           []v3ClaimInput    `json:"inputs"`
	Upstream         []v3ClaimUpstream `json:"upstream"`
	HasDownstreamDev bool              `json:"has_downstream_dev"`
}

type v3ClaimProject struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	ContextMD string `json:"context_md"`
}

type v3ClaimProduct struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	GitURL    string `json:"git_url"`
}

type v3ClaimInput struct {
	MaterialID string `json:"material_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Role       string `json:"role"`
}

type v3ClaimUpstream struct {
	TaskID     string  `json:"task_id"`
	MaterialID *string `json:"material_id,omitempty"`
	Title      *string `json:"title,omitempty"`
}

type v3ClaimRequest struct {
	WorkerID string `json:"worker_id"`
	Kind     string `json:"kind"`
}

func (h *v3Handler) claim(w http.ResponseWriter, r *http.Request) {
	var body v3ClaimRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	kind, err := enum.ParseTaskKind(body.Kind)
	if err != nil {
		invalidEnum(w, r, "kind", body.Kind)
		return
	}
	task, err := h.tasks.Claim(r.Context(), body.WorkerID, kind)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	envelope, err := h.tasks.ClaimEnvelope(r.Context(), task)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	inputs := make([]v3ClaimInput, 0, len(envelope.Inputs))
	for _, input := range envelope.Inputs {
		inputs = append(inputs, v3ClaimInput{
			MaterialID: input.MaterialID, Title: input.Title, Kind: input.Kind.String(), Role: input.Role.String(),
		})
	}
	writeJSON(w, http.StatusOK, v3ClaimResponse{
		Task: v3TaskDTO(task),
		Project: v3ClaimProject{
			ProjectID: envelope.Project.ProjectID, Name: envelope.Project.Name, ContextMD: envelope.Project.ContextMD,
		},
		Product: v3ClaimProduct{
			ProductID: envelope.Product.ProductID, Name: envelope.Product.Name, GitURL: envelope.Product.GitURL,
		},
		Inputs: inputs, Upstream: toClaimUpstream(envelope.Upstream), HasDownstreamDev: envelope.HasDownstreamDev,
	})
}

type v3LeaseRequest struct {
	TaskID  string `json:"task_id"`
	LeaseID string `json:"lease_id"`
}

func (h *v3Handler) renewLease(w http.ResponseWriter, r *http.Request) {
	var body v3LeaseRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	until, err := h.tasks.Renew(r.Context(), body.TaskID, body.LeaseID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"task_id": body.TaskID, "lease_id": body.LeaseID, "lease_until": until,
	})
}

type v3ReportRequest struct {
	TaskID     string `json:"task_id"`
	LeaseID    string `json:"lease_id"`
	MaterialID string `json:"material_id"`
}

func (h *v3Handler) report(w http.ResponseWriter, r *http.Request) {
	var body v3ReportRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	task, err := h.tasks.Report(r.Context(), body.TaskID, body.LeaseID, body.MaterialID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3TaskDTO(task))
}

type v3DecideRequest struct {
	TaskID     string `json:"task_id"`
	Decision   string `json:"decision"`
	MaterialID string `json:"material_id"`
	DecidedBy  string `json:"decided_by"`
	Reason     string `json:"reason"`
}

func (h *v3Handler) decide(w http.ResponseWriter, r *http.Request) {
	var body v3DecideRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	decision, err := enum.ParseDecision(body.Decision)
	if err != nil {
		invalidEnum(w, r, "decision", body.Decision)
		return
	}
	task, err := h.tasks.Decide(r.Context(), appv3.DecideRequest{
		TaskID: body.TaskID, Decision: decision, MaterialID: body.MaterialID,
		DecidedBy: body.DecidedBy, Reason: body.Reason,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3TaskDTO(task))
}

type v3ResetRequest struct {
	TaskID string `json:"task_id"`
}

func (h *v3Handler) reset(w http.ResponseWriter, r *http.Request) {
	var body v3ResetRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	task, err := h.tasks.Reset(r.Context(), body.TaskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3TaskDTO(task))
}

func (h *v3Handler) events(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "task_id is required",
			[]ErrorDetail{{Field: "task_id", Reason: "is required"}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.tasks.ListEvents(r.Context(), taskID, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]v3EventResponse, 0, len(events))
	for _, event := range events {
		items = append(items, v3EventResponse{
			ID: event.ID, Event: event.Event, Actor: event.Actor,
			DetailJSON: event.DetailJSON, CreateTime: event.CreateTime,
		})
	}
	writeJSON(w, http.StatusOK, v3EventListResponse{Items: items})
}

func (h *v3Handler) decisions(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "task_id is required",
			[]ErrorDetail{{Field: "task_id", Reason: "is required"}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	decisions, err := h.tasks.ListDecisions(r.Context(), taskID, limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]v3DecisionResponse, 0, len(decisions))
	for _, decision := range decisions {
		items = append(items, v3DecisionResponse{
			ID: decision.ID, TaskID: decision.TaskID, Gate: decision.Gate,
			MaterialID: decision.MaterialID, Decision: decision.Decision,
			DecidedBy: decision.DecidedBy, Reason: decision.Reason, CreateTime: decision.CreateTime,
		})
	}
	writeJSON(w, http.StatusOK, v3DecisionListResponse{Items: items})
}

type v3HeartbeatRequest struct {
	WorkerID string          `json:"worker_id"`
	State    string          `json:"state"`
	Metadata json.RawMessage `json:"metadata"`
	Kinds    []string        `json:"kinds"`
}

func (h *v3Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body v3HeartbeatRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	kinds := make([]enum.TaskKind, 0, len(body.Kinds))
	for _, raw := range body.Kinds {
		kind, err := enum.ParseTaskKind(raw)
		if err != nil {
			invalidEnum(w, r, "kinds", raw)
			return
		}
		kinds = append(kinds, kind)
	}
	metadata := "{}"
	if len(body.Metadata) > 0 {
		metadata = string(body.Metadata)
	}
	if err := h.workers.Heartbeat(r.Context(), body.WorkerID, metadata, kinds); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *v3Handler) listWorkers(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	workers, err := h.workers.List(r.Context(), limit)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]v3WorkerResponse, 0, len(workers))
	for _, worker := range workers {
		kinds, _ := h.workers.Kinds(r.Context(), worker.WorkerID)
		kindNames := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			kindNames = append(kindNames, kind.String())
		}
		// 占用口径是行 id（workers.current_task_id），对外契约要业务标识 `t_...`（issue #89 C1/E4）。
		// 映射已经并进 ListWorkers 的 LEFT JOIN（一条 SQL 取完整页），这里不再逐节点查任务，
		// 否则 100 个忙碌节点就是 100 次串行 SELECT（review：N+1）。
		items = append(items, v3WorkerResponse{
			WorkerID: worker.WorkerID, Kinds: kindNames, State: worker.State,
			CurrentTaskID: worker.CurrentTaskID, CurrentWorkItemID: h.workers.CurrentWorkItemID(worker),
			CurrentLeaseID: worker.CurrentLeaseID,
			LastSeen:       worker.LastSeen,
		})
	}
	writeJSON(w, http.StatusOK, v3WorkerListResponse{Items: items})
}

func (h *v3Handler) inbox(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	ready := enum.Ready
	inReview := enum.InReview
	tasks, err := h.tasks.ListTasks(r.Context(), storev3.TaskFilter{Status: &ready, Limit: 100})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	review, err := h.tasks.ListTasks(ctx, storev3.TaskFilter{Status: &inReview, Limit: 100})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	tasks = append(tasks, review...)
	items := make([]v3TaskResponse, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, v3TaskDTO(task))
	}
	writeJSON(w, http.StatusOK, v3TaskListResponse{Items: items})
}

type v3CreateMaterialRequest struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	BodyMD string `json:"body_md"`
}

type v3MaterialResponse struct {
	MaterialID string `json:"material_id"`
	Kind       string `json:"kind"`
	Version    int    `json:"version"`
	Title      string `json:"title"`
}

// createMaterial stores an inline markdown artifact as a material. Reports need
// a material id, and a worker should not have to invent one.
func (h *v3Handler) createMaterial(w http.ResponseWriter, r *http.Request) {
	var body v3CreateMaterialRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	kind, err := enum.ParseMaterialKind(body.Kind)
	if err != nil {
		invalidEnum(w, r, "kind", body.Kind)
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "title is required",
			[]ErrorDetail{{Field: "title", Reason: "is required"}})
		return
	}
	if body.BodyMD == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "body_md is required",
			[]ErrorDetail{{Field: "body_md", Reason: "is required"}})
		return
	}

	row, err := h.materials.CreateMaterial(r.Context(), int(kind), body.Title, []byte(body.BodyMD), ActorFromContext(r.Context()))
	if err != nil {
		switch {
		case errors.Is(err, material.ErrTooLarge):
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "material is too large", nil)
		case errors.Is(err, material.ErrEmpty):
			writeError(w, r, http.StatusBadRequest, "invalid_argument", "material body is empty", nil)
		default:
			h.fail(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, v3MaterialResponse{
		MaterialID: row.MaterialID,
		Kind:       kind.String(),
		Version:    row.Version,
		Title:      row.Title,
	})
}

func toClaimUpstream(values []storev3.UpstreamRef) []v3ClaimUpstream {
	out := make([]v3ClaimUpstream, 0, len(values))
	for _, value := range values {
		out = append(out, v3ClaimUpstream{TaskID: value.TaskID, MaterialID: value.MaterialID, Title: value.Title})
	}
	return out
}

type v3DependencyList struct {
	Items []string `json:"items"`
}

func (h *v3Handler) dependencies(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "task_id is required",
			[]ErrorDetail{{Field: "task_id", Reason: "is required"}})
		return
	}
	deps, err := h.tasks.Dependencies(r.Context(), taskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3DependencyList{Items: deps})
}

type v3AttachmentList struct {
	Items []v3ClaimInput `json:"items"`
}

func (h *v3Handler) attachments(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	if taskID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "task_id is required",
			[]ErrorDetail{{Field: "task_id", Reason: "is required"}})
		return
	}
	refs, err := h.tasks.Attachments(r.Context(), taskID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3AttachmentList{Items: attachmentItems(refs)})
}

// projectAttachments lists the project-level attachments (requirement, brief,
// context). role is optional: input / output.
func (h *v3Handler) projectAttachments(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_argument", "id is required",
			[]ErrorDetail{{Field: "id", Reason: "is required"}})
		return
	}
	var role *enum.AttachmentRole
	if raw := strings.TrimSpace(r.URL.Query().Get("role")); raw != "" {
		parsed, err := enum.ParseAttachmentRole(raw)
		if err != nil {
			invalidEnum(w, r, "role", raw)
			return
		}
		role = &parsed
	}
	refs, err := h.tasks.ProjectAttachments(r.Context(), projectID, role)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v3AttachmentList{Items: attachmentItems(refs)})
}

// attachmentItems maps material refs onto the shared attachment DTO.
func attachmentItems(refs []storev3.MaterialRef) []v3ClaimInput {
	items := make([]v3ClaimInput, 0, len(refs))
	for _, ref := range refs {
		items = append(items, v3ClaimInput{
			MaterialID: ref.MaterialID, Title: ref.Title, Kind: ref.Kind.String(), Role: ref.Role.String(),
		})
	}
	return items
}
