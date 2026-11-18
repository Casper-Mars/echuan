// Package taskstate is the pure V3 task state machine.
//
// Persistence (leases, transactions) lives in internal/store/v3; this package
// only answers "is this transition legal", "what state comes next" and "what
// label does the UI show". Keeping it pure makes the 7×7 transition table
// exhaustively testable without a database.
package taskstate

import (
	"fmt"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// Initial returns the status of a newly created task: ready when every
// dependency is done (or there is none), pending otherwise.
func Initial(hasUnmetDependencies bool) enum.TaskStatus {
	if hasUnmetDependencies {
		return enum.Pending
	}
	return enum.Ready
}

// transitions is the complete edge set. Every other pair is illegal.
//
//	pending      → ready (dependency promotion), failed, cancelled
//	ready        → in_progress (claim), failed, cancelled
//	in_progress  → in_review (report), failed (timeout/abort), cancelled
//	in_review    → done (approve), ready (reject), failed, cancelled
//	failed       → ready / pending (reset), cancelled
//	done         → ∅
//	cancelled    → ∅
var transitions = map[enum.TaskStatus]map[enum.TaskStatus]bool{
	enum.Pending: {
		enum.Ready:     true,
		enum.Failed:    true,
		enum.Cancelled: true,
	},
	enum.Ready: {
		enum.InProgress: true,
		enum.Failed:     true,
		enum.Cancelled:  true,
	},
	enum.InProgress: {
		enum.InReview:  true,
		enum.Failed:    true,
		enum.Cancelled: true,
	},
	enum.InReview: {
		enum.Done:      true,
		enum.Ready:     true,
		enum.Failed:    true,
		enum.Cancelled: true,
	},
	enum.Failed: {
		enum.Ready:     true,
		enum.Pending:   true,
		enum.Cancelled: true,
	},
}

// CanTransition reports whether from → to is a legal edge.
func CanTransition(from, to enum.TaskStatus) bool {
	return transitions[from][to]
}

// Validate is CanTransition with an error carrying both names.
func Validate(from, to enum.TaskStatus) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("task state: illegal transition %s → %s", from, to)
}

// AfterClaim is the state of a claimed task.
func AfterClaim() enum.TaskStatus { return enum.InProgress }

// AfterReport is the state after a worker reports.
func AfterReport() enum.TaskStatus { return enum.InReview }

// AfterApprove is the state after a gate approval.
func AfterApprove() enum.TaskStatus { return enum.Done }

// AfterReject is the state after a gate rejection. Rejection returns to ready,
// not pending: the dependencies are still satisfied and the rework must be
// claimable immediately.
func AfterReject() enum.TaskStatus { return enum.Ready }

// AfterFail is the state after an exhausted or aborted execution.
func AfterFail() enum.TaskStatus { return enum.Failed }

// AfterReset is the state after an operator re-queues a failed task.
func AfterReset(hasUnmetDependencies bool) enum.TaskStatus {
	return Initial(hasUnmetDependencies)
}

// AfterCancel is the terminal cancelled state.
func AfterCancel() enum.TaskStatus { return enum.Cancelled }

// IsActive reports whether a task still occupies its project: pending, ready,
// in_progress or in_review. A project with an active task cannot be closed.
func IsActive(status enum.TaskStatus) bool {
	switch status {
	case enum.Pending, enum.Ready, enum.InProgress, enum.InReview:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether no further transition is possible.
func IsTerminal(status enum.TaskStatus) bool {
	return status == enum.Done || status == enum.Cancelled
}

// labels is the kind-specific display text. The state values are shared by all
// four kinds; only the wording differs.
var labels = map[enum.TaskKind]map[enum.TaskStatus]string{
	enum.Requirement: {
		enum.Pending:    "等待前置",
		enum.Ready:      "待处理",
		enum.InProgress: "处理中",
		enum.InReview:   "待审核",
		enum.Done:       "已完成",
		enum.Failed:     "失败",
		enum.Cancelled:  "已取消",
	},
	enum.Design: {
		enum.Pending:    "等待前置",
		enum.Ready:      "待设计",
		enum.InProgress: "设计中",
		enum.InReview:   "待评审",
		enum.Done:       "已完成",
		enum.Failed:     "失败",
		enum.Cancelled:  "已取消",
	},
	enum.Breakdown: {
		enum.Pending:    "等待前置",
		enum.Ready:      "待拆分",
		enum.InProgress: "拆分中",
		enum.InReview:   "待评审",
		enum.Done:       "已完成",
		enum.Failed:     "失败",
		enum.Cancelled:  "已取消",
	},
	enum.Dev: {
		enum.Pending:    "等待前置",
		enum.Ready:      "待开发",
		enum.InProgress: "开发中",
		enum.InReview:   "待验收",
		enum.Done:       "已完成",
		enum.Failed:     "失败",
		enum.Cancelled:  "已取消",
	},
}

// Label returns the display text for one kind/status pair. Unknown pairs fall
// back to the status name so a new enum value cannot render as an empty chip.
func Label(kind enum.TaskKind, status enum.TaskStatus) string {
	if byStatus, ok := labels[kind]; ok {
		if text, ok := byStatus[status]; ok {
			return text
		}
	}
	return status.String()
}

// Labels returns a copy of the label map for one kind.
func Labels(kind enum.TaskKind) map[enum.TaskStatus]string {
	source := labels[kind]
	out := make(map[enum.TaskStatus]string, len(source))
	for status, text := range source {
		out[status] = text
	}
	return out
}
