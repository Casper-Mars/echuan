package im

import (
	"context"

	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// V3TaskSource projects in_review tasks into V3Review rows.
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
		projectName := task.ProjectCode
		if project, err := s.projects.GetProject(ctx, task.ProjectCode); err == nil {
			projectName = project.Name
		}
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
			Kind: task.Kind.String(), Title: projectName + " · " + task.Title, Status: task.Status.String(),
			Reason: reason, WorkerID: worker, Attempt: task.Attempt, MaxAttempts: task.MaxAttempts,
			UpdateTime: task.UpdateTime,
		})
	}
	return out, nil
}
