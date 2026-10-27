package api

import (
	"net/http"
	"strings"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// reportTaskRequest is the POST /v1/tasks/report body. summary_md is stored
// byte-for-byte; only its blankness is validated.
type reportTaskRequest struct {
	LeaseID   string `json:"lease_id"`
	SummaryMD string `json:"summary_md"`
}

// decideTaskRequest is the POST /v1/tasks/decide body.
type decideTaskRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// reportBodyLimit sizes the request body reader: one material plus JSON
// syntax. It tracks material_max_bytes instead of the 1 MB default so a
// report just under the configured limit reaches the writer (which then owns
// the exact 413 answer).
func (h *tasksHandler) reportBodyLimit() int64 {
	return h.service.MaxMaterialBytes() + extraBodyBytes
}

// reportTask handles POST /v1/tasks/report?id=APP-1-T0001.
func (h *tasksHandler) reportTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), taskID)

	var req reportTaskRequest
	if verr := decodeJSONBodyLimit(w, r, &req, h.reportBodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.ReportTask(r.Context(), app.ReportInput{
		TaskID:    taskID,
		LeaseID:   req.LeaseID,
		SummaryMD: req.SummaryMD,
		Actor:     ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	attrs := []any{
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"task_id", detail.Task.ID,
		"version", detail.Task.Version,
	}
	if detail.Report != nil {
		attrs = append(attrs,
			"report_material_id", detail.Report.MaterialID,
			"report_version", detail.Report.Version,
			"size", detail.Report.Size,
		)
	}
	h.logger.Info("task report submitted", attrs...)

	writeJSON(w, http.StatusOK, toTaskDetailResponse(detail))
}

// decideTask handles POST /v1/tasks/decide?id=APP-1-T0001.
func (h *tasksHandler) decideTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), taskID)

	version, verr := ifMatchVersion(r)
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	var req decideTaskRequest
	if verr := decodeJSONBody(w, r, &req); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	decision, ok := domain.ParseTaskDecision(strings.TrimSpace(req.Decision))
	if !ok {
		writeDomainError(w, r, h.logger,
			invalidField("decision", "must be accept, rework, retry or abandon"))
		return
	}

	result, err := h.service.DecideTask(r.Context(), app.TaskDecisionInput{
		TaskID:        taskID,
		Decision:      decision,
		Reason:        req.Reason,
		ExpectVersion: &version,
		Actor:         ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logger.Info("task decision",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"task_id", result.Detail.Task.ID,
		"decision", string(decision),
		"from_status", domain.TaskStatusName(result.FromStatus),
		"to_status", domain.TaskStatusName(result.ToStatus),
		"version", result.Detail.Task.Version,
	)
	if result.Project != nil {
		h.logger.Info("project advanced to delivery review",
			"request_id", RequestIDFromContext(r.Context()),
			"actor", actorOrUnknown(r),
			"project_id", result.Project.ProjectID,
			"from_status", domain.StatusName(result.Project.FromStatus),
			"to_status", domain.StatusName(result.Project.ToStatus),
			"trigger", "last_task_done",
		)
	}

	writeJSON(w, http.StatusOK, toTaskDetailResponse(result.Detail))
}
