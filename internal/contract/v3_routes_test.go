package contract

import (
	"sort"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/api"
)

// TestRoutesV3MatchOpenAPI pins the route contract: every route the V3 router
// exposes must be documented, and every documented operation must be served.
func TestRoutesV3MatchOpenAPI(t *testing.T) {
	document, err := Load("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}

	documented := map[string]bool{}
	for _, op := range document.Operations() {
		documented[op.Method+" "+op.Path] = true
	}

	served := map[string]bool{}
	for _, route := range api.RoutesV3() {
		served[route.Method+" "+route.Path] = true
		if _, ok := document.Operation(route.Method, route.Path); !ok {
			t.Errorf("served route %s %s is not documented", route.Method, route.Path)
		}
	}
	for key := range documented {
		if !served[key] {
			t.Errorf("documented operation %q is not served", key)
		}
	}
}

// TestLegacyRoutesAreNotDocumented keeps the v2 surface from creeping back.
func TestLegacyRoutesAreNotDocumented(t *testing.T) {
	document, err := Load("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}
	legacy := []string{
		"POST /v1/work-items",
		"GET /v1/work-items",
		"POST /v1/work-items/report",
		"POST /v1/projects/requirement",
		"POST /v1/projects/specs",
		"POST /v1/projects/plans",
		"POST /v1/projects/submit",
		"POST /v1/projects/decide",
		"POST /v1/projects/complete",
		"GET /v1/specs",
		"GET /v1/gate-decisions",
	}
	for _, key := range legacy {
		parts := strings.SplitN(key, " ", 2)
		if len(parts) != 2 {
			t.Fatalf("bad legacy route %q", key)
		}
		if _, ok := document.Operation(parts[0], parts[1]); ok {
			t.Errorf("legacy operation %q must be gone from the contract", key)
		}
	}
}

// TestOpenAPIOperationsAreSorted is a cheap guard against accidental duplicates
// in the YAML: operation ids are unique and the operation list is non-empty.
func TestOpenAPIOperationsAreSorted(t *testing.T) {
	document, err := Load("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}
	ops := document.Operations()
	if len(ops) == 0 {
		t.Fatal("openapi has no operations")
	}
	if !sort.SliceIsSorted(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	}) {
		t.Fatal("operations are not sorted")
	}
}
