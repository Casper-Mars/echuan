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

// defaultWorkItemListLimit is applied when ?limit= is absent.
const defaultWorkItemListLimit = 50

// workItemPlatformFields are the columns the platform maintains itself. A PATCH
// that carries any of them is rejected with 400 invalid_argument and a detail
// naming the field.
var workItemPlatformFields = []string{
	"work_id",
	"state",
	"seq_no",
	"project_id",
	"parent_id",
	"kind",
	"attempt",
	"max_attempts",
	"lease_id",
	"lease_until",
	"worker_id",
	"result_material_id",
	"version",
	"create_time",
	"update_time",
	"reason",
}

// workItemPlatformFieldSet is the lookup form of workItemPlatformFields.
var workItemPlatformFieldSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(workItemPlatformFields))
	for _, field := range workItemPlatformFields {
		set[field] = struct{}{}
	}
	return set
}()

// workItemsHandler serves /v1/work-items.
type workItemsHandler struct {
	service *app.WorkItemService
	logger  *slog.Logger
}

func newWorkItemsHandler(service *app.WorkItemService, logger *slog.Logger) *workItemsHandler {
	return &workItemsHandler{service: service, logger: logger}
}

// createWorkItemRequest is the POST /v1/work-items body.
type createWorkItemRequest struct {
	ProjectID string   `json:"project_id"`
	ParentID  string   `json:"parent_id"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	BodyMD    string   `json:"body_md"`
	Priority  int      `json:"priority"`
	DependsOn []string `json:"depends_on"`
}

// workItemResponse is the wire representation of one work item.
type workItemResponse struct {
	WorkID      string   `json:"work_id"`
	ProjectID   string   `json:"project_id"`
	ParentID    *string  `json:"parent_id"`
	Kind        string   `json:"kind"`
	SeqNo       int      `json:"seq_no"`
	Title       string   `json:"title"`
	BodyMD      string   `json:"body_md"`
	Priority    int      `json:"priority"`
	State       string   `json:"state"`
	WorkerID    *string  `json:"worker_id"`
	Attempt     int      `json:"attempt"`
	MaxAttempts int      `json:"max_attempts"`
	Reason      *string  `json:"reason"`
	Version     int      `json:"version"`
	DependsOn   []string `json:"depends_on"`
	// BlockedBy lists the dependencies that are not accepted yet; it is the
	// machine-readable form of "why this card is still blocked".
	BlockedBy        []string               `json:"blocked_by"`
	ResultMaterialID *string                `json:"result_material_id"`
	CreateTime       string                 `json:"create_time"`
	UpdateTime       string                 `json:"update_time"`
	Lease            *workItemLeaseResponse `json:"lease"`
}

// workItemLeaseResponse is the live lease of a leased work item.
type workItemLeaseResponse struct {
	LeaseID string `json:"lease_id"`
	Until   string `json:"until"`
}

// workItemDetailResponse adds the result material to the list representation.
// Both result and lease are always encoded (null when absent).
type workItemDetailResponse struct {
	workItemResponse
	Result *materialResponse `json:"result"`
}

// workItemListResponse is the list envelope; next_cursor is omitted on the last
// page.
type workItemListResponse struct {
	Items      []workItemResponse `json:"items"`
	NextCursor *string            `json:"next_cursor,omitempty"`
}

// createWorkItem handles POST /v1/work-items.
func (h *workItemsHandler) createWorkItem(w http.ResponseWriter, r *http.Request) {
	var req createWorkItemRequest
	if verr := decodeJSONBodyLimit(w, r, &req, workItemBodyLimit()); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.CreateWorkItem(r.Context(), app.CreateWorkItemInput{
		ProjectID: req.ProjectID,
		ParentID:  req.ParentID,
		Kind:      req.Kind,
		Title:     req.Title,
		BodyMD:    req.BodyMD,
		Priority:  req.Priority,
		DependsOn: req.DependsOn,
	}, ActorFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), detail.Item.WorkID)
	h.logger.Info("work item created",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", detail.Item.WorkID,
		"project_id", detail.Item.ProjectID,
		"kind", detail.Item.Kind,
		"seq_no", detail.Item.SeqNo,
	)

	w.Header().Set("Location", "/v1/work-items?id="+url.QueryEscape(detail.Item.WorkID))
	writeJSON(w, http.StatusCreated, toWorkItemDetailResponse(detail))
}

// listOrGetWorkItem handles GET /v1/work-items: ?id=/?work_id= selects the
// detail form, otherwise the board is listed.
func (h *workItemsHandler) listOrGetWorkItem(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if id := workItemQueryID(query); id != "" {
		h.getWorkItem(w, r, id)
		return
	}
	if query.Has("id") || query.Has("work_id") {
		writeDomainError(w, r, h.logger, invalidField("work_id", "is required"))
		return
	}
	h.listWorkItems(w, r)
}

// getWorkItem handles GET /v1/work-items?id=w_xxx.
func (h *workItemsHandler) getWorkItem(w http.ResponseWriter, r *http.Request, workID string) {
	WithSubject(r.Context(), workID)

	detail, err := h.service.GetWorkItem(r.Context(), workID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorkItemDetailResponse(detail))
}

// listWorkItems handles GET /v1/work-items?project_id=&kind=&state=&limit=&cursor=.
func (h *workItemsHandler) listWorkItems(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultWorkItemListLimit
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("limit", "must be an integer"))
			return
		}
		limit = parsed
	}

	if query.Has("project_id") && strings.TrimSpace(query.Get("project_id")) == "" {
		writeDomainError(w, r, h.logger, invalidField("project_id", "is required"))
		return
	}

	kinds := parseCSV(query, "kind")
	states := parseCSV(query, "state")

	var after *app.WorkItemCursor
	if query.Has("cursor") {
		cursor, err := decodeWorkItemCursor(query.Get("cursor"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("cursor", "is invalid"))
			return
		}
		after = &cursor
	}

	page, err := h.service.ListWorkItems(r.Context(), app.ListWorkItemsInput{
		ProjectID: query.Get("project_id"),
		Kinds:     kinds,
		States:    states,
		After:     after,
		Limit:     limit,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]workItemResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toWorkItemResponse(item))
	}
	response := workItemListResponse{Items: items}
	if page.Next != nil {
		next := encodeWorkItemCursor(*page.Next)
		response.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, response)
}

// updateWorkItem handles PATCH /v1/work-items?id=w_xxx.
func (h *workItemsHandler) updateWorkItem(w http.ResponseWriter, r *http.Request) {
	workID := workItemQueryID(r.URL.Query())
	if workID == "" {
		writeDomainError(w, r, h.logger, invalidField("work_id", "is required"))
		return
	}
	WithSubject(r.Context(), workID)

	input, verr := decodeWorkItemPatch(w, r)
	if verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	detail, err := h.service.UpdateWorkItem(r.Context(), workID, input, ActorFromContext(r.Context()))
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logger.Info("work item patched",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", detail.Item.WorkID,
		"version", detail.Item.Version,
	)
	writeJSON(w, http.StatusOK, toWorkItemDetailResponse(detail))
}

// deleteWorkItem handles DELETE /v1/work-items?id=w_xxx.
func (h *workItemsHandler) deleteWorkItem(w http.ResponseWriter, r *http.Request) {
	workID := workItemQueryID(r.URL.Query())
	if workID == "" {
		writeDomainError(w, r, h.logger, invalidField("work_id", "is required"))
		return
	}
	WithSubject(r.Context(), workID)

	if err := h.service.DeleteWorkItem(r.Context(), workID, ActorFromContext(r.Context())); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	h.logger.Info("work item deleted",
		"request_id", RequestIDFromContext(r.Context()),
		"actor", actorOrUnknown(r),
		"subject", workID,
	)
	w.WriteHeader(http.StatusNoContent)
}

// workItemQueryID returns the card id from ?work_id= (preferred) or ?id=.
func workItemQueryID(query url.Values) string {
	if value := strings.TrimSpace(query.Get("work_id")); value != "" {
		return value
	}
	return strings.TrimSpace(query.Get("id"))
}

// decodeWorkItemPatch decodes a PATCH body member by member so that a
// platform-maintained column is named in details.field instead of being folded
// into a generic "unknown field".
func decodeWorkItemPatch(w http.ResponseWriter, r *http.Request) (app.UpdateWorkItemInput, error) {
	const limit = maxRequestBodyBytes

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return app.UpdateWorkItemInput{}, bodyDecodeError(err, limit)
	}
	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return app.UpdateWorkItemInput{}, invalidField("body", "unexpected trailing data")
	}
	if raw == nil {
		return app.UpdateWorkItemInput{}, invalidField("body", "must be a JSON object")
	}
	if len(raw) == 0 {
		return app.UpdateWorkItemInput{}, invalidField("body",
			"at least one of title, body_md, priority is required")
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var (
		input  app.UpdateWorkItemInput
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
			if _, platform := workItemPlatformFieldSet[key]; platform {
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
		return app.UpdateWorkItemInput{}, &domain.ValidationError{Fields: fields}
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

// parseCSV reads a filter that may be repeated (?kind=dev&kind=design) or comma
// separated (?kind=dev,design).
func parseCSV(query url.Values, key string) []string {
	values, ok := query[key]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// encodeWorkItemCursor renders the opaque cursor as
// base64url("p:<priority>:s:<seq_no>:i:<id>").
func encodeWorkItemCursor(c app.WorkItemCursor) string {
	raw := fmt.Sprintf("p:%d:s:%d:i:%d", c.Priority, c.SeqNo, c.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeWorkItemCursor parses the opaque cursor back into the keyset position.
func decodeWorkItemCursor(value string) (app.WorkItemCursor, error) {
	if value == "" {
		return app.WorkItemCursor{}, errors.New("empty cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return app.WorkItemCursor{}, err
	}

	parts := strings.Split(string(decoded), ":")
	if len(parts) != 6 || parts[0] != "p" || parts[2] != "s" || parts[4] != "i" {
		return app.WorkItemCursor{}, errors.New("cursor is not a work item cursor")
	}

	priority, err := strconv.Atoi(parts[1])
	if err != nil {
		return app.WorkItemCursor{}, err
	}
	seqNo, err := strconv.Atoi(parts[3])
	if err != nil {
		return app.WorkItemCursor{}, err
	}
	id, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil {
		return app.WorkItemCursor{}, err
	}
	if seqNo < 0 || id < 0 {
		return app.WorkItemCursor{}, errors.New("cursor position is negative")
	}
	return app.WorkItemCursor{Priority: priority, SeqNo: seqNo, ID: id}, nil
}

// workItemBodyLimit sizes the body reader for one card.
func workItemBodyLimit() int64 {
	return int64(app.MaxWorkItemBodyBytes) + extraBodyBytes
}

// toWorkItemResponse maps the use-case view to the list DTO.
func toWorkItemResponse(item app.WorkItemView) workItemResponse {
	var lease *workItemLeaseResponse
	if item.Lease != nil {
		lease = &workItemLeaseResponse{
			LeaseID: item.Lease.LeaseID,
			Until:   item.Lease.Until.UTC().Format(time.RFC3339Nano),
		}
	}
	return workItemResponse{
		WorkID:           item.WorkID,
		ProjectID:        item.ProjectID,
		ParentID:         item.ParentID,
		Kind:             item.Kind,
		SeqNo:            item.SeqNo,
		Title:            item.Title,
		BodyMD:           item.BodyMD,
		Priority:         item.Priority,
		State:            item.State,
		WorkerID:         item.WorkerID,
		Attempt:          item.Attempt,
		MaxAttempts:      item.MaxAttempts,
		Reason:           nullableString(item.Reason),
		Version:          item.Version,
		DependsOn:        item.DependsOn,
		BlockedBy:        item.BlockedBy,
		ResultMaterialID: item.ResultMaterialID,
		CreateTime:       item.CreateTime.UTC().Format(time.RFC3339Nano),
		UpdateTime:       item.UpdateTime.UTC().Format(time.RFC3339Nano),
		Lease:            lease,
	}
}

// toWorkItemDetailResponse maps the detail aggregate to the wire DTO.
func toWorkItemDetailResponse(detail app.WorkItemDetail) workItemDetailResponse {
	response := workItemDetailResponse{workItemResponse: toWorkItemResponse(detail.Item)}
	if detail.Result != nil {
		result := toMaterialResponse(*detail.Result)
		response.Result = &result
	}
	return response
}
