package currentver

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"sort"
	"testing"
)

// fixture mirrors one entry of the cross-language fixture file. Web reads the
// very same file (web/scripts/current-versions-parity.mjs), so a rule change
// must touch both sides in one batch (design v0.2 §7.1.2 lock 1).
type fixture struct {
	Cases []struct {
		Name            string   `json:"name"`
		Refs            []Ref    `json:"refs"`
		WantMaterialIDs []string `json:"want_material_ids"`
	} `json:"cases"`
}

const (
	fixturePath = "testdata/current-versions-cases.json"
	// webImplPath is web/src/lib/current-versions.ts, relative to this package.
	webImplPath = "../../web/src/lib/current-versions.ts"
	// minFixtureCases keeps the fixture from shrinking below the coverage the
	// card requires (R-a…R-f).
	minFixtureCases = 8
)

// TestCurrentFixtures drives Current with the shared fixture and asserts the
// material_id sequence, which pins both the surviving set and its order.
func TestCurrentFixtures(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	var data fixture
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("parse fixture %s: %v", fixturePath, err)
	}
	if len(data.Cases) < minFixtureCases {
		t.Fatalf("fixture has %d cases, want >= %d (R-a…R-f must stay covered)", len(data.Cases), minFixtureCases)
	}

	for _, c := range data.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := Current(c.Refs)
			gotIDs := make([]string, 0, len(got))
			for _, ref := range got {
				gotIDs = append(gotIDs, ref.MaterialID)
			}
			if !slices.Equal(gotIDs, c.WantMaterialIDs) {
				t.Fatalf("Current() material ids = %v, want %v\nrefs: %s", gotIDs, c.WantMaterialIDs, dumpRefs(c.Refs))
			}
			// The projection only removes rows: it never adds or reorders.
			if len(got) > len(c.Refs) {
				t.Fatalf("Current() grew the set: %d -> %d", len(c.Refs), len(got))
			}
		})
	}
}

// TestKindConstantMatchesWeb is lock 2 of the anti-drift design: it parses
// VERSIONED_KINDS straight out of the TypeScript source and requires it to equal
// the Go constant set. Changing either side alone (without the other) fails here.
func TestKindConstantMatchesWeb(t *testing.T) {
	raw, err := os.ReadFile(webImplPath)
	if err != nil {
		t.Fatalf("read %s: %v (the Web side of the parity lock must stay readable)", webImplPath, err)
	}

	webKinds := parseVersionedKinds(t, string(raw))
	goKinds := make([]string, 0, len(versionedKinds))
	for kind := range versionedKinds {
		goKinds = append(goKinds, kind)
	}
	sort.Strings(goKinds)

	if len(goKinds) == 0 || len(webKinds) == 0 {
		t.Fatalf("empty kind set: go=%v web=%v", goKinds, webKinds)
	}
	if !slices.Equal(goKinds, webKinds) {
		t.Fatalf("kind sets drifted: go versionedKinds=%v, web VERSIONED_KINDS=%v (change both in one batch)", goKinds, webKinds)
	}
}

// parseVersionedKinds extracts the string literals of
// `const VERSIONED_KINDS: ReadonlySet<string> = new Set([...])`.
func parseVersionedKinds(t *testing.T, source string) []string {
	t.Helper()
	setPattern := regexp.MustCompile(`(?s)VERSIONED_KINDS[^=]*=\s*new\s+Set\(\s*\[(.*?)\]\s*\)`)
	setMatch := setPattern.FindStringSubmatch(source)
	if setMatch == nil {
		t.Fatalf("%s: cannot find `VERSIONED_KINDS = new Set([...])`", webImplPath)
	}
	literalPattern := regexp.MustCompile(`['"]([^'"]+)['"]`)
	kinds := make([]string, 0, 4)
	for _, match := range literalPattern.FindAllStringSubmatch(setMatch[1], -1) {
		kinds = append(kinds, match[1])
	}
	sort.Strings(kinds)
	return kinds
}

// TestCurrentDoesNotMutateInput guards the "pure function" contract: callers
// pass the ListTaskAttachmentRefs slice directly.
func TestCurrentDoesNotMutateInput(t *testing.T) {
	refs := []Ref{
		{MaterialID: "m_in", Kind: "requirement", Role: "input"},
		{MaterialID: "m_v1", Kind: "requirement", Role: "output"},
		{MaterialID: "m_v2", Kind: "requirement", Role: "output"},
	}
	before := slices.Clone(refs)

	got := Current(refs)

	if !slices.Equal(refs, before) {
		t.Fatalf("Current() mutated its input: before=%v after=%v", before, refs)
	}
	if len(got) == 0 {
		t.Fatalf("Current() returned nothing for a non-empty input")
	}
}

// TestCurrentKeepsUnjudgeableRef covers the "宁多不藏" exit: a ref with an empty
// material_id, an unknown kind, an unknown role and a blank source are kept as
// ordinary rows instead of being filtered out.
//
// Note it is only the *empty id* that must not cause a drop; a versioned output
// with an empty id still loses to a newer output of the same kind under R-d,
// exactly as the Web side does (parity is the point, not leniency).
func TestCurrentKeepsUnjudgeableRef(t *testing.T) {
	refs := []Ref{
		{MaterialID: "", Title: "缺 id 的上下文", Kind: "context", Role: "output", SourceMaterialID: "   "},
		{MaterialID: "m_unknown_kind", Title: "未知 kind", Kind: "mystery", Role: "output"},
		{MaterialID: "m_unknown_role", Title: "未知 role", Kind: "report", Role: "reviewer"},
		{MaterialID: "m_report", Title: "交付报告", Kind: "report", Role: "output"},
	}
	want := []string{"", "m_unknown_kind", "m_unknown_role", "m_report"}

	got := Current(refs)
	gotIDs := make([]string, 0, len(got))
	for _, ref := range got {
		gotIDs = append(gotIDs, ref.MaterialID)
	}
	if !slices.Equal(gotIDs, want) {
		t.Fatalf("Current() = %v, want %v", gotIDs, want)
	}
}

func dumpRefs(refs []Ref) string {
	raw, err := json.Marshal(refs)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(raw)
}
