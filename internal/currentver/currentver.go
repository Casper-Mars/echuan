// Package currentver is the single place where "current version" is decided on
// the Go side of the task detail projection (tech design v0.2 §7.1).
//
// It is a pure projection: no IO, no clock, no logging. It mirrors
// web/src/lib/current-versions.ts rule by rule, so that the card and the Web
// attachment area show the same set of files:
//
//	R-a role=input is never hidden and never moves;
//	R-b only role=output with kind ∈ {requirement, design, plan, report} takes
//	    part in de-duplication; context / other are listed as-is;
//	R-c the group key is kind (task detail flavour; the project flavour groups by
//	    (task_id, kind) and lives in the Web only);
//	R-d within a group the *last* item wins, and the output keeps input order;
//	R-e a brief (kind=context with a source) is hidden together with its source;
//	R-f true orphan briefs (source never present) and context without a source
//	    are always kept.
//
// R-g (missing task_id) and R-h (reviewTargetId) are explicitly out of scope
// here: the task view does not group by task_id, and the decision path keeps
// using appv3.LatestOutput.
package currentver

import "strings"

// Ref is the minimal shape of one attachment projection. Field names and
// meaning are identical to web/src/lib/current-versions.ts's VersionRef.
type Ref struct {
	MaterialID       string `json:"material_id"`
	Title            string `json:"title"`
	Kind             string `json:"kind"` // requirement|design|plan|report|context|other
	Role             string `json:"role"` // input|output
	SourceMaterialID string `json:"source_material_id,omitempty"`
}

// versionedKinds is the set of body kinds that take part in de-duplication
// (R-b). It must stay identical to VERSIONED_KINDS in
// web/src/lib/current-versions.ts — changing one side alone is caught by
// TestKindConstantMatchesWeb, and that drift would let audits point at another
// version (design §11 R1).
var versionedKinds = map[string]struct{}{
	"requirement": {},
	"design":      {},
	"plan":        {},
	"report":      {},
}

// isVersionedBody reports whether ref is a de-duplicable body version (R-b).
func isVersionedBody(ref Ref) bool {
	if ref.Role != "output" {
		return false
	}
	_, ok := versionedKinds[ref.Kind]
	return ok
}

// groupKeyOf returns the de-duplication group key of ref (R-c: kind), or ""
// when the ref cannot be attributed to a version group (R-a / R-b / R-f
// common exit: those refs are never dropped).
func groupKeyOf(ref Ref) string {
	if !isVersionedBody(ref) {
		return ""
	}
	return ref.Kind
}

// briefSourceOf returns the material id a brief explains, or "" when ref is not
// a brief or its source cannot be determined (missing / blank) — the latter is
// kept as an ordinary row (R-f).
func briefSourceOf(ref Ref) string {
	if ref.Kind != "context" {
		return ""
	}
	return strings.TrimSpace(ref.SourceMaterialID)
}

// Current returns the attachment set visible in the task detail "attachments"
// area, preserving the order of refs (rule semantics: design §7.1.1).
//
// refs must be in ListTaskAttachmentRefs order; callers must not sort. The
// result only removes items: it never adds or reorders, so
// 0 <= len(Current(refs)) <= len(refs). A ref with an empty MaterialID is kept
// (R-f "never hide a file we cannot judge").
func Current(refs []Ref) []Ref {
	// R-d: remember the last index per group; every earlier member is dropped.
	lastIndexByGroup := make(map[string]int, len(refs))
	for index, ref := range refs {
		if key := groupKeyOf(ref); key != "" {
			lastIndexByGroup[key] = index
		}
	}

	// Drop by *index*, never by material id: the same material_id can appear
	// both as an input row and as an output row (an old body re-attached as an
	// input), and id-based bookkeeping would erase that input row as well,
	// violating R-a.
	dropped := make([]bool, len(refs))
	droppedIDs := make(map[string]struct{}, len(refs))
	for index, ref := range refs {
		key := groupKeyOf(ref)
		if key != "" && lastIndexByGroup[key] != index {
			dropped[index] = true
			droppedIDs[ref.MaterialID] = struct{}{}
		}
	}

	// R-e: a brief hides together with a dropped source. Normally the chain is
	// one level deep; iterating to a fixed point also covers "a brief whose
	// source is itself a brief" in either order (R-f keeps real orphans).
	for changed := len(droppedIDs) > 0; changed; {
		changed = false
		for index, ref := range refs {
			if dropped[index] {
				continue
			}
			source := briefSourceOf(ref)
			if source == "" {
				continue
			}
			if _, ok := droppedIDs[source]; !ok {
				continue
			}
			dropped[index] = true
			droppedIDs[ref.MaterialID] = struct{}{}
			changed = true
		}
	}

	out := make([]Ref, 0, len(refs))
	for index, ref := range refs {
		if !dropped[index] {
			out = append(out, ref)
		}
	}
	return out
}
