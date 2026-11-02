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

// planItemRequest is one element of POST /v1/projects/plans. Priority and
// DependsOn are optional; their zero values match the documented defaults.
type planItemRequest struct {
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Priority  int    `json:"priority"`
	DependsOn []int  `json:"depends_on"`
}

// submitPlanRequest keeps items undecoded so every element can be strictly
// decoded on its own. That is what lets an unknown or mistyped member inside
// work_items[i] be reported as "work_items[i].field".
type submitPlanRequest struct {
	WorkItems []json.RawMessage `json:"work_items"`
}

// planItemResponse is one inserted card in the submit response. depends_on
// carries business ids (w_xxx), never batch sequence numbers.
type planItemResponse struct {
	WorkID    string   `json:"work_id"`
	SeqNo     int      `json:"seq_no"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Priority  int      `json:"priority"`
	State     string   `json:"state"`
	DependsOn []string `json:"depends_on"`
}

// submitPlanResponse is the 201 body of POST /v1/projects/plans.
type submitPlanResponse struct {
	ProjectID      string             `json:"project_id"`
	Status         int                `json:"status"`
	StatusName     string             `json:"status_name"`
	ProjectVersion int                `json:"project_version"`
	WorkItems      []planItemResponse `json:"work_items"`
}

// submitPlan handles POST /v1/projects/plans?id=APP-1.
func (h *projectsHandler) submitPlan(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("id"))
	if projectID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), projectID)

	items, verr := decodeSubmitPlanRequest(w, r, planBodyLimit())
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.SubmitPlan(r.Context(), projectID, ActorFromContext(r.Context()), items)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	response := submitPlanResponse{
		ProjectID:      result.Project.ProjectID,
		Status:         result.Project.Status,
		StatusName:     domain.StatusName(result.Project.Status),
		ProjectVersion: result.Project.Version,
		WorkItems:      make([]planItemResponse, 0, len(result.WorkItems)),
	}
	for _, item := range result.WorkItems {
		dependsOn := item.DependsOn
		if dependsOn == nil {
			dependsOn = []string{}
		}
		response.WorkItems = append(response.WorkItems, planItemResponse{
			WorkID:    item.WorkID,
			SeqNo:     item.SeqNo,
			Kind:      item.Kind,
			Title:     item.Title,
			Priority:  item.Priority,
			State:     item.State,
			DependsOn: dependsOn,
		})
	}

	h.logger.Info("plan submitted",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", result.Project.ProjectID,
		"work_item_count", len(response.WorkItems),
		"project_version", result.Project.Version,
	)

	w.Header().Set("Location", "/v1/projects?id="+url.QueryEscape(result.Project.ProjectID))
	writeJSON(w, http.StatusCreated, response)
}

// planBodyLimit sizes the body reader for the largest legal batch.
func planBodyLimit() int64 {
	return int64(app.MaxPlanItems)*int64(app.MaxWorkItemBodyBytes) + extraBodyBytes
}

// decodeSubmitPlanRequest decodes the plan body strictly, including every item
// element, and maps nested failures to work_items[i].field.
func decodeSubmitPlanRequest(w http.ResponseWriter, r *http.Request, limit int64) ([]app.PlanItem, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()

	var envelope submitPlanRequest
	if err := decoder.Decode(&envelope); err != nil {
		return nil, bodyDecodeError(err, limit)
	}
	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return nil, invalidField("body", "unexpected trailing data")
	}
	if envelope.WorkItems == nil {
		return nil, invalidField("work_items", "is required")
	}

	items := make([]app.PlanItem, 0, len(envelope.WorkItems))
	for i, raw := range envelope.WorkItems {
		item, err := decodePlanItem(i, raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// decodePlanItem decodes one work_items[i] object strictly and prefixes any
// failure with its position in the batch.
func decodePlanItem(index int, raw json.RawMessage) (app.PlanItem, error) {
	var item planItemRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil {
		return app.PlanItem{}, planItemDecodeError(index, err)
	}
	return app.PlanItem{
		Title:     item.Title,
		BodyMD:    item.BodyMD,
		Priority:  item.Priority,
		DependsOn: item.DependsOn,
	}, nil
}

// planItemDecodeError maps a per-element decode failure onto
// work_items[i].field.
func planItemDecodeError(index int, err error) error {
	prefix := "work_items[" + strconv.Itoa(index) + "]"

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
