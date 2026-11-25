package v3

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// The name→id mapping lives here, once, in the application layer (D2‴): the
// HTTP boundary hands in whatever the client sent, the engine below only ever
// sees ids. Nothing downstream of this file may look a stage up by name.

// ResolveKind maps a client-supplied stage spelling onto the id that lands in
// tasks.kind. The accepted spellings, in order, are the stage name (the public
// identifier kit uses), the display label, and the numeric id (a string, so
// "5" and "safety_review" are the same stage).
//
// The order matters: R4 makes name ∪ label unique, but a stage name may look
// like another stage's id, and the name is the contract — resolving the id
// first would silently pick the wrong stage.
func (s *Service) ResolveKind(raw string) (enum.TaskKind, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, invalid("kind is required（可用环节: %s）", s.AvailableStages())
	}
	if stage, ok := s.flow.ByName(value); ok {
		return enum.TaskKind(stage.ID), nil
	}
	for _, stage := range s.flow.Stages {
		if strings.TrimSpace(stage.Label) != "" && strings.TrimSpace(stage.Label) == value {
			return enum.TaskKind(stage.ID), nil
		}
	}
	if id, err := strconv.Atoi(value); err == nil {
		if stage, ok := s.flow.ByID(id); ok {
			return enum.TaskKind(stage.ID), nil
		}
	}
	return 0, invalid("kind %q 不是流程配置里的环节（可用环节: %s）", raw, s.AvailableStages())
}

// AvailableStages renders the "name(label)" list every mapping error carries, so
// a client can copy a usable value straight out of the 400.
func (s *Service) AvailableStages() string {
	parts := make([]string, 0, len(s.flow.Stages))
	for _, stage := range s.flow.Stages {
		parts = append(parts, fmt.Sprintf("%s(%s)", stage.Name, stage.LabelOrName()))
	}
	return strings.Join(parts, ", ")
}

// Stage returns the configured stage of one id. The boolean is false for a card
// of a stage the configuration no longer has (history after a stage was
// removed); the API renders such a card as kind(<id>) instead of failing.
func (s *Service) Stage(kind enum.TaskKind) (platform.Stage, bool) {
	return s.flow.ByID(int(kind))
}

// Flow returns the process-wide flow snapshot. The API layer projects it (GET
// /v1/flow, kind/kind_id/kind_label) from this one copy, so a running process
// never serves two different flows.
func (s *Service) Flow() platform.FlowConfig { return s.flow }
