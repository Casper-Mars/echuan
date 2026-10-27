package domain

import (
	"errors"
	"fmt"
	"testing"
)

// taskDecisionEdges is the expected legal matrix of the four task verdicts,
// written out independently of taskDecisionTransitions so a drift in either
// direction fails the build.
var taskDecisionEdges = map[string]int{
	"3/accept":  4,
	"3/rework":  1,
	"5/retry":   1,
	"5/abandon": 7,
}

// TestTaskDecisionMatrix is the exhaustive 8 statuses × 5 decision values
// matrix: only the four pairs in the table may succeed, everything else must
// be invalid_state.
func TestTaskDecisionMatrix(t *testing.T) {
	decisions := []TaskDecision{
		TaskDecisionAccept, TaskDecisionRework, TaskDecisionRetry,
		TaskDecisionAbandon, "approve", "",
	}

	for status := TaskBacklog; status <= TaskCancelled; status++ {
		for _, decision := range decisions {
			key := fmt.Sprintf("%d/%s", status, decision)
			want, legal := taskDecisionEdges[key]

			got, err := TaskDecisionNextStatus(status, decision)
			if legal {
				if err != nil {
					t.Errorf("%s: unexpected error %v", key, err)
					continue
				}
				if got != want {
					t.Errorf("%s: next = %d, want %d", key, got, want)
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
		}
	}
}

// TestTaskDecisionValid covers every value the wire format can carry,
// including the project verdicts which are not task verdicts.
func TestTaskDecisionValid(t *testing.T) {
	valid := []TaskDecision{TaskDecisionAccept, TaskDecisionRework, TaskDecisionRetry, TaskDecisionAbandon}
	for _, decision := range valid {
		if !decision.Valid() {
			t.Errorf("%q.Valid() = false", decision)
		}
		parsed, ok := ParseTaskDecision(string(decision))
		if !ok || parsed != decision {
			t.Errorf("ParseTaskDecision(%q) = %q/%v", decision, parsed, ok)
		}
	}

	invalid := []TaskDecision{"", "approve", "reject", "Accept", "Retry", "rework "}
	for _, decision := range invalid {
		if decision.Valid() {
			t.Errorf("%q.Valid() = true", decision)
		}
		if _, ok := ParseTaskDecision(string(decision)); ok {
			t.Errorf("ParseTaskDecision(%q) accepted an invalid verdict", decision)
		}
	}
}

// TestTaskDecisionHelpers pins which verdicts need a reason and which reset the
// attempt budget, because the HTTP and store layers branch on both.
func TestTaskDecisionPolicyHelpers(t *testing.T) {
	reason := map[TaskDecision]bool{
		TaskDecisionAccept:  false,
		TaskDecisionRework:  true,
		TaskDecisionRetry:   false,
		TaskDecisionAbandon: true,
	}
	reset := map[TaskDecision]bool{
		TaskDecisionAccept:  false,
		TaskDecisionRework:  false,
		TaskDecisionRetry:   true,
		TaskDecisionAbandon: false,
	}
	for decision, want := range reason {
		if got := TaskDecisionRequiresReason(decision); got != want {
			t.Errorf("TaskDecisionRequiresReason(%q) = %v, want %v", decision, got, want)
		}
	}
	for decision, want := range reset {
		if got := TaskDecisionResetsAttempts(decision); got != want {
			t.Errorf("TaskDecisionResetsAttempts(%q) = %v, want %v", decision, got, want)
		}
	}
}
