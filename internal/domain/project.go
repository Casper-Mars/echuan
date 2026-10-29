package domain

import (
	"fmt"
	"time"
)

// Project status values (tech design §3.2). The integer is the canonical value
// stored in projects.status; the names are code aliases and are never
// persisted.
const (
	// StatusRequirementDraft is a freshly created project: the requirement is
	// still editable and has not been submitted for review.
	StatusRequirementDraft = 0
	// StatusRequirementReview waits for a human decision on the requirement.
	StatusRequirementReview = 1
	// StatusSpecDraft is writing the implementation spec.
	StatusSpecDraft = 2
	// StatusSpecReview waits for a human decision on the spec.
	StatusSpecReview = 3
	// StatusTasksDraft is breaking the project down into task cards.
	StatusTasksDraft = 4
	// StatusTasksReview waits for a human decision on the task batch.
	StatusTasksReview = 5
	// StatusPendingDone has every card settled but is not closed yet (the
	// umbrella task is still open).
	StatusPendingDone = 6
	// StatusCompleted is finished; terminal.
	StatusCompleted = 7
	// StatusCancelled ended without acceptance; terminal.
	StatusCancelled = 8
)

// Material kinds (tech design §3.2).
const (
	// MaterialKindRequirement holds the requirement document (kind=1).
	MaterialKindRequirement = 1
	// MaterialKindSpec holds the implementation spec (kind=2).
	MaterialKindSpec = 2
	// MaterialKindReport holds a task report (kind=3).
	MaterialKindReport = 3
)

// Decision is one human verdict on a review gate. The persisted form is the
// string carried by the request body; the state machine only accepts the two
// values below.
type Decision string

const (
	// DecisionApprove accepts the reviewed artifact and advances the state.
	DecisionApprove Decision = "approve"
	// DecisionReject sends the artifact back and requires a reason.
	DecisionReject Decision = "reject"
)

// Valid reports whether d is one of the two supported verdicts.
func (d Decision) Valid() bool {
	return d == DecisionApprove || d == DecisionReject
}

// ParseDecision normalises a wire value into a Decision.
func ParseDecision(value string) (Decision, bool) {
	d := Decision(value)
	return d, d.Valid()
}

// Project actions. "submit" sends the current draft for review, "decide"
// records a verdict and "cancel" aborts a project that is not terminal.
//
// The remaining three actions are internal state moves driven by the task
// endpoints, never by a human request: the first card of a tasks draft opens
// its review, an approved batch moves to pending_done, and a fully settled
// pending_done project closes itself.
const (
	// ProjectActionSubmit moves a draft to its review gate.
	ProjectActionSubmit = "submit"
	// ProjectActionDecide records an approve/reject verdict.
	ProjectActionDecide = "decide"
	// ProjectActionCancel moves any non-terminal project to cancelled.
	ProjectActionCancel = "cancel"
	// ProjectActionAddFirstTask moves a tasks draft to tasks review.
	ProjectActionAddFirstTask = "add_first_task"
	// ProjectActionReviewTasks moves a reviewed batch to pending_done.
	ProjectActionReviewTasks = "review_tasks"
	// ProjectActionSettleTasks closes a pending_done project.
	ProjectActionSettleTasks = "settle_tasks"
)

// ProjectTransition is one legal edge of the project state machine. It is the
// single authoritative allow-list: every (from, action, decision) combination
// absent from projectTransitions is rejected with 409 invalid_state.
//
// Decision is empty for submit, cancel and the internal moves, which carry no
// verdict.
type ProjectTransition struct {
	From     int
	Action   string
	Decision Decision
	To       int
}

// projectTransitions mirrors the nine-state machine exactly. Terminal statuses
// (completed, cancelled) have no outgoing edge, which is what makes every
// action on them an invalid state.
var projectTransitions = []ProjectTransition{
	{From: StatusRequirementDraft, Action: ProjectActionSubmit, Decision: "", To: StatusRequirementReview},

	{From: StatusRequirementReview, Action: ProjectActionDecide, Decision: DecisionApprove, To: StatusSpecDraft},
	{From: StatusRequirementReview, Action: ProjectActionDecide, Decision: DecisionReject, To: StatusRequirementDraft},

	{From: StatusSpecDraft, Action: ProjectActionSubmit, Decision: "", To: StatusSpecReview},

	{From: StatusSpecReview, Action: ProjectActionDecide, Decision: DecisionApprove, To: StatusTasksDraft},
	{From: StatusSpecReview, Action: ProjectActionDecide, Decision: DecisionReject, To: StatusSpecDraft},

	{From: StatusTasksDraft, Action: ProjectActionAddFirstTask, Decision: "", To: StatusTasksReview},

	{From: StatusTasksReview, Action: ProjectActionReviewTasks, Decision: DecisionApprove, To: StatusPendingDone},
	// A card added while the project waits for its umbrella task (6) lands in
	// backlog again, so the batch approval repeats: the same action turns those
	// cards ready without moving the project, which stays in 6 until every card
	// is settled (tech design §5).
	{From: StatusPendingDone, Action: ProjectActionReviewTasks, Decision: DecisionApprove, To: StatusPendingDone},

	{From: StatusPendingDone, Action: ProjectActionSettleTasks, Decision: "", To: StatusCompleted},

	{From: StatusRequirementDraft, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusRequirementReview, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusSpecDraft, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusSpecReview, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusTasksDraft, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusTasksReview, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusPendingDone, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
}

// IsTerminalProjectStatus reports whether a project status accepts no further
// action.
func IsTerminalProjectStatus(status int) bool {
	return status == StatusCompleted || status == StatusCancelled
}

// ProjectNextStatus returns the target status for one step. An unknown or
// illegal combination yields an InvalidStateError: the caller must re-read the
// resource instead of retrying blindly.
func ProjectNextStatus(current int, action string, decision Decision) (int, error) {
	for _, transition := range projectTransitions {
		if transition.From == current && transition.Action == action && transition.Decision == decision {
			return transition.To, nil
		}
	}
	return 0, &InvalidStateError{Reason: fmt.Sprintf(
		"project status %q does not accept %s", StatusName(current), describeAction(action, decision))}
}

// ValidateProjectTransition is ProjectNextStatus without the target value.
func ValidateProjectTransition(current int, action string, decision Decision) error {
	_, err := ProjectNextStatus(current, action, decision)
	return err
}

// describeAction renders an action/decision pair for error messages.
func describeAction(action string, decision Decision) string {
	if decision == "" {
		return action
	}
	return action + "=" + string(decision)
}

// statusNames is the single place that turns projects.status into a name. The
// column only ever stores the integer.
var statusNames = [...]string{
	"requirement_draft",
	"requirement_review",
	"spec_draft",
	"spec_review",
	"tasks_draft",
	"tasks_review",
	"pending_done",
	"completed",
	"cancelled",
}

// StatusName maps a projects.status value to its stable name. Unknown values
// render as "unknown" instead of panicking: a row written by a newer binary
// must not take the process down.
func StatusName(status int) string {
	if status < 0 || status >= len(statusNames) {
		return "unknown"
	}
	return statusNames[status]
}

// Material is one version of a material chain. The bytes live in the
// content-addressed object store; SHA256/Size describe them and are what the
// API exposes.
//
// Path is deliberately absent: it is a storage detail owned by the store
// package and the domain layer performs no I/O.
type Material struct {
	MaterialID string
	Kind       int
	Version    int
	Title      string
	SHA256     string
	Size       int64
	UploadedBy string
	CreateTime time.Time
}

// TaskStats summarises the tasks of one project.
//
// ByStatus is only filled in by the detail endpoint (the list stays a single
// query); Done counts tasks whose status is TaskStatusDone.
type TaskStats struct {
	Total    int64
	Done     int64
	ByStatus map[int]int64
}

// Project is one requirement being tracked to completion.
//
// ID is the physical row id and stays internal (it only surfaces inside the
// opaque list cursor); ProjectID ("APP-1") is the business id used by the API.
// RequirementMaterialID/SpecMaterialID are physical material ids, 0 when the
// pointer is empty.
type Project struct {
	ID                    int64
	ProjectID             string
	ProductID             string
	SeqNo                 int
	Title                 string
	Status                int
	Reason                string
	LastActor             string
	Version               int
	RequirementMaterialID int64
	SpecMaterialID        int64
	TaskStats             TaskStats
	CreateTime            time.Time
	UpdateTime            time.Time
}

// ProjectDetail is a project plus the material metadata the detail endpoint
// returns. Requirement/Spec are nil when the pointer is empty.
//
// The bodies are deliberately absent: a detail response is a fixed-size
// metadata document, and clients load the bytes from GET /v1/materials using
// Requirement.MaterialID or Spec.MaterialID. That keeps a multi-megabyte
// requirement from being re-serialised on every poll.
type ProjectDetail struct {
	Project     Project
	Requirement *Material
	Spec        *Material
}
