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
// defaults to idle. kinds is mandatory and replaces the stored declaration.
type workerHeartbeatRequest struct {
	WorkerID string          `json:"worker_id"`
	State    string          `json:"state"`
	Kinds    []string        `json:"kinds"`
	Metadata json.RawMessage `json:"metadata"`
}

// workerResponse is the wire representation of one worker.
type workerResponse struct {
	WorkerID            string          `json:"worker_id"`
	Presence            string          `json:"presence"`
	State               string          `json:"state"`
	Kinds               []string        `json:"kinds"`
	CurrentWorkItemID   *string         `json:"current_work_item_id"`
	Metadata            json.RawMessage `json:"metadata"`
	FirstSeen           string          `json:"first_seen"`
	LastSeen            string          `json:"last_seen"`
	HeartbeatIntervalMS int64           `json:"heartbeat_interval_ms"`
	LeaseTTLSeconds     int64           `json:"lease_ttl_seconds"`
}

// workerCommandResponse is one instruction returned to the worker. dispatch is
// present exactly when kind is "dispatch".
type workerCommandResponse struct {
	Kind     string            `json:"kind"`
	Dispatch *dispatchResponse `json:"dispatch,omitempty"`
}

// workerHeartbeatResponse is the 200 body of POST /v1/workers/heartbeat.
// Commands is always an array: "nothing to do" is [], never null.
type workerHeartbeatResponse struct {
	Worker   workerResponse          `json:"worker"`
	Commands []workerCommandResponse `json:"commands"`
}

// workerListResponse is the list envelope.
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
		Kinds:    req.Kinds,
		Metadata: req.Metadata,
		Actor:    ActorFromContext(r.Context()),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), result.Worker.WorkerID)
	writeJSON(w, http.StatusOK, workerHeartbeatResponse{
		Worker:   toWorkerResponse(result.Worker),
		Commands: toWorkerCommands(result.Commands),
	})
}

// listOrGetWorker handles GET /v1/workers.
func (h *workersHandler) listOrGetWorker(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if id := strings.TrimSpace(query.Get("id")); id != "" {
		h.getWorker(w, r, id)
		return
	}
	if query.Has("id") {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	h.listWorkers(w, r)
}

// getWorker handles GET /v1/workers?id=w_xxx.
func (h *workersHandler) getWorker(w http.ResponseWriter, r *http.Request, workerID string) {
	WithSubject(r.Context(), workerID)

	snapshot, err := h.service.GetWorker(r.Context(), workerID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorkerViewResponse(snapshot))
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

	items, next, err := h.service.ListWorkers(r.Context(), afterID, limit)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	response := workerListResponse{Items: make([]workerResponse, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, toWorkerViewResponse(item))
	}
	if next != nil {
		cursor := encodeWorkerCursor(*next)
		response.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, response)
}

// toWorkerResponse maps a heartbeat snapshot to the wire DTO.
func toWorkerResponse(snapshot app.WorkerSnapshot) workerResponse {
	metadata := snapshot.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}
	return workerResponse{
		WorkerID:            snapshot.WorkerID,
		Presence:            snapshot.Presence,
		State:               snapshot.State,
		Kinds:               snapshot.Kinds,
		CurrentWorkItemID:   snapshot.CurrentWorkItemID,
		Metadata:            metadata,
		FirstSeen:           snapshot.FirstSeen.UTC().Format(time.RFC3339Nano),
		LastSeen:            snapshot.LastSeen.UTC().Format(time.RFC3339Nano),
		HeartbeatIntervalMS: snapshot.HeartbeatIntervalMS,
		LeaseTTLSeconds:     snapshot.LeaseTTLSeconds,
	}
}

// toWorkerViewResponse maps a registry row to the wire DTO. The two timing
// fields carry the same service configuration the heartbeat payload reports.
func toWorkerViewResponse(view app.WorkerView) workerResponse {
	metadata := view.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}
	return workerResponse{
		WorkerID:            view.WorkerID,
		Presence:            view.Presence,
		State:               view.State,
		Kinds:               view.Kinds,
		CurrentWorkItemID:   view.CurrentWorkItemID,
		Metadata:            metadata,
		FirstSeen:           view.FirstSeen.UTC().Format(time.RFC3339Nano),
		LastSeen:            view.LastSeen.UTC().Format(time.RFC3339Nano),
		HeartbeatIntervalMS: view.HeartbeatIntervalMS,
		LeaseTTLSeconds:     view.LeaseTTLSeconds,
	}
}

// toWorkerCommands maps the command list, keeping it non-nil so it encodes as
// [] rather than null.
func toWorkerCommands(commands []app.WorkerCommand) []workerCommandResponse {
	out := make([]workerCommandResponse, 0, len(commands))
	for _, command := range commands {
		response := workerCommandResponse{Kind: command.Kind}
		if command.Dispatch != nil {
			dispatch := toDispatchResponse(*command.Dispatch)
			response.Dispatch = &dispatch
		}
		out = append(out, response)
	}
	return out
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
