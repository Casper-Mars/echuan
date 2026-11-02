package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Casper-Mars/echuan/internal/app"
)

// defaultGateDecisionListLimit is applied when ?limit= is absent.
const defaultGateDecisionListLimit = 50

// gateDecisionResponse is one row of the decision history.
type gateDecisionResponse struct {
	ID              int64   `json:"id"`
	SubjectType     string  `json:"subject_type"`
	SubjectID       string  `json:"subject_id"`
	Gate            string  `json:"gate"`
	MaterialID      *string `json:"material_id"`
	MaterialVersion *int    `json:"material_version"`
	Decision        string  `json:"decision"`
	DecidedBy       string  `json:"decided_by"`
	Reason          *string `json:"reason"`
	CreateTime      string  `json:"create_time"`
}

// gateDecisionListResponse is the list envelope; next_cursor is omitted on the
// last page.
type gateDecisionListResponse struct {
	Items      []gateDecisionResponse `json:"items"`
	NextCursor *string                `json:"next_cursor,omitempty"`
}

// projectDecisionResponse is the 200 body of POST /v1/projects/decide: the
// project plus the material version the verdict was bound to.
type projectDecisionResponse struct {
	projectResponse
	MaterialID *string `json:"material_id"`
}

// workItemDecisionResponse is the 200 body of POST /v1/work-items/decide.
type workItemDecisionResponse struct {
	workItemDetailResponse
	MaterialID *string `json:"material_id"`
}

// forbidAgentActor answers 403 agent_forbidden when the declared actor is an
// agent. Gate verdicts are reserved for humans: kits act as "agent:<name>" and
// may report work, but must never approve or reject it. An absent actor keeps
// the historical behaviour (the request is allowed).
func forbidAgentActor(w http.ResponseWriter, r *http.Request) bool {
	actor := strings.TrimSpace(ActorFromContext(r.Context()))
	if !strings.HasPrefix(actor, "agent:") {
		return false
	}
	writeError(w, r, http.StatusForbidden, "agent_forbidden",
		"门禁只接受人工决策：agent 不能调用 decision 端点", nil)
	return true
}

// materialRefID maps an optional material to its wire id.
func materialRefID(material *app.GateMaterial) *string {
	if material == nil {
		return nil
	}
	id := material.MaterialID
	return &id
}

// listGateDecisions handles GET /v1/gate-decisions.
func (h *projectsHandler) listGateDecisions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultGateDecisionListLimit
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
		id, err := decodeCursor(query.Get("cursor"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("cursor", "is invalid"))
			return
		}
		afterID = id
	}

	page, err := h.service.ListGateDecisions(r.Context(), app.ListGateDecisionsInput{
		ProjectID:   query.Get("project_id"),
		SubjectType: query.Get("subject_type"),
		SubjectID:   query.Get("subject_id"),
		AfterID:     afterID,
		Limit:       limit,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]gateDecisionResponse, 0, len(page.Items))
	for _, item := range page.Items {
		row := gateDecisionResponse{
			ID:          item.ID,
			SubjectType: item.SubjectType,
			SubjectID:   item.SubjectID,
			Gate:        item.Gate,
			Decision:    item.Decision,
			DecidedBy:   item.DecidedBy,
			CreateTime:  item.CreateTime,
		}
		if item.MaterialID != "" {
			id := item.MaterialID
			row.MaterialID = &id
		}
		if item.MaterialVersion > 0 {
			version := item.MaterialVersion
			row.MaterialVersion = &version
		}
		if item.Reason != "" {
			reason := item.Reason
			row.Reason = &reason
		}
		items = append(items, row)
	}

	resp := gateDecisionListResponse{Items: items}
	if page.NextID > 0 {
		cursor := encodeCursor(page.NextID)
		resp.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, resp)
}
