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
//	POST /v1/work-items/claim
//	POST /v1/work-items/lease
type claimHandler struct {
	service *app.ClaimService
	logger  *slog.Logger
}

func newClaimHandler(service *app.ClaimService, logger *slog.Logger) *claimHandler {
	return &claimHandler{service: service, logger: logger}
}

// claimRequest is the POST /v1/work-items/claim body.
type claimRequest struct {
	WorkerID string `json:"worker_id"`
	WorkID   string `json:"work_id"`
}

// leaseRequest is the POST /v1/work-items/lease body.
type leaseRequest struct {
	WorkID  string `json:"work_id"`
	LeaseID string `json:"lease_id"`
}

// leaseRenewalResponse is the 200 body of the lease endpoint. until is the
// renewed deadline.
type leaseRenewalResponse struct {
	LeaseID    string `json:"lease_id"`
	Until      string `json:"until"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// dispatchInputResponse is one material the worker needs.
type dispatchInputResponse struct {
	MaterialID string `json:"material_id"`
	Kind       int    `json:"kind"`
	Version    int    `json:"version"`
}

// dispatchWorkItemResponse is the card part of a dispatch.
type dispatchWorkItemResponse struct {
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Priority  int    `json:"priority"`
	Attempt   int    `json:"attempt"`
}

// dispatchLeaseResponse is the freshly issued lease.
type dispatchLeaseResponse struct {
	LeaseID    string `json:"lease_id"`
	Until      string `json:"until"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// dispatchProductResponse is the product snapshot of the execution context.
type dispatchProductResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
}

// dispatchContextResponse carries everything the worker needs to start.
type dispatchContextResponse struct {
	SpecVersion int                     `json:"spec_version"`
	SpecDigest  string                  `json:"spec_digest"`
	Product     dispatchProductResponse `json:"product"`
}

// dispatchResponse is the dispatch payload carried by a 200 claim and by the
// heartbeat dispatch command. One shape, two delivery paths.
type dispatchResponse struct {
	Protocol string                   `json:"protocol"`
	WorkID   string                   `json:"work_id"`
	Kind     string                   `json:"kind"`
	Inputs   []dispatchInputResponse  `json:"inputs"`
	WorkItem dispatchWorkItemResponse `json:"work_item"`
	Lease    dispatchLeaseResponse    `json:"lease"`
	Context  dispatchContextResponse  `json:"context"`
}

// claimResponse is the 200 body of POST /v1/work-items/claim.
type claimResponse struct {
	Dispatch dispatchResponse `json:"dispatch"`
}

// claim handles POST /v1/work-items/claim. No claimable work is a 204 with an
// intentionally empty body: a worker polls this endpoint, and an empty body is
// cheaper to distinguish than a nullable envelope.
func (h *claimHandler) claim(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.Claim(r.Context(), app.ClaimInput{
		WorkerID: req.WorkerID,
		WorkID:   req.WorkID,
		Actor:    ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	WithSubject(r.Context(), result.WorkID)
	writeJSON(w, http.StatusOK, claimResponse{Dispatch: toDispatchResponse(*result)})
}

// renewLease handles POST /v1/work-items/lease.
func (h *claimHandler) renewLease(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}
	workID := strings.TrimSpace(req.WorkID)
	if workID == "" {
		writeDomainError(w, r, h.logger, invalidField("work_id", "is required"))
		return
	}
	WithSubject(r.Context(), workID)

	renewal, err := h.service.RenewLease(r.Context(), workID, req.LeaseID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	writeJSON(w, http.StatusOK, leaseRenewalResponse{
		LeaseID:    renewal.LeaseID,
		Until:      renewal.Until.UTC().Format(time.RFC3339Nano),
		TTLSeconds: int(renewal.TTL / time.Second),
	})
}

// toDispatchResponse maps the use-case dispatch to the wire DTO.
func toDispatchResponse(dispatch app.Dispatch) dispatchResponse {
	inputs := make([]dispatchInputResponse, 0, len(dispatch.Inputs))
	for _, input := range dispatch.Inputs {
		inputs = append(inputs, dispatchInputResponse{
			MaterialID: input.MaterialID,
			Kind:       input.Kind,
			Version:    input.Version,
		})
	}
	return dispatchResponse{
		Protocol: dispatch.Protocol,
		WorkID:   dispatch.WorkID,
		Kind:     dispatch.Kind,
		Inputs:   inputs,
		WorkItem: dispatchWorkItemResponse{
			ProjectID: dispatch.WorkItem.ProjectID,
			Title:     dispatch.WorkItem.Title,
			BodyMD:    dispatch.WorkItem.BodyMD,
			Priority:  dispatch.WorkItem.Priority,
			Attempt:   dispatch.WorkItem.Attempt,
		},
		Lease: dispatchLeaseResponse{
			LeaseID:    dispatch.Lease.LeaseID,
			Until:      dispatch.Lease.Until.UTC().Format(time.RFC3339Nano),
			TTLSeconds: int(dispatch.Lease.TTL / time.Second),
		},
		Context: dispatchContextResponse{
			SpecVersion: dispatch.Context.SpecVersion,
			SpecDigest:  dispatch.Context.SpecDigest,
			Product: dispatchProductResponse{
				ID:     dispatch.Context.Product.ID,
				Name:   dispatch.Context.Product.Name,
				GitURL: dispatch.Context.Product.GitURL,
			},
		},
	}
}
