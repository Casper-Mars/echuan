package domain

import (
	"fmt"
	"time"
)

// Task status values (tech design §3.2 / §3.3). The integer is canonical; the
// names are code aliases and are never persisted.
const (
	// TaskBacklog is queued and not yet ready to be claimed.
	TaskBacklog = 0
	// TaskReady is queued and claimable.
	TaskReady = 1
	// TaskInProgress is owned by an agent holding a lease.
	TaskInProgress = 2
	// TaskPendingAcceptance waits for human acceptance of the report.
	TaskPendingAcceptance = 3
	// TaskDone is accepted; terminal.
	TaskDone = 4
	// TaskFailed exhausted its attempts.
	TaskFailed = 5
	// TaskBlocked waits for an external dependency.
	TaskBlocked = 6
	// TaskCancelled is abandoned; terminal.
	TaskCancelled = 7
)

// TaskStatusDone is the tasks.status value counted as finished by
// task_stats.done (kept as an alias for the historical name).
const TaskStatusDone = TaskDone

var taskStatusNames = [...]string{
	"backlog",
	"ready",
	"in_progress",
	"pending_acceptance",
	"done",
	"failed",
	"blocked",
	"cancelled",
}

// TaskStatusName maps a tasks.status value to its stable name. Unknown values
// render as "unknown" instead of panicking.
func TaskStatusName(status int) string {
	if status < 0 || status >= len(taskStatusNames) {
		return "unknown"
	}
	return taskStatusNames[status]
}

// TaskTransition is one legal edge of the task state machine.
//
// The task machine is defined by (from, to) only: unlike a project decision
// there is no verdict dimension. Task-11 will expose it over HTTP; Task-06
// uses the same table for its own writes (plan approval, rework, cancellation).
type TaskTransition struct {
	From int
	To   int
}

// taskTransitions mirrors tech design §3.3. done and cancelled are terminal.
var taskTransitions = []TaskTransition{
	// Preparation.
	{From: TaskBacklog, To: TaskReady},
	// Claiming and reporting.
	{From: TaskReady, To: TaskInProgress},
	{From: TaskInProgress, To: TaskPendingAcceptance},
	// Lease expiry puts the task back in the queue.
	{From: TaskInProgress, To: TaskReady},
	// Human acceptance, or rework after a rejection.
	{From: TaskPendingAcceptance, To: TaskDone},
	{From: TaskPendingAcceptance, To: TaskReady},
	// Blocking is allowed from every non-terminal state.
	{From: TaskBacklog, To: TaskBlocked},
	{From: TaskReady, To: TaskBlocked},
	{From: TaskInProgress, To: TaskBlocked},
	{From: TaskPendingAcceptance, To: TaskBlocked},
	{From: TaskFailed, To: TaskBlocked},
	// Unblocking always re-queues.
	{From: TaskBlocked, To: TaskReady},
	// Attempt accounting.
	{From: TaskInProgress, To: TaskFailed},
	{From: TaskFailed, To: TaskReady},
	{From: TaskFailed, To: TaskCancelled},
}

// IsTerminalTaskStatus reports whether a task status accepts no further
// transition.
func IsTerminalTaskStatus(status int) bool {
	return status == TaskDone || status == TaskCancelled
}

// ValidateTaskTransition reports whether from → to is one edge of the task
// state machine.
func ValidateTaskTransition(from, to int) error {
	for _, transition := range taskTransitions {
		if transition.From == from && transition.To == to {
			return nil
		}
	}
	return &InvalidStateError{Reason: fmt.Sprintf(
		"task status %q cannot move to %q", TaskStatusName(from), TaskStatusName(to))}
}

// TaskTargets returns every status reachable in one step from from. The result
// is sorted by status value because taskTransitions is.
func TaskTargets(from int) []int {
	targets := make([]int, 0, len(taskTransitions))
	for _, transition := range taskTransitions {
		if transition.From == from {
			targets = append(targets, transition.To)
		}
	}
	return targets
}

// Task is the domain entity of one unit of work. The transport DTO is built by
// the API layer; Task-06 only needs enough fields to report a decision result.
type Task struct {
	ID           int64
	TaskID       string
	ProjectID    string
	SeqNo        int
	Title        string
	BodyMD       string
	Status       int
	Reason       string
	LastActor    string
	Version      int
	AttemptCount int
	CreateTime   time.Time
	UpdateTime   time.Time
}
