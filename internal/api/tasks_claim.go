package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
)

// claimHandler serves the pull-mode endpoints:
//
//	POST /v1/tasks/claim
//	POST /v1/tasks/heartbeat
//
// They are separate from tasksHandler because they are driven by workers
// rather than by the board, and because their error contract has one extra
// code: 409 lease_lost.
type claimHandler struct {
	service *app.ClaimService
	logger  *slog.Logger
}

func newClaimHandler(service *app.ClaimService, logger *slog.Logger) *claimHandler {
	return &claimHandler{service: service, logger: logger}
}

// claimRequest is the POST /v1/tasks/claim body. product is the business
// product id; task_id restricts the attempt to that single card; worker_id
// optionally binds the card to a registered worker (tech design v0.6 §4.2).
type claimRequest struct {
	Product  string `json:"product"`
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
}

// claimedTaskResponse is the task part of a claim (§4.4).
type claimedTaskResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Attempt   int    `json:"attempt"`
	Priority  int    `json:"priority"`
}

// claimLeaseResponse is the lease issued with the claim.
type claimLeaseResponse struct {
	LeaseID    string `json:"lease_id"`
	Until      string `json:"until"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// claimProductResponse is the product snapshot of the execution context.
type claimProductResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
}

// claimContextResponse carries everything the worker needs to start.
type claimContextResponse struct {
	SpecVersion int                  `json:"spec_version"`
	SpecDigest  string               `json:"spec_digest"`
	Product     claimProductResponse `json:"product"`
	Workdir     string               `json:"workdir"`
}

// claimResponse is the 200 body of POST /v1/tasks/claim.
type claimResponse struct {
	Task    claimedTaskResponse  `json:"task"`
	Lease   claimLeaseResponse   `json:"lease"`
	Context claimContextResponse `json:"context"`
}

// heartbeatRequest is the POST /v1/tasks/heartbeat body.
type heartbeatRequest struct {
	LeaseID string `json:"lease_id"`
}

// heartbeatResponse is the 200 body of the heartbeat endpoint. until is the
// renewed deadline.
type heartbeatResponse struct {
	LeaseID    string `json:"lease_id"`
	Until      string `json:"until"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// claim handles POST /v1/tasks/claim. No claimable work is a 204 with an
// intentionally empty body: a worker polls this endpoint, and an empty card is
// cheaper to distinguish than a nullable envelope.
func (h *claimHandler) claim(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.Claim(r.Context(), app.ClaimInput{
		Product:  req.Product,
		TaskID:   req.TaskID,
		Actor:    ActorFromContext(r.Context()),
		WorkerID: req.WorkerID,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	WithSubject(r.Context(), result.Task.ID)
	writeJSON(w, http.StatusOK, toClaimResponse(*result))
}

// heartbeat handles POST /v1/tasks/heartbeat?id=APP-1-T0001.
func (h *claimHandler) heartbeat(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("id"))
	if taskID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), taskID)

	var req heartbeatRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	renewal, err := h.service.Heartbeat(r.Context(), taskID, req.LeaseID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, heartbeatResponse{
		LeaseID:    renewal.LeaseID,
		Until:      renewal.Until.UTC().Format(time.RFC3339Nano),
		TTLSeconds: int(renewal.TTL / time.Second),
	})
}

// toClaimResponse maps the use-case result to the wire DTO.
func toClaimResponse(result app.ClaimResult) claimResponse {
	return claimResponse{
		Task:    toClaimedTaskResponse(result.Task),
		Lease:   toClaimLeaseResponse(result.Lease),
		Context: toClaimContextResponse(result.Context),
	}
}

// The three mappers below are shared with the worker heartbeat response: a card
// dispatched by a heartbeat carries exactly the same task/lease/context
// payloads as one returned by the pull-mode claim, so there is only one place
// that renders them.
func toClaimedTaskResponse(task app.ClaimedTask) claimedTaskResponse {
	return claimedTaskResponse{
		ID:        task.ID,
		ProjectID: task.ProjectID,
		Title:     task.Title,
		BodyMD:    task.BodyMD,
		Attempt:   task.Attempt,
		Priority:  task.Priority,
	}
}

func toClaimLeaseResponse(lease app.ClaimLease) claimLeaseResponse {
	return claimLeaseResponse{
		LeaseID:    lease.LeaseID,
		Until:      lease.Until.UTC().Format(time.RFC3339Nano),
		TTLSeconds: int(lease.TTL / time.Second),
	}
}

func toClaimContextResponse(context app.ClaimContext) claimContextResponse {
	return claimContextResponse{
		SpecVersion: context.SpecVersion,
		SpecDigest:  context.SpecDigest,
		Product: claimProductResponse{
			ID:     context.Product.ID,
			Name:   context.Product.Name,
			GitURL: context.Product.GitURL,
		},
		Workdir: context.Workdir,
	}
}
