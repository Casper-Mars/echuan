package domain

import (
	"errors"
	"fmt"
	"testing"
)

// projectEdges is the expected legal matrix, written out independently of
// projectTransitions so the test fails if the table drifts.
var projectEdges = map[string]int{
	"0/accept/":        1,
	"0/cancel/":        7,
	"1/decide/approve": 2,
	"1/decide/reject":  1,
	"1/cancel/":        7,
	"2/cancel/":        7,
	"3/decide/approve": 4,
	"3/decide/reject":  2,
	"3/cancel/":        7,
	"4/cancel/":        7,
	"5/decide/approve": 6,
	"5/decide/reject":  4,
	"5/cancel/":        7,
}

func TestProjectTransitionMatrix(t *testing.T) {
	actions := []string{ProjectActionAccept, ProjectActionDecide, ProjectActionCancel}
	decisions := []Decision{"", DecisionApprove, DecisionReject, "bogus"}

	for status := StatusDraft; status <= StatusCancelled; status++ {
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
	for _, status := range []int{StatusAccepted, StatusCancelled} {
		if !IsTerminalProjectStatus(status) {
			t.Errorf("IsTerminalProjectStatus(%d) = false", status)
		}
		for _, action := range []string{ProjectActionAccept, ProjectActionDecide, ProjectActionCancel} {
			if err := ValidateProjectTransition(status, action, DecisionApprove); !errors.Is(err, ErrInvalidState) {
				t.Errorf("status %d action %s: error = %v, want invalid_state", status, action, err)
			}
		}
	}
	for _, status := range []int{StatusDraft, StatusSpecReview, StatusPlanning, StatusPlanReview, StatusDeveloping, StatusDeliveryReview} {
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
	want := []string{"draft", "spec_review", "planning", "plan_review", "developing", "delivery_review", "accepted", "cancelled"}
	for status, name := range want {
		if got := StatusName(status); got != name {
			t.Errorf("StatusName(%d) = %q, want %q", status, got, name)
		}
	}
	if got := StatusName(-1); got != "unknown" {
		t.Errorf("StatusName(-1) = %q", got)
	}
	if got := StatusName(8); got != "unknown" {
		t.Errorf("StatusName(8) = %q", got)
	}
}
