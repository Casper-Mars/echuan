package im

import (
	"context"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// V3TaskSource projects in_review tasks into V3Review rows and serves the
// attachments / task meta the card needs.
type V3TaskSource struct {
	tasks    *appv3.Service
	projects *appv3.ProjectService
}

// NewV3TaskSource builds the source.
func NewV3TaskSource(tasks *appv3.Service, projects *appv3.ProjectService) *V3TaskSource {
	return &V3TaskSource{tasks: tasks, projects: projects}
}

// PendingReviews lists tasks waiting for a human gate.
func (s *V3TaskSource) PendingReviews(ctx context.Context, limit int) ([]V3Review, error) {
	status := enum.InReview
	tasks, err := s.tasks.ListTasks(ctx, storev3.TaskFilter{Status: &status, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]V3Review, 0, len(tasks))
	for _, task := range tasks {
		reason := ""
		if task.Reason != nil {
			reason = *task.Reason
		}
		worker := ""
		if task.WorkerID != nil {
			worker = *task.WorkerID
		}
		out = append(out, V3Review{
			TaskID: task.TaskID, ProjectID: task.ProjectCode,
			// The project name and the task title stay separate: the title goes
			// into the card header, the project name into its subtitle, and
			// nothing in the header may carry the raw task id.
			ProjectName: s.projectName(ctx, task.ProjectCode), TaskTitle: task.Title,
			Kind: task.Kind.String(), Status: task.Status.String(),
			Reason: reason, WorkerID: worker, Attempt: task.Attempt, MaxAttempts: task.MaxAttempts,
			UpdateTime: task.UpdateTime,
		})
	}
	return out, nil
}

// TaskAttachments returns every attachment of one task, in Web attachment
// order (input rows first, then by the order they were attached).
func (s *V3TaskSource) TaskAttachments(ctx context.Context, taskID string) ([]MaterialRef, error) {
	refs, err := s.tasks.Attachments(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]MaterialRef, 0, len(refs))
	for _, ref := range refs {
		source := ""
		if ref.SourceMaterialID != nil {
			source = *ref.SourceMaterialID
		}
		out = append(out, MaterialRef{
			MaterialID: ref.MaterialID, Title: ref.Title,
			Kind: ref.Kind.String(), Role: ref.Role.String(),
			SourceMaterialID: source,
		})
	}
	return out, nil
}

// TaskMeta resolves the task title and project name for the terminal card.
func (s *V3TaskSource) TaskMeta(ctx context.Context, taskID string) (TaskMeta, error) {
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return TaskMeta{}, err
	}
	return TaskMeta{
		ProjectID: task.ProjectCode, ProjectName: s.projectName(ctx, task.ProjectCode),
		TaskTitle: task.Title,
	}, nil
}

// projectName returns the display name of a project, falling back to its code
// when the lookup fails: a subtitle is cosmetic and must not block a card.
func (s *V3TaskSource) projectName(ctx context.Context, projectID string) string {
	if s.projects != nil {
		if project, err := s.projects.GetProject(ctx, projectID); err == nil {
			return project.Name
		}
	}
	return projectID
}
