package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// defaultTaskListLimit is applied when ?limit= is absent.
const defaultTaskListLimit = 50

// taskPlatformFields are the columns the platform maintains itself (tech design
// §4.3). A PATCH that carries any of them is rejected with 400
// invalid_argument and a detail naming the field — including status, which is
// intentionally a 400 and not a 409: the client must not "fix" it by retrying.
var taskPlatformFields = []string{
	"status",
	"task_id",
	"seq_no",
	"project_id",
	"attempt_count",
	"max_attempts",
	"lease_id",
	"lease_until",
	"report_material_id",
	"last_actor",
	"version",
	"create_time",
	"update_time",
	"reason",
}

// taskPlatformFieldSet is the lookup form of taskPlatformFields.
var taskPlatformFieldSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(taskPlatformFields))
	for _, field := range taskPlatformFields {
		set[field] = struct{}{}
	}
	return set
}()

// tasksHandler serves /v1/tasks.
type tasksHandler struct {
	service *app.TaskService
	logger  *slog.Logger
}

func newTasksHandler(service *app.TaskService, logger *slog.Logger) *tasksHandler {
	return &tasksHandler{service: service, logger: logger}
}

// createTaskRequest is the POST /v1/tasks body. depends_on carries business
// task ids of the same project, never sequence numbers.
type createTaskRequest struct {
	Title     string   `json:"title"`
	BodyMD    string   `json:"body_md"`
	Priority  int      `json:"priority"`
	DependsOn []string `json:"depends_on"`
}

// taskResponse is the wire representation of one task (§4.2).
//
// worker_id is always encoded (null when the card is not in progress): the
// client renders "who is executing" without a second lookup and a missing
// member would be indistinguishable from an older server.
type taskResponse struct {
	ID           string   `json:"id"`
	TaskID       string   `json:"task_id"`
	ProjectID    string   `json:"project_id"`
	SeqNo        int      `json:"seq_no"`
	Title        string   `json:"title"`
	BodyMD       string   `json:"body_md"`
	Priority     int      `json:"priority"`
	Status       int      `json:"status"`
	StatusName   string   `json:"status_name"`
	WorkerID     *string  `json:"worker_id"`
	AttemptCount int      `json:"attempt_count"`
	MaxAttempts  int      `json:"max_attempts"`
	Reason       *string  `json:"reason"`
	LastActor    *string  `json:"last_actor"`
	Version      int      `json:"version"`
	DependsOn    []string `json:"depends_on"`
	CreateTime   string   `json:"create_time"`
	UpdateTime   string   `json:"update_time"`
}

// taskLeaseResponse is the live lease of an in-progress task.
type taskLeaseResponse struct {
	LeaseID string `json:"lease_id"`
	Until   string `json:"until"`
}

// taskDetailResponse adds the current report and the live lease to the list
// representation. Both are null when absent, so the client can rely on the
// members always being present.
type taskDetailResponse struct {
	taskResponse
	Report *materialResponse  `json:"report"`
	Lease  *taskLeaseResponse `json:"lease"`
}

// taskListResponse is the list envelope; next_cursor is omitted on the last
// page.
type taskListResponse struct {
	Items      []taskResponse `json:"items"`
	NextCursor *string        `json:"next_cursor,omitempty"`
}

// createTask handles POST /v1/tasks?project_id=APP-1.
func (h *tasksHandler) createTask(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("project_id", "is required"))
		return
	}

	var req createTaskRequest
	if verr := decodeJSONBodyLimit(w, r, &req, taskBodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.CreateTask(r.Context(), app.CreateTaskInput{
		ProjectID: projectID,
		Title:     req.Title,
		BodyMD:    req.BodyMD,
		Priority:  req.Priority,
		DependsOn: req.DependsOn,
	}, ActorFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), detail.Task.ID)
	h.logger.Info("task created",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", detail.Task.ID,
		"project_id", detail.Task.ProjectID,
		"seq_no", detail.Task.SeqNo,
		"depends_on", len(detail.Task.DependsOn),
	)

	w.Header().Set("Location", "/v1/tasks?id="+url.QueryEscape(detail.Task.ID))
	writeJSON(w, http.StatusCreated, toTaskDetailResponse(detail))
}

// listOrGetTask handles GET /v1/tasks: ?id= selects the detail form, otherwise
// the board is listed.
func (h *tasksHandler) listOrGetTask(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("id") {
		h.getTask(w, r, query.Get("id"))
		return
	}
	h.listTasks(w, r)
}

// getTask handles GET /v1/tasks?id=APP-1-T0001.
func (h *tasksHandler) getTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if strings.TrimSpace(taskID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), taskID)

	detail, err := h.service.GetTask(r.Context(), taskID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toTaskDetailResponse(detail))
}

// listTasks handles GET /v1/tasks?project_id=&status=&limit=&cursor=.
func (h *tasksHandler) listTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultTaskListLimit
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("limit", "must be an integer"))
			return
		}
		limit = parsed
	}

	// An explicitly blank project_id is a client mistake, not "every
	// project": silently listing the whole board would look like a filter that
	// matched everything.
	if query.Has("project_id") && strings.TrimSpace(query.Get("project_id")) == "" {
		writeDomainError(w, r, h.logger, invalidField("project_id", "is required"))
		return
	}

	statuses, verr := parseTaskStatuses(query)
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	var after *app.TaskCursor
	if query.Has("cursor") {
		cursor, err := decodeTaskCursor(query.Get("cursor"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("cursor", "is invalid"))
			return
		}
		after = &cursor
	}

	page, err := h.service.ListTasks(r.Context(), app.ListTasksInput{
		ProjectID: query.Get("project_id"),
		Statuses:  statuses,
		After:     after,
		Limit:     limit,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]taskResponse, 0, len(page.Items))
	for _, task := range page.Items {
		items = append(items, toTaskResponse(task))
	}
	response := taskListResponse{Items: items}
	if page.Next != nil {
		next := encodeTaskCursor(*page.Next)
		response.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, response)
}

// updateTask handles PATCH /v1/tasks?id=APP-1-T0001.
func (h *tasksHandler) updateTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), taskID)

	input, verr := decodeTaskPatch(w, r)
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.UpdateTask(r.Context(), taskID, input, ActorFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logger.Info("task patched",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", detail.Task.ID,
		"version", detail.Task.Version,
	)
	writeJSON(w, http.StatusOK, toTaskDetailResponse(detail))
}

// deleteTask handles DELETE /v1/tasks?id=APP-1-T0001.
func (h *tasksHandler) deleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), taskID)

	if err := h.service.DeleteTask(r.Context(), taskID); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logger.Info("task deleted",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", taskID,
	)
	w.WriteHeader(http.StatusNoContent)
}

// decodeTaskPatch decodes a PATCH body member by member so that a
// platform-maintained column is named in details.field instead of being folded
// into a generic "unknown field".
//
// The body is read as a raw object rather than into a struct because the two
// failure classes must stay distinguishable: platform columns (400, "is
// maintained by the platform") and genuinely unknown members (400, "unknown
// field").
func decodeTaskPatch(w http.ResponseWriter, r *http.Request) (app.UpdateTaskInput, error) {
	const limit = maxRequestBodyBytes

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return app.UpdateTaskInput{}, bodyDecodeError(err, limit)
	}
	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return app.UpdateTaskInput{}, invalidField("body", "unexpected trailing data")
	}
	if raw == nil {
		// "null" (or an empty body decoded to nil) is not an object.
		return app.UpdateTaskInput{}, invalidField("body", "must be a JSON object")
	}
	if len(raw) == 0 {
		return app.UpdateTaskInput{}, invalidField("body",
			"at least one of title, body_md, priority is required")
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var (
		input  app.UpdateTaskInput
		fields []domain.FieldError
	)
	for _, key := range keys {
		value := raw[key]
		switch key {
		case "title":
			title, err := decodeOptionalString(value)
			if err != nil {
				fields = append(fields, domain.FieldError{Field: key, Reason: "has the wrong type"})
				continue
			}
			input.Title = title
		case "body_md":
			body, err := decodeOptionalString(value)
			if err != nil {
				fields = append(fields, domain.FieldError{Field: key, Reason: "has the wrong type"})
				continue
			}
			input.BodyMD = body
		case "priority":
			priority, err := decodeOptionalInt(value)
			if err != nil {
				fields = append(fields, domain.FieldError{Field: key, Reason: "has the wrong type"})
				continue
			}
			input.Priority = priority
		default:
			if _, platform := taskPlatformFieldSet[key]; platform {
				fields = append(fields, domain.FieldError{
					Field:  key,
					Reason: "is maintained by the platform and cannot be patched",
				})
				continue
			}
			fields = append(fields, domain.FieldError{Field: key, Reason: "unknown field"})
		}
	}
	if len(fields) > 0 {
		return app.UpdateTaskInput{}, &domain.ValidationError{Fields: fields}
	}
	return input, nil
}

// decodeOptionalString decodes one JSON string member. JSON null means "not
// supplied" and yields a nil pointer.
func decodeOptionalString(raw json.RawMessage) (*string, error) {
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// decodeOptionalInt decodes one JSON integer member. JSON null means "not
// supplied" and yields a nil pointer.
func decodeOptionalInt(raw json.RawMessage) (*int, error) {
	var value *int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// parseTaskStatuses reads status filters that may be repeated
// (?status=1&status=2) or comma separated (?status=1,2). Both forms are
// equivalent and duplicates collapse.
func parseTaskStatuses(query url.Values) ([]int, error) {
	values, ok := query["status"]
	if !ok {
		return nil, nil
	}

	statuses := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				return nil, invalidField("status", "must be an integer")
			}
			status, err := strconv.Atoi(part)
			if err != nil {
				return nil, invalidField("status", "must be an integer")
			}
			if status < domain.TaskBacklog || status > domain.TaskCancelled {
				return nil, invalidField("status", fmt.Sprintf("must be between %d and %d",
					domain.TaskBacklog, domain.TaskCancelled))
			}
			if _, dup := seen[status]; dup {
				continue
			}
			seen[status] = struct{}{}
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

// encodeTaskCursor renders the opaque cursor as
// base64url("p:<priority>:s:<seq_no>:i:<id>").
func encodeTaskCursor(c app.TaskCursor) string {
	raw := fmt.Sprintf("p:%d:s:%d:i:%d", c.Priority, c.SeqNo, c.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeTaskCursor parses the opaque cursor back into the keyset position. Any
// deviation from the documented shape is rejected, never guessed at.
func decodeTaskCursor(value string) (app.TaskCursor, error) {
	if value == "" {
		return app.TaskCursor{}, errors.New("empty cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return app.TaskCursor{}, err
	}

	parts := strings.Split(string(decoded), ":")
	if len(parts) != 6 || parts[0] != "p" || parts[2] != "s" || parts[4] != "i" {
		return app.TaskCursor{}, errors.New("cursor is not a task cursor")
	}

	priority, err := strconv.Atoi(parts[1])
	if err != nil {
		return app.TaskCursor{}, err
	}
	seqNo, err := strconv.Atoi(parts[3])
	if err != nil {
		return app.TaskCursor{}, err
	}
	id, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil {
		return app.TaskCursor{}, err
	}
	if seqNo < 0 || id < 0 {
		return app.TaskCursor{}, errors.New("cursor position is negative")
	}
	return app.TaskCursor{Priority: priority, SeqNo: seqNo, ID: id}, nil
}

// taskBodyLimit sizes the body reader for one task: the largest legal body_md
// plus room for JSON syntax and the title.
func taskBodyLimit() int64 {
	return int64(app.MaxTaskBodyBytes) + extraBodyBytes
}

// toTaskResponse maps the use-case view to the list DTO.
func toTaskResponse(task app.TaskView) taskResponse {
	return taskResponse{
		ID:           task.ID,
		TaskID:       task.TaskID,
		ProjectID:    task.ProjectID,
		SeqNo:        task.SeqNo,
		Title:        task.Title,
		BodyMD:       task.BodyMD,
		Priority:     task.Priority,
		Status:       task.Status,
		StatusName:   domain.TaskStatusName(task.Status),
		WorkerID:     task.WorkerID,
		AttemptCount: task.AttemptCount,
		MaxAttempts:  task.MaxAttempts,
		Reason:       nullableString(task.Reason),
		LastActor:    nullableString(task.LastActor),
		Version:      task.Version,
		DependsOn:    task.DependsOn,
		CreateTime:   task.CreateTime.UTC().Format(time.RFC3339Nano),
		UpdateTime:   task.UpdateTime.UTC().Format(time.RFC3339Nano),
	}
}

// toTaskDetailResponse maps the detail aggregate to the wire DTO. report and
// lease are always encoded (null when absent), unlike the list form.
func toTaskDetailResponse(detail app.TaskDetail) taskDetailResponse {
	response := taskDetailResponse{taskResponse: toTaskResponse(detail.Task)}
	if detail.Report != nil {
		report := toMaterialResponse(*detail.Report)
		response.Report = &report
	}
	if detail.Task.Lease != nil {
		response.Lease = &taskLeaseResponse{
			LeaseID: detail.Task.Lease.LeaseID,
			Until:   detail.Task.Lease.Until.UTC().Format(time.RFC3339Nano),
		}
	}
	return response
}
