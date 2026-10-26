// Package domain holds the pure domain model: entities, value types and state
// invariants. It must not perform I/O and only depends on the standard
// library.
//
// Task-03 adds the Product entity and the shared error types; Task-04 adds the
// Project entity, the material entity and the projects.status table. Tasks,
// attempts and inbox follow in later tasks.
package domain

// Task status names used by the early inbox sketches.
//
// TODO(Task-06): replace these strings with the numeric tasks.status state
// machine (0 backlog … 7 cancelled) and drop this block; the database only
// ever stores the integer.
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
