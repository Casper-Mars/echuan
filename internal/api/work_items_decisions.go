package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// reportWorkItemRequest is the POST /v1/work-items/report body. plan is kept
// raw so its members can be decoded one by one with request-accurate field
// paths (plan.items[i].seq_no) instead of one generic "wrong type".
type reportWorkItemRequest struct {
	WorkID     string          `json:"work_id"`
	MaterialID string          `json:"material_id"`
	LeaseID    string          `json:"lease_id"`
	Plan       json.RawMessage `json:"plan"`
}

// reportPlanRequest is the plan envelope; items stay raw for per-item decoding.
type reportPlanRequest struct {
	Items []json.RawMessage `json:"items"`
}

// breakdownPlanItemRequest is one plan.items[i] member set.
type breakdownPlanItemRequest struct {
	SeqNo     int    `json:"seq_no"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Priority  int    `json:"priority"`
	DependsOn []int  `json:"depends_on"`
}

// decideWorkItemRequest is the POST /v1/work-items/decide body.
type decideWorkItemRequest struct {
	WorkID   string `json:"work_id"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// reviewWorkItemsRequest is the POST /v1/work-items/review body.
type reviewWorkItemsRequest struct {
	ProjectID string `json:"project_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
}

// reviewWorkItemsResponse is the 200 body of POST /v1/work-items/review.
type reviewWorkItemsResponse struct {
	ProjectID      string `json:"project_id"`
	Status         int    `json:"status"`
	StatusName     string `json:"status_name"`
	Version        int    `json:"version"`
	ReadyWorkItems int64  `json:"ready_work_items"`
	Reason         string `json:"reason,omitempty"`
	RejectedAt     string `json:"rejected_at,omitempty"`
}

// reportWorkItem handles POST /v1/work-items/report.
func (h *workItemsHandler) reportWorkItem(w http.ResponseWriter, r *http.Request) {
	var req reportWorkItemRequest
	if verr := decodeJSONBodyLimit(w, r, &req, planBodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	plan, verr := decodeReportPlan(req.Plan)
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.ReportWorkItem(r.Context(), app.ReportWorkItemInput{
		WorkID:     req.WorkID,
		MaterialID: req.MaterialID,
		LeaseID:    req.LeaseID,
		Plan:       plan,
		Actor:      ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	attrs := []any{
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"work_id", detail.Item.WorkID,
		"version", detail.Item.Version,
	}
	if detail.Result != nil {
		attrs = append(attrs,
			"result_material_id", detail.Result.MaterialID,
			"result_version", detail.Result.Version,
		)
	}
	h.logger.Info("work item reported", attrs...)

	writeJSON(w, http.StatusOK, toWorkItemDetailResponse(detail))
}

// decideWorkItem handles POST /v1/work-items/decide.
func (h *workItemsHandler) decideWorkItem(w http.ResponseWriter, r *http.Request) {
	var req decideWorkItemRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.DecideWorkItem(r.Context(), app.WorkItemDecisionInput{
		WorkID:   req.WorkID,
		Decision: req.Decision,
		Reason:   req.Reason,
		Actor:    ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logger.Info("work item decision",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"work_id", result.Detail.Item.WorkID,
		"decision", req.Decision,
		"from_state", result.FromState,
		"to_state", result.ToState,
		"version", result.Detail.Item.Version,
	)
	if result.Project != nil {
		h.logger.Info("project settled after last work item",
			"request_id", RequestIDFromContext(r.Context()),
			"actor", actorOrUnknown(r),
			"project_id", result.Project.ProjectID,
			"from_status", domain.StatusName(result.Project.FromStatus),
			"to_status", domain.StatusName(result.Project.ToStatus),
		)
	}

	writeJSON(w, http.StatusOK, toWorkItemDetailResponse(result.Detail))
}

// reviewWorkItems handles POST /v1/work-items/review.
func (h *workItemsHandler) reviewWorkItems(w http.ResponseWriter, r *http.Request) {
	var req reviewWorkItemsRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	decision, ok := domain.ParseDecision(req.Decision)
	if !ok {
		writeDomainError(w, r, h.logger, invalidField("decision", "must be approve or reject"))
		return
	}

	result, err := h.service.ReviewWorkItems(r.Context(), app.ReviewWorkItemsInput{
		ProjectID: req.ProjectID,
		Decision:  decision,
		Reason:    req.Reason,
		Actor:     ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), result.ProjectID)
	h.logger.Info("plan reviewed",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", result.ProjectID,
		"decision", string(decision),
		"ready_work_items", result.ReadyItems,
		"project_version", result.Version,
	)

	writeJSON(w, http.StatusOK, reviewWorkItemsResponse{
		ProjectID:      result.ProjectID,
		Status:         result.ToStatus,
		StatusName:     domain.StatusName(result.ToStatus),
		Version:        result.Version,
		ReadyWorkItems: result.ReadyItems,
		Reason:         result.Reason,
		RejectedAt:     result.RejectedAt,
	})
}

// decodeReportPlan decodes the optional plan member. Absent or JSON null means
// "no plan"; the app layer then decides whether that is legal for the card's
// kind. A present object is decoded strictly so every failure names the exact
// request field.
func decodeReportPlan(raw json.RawMessage) (*app.BreakdownPlan, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}

	var envelope reportPlanRequest
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, planDecodeError("plan", err)
	}

	plan := &app.BreakdownPlan{}
	if envelope.Items == nil {
		// The member was omitted (or null): an empty plan. The app layer
		// renders a different message than for items: [].
		return plan, nil
	}
	// Preserve the explicit-empty form so "no children" stays distinguishable
	// from "no plan" all the way to the use case.
	plan.Items = make([]app.BreakdownPlanItem, 0, len(envelope.Items))
	for i, rawItem := range envelope.Items {
		var item breakdownPlanItemRequest
		decoder := json.NewDecoder(bytes.NewReader(rawItem))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&item); err != nil {
			return nil, planDecodeError(fmt.Sprintf("plan.items[%d]", i), err)
		}
		plan.Items = append(plan.Items, app.BreakdownPlanItem{
			SeqNo:     item.SeqNo,
			ProjectID: item.ProjectID,
			Title:     item.Title,
			BodyMD:    item.BodyMD,
			Priority:  item.Priority,
			DependsOn: item.DependsOn,
		})
	}
	return plan, nil
}

// planDecodeError maps a strict decode failure onto a request path. prefix is
// the already-known parent path ("plan" or "plan.items[i]").
func planDecodeError(prefix string, err error) error {
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
