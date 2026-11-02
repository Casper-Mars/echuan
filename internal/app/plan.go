package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Plan batches (tech design §4.5).
//
// A plan is a batch of dev work items added to a project. Cards land blocked and
// the first batch opens the tasks review gate; approval queues them.

const (
	// MaxPlanItems bounds one POST /v1/projects/plans batch.
	MaxPlanItems = 200
)

// PlanItem is one card of a submitted batch. DependsOn holds 1-based sequence
// numbers of the same batch.
type PlanItem struct {
	Title     string
	BodyMD    string
	Priority  int
	DependsOn []int
}

// PlannedWorkItem is one persisted card as reported by SubmitPlan.
type PlannedWorkItem struct {
	WorkID    string
	SeqNo     int
	Kind      string
	Title     string
	Priority  int
	State     string
	DependsOn []string
}

// PlanResult is the outcome of one plan submission.
type PlanResult struct {
	Project   domain.Project
	WorkItems []PlannedWorkItem
}

// SubmitPlan adds one batch of dev cards to a project.
//
// Legal while the project is in tasks_draft, tasks_review or pending_done. When
// the project is still in tasks_draft the batch opens its review gate in the
// same transaction (tasks_draft → tasks_review).
func (s *ProjectService) SubmitPlan(ctx context.Context, projectID, actor string, items []PlanItem) (PlanResult, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return PlanResult{}, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}}
	}

	normalized, verr := validatePlan(items)
	if verr != nil {
		return PlanResult{}, verr
	}

	actor = actorOrDefault(actor)

	var result PlanResult
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		row, err := store.GetProjectByProjectID(ctx, tx, projectID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "project", Key: projectID}
			}
			return err
		}
		if row.Status != domain.StatusTasksDraft &&
			row.Status != domain.StatusTasksReview &&
			row.Status != domain.StatusPendingDone {
			return &domain.InvalidStateError{Reason: fmt.Sprintf(
				"project %q is in status %s; work items can only be added in tasks_draft, tasks_review or pending_done",
				projectID, domain.StatusName(row.Status))}
		}

		updateTime := formatTimestamp(s.nextTimestamp(row.UpdateTime))

		firstSeq, err := store.NextSeqNo(ctx, tx, "work_items", "project_id", row.ID)
		if err != nil {
			return err
		}

		created := make([]int64, len(normalized))
		workIDs := make([]string, len(normalized))
		for i, item := range normalized {
			workID, err := platform.NewWorkID(s.timestamp())
			if err != nil {
				return err
			}
			workIDs[i] = workID
			seq := firstSeq + i
			id, err := store.CreateWorkItem(ctx, tx, store.NewWorkItem{
				WorkID:      workID,
				ProjectID:   row.ID,
				Kind:        "dev",
				SeqNo:       seq,
				Title:       item.Title,
				BodyMD:      item.BodyMD,
				Priority:    item.Priority,
				State:       store.WorkItemStateBlocked,
				MaxAttempts: s.maxAttempts,
				CreateTime:  updateTime,
				UpdateTime:  updateTime,
			})
			if err != nil {
				if errors.Is(err, store.ErrUniqueViolation) {
					return &domain.ConflictError{Reason: fmt.Sprintf(
						"work item %q already exists in project %q", workID, projectID)}
				}
				return err
			}
			created[i] = id
		}

		for i, item := range normalized {
			for _, dependsOn := range item.DependsOn {
				if err := store.InsertWorkDependency(ctx, tx, created[i], created[dependsOn-1]); err != nil {
					return err
				}
			}
		}

		if row.Status == domain.StatusTasksDraft {
			next, err := domain.ProjectNextStatus(row.Status, domain.ProjectActionAddFirstTask, "")
			if err != nil {
				return err
			}
			affected, err := store.TransitionProject(ctx, tx, row.ID,
				row.Status, row.Version, next, row.Reason, actor, updateTime)
			if err != nil {
				return err
			}
			if affected == 0 {
				fresh, readErr := store.GetProjectByProjectID(ctx, tx, projectID)
				if readErr != nil {
					return readErr
				}
				return &domain.InvalidVersionError{Current: fresh.Version, Expected: row.Version}
			}
		}

		result, err = s.planResult(ctx, tx, row.ID, created)
		return err
	})
	if err != nil {
		return PlanResult{}, err
	}
	return result, nil
}

// planResult reads the committed batch back inside the transaction.
func (s *ProjectService) planResult(ctx context.Context, q store.Querier, projectPhysicalID int64, created []int64) (PlanResult, error) {
	fresh, err := store.GetProjectByID(ctx, q, projectPhysicalID)
	if err != nil {
		return PlanResult{}, err
	}
	project, err := projectFromRow(fresh)
	if err != nil {
		return PlanResult{}, err
	}

	rows, err := store.ListWorkItemsByProject(ctx, q, projectPhysicalID)
	if err != nil {
		return PlanResult{}, err
	}
	deps, err := store.ListWorkDependencies(ctx, q, projectPhysicalID)
	if err != nil {
		return PlanResult{}, err
	}

	inBatch := make(map[int64]struct{}, len(created))
	for _, id := range created {
		inBatch[id] = struct{}{}
	}

	codeByID := make(map[int64]string, len(rows))
	for _, row := range rows {
		codeByID[row.ID] = row.WorkID
	}
	depsByItem := make(map[int64][]string, len(created))
	for _, dep := range deps {
		if _, ok := inBatch[dep.WorkItemID]; !ok {
			continue
		}
		code, ok := codeByID[dep.DependsOn]
		if !ok {
			return PlanResult{}, fmt.Errorf("store: work_deps %d -> %d references an item outside project %d",
				dep.WorkItemID, dep.DependsOn, projectPhysicalID)
		}
		depsByItem[dep.WorkItemID] = append(depsByItem[dep.WorkItemID], code)
	}

	planned := make([]PlannedWorkItem, 0, len(created))
	for _, row := range rows {
		if _, ok := inBatch[row.ID]; !ok {
			continue
		}
		dependsOn := depsByItem[row.ID]
		if dependsOn == nil {
			dependsOn = []string{}
		}
		planned = append(planned, PlannedWorkItem{
			WorkID:    row.WorkID,
			SeqNo:     row.SeqNo,
			Kind:      row.Kind,
			Title:     row.Title,
			Priority:  row.Priority,
			State:     row.State,
			DependsOn: dependsOn,
		})
	}
	return PlanResult{Project: project, WorkItems: planned}, nil
}

// validatePlan normalises the batch and returns every field-level failure.
func validatePlan(items []PlanItem) ([]PlanItem, *domain.ValidationError) {
	var fields []domain.FieldError

	switch {
	case len(items) == 0:
		fields = append(fields, domain.FieldError{Field: "work_items", Reason: "must contain at least 1 item"})
	case len(items) > MaxPlanItems:
		fields = append(fields, domain.FieldError{
			Field:  "work_items",
			Reason: fmt.Sprintf("must contain at most %d items", MaxPlanItems),
		})
		return nil, &domain.ValidationError{Fields: fields}
	}

	normalized := make([]PlanItem, len(items))
	for i, item := range items {
		prefix := fmt.Sprintf("work_items[%d]", i)
		normalized[i] = item

		normalized[i].Title = strings.TrimSpace(item.Title)
		switch {
		case normalized[i].Title == "":
			fields = append(fields, domain.FieldError{Field: prefix + ".title", Reason: "is required"})
		case utf8.RuneCountInString(normalized[i].Title) > MaxWorkItemTitleLen:
			fields = append(fields, domain.FieldError{
				Field:  prefix + ".title",
				Reason: fmt.Sprintf("must be at most %d characters", MaxWorkItemTitleLen),
			})
		}

		if strings.TrimSpace(item.BodyMD) == "" {
			fields = append(fields, domain.FieldError{Field: prefix + ".body_md", Reason: "is required"})
		} else if len(item.BodyMD) > MaxWorkItemBodyBytes {
			fields = append(fields, domain.FieldError{
				Field:  prefix + ".body_md",
				Reason: fmt.Sprintf("must be at most %d bytes", MaxWorkItemBodyBytes),
			})
		}

		if item.Priority < MinWorkItemPriority || item.Priority > MaxWorkItemPriority {
			fields = append(fields, domain.FieldError{
				Field:  prefix + ".priority",
				Reason: fmt.Sprintf("must be between %d and %d", MinWorkItemPriority, MaxWorkItemPriority),
			})
		}

		seen := make(map[int]struct{}, len(item.DependsOn))
		dependsOn := make([]int, 0, len(item.DependsOn))
		for _, dep := range item.DependsOn {
			if dep < 1 || dep > len(items) {
				fields = append(fields, domain.FieldError{
					Field:  prefix + ".depends_on",
					Reason: fmt.Sprintf("must be between 1 and %d", len(items)),
				})
				continue
			}
			if dep == i+1 {
				fields = append(fields, domain.FieldError{
					Field:  prefix + ".depends_on",
					Reason: "must not contain the item itself",
				})
				continue
			}
			if _, dup := seen[dep]; dup {
				fields = append(fields, domain.FieldError{
					Field:  prefix + ".depends_on",
					Reason: fmt.Sprintf("must not contain duplicate %d", dep),
				})
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

	if cycle := findPlanCycle(normalized); len(cycle) > 0 {
		message := "dependency cycle: " + strings.Join(cycle, " → ")
		return nil, &domain.ValidationError{
			Message: message,
			Fields:  []domain.FieldError{{Field: "work_items", Reason: message}},
		}
	}
	return normalized, nil
}

// findPlanCycle detects a cycle in the batch graph (sequence numbers are nodes).
func findPlanCycle(items []PlanItem) []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)

	color := make([]int, len(items)+1)
	stack := make([]int, 0, len(items))

	var visit func(node int) []string
	visit = func(node int) []string {
		color[node] = grey
		stack = append(stack, node)

		for _, dep := range items[node-1].DependsOn {
			switch color[dep] {
			case grey:
				from := 0
				for i, stacked := range stack {
					if stacked == dep {
						from = i
						break
					}
				}
				cycle := make([]string, 0, len(stack)-from+1)
				for _, n := range stack[from:] {
					cycle = append(cycle, fmt.Sprintf("%d", n))
				}
				return append(cycle, fmt.Sprintf("%d", dep))
			case white:
				if cycle := visit(dep); len(cycle) > 0 {
					return cycle
				}
			}
		}

		stack = stack[:len(stack)-1]
		color[node] = black
		return nil
	}

	for node := 1; node <= len(items); node++ {
		if color[node] == white {
			if cycle := visit(node); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}
