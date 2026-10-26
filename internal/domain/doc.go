// Package domain holds the pure domain model: entities, value types and state
// invariants. It must not perform I/O and only depends on the standard
// library.
//
// Task-01 only defines the task status constants; the full model (projects,
// tasks, attempts, materials, inbox) is added by Task-03.
package domain

// Task status values.
//
// TODO(Task-03): confirm the final state machine (including review/rejected
// transitions) when the domain model is implemented.
const (
	// TaskPending is queued and not yet claimed by an agent.
	TaskPending = "pending"
	// TaskClaimed has an active lease owned by an agent.
	TaskClaimed = "claimed"
	// TaskReview waits for human review of the submitted work.
	TaskReview = "review"
	// TaskDone is completed and accepted.
	TaskDone = "done"
	// TaskFailed ended unsuccessfully (attempts exhausted or rejected).
	TaskFailed = "failed"
)
