package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// Breakdown plans (tech design §3.4 / §4.1).
//
// The breakdown card is the only work item whose report has a side effect: it
// carries the plan of the next stage, and approving that plan is what releases
// the dev cards. The plan is therefore validated structurally (never for
// content quality) before anything is written, and the generated dev cards are
// created in the same transaction that completes the breakdown card.

const (
	// MinBreakdownPriority / MaxBreakdownPriority bound a plan item's
	// scheduling hint. The plan is a reviewable artifact, so it uses the
	// narrower documented range rather than the work-item editing range.
	MinBreakdownPriority = 0
	MaxBreakdownPriority = 99

	// breakdownPlanField is the request member that carries the plan.
	breakdownPlanField = "plan"

	// breakdownItemsField is the sequence member of the plan.
	breakdownItemsField = "plan.items"
)

// BreakdownPlanItem is one planned dev card. SeqNo is the plan-local 1..N
// reference used by DependsOn; the persisted work item gets a project-level
// seq_no from NextSeqNo, so the two numbering spaces never mix.
type BreakdownPlanItem struct {
	SeqNo     int
	ProjectID string
	Title     string
	BodyMD    string
	Priority  int
	DependsOn []int
}

// BreakdownPlan is the plan carried by a breakdown report. A nil Items slice
// means the member was omitted (empty plan); an empty non-nil slice means the
// caller explicitly submitted no child (no items).
type BreakdownPlan struct {
	Items []BreakdownPlanItem
}

// validateBreakdownPlan checks one plan against its target project and returns
// the items in seq_no order.
//
// Every failure is reported at the exact request field (plan.items[i].seq_no,
// plan.items[i].project_id, ...). Cross-item failures (a dependency cycle) use
// plan.items with the rendered cycle as both message and reason.
func validateBreakdownPlan(plan *BreakdownPlan, projectCode string) ([]BreakdownPlanItem, *domain.ValidationError) {
	if plan == nil {
		return nil, invalidField(breakdownPlanField, "is required for breakdown work items")
	}
	if plan.Items == nil {
		return nil, invalidField(breakdownItemsField, "is required for breakdown work items")
	}
	if len(plan.Items) == 0 {
		return nil, invalidField(breakdownItemsField, "must contain at least 1 item")
	}
	if len(plan.Items) > MaxPlanItems {
		return nil, invalidField(breakdownItemsField,
			fmt.Sprintf("must contain at most %d items", MaxPlanItems))
	}

	var fields []domain.FieldError
	normalized := make([]BreakdownPlanItem, len(plan.Items))
	count := len(plan.Items)

	for i, item := range plan.Items {
		prefix := fmt.Sprintf("%s[%d]", breakdownItemsField, i)
		normalized[i] = item

		// seq_no must be exactly 1..N in array order. Validating it first keeps
		// the dependency rules below unambiguous: node i is always i+1.
		switch {
		case item.SeqNo == 0:
			fields = append(fields, domain.FieldError{Field: prefix + ".seq_no", Reason: "is required"})
		case item.SeqNo < 1 || item.SeqNo > count:
			fields = append(fields, domain.FieldError{Field: prefix + ".seq_no",
				Reason: fmt.Sprintf("must be between 1 and %d", count)})
		case item.SeqNo != i+1:
			fields = append(fields, domain.FieldError{Field: prefix + ".seq_no",
				Reason: fmt.Sprintf("must be 1..%d in ascending order without gaps or duplicates", count)})
		}

		// project_id is optional, but when present it must name the target.
		if projectID := strings.TrimSpace(item.ProjectID); projectID != "" && projectID != projectCode {
			fields = append(fields, domain.FieldError{Field: prefix + ".project_id",
				Reason: fmt.Sprintf("must equal %q", projectCode)})
		}
		normalized[i].ProjectID = strings.TrimSpace(item.ProjectID)

		normalized[i].Title = strings.TrimSpace(item.Title)
		switch {
		case normalized[i].Title == "":
			fields = append(fields, domain.FieldError{Field: prefix + ".title", Reason: "is required"})
		case utf8.RuneCountInString(normalized[i].Title) > MaxWorkItemTitleLen:
			fields = append(fields, domain.FieldError{Field: prefix + ".title",
				Reason: fmt.Sprintf("must be at most %d characters", MaxWorkItemTitleLen)})
		}

		if strings.TrimSpace(item.BodyMD) == "" {
			fields = append(fields, domain.FieldError{Field: prefix + ".body_md", Reason: "is required"})
		} else if len(item.BodyMD) > MaxWorkItemBodyBytes {
			fields = append(fields, domain.FieldError{Field: prefix + ".body_md",
				Reason: fmt.Sprintf("must be at most %d bytes", MaxWorkItemBodyBytes)})
		}

		if item.Priority < MinBreakdownPriority || item.Priority > MaxBreakdownPriority {
			fields = append(fields, domain.FieldError{Field: prefix + ".priority",
				Reason: fmt.Sprintf("must be between %d and %d", MinBreakdownPriority, MaxBreakdownPriority)})
		}

		seen := make(map[int]struct{}, len(item.DependsOn))
		dependsOn := make([]int, 0, len(item.DependsOn))
		for _, dep := range item.DependsOn {
			if dep < 1 || dep > count {
				fields = append(fields, domain.FieldError{Field: prefix + ".depends_on",
					Reason: fmt.Sprintf("must be between 1 and %d", count)})
				continue
			}
			if dep == item.SeqNo {
				fields = append(fields, domain.FieldError{Field: prefix + ".depends_on",
					Reason: "must not contain the item itself"})
				continue
			}
			if _, dup := seen[dep]; dup {
				fields = append(fields, domain.FieldError{Field: prefix + ".depends_on",
					Reason: fmt.Sprintf("must not contain duplicate %d", dep)})
				continue
			}
			seen[dep] = struct{}{}
			dependsOn = append(dependsOn, dep)
		}
		normalized[i].DependsOn = dependsOn
	}

	if len(fields) > 0 {
		return nil, &domain.ValidationError{Fields: fields}
	}

	// The graph is valid only once every node is exactly 1..N.
	graph := make([]PlanItem, len(normalized))
	for i, item := range normalized {
		graph[i] = PlanItem{DependsOn: item.DependsOn}
	}
	if cycle := findPlanCycle(graph); len(cycle) > 0 {
		message := "dependency cycle: " + strings.Join(cycle, " → ")
		return nil, &domain.ValidationError{
			Message: message,
			Fields:  []domain.FieldError{{Field: breakdownItemsField, Reason: message}},
		}
	}

	return normalized, nil
}
