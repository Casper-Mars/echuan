package contract

// The decide operation documents its one non-obvious side effect: a rejected
// requirement gate enqueues the requirement rework card. The checker's
// Operation type ignores descriptions (it only validates what it enforces), so
// this test reads the YAML text itself and pins the wording the document has to
// carry, plus the request/response structure that must stay untouched.

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// documentedDecide is the minimal view of api/openapi.yaml this test needs.
type documentedDecide struct {
	Paths map[string]struct {
		Post struct {
			OperationID string `yaml:"operationId"`
			Summary     string `yaml:"summary"`
			Description string `yaml:"description"`
		} `yaml:"post"`
	} `yaml:"paths"`
}

// TestDecideContractDocumentsRequirementReworkCard pins AC7: the reject side
// effect, its card shape, its transactional idempotency and the two gates that
// deliberately produce no card.
func TestDecideContractDocumentsRequirementReworkCard(t *testing.T) {
	raw, err := os.ReadFile(contractDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", contractDocPath, err)
	}

	var doc documentedDecide
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", contractDocPath, err)
	}
	decide, ok := doc.Paths["/v1/projects/decide"]
	if !ok {
		t.Fatalf("%s has no /v1/projects/decide path", contractDocPath)
	}
	if decide.Post.OperationID != "decideProject" {
		t.Fatalf("operationId = %q, want decideProject", decide.Post.OperationID)
	}
	if strings.TrimSpace(decide.Post.Description) == "" {
		t.Fatal("decideProject has no description, want the rework-card rule documented")
	}
	for _, want := range []string{
		"需求返工卡", "requirement", "queued", "990",
		"需求撰写", "同事务", "幂等",
		"spec_review", "tasks_review", "不产生任何阶段工作项",
	} {
		if !strings.Contains(decide.Post.Description, want) {
			t.Errorf("decideProject description = %q, want it to mention %q", decide.Post.Description, want)
		}
	}

	// The description is additive: the request/response and error structure of
	// the operation is unchanged.
	parsed, err := Load(contractDocPath)
	if err != nil {
		t.Fatalf("load %s: %v", contractDocPath, err)
	}
	operation := parsed.Paths["/v1/projects/decide"].Post
	if operation.OperationID != "decideProject" {
		t.Errorf("parsed operationId = %q, want decideProject", operation.OperationID)
	}
	for _, code := range []string{"200", "400", "404", "409"} {
		if _, ok := operation.Responses[code]; !ok {
			t.Errorf("decideProject responses = %v, want a documented %s", operation.Responses, code)
		}
	}
	if parsed.Components.Schemas["ProjectDecideRequest"] == nil {
		t.Error("ProjectDecideRequest schema is missing")
	}
}
