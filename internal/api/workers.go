package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
)

// defaultWorkerListLimit is applied when ?limit= is absent.
const defaultWorkerListLimit = 50

// workersHandler serves the worker-facing endpoints:
//
//	POST /v1/workers/heartbeat   register, refresh and receive work
//	GET  /v1/workers             list the registry, or fetch one with ?id=
//
// It is separate from tasksHandler and claimHandler because the subject is the
// worker, not the card: a heartbeat is one round trip that may also register
// and dispatch.
type workersHandler struct {
	service *app.WorkerService
	logger  *slog.Logger
}

func newWorkersHandler(service *app.WorkerService, logger *slog.Logger) *workersHandler {
	return &workersHandler{service: service, logger: logger}
}

// workerHeartbeatRequest is the POST /v1/workers/heartbeat body.
//
// worker_id is empty on the very first call (the server mints one). state
// defaults to idle. metadata is kept raw so the service can decide whether it
// is a JSON object and whether it fits the configured cap; a null or an absent
// document both reach the service as an empty slice, which means "not
// supplied".
type workerHeartbeatRequest struct {
	WorkerID string          `json:"worker_id"`
	State    string          `json:"state"`
	Metadata json.RawMessage `json:"metadata"`
}

// workerResponse is the wire representation of one worker (§4.3).
//
// current_task_id is the business task id the worker holds, null when it holds
// none; that pointer — not state — is what makes a worker occupied.
type workerResponse struct {
	WorkerID            string          `json:"worker_id"`
	Presence            string          `json:"presence"`
	State               string          `json:"state"`
	CurrentTaskID       *string         `json:"current_task_id"`
	Metadata            json.RawMessage `json:"metadata"`
	FirstSeen           string          `json:"first_seen"`
	LastSeen            string          `json:"last_seen"`
	HeartbeatIntervalMS int             `json:"heartbeat_interval_ms"`
	LeaseTTLSeconds     int             `json:"lease_ttl_seconds"`
}

// workerCommandResponse is one instruction returned to the worker. task, lease
// and context are present exactly when kind is run_task and reuse the pull-mode
// claim DTOs so a worker parses one payload shape however it got the card.
type workerCommandResponse struct {
	Kind    string                `json:"kind"`
	Task    *claimedTaskResponse  `json:"task,omitempty"`
	Lease   *claimLeaseResponse   `json:"lease,omitempty"`
	Context *claimContextResponse `json:"context,omitempty"`
}

// workerHeartbeatResponse is the 200 body of POST /v1/workers/heartbeat.
// Commands is always an array: "nothing to do" is [], never null.
type workerHeartbeatResponse struct {
	Worker   workerResponse          `json:"worker"`
	Commands []workerCommandResponse `json:"commands"`
}

// workerListResponse is the list envelope; next_cursor is omitted on the last
// page.
type workerListResponse struct {
	Items      []workerResponse `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

// heartbeat handles POST /v1/workers/heartbeat.
func (h *workersHandler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req workerHeartbeatRequest
	if verr := decodeJSONBodyLimit(w, r, &req, maxRequestBodyBytes); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	result, err := h.service.Heartbeat(r.Context(), app.HeartbeatInput{
		WorkerID: req.WorkerID,
		State:    req.State,
		Metadata: req.Metadata,
		Actor:    ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), result.Worker.WorkerID)
	writeJSON(w, http.StatusOK, workerHeartbeatResponse{
		Worker:   h.toWorkerResponse(result.Worker),
		Commands: h.toWorkerCommands(result.Commands),
	})
}

// listOrGetWorker handles GET /v1/workers: ?id= selects the detail form,
// otherwise the registry is listed with limit/cursor.
func (h *workersHandler) listOrGetWorker(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("id") {
		h.getWorker(w, r, query.Get("id"))
		return
	}
	h.listWorkers(w, r)
}

// getWorker handles GET /v1/workers?id=w_xxx.
func (h *workersHandler) getWorker(w http.ResponseWriter, r *http.Request, workerID string) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), workerID)

	snapshot, err := h.service.GetWorker(r.Context(), workerID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, h.toWorkerResponse(snapshot))
}

// listWorkers handles GET /v1/workers?limit=&cursor=.
func (h *workersHandler) listWorkers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultWorkerListLimit
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
		id, err := decodeWorkerCursor(query.Get("cursor"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("cursor", "is invalid"))
			return
		}
		afterID = id
	}

	page, err := h.service.ListWorkers(r.Context(), afterID, limit)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]workerResponse, 0, len(page.Items))
	for _, snapshot := range page.Items {
		items = append(items, h.toWorkerResponse(snapshot))
	}
	response := workerListResponse{Items: items}
	if page.NextID != 0 {
		next := encodeWorkerCursor(page.NextID)
		response.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, response)
}

// toWorkerResponse maps the read model to the wire DTO. The two timing fields
// come from the service configuration so every response reports the same
// values the worker must honour.
func (h *workersHandler) toWorkerResponse(snapshot app.WorkerSnapshot) workerResponse {
	metadata := snapshot.Metadata
	if len(metadata) == 0 {
		// The column defaults to '{}'; an empty document would otherwise
		// encode as invalid JSON.
		metadata = json.RawMessage("{}")
	}
	return workerResponse{
		WorkerID:            snapshot.WorkerID,
		Presence:            snapshot.Presence,
		State:               snapshot.State,
		CurrentTaskID:       snapshot.CurrentTaskID,
		Metadata:            metadata,
		FirstSeen:           snapshot.FirstSeen.UTC().Format(time.RFC3339Nano),
		LastSeen:            snapshot.LastSeen.UTC().Format(time.RFC3339Nano),
		HeartbeatIntervalMS: int(h.service.HeartbeatInterval() / time.Millisecond),
		LeaseTTLSeconds:     int(h.service.LeaseTTL() / time.Second),
	}
}

// toWorkerCommands maps the command list, keeping it non-nil so it encodes as
// [] rather than null.
func (h *workersHandler) toWorkerCommands(commands []app.WorkerCommand) []workerCommandResponse {
	out := make([]workerCommandResponse, 0, len(commands))
	for _, command := range commands {
		out = append(out, toWorkerCommandResponse(command))
	}
	return out
}

func toWorkerCommandResponse(command app.WorkerCommand) workerCommandResponse {
	response := workerCommandResponse{Kind: command.Kind}
	if command.Task != nil {
		task := toClaimedTaskResponse(*command.Task)
		response.Task = &task
	}
	if command.Lease != nil {
		lease := toClaimLeaseResponse(*command.Lease)
		response.Lease = &lease
	}
	if command.Context != nil {
		context := toClaimContextResponse(*command.Context)
		response.Context = &context
	}
	return response
}

// encodeWorkerCursor renders the opaque cursor as base64url("i:<physical id>").
func encodeWorkerCursor(id int64) string {
	raw := "i:" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeWorkerCursor parses the opaque cursor back into the last physical id.
func decodeWorkerCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, errors.New("empty cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	const prefix = "i:"
	value := string(decoded)
	if !strings.HasPrefix(value, prefix) {
		return 0, errors.New("cursor is not a worker cursor")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	if err != nil {
		return 0, err
	}
	if id < 0 {
		return 0, errors.New("cursor id is negative")
	}
	return id, nil
}
