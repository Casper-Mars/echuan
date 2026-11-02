package api

import (
	"net/http"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// reportWorkItemRequest is the POST /v1/work-items/report body.
type reportWorkItemRequest struct {
	WorkID     string `json:"work_id"`
	MaterialID string `json:"material_id"`
	LeaseID    string `json:"lease_id"`
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
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.ReportWorkItem(r.Context(), app.ReportWorkItemInput{
		WorkID:     req.WorkID,
		MaterialID: req.MaterialID,
		LeaseID:    req.LeaseID,
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
