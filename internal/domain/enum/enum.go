// Package enum is the single source of truth for every persisted enumeration
// in the V3 schema.
//
// Rules (tech design v2.0 §2.1):
//   - every value is an int that starts at 1; 0 is never valid;
//   - the database stores the integer, the JSON boundary uses the string name;
//   - business code must use these constants, never a bare number;
//   - a new value must be added here, in the SQL CHECK and in the tests at the
//     same time.
package enum

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"sort"
)

// ── projects.status ─────────────────────────────────────────────────────

// ProjectStatus is projects.status.
type ProjectStatus int

const (
	ProjectOpen   ProjectStatus = 1
	ProjectClosed ProjectStatus = 2
)

var projectStatusNames = map[ProjectStatus]string{
	ProjectOpen:   "open",
	ProjectClosed: "closed",
}

func (v ProjectStatus) String() string { return nameOf(projectStatusNames, v) }
func (v ProjectStatus) Valid() bool    { return validOf(projectStatusNames, v) }
func (v ProjectStatus) Value() (driver.Value, error) {
	return valueOf(v, projectStatusNames, "project status")
}
func (v ProjectStatus) MarshalJSON() ([]byte, error) {
	return marshalOf(v, projectStatusNames, "project status")
}
func (v *ProjectStatus) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, projectStatusNames, "project status")
}
func (v *ProjectStatus) Scan(src any) error {
	return scanOf(src, v, projectStatusNames, "project status")
}

// ParseProjectStatus parses the JSON/database string name.
func ParseProjectStatus(s string) (ProjectStatus, error) {
	return parseOf(projectStatusNames, "project status", s)
}

// ProjectStatuses returns all values in numeric order.
func ProjectStatuses() []ProjectStatus { return valuesOf(projectStatusNames) }

// ── tasks.kind / worker_kinds.kind / gate_decisions.gate ─────────────────

// TaskKind is the task type.
type TaskKind int

const (
	Requirement TaskKind = 1
	Design      TaskKind = 2
	Breakdown   TaskKind = 3
	Dev         TaskKind = 4
)

var taskKindNames = map[TaskKind]string{
	Requirement: "requirement",
	Design:      "design",
	Breakdown:   "breakdown",
	Dev:         "dev",
}

func (v TaskKind) String() string { return nameOf(taskKindNames, v) }
func (v TaskKind) Valid() bool    { return validOf(taskKindNames, v) }
func (v TaskKind) Value() (driver.Value, error) {
	return valueOf(v, taskKindNames, "task kind")
}
func (v TaskKind) MarshalJSON() ([]byte, error) {
	return marshalOf(v, taskKindNames, "task kind")
}
func (v *TaskKind) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, taskKindNames, "task kind")
}
func (v *TaskKind) Scan(src any) error {
	return scanOf(src, v, taskKindNames, "task kind")
}

// ParseTaskKind parses the JSON/database string name.
func ParseTaskKind(s string) (TaskKind, error) { return parseOf(taskKindNames, "task kind", s) }

// TaskKinds returns all values in numeric order.
func TaskKinds() []TaskKind { return valuesOf(taskKindNames) }

// ── tasks.status ────────────────────────────────────────────────────────

// TaskStatus is the task business state. See tech design §3.2:
// pending waits for dependencies, ready is dispatchable, in_progress holds a
// lease, in_review waits for a gate decision, done/failed/cancelled are
// terminal.
type TaskStatus int

const (
	Pending    TaskStatus = 1
	Ready      TaskStatus = 2
	InProgress TaskStatus = 3
	InReview   TaskStatus = 4
	Done       TaskStatus = 5
	Failed     TaskStatus = 6
	Cancelled  TaskStatus = 7
)

var taskStatusNames = map[TaskStatus]string{
	Pending:    "pending",
	Ready:      "ready",
	InProgress: "in_progress",
	InReview:   "in_review",
	Done:       "done",
	Failed:     "failed",
	Cancelled:  "cancelled",
}

func (v TaskStatus) String() string { return nameOf(taskStatusNames, v) }
func (v TaskStatus) Valid() bool    { return validOf(taskStatusNames, v) }
func (v TaskStatus) Value() (driver.Value, error) {
	return valueOf(v, taskStatusNames, "task status")
}
func (v TaskStatus) MarshalJSON() ([]byte, error) {
	return marshalOf(v, taskStatusNames, "task status")
}
func (v *TaskStatus) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, taskStatusNames, "task status")
}
func (v *TaskStatus) Scan(src any) error {
	return scanOf(src, v, taskStatusNames, "task status")
}

// ParseTaskStatus parses the JSON/database string name.
func ParseTaskStatus(s string) (TaskStatus, error) {
	return parseOf(taskStatusNames, "task status", s)
}

// TaskStatuses returns all values in numeric order.
func TaskStatuses() []TaskStatus { return valuesOf(taskStatusNames) }

// ── materials.kind ──────────────────────────────────────────────────────

// MaterialKind is the material content category. It only affects rendering; it
// never drives the workflow.
type MaterialKind int

const (
	MaterialRequirement MaterialKind = 1
	MaterialDesign      MaterialKind = 2
	MaterialPlan        MaterialKind = 3
	MaterialReport      MaterialKind = 4
	MaterialContext     MaterialKind = 5
	MaterialOther       MaterialKind = 6
)

var materialKindNames = map[MaterialKind]string{
	MaterialRequirement: "requirement",
	MaterialDesign:      "design",
	MaterialPlan:        "plan",
	MaterialReport:      "report",
	MaterialContext:     "context",
	MaterialOther:       "other",
}

func (v MaterialKind) String() string { return nameOf(materialKindNames, v) }
func (v MaterialKind) Valid() bool    { return validOf(materialKindNames, v) }
func (v MaterialKind) Value() (driver.Value, error) {
	return valueOf(v, materialKindNames, "material kind")
}
func (v MaterialKind) MarshalJSON() ([]byte, error) {
	return marshalOf(v, materialKindNames, "material kind")
}
func (v *MaterialKind) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, materialKindNames, "material kind")
}
func (v *MaterialKind) Scan(src any) error {
	return scanOf(src, v, materialKindNames, "material kind")
}

// ParseMaterialKind parses the JSON/database string name.
func ParseMaterialKind(s string) (MaterialKind, error) {
	return parseOf(materialKindNames, "material kind", s)
}

// MaterialKinds returns all values in numeric order.
func MaterialKinds() []MaterialKind { return valuesOf(materialKindNames) }

// ── attachments.role ────────────────────────────────────────────────────

// AttachmentRole tells a reference input from a deliverable.
type AttachmentRole int

const (
	Input  AttachmentRole = 1
	Output AttachmentRole = 2
)

var attachmentRoleNames = map[AttachmentRole]string{
	Input:  "input",
	Output: "output",
}

func (v AttachmentRole) String() string { return nameOf(attachmentRoleNames, v) }
func (v AttachmentRole) Valid() bool    { return validOf(attachmentRoleNames, v) }
func (v AttachmentRole) Value() (driver.Value, error) {
	return valueOf(v, attachmentRoleNames, "attachment role")
}
func (v AttachmentRole) MarshalJSON() ([]byte, error) {
	return marshalOf(v, attachmentRoleNames, "attachment role")
}
func (v *AttachmentRole) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, attachmentRoleNames, "attachment role")
}
func (v *AttachmentRole) Scan(src any) error {
	return scanOf(src, v, attachmentRoleNames, "attachment role")
}

// ParseAttachmentRole parses the JSON/database string name.
func ParseAttachmentRole(s string) (AttachmentRole, error) {
	return parseOf(attachmentRoleNames, "attachment role", s)
}

// AttachmentRoles returns all values in numeric order.
func AttachmentRoles() []AttachmentRole { return valuesOf(attachmentRoleNames) }

// ── gate_decisions.decision ─────────────────────────────────────────────

// Decision is one human verdict on a task gate.
type Decision int

const (
	Approve Decision = 1
	Reject  Decision = 2
)

var decisionNames = map[Decision]string{
	Approve: "approve",
	Reject:  "reject",
}

func (v Decision) String() string { return nameOf(decisionNames, v) }
func (v Decision) Valid() bool    { return validOf(decisionNames, v) }
func (v Decision) Value() (driver.Value, error) {
	return valueOf(v, decisionNames, "decision")
}
func (v Decision) MarshalJSON() ([]byte, error) {
	return marshalOf(v, decisionNames, "decision")
}
func (v *Decision) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, decisionNames, "decision")
}
func (v *Decision) Scan(src any) error { return scanOf(src, v, decisionNames, "decision") }

// ParseDecision parses the JSON/database string name.
func ParseDecision(s string) (Decision, error) { return parseOf(decisionNames, "decision", s) }

// Decisions returns all values in numeric order.
func Decisions() []Decision { return valuesOf(decisionNames) }

// ── task_events.event ───────────────────────────────────────────────────

// Event is the append-only task event type.
type Event int

const (
	EventCreated        Event = 1
	EventClaimed        Event = 2
	EventRenewed        Event = 3
	EventReported       Event = 4
	EventApproved       Event = 5
	EventRejected       Event = 6
	EventFailed         Event = 7
	EventCancelled      Event = 8
	EventFlowAutoCreate Event = 9
	EventPromoted       Event = 10
	EventReset          Event = 11
)

var eventNames = map[Event]string{
	EventCreated:        "created",
	EventClaimed:        "claimed",
	EventRenewed:        "renewed",
	EventReported:       "reported",
	EventApproved:       "approved",
	EventRejected:       "rejected",
	EventFailed:         "failed",
	EventCancelled:      "cancelled",
	EventFlowAutoCreate: "flow.auto_create",
	EventPromoted:       "promoted",
	EventReset:          "reset",
}

func (v Event) String() string { return nameOf(eventNames, v) }
func (v Event) Valid() bool    { return validOf(eventNames, v) }
func (v Event) Value() (driver.Value, error) {
	return valueOf(v, eventNames, "task event")
}
func (v Event) MarshalJSON() ([]byte, error) {
	return marshalOf(v, eventNames, "task event")
}
func (v *Event) UnmarshalJSON(b []byte) error { return unmarshalOf(b, v, eventNames, "task event") }
func (v *Event) Scan(src any) error           { return scanOf(src, v, eventNames, "task event") }

// ParseEvent parses the JSON/database string name.
func ParseEvent(s string) (Event, error) { return parseOf(eventNames, "task event", s) }

// Events returns all values in numeric order.
func Events() []Event { return valuesOf(eventNames) }

// ── workers.state ───────────────────────────────────────────────────────

// WorkerState is workers.state.
type WorkerState int

const (
	WorkerIdle WorkerState = 1
	WorkerBusy WorkerState = 2
)

var workerStateNames = map[WorkerState]string{
	WorkerIdle: "idle",
	WorkerBusy: "busy",
}

func (v WorkerState) String() string { return nameOf(workerStateNames, v) }
func (v WorkerState) Valid() bool    { return validOf(workerStateNames, v) }
func (v WorkerState) Value() (driver.Value, error) {
	return valueOf(v, workerStateNames, "worker state")
}
func (v WorkerState) MarshalJSON() ([]byte, error) {
	return marshalOf(v, workerStateNames, "worker state")
}
func (v *WorkerState) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, workerStateNames, "worker state")
}
func (v *WorkerState) Scan(src any) error {
	return scanOf(src, v, workerStateNames, "worker state")
}

// ParseWorkerState parses the JSON/database string name.
func ParseWorkerState(s string) (WorkerState, error) {
	return parseOf(workerStateNames, "worker state", s)
}

// WorkerStates returns all values in numeric order.
func WorkerStates() []WorkerState { return valuesOf(workerStateNames) }

// ── im_card.subject_kind ────────────────────────────────────────────────

// SubjectKind is the IM card subject type.
type SubjectKind int

const (
	SubjectProject SubjectKind = 1
	SubjectTask    SubjectKind = 2
)

var subjectKindNames = map[SubjectKind]string{
	SubjectProject: "project",
	SubjectTask:    "task",
}

func (v SubjectKind) String() string { return nameOf(subjectKindNames, v) }
func (v SubjectKind) Valid() bool    { return validOf(subjectKindNames, v) }
func (v SubjectKind) Value() (driver.Value, error) {
	return valueOf(v, subjectKindNames, "subject kind")
}
func (v SubjectKind) MarshalJSON() ([]byte, error) {
	return marshalOf(v, subjectKindNames, "subject kind")
}
func (v *SubjectKind) UnmarshalJSON(b []byte) error {
	return unmarshalOf(b, v, subjectKindNames, "subject kind")
}
func (v *SubjectKind) Scan(src any) error {
	return scanOf(src, v, subjectKindNames, "subject kind")
}

// ParseSubjectKind parses the JSON/database string name.
func ParseSubjectKind(s string) (SubjectKind, error) {
	return parseOf(subjectKindNames, "subject kind", s)
}

// SubjectKinds returns all values in numeric order.
func SubjectKinds() []SubjectKind { return valuesOf(subjectKindNames) }

// ── generic helpers ─────────────────────────────────────────────────────

func nameOf[T ~int](names map[T]string, v T) string {
	if name, ok := names[v]; ok {
		return name
	}
	return fmt.Sprintf("invalid(%d)", int(v))
}

func validOf[T ~int](names map[T]string, v T) bool {
	_, ok := names[v]
	return ok
}

func parseOf[T ~int](names map[T]string, field, s string) (T, error) {
	for v, name := range names {
		if name == s {
			return v, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("%s: unknown value %q", field, s)
}

func valuesOf[T ~int](names map[T]string) []T {
	values := make([]T, 0, len(names))
	for v := range names {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func valueOf[T ~int](v T, names map[T]string, field string) (driver.Value, error) {
	if !validOf(names, v) {
		return nil, fmt.Errorf("%s: invalid value %d", field, int(v))
	}
	return int64(v), nil
}

func scanOf[T ~int](src any, dst *T, names map[T]string, field string) error {
	var value int64
	switch raw := src.(type) {
	case int64:
		value = raw
	case int:
		value = int64(raw)
	case []byte:
		if _, err := fmt.Sscan(string(raw), &value); err != nil {
			return fmt.Errorf("%s: scan %q: %w", field, string(raw), err)
		}
	case string:
		if _, err := fmt.Sscan(raw, &value); err != nil {
			return fmt.Errorf("%s: scan %q: %w", field, raw, err)
		}
	case nil:
		return fmt.Errorf("%s: cannot scan NULL", field)
	default:
		return fmt.Errorf("%s: unsupported scan type %T", field, src)
	}
	converted := T(value)
	if !validOf(names, converted) {
		return fmt.Errorf("%s: invalid value %d", field, value)
	}
	*dst = converted
	return nil
}

func marshalOf[T ~int](v T, names map[T]string, field string) ([]byte, error) {
	if !validOf(names, v) {
		return nil, fmt.Errorf("%s: invalid value %d", field, int(v))
	}
	return json.Marshal(names[v])
}

func unmarshalOf[T ~int](b []byte, dst *T, names map[T]string, field string) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	value, err := parseOf(names, field, name)
	if err != nil {
		return err
	}
	*dst = value
	return nil
}
