package domain

import (
	"fmt"
	"time"
)

// Project status values (tech design §3.2). The integer is the canonical value
// stored in projects.status; the names are code aliases and are never
// persisted.
const (
	// StatusDraft is a freshly created project: the requirement is its only
	// material and nothing has been planned yet.
	StatusDraft = 0
	// StatusSpecReview waits for a human decision on the submitted spec.
	StatusSpecReview = 1
	// StatusPlanning is being broken down into tasks by the CTO.
	StatusPlanning = 2
	// StatusPlanReview waits for a human decision on the task breakdown.
	StatusPlanReview = 3
	// StatusDeveloping has work in flight.
	StatusDeveloping = 4
	// StatusDeliveryReview waits for acceptance of the delivered work.
	StatusDeliveryReview = 5
	// StatusAccepted is finished and accepted; terminal.
	StatusAccepted = 6
	// StatusCancelled ended without acceptance; terminal.
	StatusCancelled = 7
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

// Project actions. "accept" starts the review, "decide" records a verdict and
// "cancel" aborts a project that has not been accepted.
const (
	// ProjectActionAccept moves draft to spec_review.
	ProjectActionAccept = "accept"
	// ProjectActionDecide records an approve/reject verdict.
	ProjectActionDecide = "decide"
	// ProjectActionCancel moves any non-terminal project to cancelled.
	ProjectActionCancel = "cancel"
)

// ProjectTransition is one legal edge of the project state machine. It is the
// single authoritative allow-list: every (from, action, decision) combination
// absent from projectTransitions is rejected with 409 invalid_state.
//
// Decision is empty for accept and cancel, which carry no verdict.
type ProjectTransition struct {
	From     int
	Action   string
	Decision Decision
	To       int
}

// projectTransitions mirrors tech design §3.3 exactly. Terminal statuses
// (accepted, cancelled) have no outgoing edge, which is what makes every
// action on them an invalid state.
var projectTransitions = []ProjectTransition{
	{From: StatusDraft, Action: ProjectActionAccept, Decision: "", To: StatusSpecReview},

	{From: StatusSpecReview, Action: ProjectActionDecide, Decision: DecisionApprove, To: StatusPlanning},
	{From: StatusSpecReview, Action: ProjectActionDecide, Decision: DecisionReject, To: StatusSpecReview},
	{From: StatusPlanReview, Action: ProjectActionDecide, Decision: DecisionApprove, To: StatusDeveloping},
	{From: StatusPlanReview, Action: ProjectActionDecide, Decision: DecisionReject, To: StatusPlanning},
	{From: StatusDeliveryReview, Action: ProjectActionDecide, Decision: DecisionApprove, To: StatusAccepted},
	{From: StatusDeliveryReview, Action: ProjectActionDecide, Decision: DecisionReject, To: StatusDeveloping},

	{From: StatusDraft, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusSpecReview, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusPlanning, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusPlanReview, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusDeveloping, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
	{From: StatusDeliveryReview, Action: ProjectActionCancel, Decision: "", To: StatusCancelled},
}

// IsTerminalProjectStatus reports whether a project status accepts no further
// action.
func IsTerminalProjectStatus(status int) bool {
	return status == StatusAccepted || status == StatusCancelled
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
	"draft",
	"spec_review",
	"planning",
	"plan_review",
	"developing",
	"delivery_review",
	"accepted",
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

// Project is one requirement being tracked to acceptance.
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

// ProjectDetail is a project plus the extra material data the detail endpoint
// returns. Requirement/Spec are nil when the pointer is empty; RequirementMD
// is the requirement body read back from the object store.
type ProjectDetail struct {
	Project       Project
	Requirement   *Material
	RequirementMD string
	Spec          *Material
}
