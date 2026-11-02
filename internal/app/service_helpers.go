package app

import (
	"strings"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// invalidField builds a one-field validation error.
func invalidField(field, reason string) *domain.ValidationError {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Reason: reason}}}
}

// decideReason resolves the reason column of one project transition: a submit
// or an automated advance clears it, an approve clears it, and any other
// verdict stores the trimmed reason.
func decideReason(action string, decision domain.Decision, reason string) *string {
	switch {
	case action == domain.ProjectActionSubmit:
		return nil
	case action == domain.ProjectActionAddFirstTask,
		action == domain.ProjectActionReviewTasks,
		action == domain.ProjectActionSettleTasks:
		return nil
	case action == domain.ProjectActionDecide && decision == domain.DecisionApprove:
		return nil
	case strings.TrimSpace(reason) == "":
		return nil
	default:
		value := strings.TrimSpace(reason)
		return &value
	}
}
