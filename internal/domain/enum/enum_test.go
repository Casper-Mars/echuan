package enum

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// enumSpec is the per-type surface used by the generic contract test. The
// closures are written at the call site so that method resolution happens on
// the concrete type, not on the type parameter.
type enumSpec[T ~int] struct {
	label  string
	values []T
	names  []string
	max    int
	// open marks an enum whose legal set is "any positive id" rather than the
	// built-in name map (TaskKind: the stage vocabulary belongs to the flow
	// configuration, ADR-0003 D2"). max stays the highest built-in value, so a
	// number above it is legal but has no name.
	open     bool
	valid    func(T) bool
	text     func(T) string
	parse    func(string) (T, error)
	value    func(T) (driver.Value, error)
	scan     func(any) (T, error)
	toJSON   func(T) ([]byte, error)
	fromJSON func([]byte) (T, error)
}

func testEnumContract[T ~int](t *testing.T, spec enumSpec[T]) {
	t.Helper()

	if len(spec.values) != len(spec.names) {
		t.Fatalf("%s: values/names length mismatch", spec.label)
	}

	for i, v := range spec.values {
		wantName := spec.names[i]

		if !spec.valid(v) {
			t.Errorf("%s: %d should be valid", spec.label, v)
		}
		if got := spec.text(v); got != wantName {
			t.Errorf("%s: String(%d) = %q, want %q", spec.label, v, got, wantName)
		}
		parsed, err := spec.parse(wantName)
		if err != nil {
			t.Errorf("%s: Parse(%q) error: %v", spec.label, wantName, err)
		} else if parsed != v {
			t.Errorf("%s: Parse(%q) = %d, want %d", spec.label, wantName, parsed, v)
		}

		value, err := spec.value(v)
		if err != nil {
			t.Errorf("%s: Value(%d) error: %v", spec.label, v, err)
		} else if value != int64(v) {
			t.Errorf("%s: Value(%d) = %v, want %d", spec.label, v, value, int64(v))
		}

		for _, raw := range []any{int64(v), int(v), []byte(strconv.Itoa(int(v))), strconv.Itoa(int(v))} {
			scanned, err := spec.scan(raw)
			if err != nil {
				t.Errorf("%s: Scan(%T %v) error: %v", spec.label, raw, raw, err)
			} else if scanned != v {
				t.Errorf("%s: Scan(%T %v) = %d, want %d", spec.label, raw, raw, scanned, v)
			}
		}

		blob, err := spec.toJSON(v)
		if err != nil {
			t.Errorf("%s: MarshalJSON(%d) error: %v", spec.label, v, err)
		} else if string(blob) != `"`+wantName+`"` {
			t.Errorf("%s: MarshalJSON(%d) = %s, want %q", spec.label, v, blob, wantName)
		}
		decoded, err := spec.fromJSON([]byte(`"` + wantName + `"`))
		if err != nil {
			t.Errorf("%s: UnmarshalJSON(%q) error: %v", spec.label, wantName, err)
		} else if decoded != v {
			t.Errorf("%s: UnmarshalJSON(%q) = %d, want %d", spec.label, wantName, decoded, v)
		}
	}

	// 0 is always invalid: it is the Go zero value and the database rejects it.
	if spec.valid(0) {
		t.Errorf("%s: 0 must be invalid", spec.label)
	}
	if got := spec.text(0); !strings.HasPrefix(got, "invalid(") {
		t.Errorf("%s: String(0) = %q, want invalid(...)", spec.label, got)
	}
	if _, err := spec.value(0); err == nil {
		t.Errorf("%s: Value(0) should fail", spec.label)
	}
	if _, err := spec.scan(int64(0)); err == nil {
		t.Errorf("%s: Scan(0) should fail", spec.label)
	}
	if _, err := spec.scan(nil); err == nil {
		t.Errorf("%s: Scan(nil) should fail", spec.label)
	}
	if _, err := spec.toJSON(0); err == nil {
		t.Errorf("%s: MarshalJSON(0) should fail", spec.label)
	}
	if _, err := spec.parse("nope"); err == nil {
		t.Errorf("%s: Parse(nope) should fail", spec.label)
	}
	if _, err := spec.fromJSON([]byte(`"nope"`)); err == nil {
		t.Errorf("%s: UnmarshalJSON(nope) should fail", spec.label)
	}
	if _, err := spec.fromJSON([]byte(`1`)); err == nil {
		t.Errorf("%s: UnmarshalJSON(number) should fail", spec.label)
	}

	// max+1 is out of range for a closed enum, and an unnamed-but-legal id for
	// an open one.
	out := T(spec.max + 1)
	if spec.open {
		checkOpenValue(t, spec, out)
	} else {
		if spec.valid(out) {
			t.Errorf("%s: %d must be invalid", spec.label, out)
		}
		if _, err := spec.value(out); err == nil {
			t.Errorf("%s: Value(%d) should fail", spec.label, out)
		}
		if _, err := spec.scan(int64(out)); err == nil {
			t.Errorf("%s: Scan(%d) should fail", spec.label, out)
		}
	}
}

// checkOpenValue pins the open-enum surface for one id above the built-in set:
// it is a legal value (persisted, scanned, validated) but has no name, so the
// string and JSON surfaces refuse it instead of inventing one.
func checkOpenValue[T ~int](t *testing.T, spec enumSpec[T], v T) {
	t.Helper()

	if !spec.valid(v) {
		t.Errorf("%s: %d must be valid (open enum)", spec.label, v)
	}
	value, err := spec.value(v)
	if err != nil {
		t.Errorf("%s: Value(%d) error: %v", spec.label, v, err)
	} else if value != int64(v) {
		t.Errorf("%s: Value(%d) = %v, want %d", spec.label, v, value, int64(v))
	}
	if scanned, err := spec.scan(int64(v)); err != nil {
		t.Errorf("%s: Scan(%d) error: %v", spec.label, v, err)
	} else if scanned != v {
		t.Errorf("%s: Scan(%d) = %d", spec.label, v, scanned)
	}
	if got, want := spec.text(v), fmt.Sprintf("invalid(%d)", int(v)); got != want {
		t.Errorf("%s: String(%d) = %q, want %q", spec.label, v, got, want)
	}
	if blob, err := spec.toJSON(v); err == nil {
		t.Errorf("%s: MarshalJSON(%d) = %s, want an error (no name)", spec.label, v, blob)
	}
	if _, err := spec.parse(fmt.Sprintf("invalid(%d)", int(v))); err == nil {
		t.Errorf("%s: Parse(%q) should fail", spec.label, fmt.Sprintf("invalid(%d)", int(v)))
	}
}

func TestProjectStatusContract(t *testing.T) {
	testEnumContract(t, enumSpec[ProjectStatus]{
		label:  "project status",
		values: ProjectStatuses(),
		names:  []string{"open", "closed"},
		max:    2,
		valid:  ProjectStatus.Valid,
		text:   ProjectStatus.String,
		parse:  ParseProjectStatus,
		value:  ProjectStatus.Value,
		scan: func(src any) (ProjectStatus, error) {
			var v ProjectStatus
			err := v.Scan(src)
			return v, err
		},
		toJSON: ProjectStatus.MarshalJSON,
		fromJSON: func(b []byte) (ProjectStatus, error) {
			var v ProjectStatus
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestTaskKindContract(t *testing.T) {
	testEnumContract(t, enumSpec[TaskKind]{
		label:  "task kind",
		values: TaskKinds(),
		names:  []string{"requirement", "design", "breakdown", "dev"},
		max:    4,
		open:   true,
		valid:  TaskKind.Valid,
		text:   TaskKind.String,
		parse:  ParseTaskKind,
		value:  TaskKind.Value,
		scan: func(src any) (TaskKind, error) {
			var v TaskKind
			err := v.Scan(src)
			return v, err
		},
		toJSON: TaskKind.MarshalJSON,
		fromJSON: func(b []byte) (TaskKind, error) {
			var v TaskKind
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestTaskStatusContract(t *testing.T) {
	testEnumContract(t, enumSpec[TaskStatus]{
		label:  "task status",
		values: TaskStatuses(),
		names:  []string{"pending", "ready", "in_progress", "in_review", "done", "failed", "cancelled"},
		max:    7,
		valid:  TaskStatus.Valid,
		text:   TaskStatus.String,
		parse:  ParseTaskStatus,
		value:  TaskStatus.Value,
		scan: func(src any) (TaskStatus, error) {
			var v TaskStatus
			err := v.Scan(src)
			return v, err
		},
		toJSON: TaskStatus.MarshalJSON,
		fromJSON: func(b []byte) (TaskStatus, error) {
			var v TaskStatus
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestMaterialKindContract(t *testing.T) {
	testEnumContract(t, enumSpec[MaterialKind]{
		label:  "material kind",
		values: MaterialKinds(),
		names:  []string{"requirement", "design", "plan", "report", "context", "other"},
		max:    6,
		valid:  MaterialKind.Valid,
		text:   MaterialKind.String,
		parse:  ParseMaterialKind,
		value:  MaterialKind.Value,
		scan: func(src any) (MaterialKind, error) {
			var v MaterialKind
			err := v.Scan(src)
			return v, err
		},
		toJSON: MaterialKind.MarshalJSON,
		fromJSON: func(b []byte) (MaterialKind, error) {
			var v MaterialKind
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestAttachmentRoleContract(t *testing.T) {
	testEnumContract(t, enumSpec[AttachmentRole]{
		label:  "attachment role",
		values: AttachmentRoles(),
		names:  []string{"input", "output"},
		max:    2,
		valid:  AttachmentRole.Valid,
		text:   AttachmentRole.String,
		parse:  ParseAttachmentRole,
		value:  AttachmentRole.Value,
		scan: func(src any) (AttachmentRole, error) {
			var v AttachmentRole
			err := v.Scan(src)
			return v, err
		},
		toJSON: AttachmentRole.MarshalJSON,
		fromJSON: func(b []byte) (AttachmentRole, error) {
			var v AttachmentRole
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestDecisionContract(t *testing.T) {
	testEnumContract(t, enumSpec[Decision]{
		label:  "decision",
		values: Decisions(),
		names:  []string{"approve", "reject"},
		max:    2,
		valid:  Decision.Valid,
		text:   Decision.String,
		parse:  ParseDecision,
		value:  Decision.Value,
		scan: func(src any) (Decision, error) {
			var v Decision
			err := v.Scan(src)
			return v, err
		},
		toJSON: Decision.MarshalJSON,
		fromJSON: func(b []byte) (Decision, error) {
			var v Decision
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestEventContract(t *testing.T) {
	testEnumContract(t, enumSpec[Event]{
		label:  "task event",
		values: Events(),
		names: []string{
			"created", "claimed", "renewed", "reported", "approved", "rejected",
			"failed", "cancelled", "flow.auto_create", "promoted", "reset",
		},
		max:   11,
		valid: Event.Valid,
		text:  Event.String,
		parse: ParseEvent,
		value: Event.Value,
		scan: func(src any) (Event, error) {
			var v Event
			err := v.Scan(src)
			return v, err
		},
		toJSON: Event.MarshalJSON,
		fromJSON: func(b []byte) (Event, error) {
			var v Event
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestWorkerStateContract(t *testing.T) {
	testEnumContract(t, enumSpec[WorkerState]{
		label:  "worker state",
		values: WorkerStates(),
		names:  []string{"idle", "busy"},
		max:    2,
		valid:  WorkerState.Valid,
		text:   WorkerState.String,
		parse:  ParseWorkerState,
		value:  WorkerState.Value,
		scan: func(src any) (WorkerState, error) {
			var v WorkerState
			err := v.Scan(src)
			return v, err
		},
		toJSON: WorkerState.MarshalJSON,
		fromJSON: func(b []byte) (WorkerState, error) {
			var v WorkerState
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

func TestSubjectKindContract(t *testing.T) {
	testEnumContract(t, enumSpec[SubjectKind]{
		label:  "subject kind",
		values: SubjectKinds(),
		names:  []string{"project", "task"},
		max:    2,
		valid:  SubjectKind.Valid,
		text:   SubjectKind.String,
		parse:  ParseSubjectKind,
		value:  SubjectKind.Value,
		scan: func(src any) (SubjectKind, error) {
			var v SubjectKind
			err := v.Scan(src)
			return v, err
		},
		toJSON: SubjectKind.MarshalJSON,
		fromJSON: func(b []byte) (SubjectKind, error) {
			var v SubjectKind
			err := v.UnmarshalJSON(b)
			return v, err
		},
	})
}

// TestEnumJSONRoundTripInStruct pins the wire shape: a struct field of an enum
// type serialises to its name, not its integer.
func TestEnumJSONRoundTripInStruct(t *testing.T) {
	type payload struct {
		Kind   TaskKind   `json:"kind"`
		Status TaskStatus `json:"status"`
	}
	in := payload{Kind: Dev, Status: InReview}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(blob) != `{"kind":"dev","status":"in_review"}` {
		t.Fatalf("JSON = %s", blob)
	}
	var out payload
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}
