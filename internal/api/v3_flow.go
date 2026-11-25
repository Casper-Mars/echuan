package api

import (
	"fmt"
	"net/http"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// The stage table is served and projected from the task service's snapshot: the
// API layer owns the wire spelling of a kind (name in, name out) but never a
// second copy of the configuration (§7.1 / §7.2).

// stageNames renders one kind as (name, label, material). An id the running flow
// does not know answers "kind(<id>)" for both name and label and an empty
// material: a historical card must render, not fail (§7.4).
func (h *v3Handler) stageNames(kind enum.TaskKind) (name, label, material string) {
	if stage, ok := h.flow.ByID(int(kind)); ok {
		return stage.Name, stage.LabelOrName(), stage.Material
	}
	unknown := fmt.Sprintf("kind(%d)", int(kind))
	return unknown, unknown, ""
}

// stageName is the name-only form used where a display label would be noise
// (worker kind lists, claim log lines).
func (h *v3Handler) stageName(kind enum.TaskKind) string {
	name, _, _ := h.stageNames(kind)
	return name
}

// v3FlowResponse is GET /v1/flow: the effective stage table, in configured
// order, plus the provenance an operator needs to know which configuration the
// process loaded.
type v3FlowResponse struct {
	Auto        bool          `json:"auto"`
	Default     bool          `json:"default"`
	Source      string        `json:"source"`
	Fingerprint string        `json:"fingerprint"`
	Stages      []v3FlowStage `json:"stages"`
}

type v3FlowStage struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	Material  string `json:"material"`
	OpenCards string `json:"open_cards"`
	Closing   bool   `json:"closing"`
	Title     string `json:"title"`
	// Description is the template as written; "" means the operator wrote
	// description: "" or did not configure one (the card then falls back to the
	// flow default, §8.4).
	Description string       `json:"description"`
	Derive      v3FlowDerive `json:"derive"`
}

type v3FlowDerive struct {
	Mode   string       `json:"mode"`
	To     string       `json:"to,omitempty"`
	Source string       `json:"source,omitempty"`
	Format string       `json:"format,omitempty"`
	Items  []v3FlowItem `json:"items,omitempty"`
}

type v3FlowItem struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	BodyMD    string   `json:"body_md"`
	Priority  int      `json:"priority"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// flowDoc answers GET /v1/flow. No error path: the flow is validated at
// startup and snapshotted, so this endpoint only projects what the process
// already loaded.
func (h *v3Handler) flowDoc(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.flowResponse(h.flow))
}

func (h *v3Handler) flowResponse(flow platform.FlowConfig) v3FlowResponse {
	stages := make([]v3FlowStage, 0, len(flow.Stages))
	for _, stage := range flow.Stages {
		description := ""
		if stage.Description != nil {
			description = *stage.Description
		}
		derive := v3FlowDerive{
			Mode:   string(stage.Derive.Mode),
			To:     stage.Derive.To,
			Source: stage.Derive.Source,
			Format: stage.Derive.Format,
		}
		if derive.Mode == "" {
			derive.Mode = string(platform.DeriveNone)
		}
		for _, item := range stage.Derive.Items {
			derive.Items = append(derive.Items, v3FlowItem{
				Key: item.Key, Title: item.Title, BodyMD: item.BodyMD,
				Priority: item.Priority, DependsOn: item.DependsOn,
			})
		}
		stages = append(stages, v3FlowStage{
			ID:          stage.ID,
			Name:        stage.Name,
			Label:       stage.LabelOrName(),
			Material:    stage.Material,
			OpenCards:   string(stage.OpenCards),
			Closing:     stage.Closing,
			Title:       stage.Title,
			Description: description,
			Derive:      derive,
		})
	}
	return v3FlowResponse{
		Auto:        flow.Auto,
		Default:     flow.Source == platform.BuiltinFlowSource,
		Source:      flow.Source,
		Fingerprint: flow.Fingerprint(),
		Stages:      stages,
	}
}
