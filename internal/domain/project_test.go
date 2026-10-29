package domain

import (
	"errors"
	"fmt"
	"testing"
)

// projectEdges is the expected legal matrix, written out independently of
// projectTransitions so the test fails if the table drifts.
var projectEdges = map[string]int{
	"0/submit/":              1,
	"1/decide/approve":       2,
	"1/decide/reject":        0,
	"2/submit/":              3,
	"3/decide/approve":       4,
	"3/decide/reject":        2,
	"4/add_first_task/":      5,
	"5/review_tasks/approve": 6,
	"6/settle_tasks/":        7,

	"0/cancel/": 8,
	"1/cancel/": 8,
	"2/cancel/": 8,
	"3/cancel/": 8,
	"4/cancel/": 8,
	"5/cancel/": 8,
	"6/cancel/": 8,
}

func TestProjectTransitionMatrix(t *testing.T) {
	actions := []string{
		ProjectActionSubmit, ProjectActionDecide, ProjectActionCancel,
		ProjectActionAddFirstTask, ProjectActionReviewTasks, ProjectActionSettleTasks,
	}
	decisions := []Decision{"", DecisionApprove, DecisionReject, "bogus"}

	for status := StatusRequirementDraft; status <= StatusCancelled; status++ {
		for _, action := range actions {
			for _, decision := range decisions {
				key := fmt.Sprintf("%d/%s/%s", status, action, decision)
				want, legal := projectEdges[key]

				got, err := ProjectNextStatus(status, action, decision)
				if legal {
					if err != nil {
						t.Errorf("%s: unexpected error %v", key, err)
						continue
					}
					if got != want {
						t.Errorf("%s: next = %d, want %d", key, got, want)
					}
					if err := ValidateProjectTransition(status, action, decision); err != nil {
						t.Errorf("%s: Validate = %v, want nil", key, err)
					}
					continue
				}

				if err == nil {
					t.Errorf("%s: next = %d, want invalid_state", key, got)
					continue
				}
				if !errors.Is(err, ErrInvalidState) {
					t.Errorf("%s: error = %v, want ErrInvalidState", key, err)
				}
				var invalid *InvalidStateError
				if !errors.As(err, &invalid) {
					t.Errorf("%s: error = %T, want *InvalidStateError", key, err)
				}
				if err := ValidateProjectTransition(status, action, decision); err == nil {
					t.Errorf("%s: Validate = nil, want error", key)
				}
			}
		}
	}
}

func TestProjectTerminalStatusesAcceptNothing(t *testing.T) {
	for _, status := range []int{StatusCompleted, StatusCancelled} {
		if !IsTerminalProjectStatus(status) {
			t.Errorf("IsTerminalProjectStatus(%d) = false", status)
		}
		for _, action := range []string{
			ProjectActionSubmit, ProjectActionDecide, ProjectActionCancel,
			ProjectActionAddFirstTask, ProjectActionReviewTasks, ProjectActionSettleTasks,
		} {
			if err := ValidateProjectTransition(status, action, DecisionApprove); !errors.Is(err, ErrInvalidState) {
				t.Errorf("status %d action %s: error = %v, want invalid_state", status, action, err)
			}
		}
	}
	for status := StatusRequirementDraft; status <= StatusPendingDone; status++ {
		if IsTerminalProjectStatus(status) {
			t.Errorf("IsTerminalProjectStatus(%d) = true", status)
		}
	}
}

func TestDecisionParsing(t *testing.T) {
	for _, value := range []string{"approve", "reject"} {
		decision, ok := ParseDecision(value)
		if !ok || string(decision) != value {
			t.Errorf("ParseDecision(%q) = %q/%v", value, decision, ok)
		}
	}
	for _, value := range []string{"", "Approve", "no"} {
		if _, ok := ParseDecision(value); ok {
			t.Errorf("ParseDecision(%q) accepted an invalid decision", value)
		}
	}
}

func TestStatusName(t *testing.T) {
	want := []string{
		"requirement_draft", "requirement_review", "spec_draft", "spec_review",
		"tasks_draft", "tasks_review", "pending_done", "completed", "cancelled",
	}
	for status, name := range want {
		if got := StatusName(status); got != name {
			t.Errorf("StatusName(%d) = %q, want %q", status, got, name)
		}
	}
	if got := StatusName(-1); got != "unknown" {
		t.Errorf("StatusName(-1) = %q", got)
	}
	if got := StatusName(9); got != "unknown" {
		t.Errorf("StatusName(9) = %q", got)
	}
}
