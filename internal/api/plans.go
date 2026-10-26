package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// planTaskRequest is one element of POST /v1/projects/plans. Priority and
// DependsOn are optional; their zero values match the documented defaults.
type planTaskRequest struct {
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Priority  int    `json:"priority"`
	DependsOn []int  `json:"depends_on"`
}

// submitPlanRequest keeps tasks undecoded so every element can be strictly
// decoded on its own. That is what lets an unknown or mistyped member inside
// tasks[i] be reported as "tasks[i].field" instead of the bare JSON field name.
type submitPlanRequest struct {
	Tasks []json.RawMessage `json:"tasks"`
}

// planTaskResponse is one inserted task in the submit response. depends_on
// carries business ids (APP-1-T0001), never batch sequence numbers.
type planTaskResponse struct {
	ID         string   `json:"id"`
	SeqNo      int      `json:"seq_no"`
	Title      string   `json:"title"`
	Priority   int      `json:"priority"`
	Status     int      `json:"status"`
	StatusName string   `json:"status_name"`
	DependsOn  []string `json:"depends_on"`
}

// submitPlanResponse is the 201 body of POST /v1/projects/plans.
type submitPlanResponse struct {
	ProjectID      string             `json:"project_id"`
	Status         int                `json:"status"`
	StatusName     string             `json:"status_name"`
	ProjectVersion int                `json:"project_version"`
	Tasks          []planTaskResponse `json:"tasks"`
}

// submitPlan handles POST /v1/projects/plans?id=APP-1.
func (h *projectsHandler) submitPlan(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	tasks, verr := decodeSubmitPlanRequest(w, r, planBodyLimit())
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.SubmitPlan(r.Context(), projectID, ActorFromContext(r.Context()), tasks)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	response := submitPlanResponse{
		ProjectID:      result.Project.ProjectID,
		Status:         result.Project.Status,
		StatusName:     domain.StatusName(result.Project.Status),
		ProjectVersion: result.Project.Version,
		Tasks:          make([]planTaskResponse, 0, len(result.Tasks)),
	}
	for _, task := range result.Tasks {
		dependsOn := task.DependsOn
		if dependsOn == nil {
			dependsOn = []string{}
		}
		response.Tasks = append(response.Tasks, planTaskResponse{
			ID:         task.ID,
			SeqNo:      task.SeqNo,
			Title:      task.Title,
			Priority:   task.Priority,
			Status:     task.Status,
			StatusName: domain.TaskStatusName(task.Status),
			DependsOn:  dependsOn,
		})
	}

	firstTaskID := ""
	if len(response.Tasks) > 0 {
		firstTaskID = response.Tasks[0].ID
	}
	h.logger.Info("plan submitted",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", result.Project.ProjectID,
		"task_count", len(response.Tasks),
		"first_task_id", firstTaskID,
		"project_version", result.Project.Version,
	)

	w.Header().Set("Location", "/v1/projects?id="+url.QueryEscape(result.Project.ProjectID))
	writeJSON(w, http.StatusCreated, response)
}

// planBodyLimit sizes the body reader for the largest legal batch: every task
// body at its cap plus room for JSON syntax and titles. A body above this is
// 413; an individual body_md above its cap is still the service's 400.
func planBodyLimit() int64 {
	return int64(app.MaxPlanTasks)*int64(app.MaxTaskBodyBytes) + extraBodyBytes
}

// decodeSubmitPlanRequest decodes the plan body strictly, including every task
// element, and maps nested failures to tasks[i].field.
func decodeSubmitPlanRequest(w http.ResponseWriter, r *http.Request, limit int64) ([]app.PlanTask, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()

	var envelope submitPlanRequest
	if err := decoder.Decode(&envelope); err != nil {
		return nil, bodyDecodeError(err, limit)
	}
	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return nil, invalidField("body", "unexpected trailing data")
	}
	if envelope.Tasks == nil {
		return nil, invalidField("tasks", "is required")
	}

	tasks := make([]app.PlanTask, 0, len(envelope.Tasks))
	for i, raw := range envelope.Tasks {
		item, err := decodePlanTask(i, raw)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, item)
	}
	return tasks, nil
}

// decodePlanTask decodes one tasks[i] object strictly and prefixes any failure
// with its position in the batch.
func decodePlanTask(index int, raw json.RawMessage) (app.PlanTask, error) {
	var item planTaskRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil {
		return app.PlanTask{}, planTaskDecodeError(index, err)
	}
	return app.PlanTask{
		Title:     item.Title,
		BodyMD:    item.BodyMD,
		Priority:  item.Priority,
		DependsOn: item.DependsOn,
	}, nil
}

// planTaskDecodeError maps a per-element decode failure onto tasks[i].field.
func planTaskDecodeError(index int, err error) error {
	prefix := "tasks[" + strconv.Itoa(index) + "]"

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field == "" {
			return invalidField(prefix, "must be an object")
		}
		return invalidField(prefix+"."+typeErr.Field, "has the wrong type")
	}

	const unknownPrefix = `json: unknown field "`
	if strings.HasPrefix(err.Error(), unknownPrefix) {
		rest := strings.TrimPrefix(err.Error(), unknownPrefix)
		if end := strings.IndexByte(rest, '"'); end > 0 {
			return invalidField(prefix+"."+rest[:end], "unknown field")
		}
	}
	if errors.Is(err, io.EOF) {
		return invalidField(prefix, "must be an object")
	}
	return invalidField(prefix, "invalid JSON")
}
