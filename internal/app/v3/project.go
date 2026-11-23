package v3

import (
	"context"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// ProjectService is the V3 project use-case layer. A project is only a
// container: name, product, background context and open/closed.
type ProjectService struct {
	st  *storedb.Store
	now func() time.Time
}

// NewProjectService returns a service with the real clock.
func NewProjectService(st *storedb.Store) *ProjectService {
	return &ProjectService{st: st, now: time.Now}
}

// WithClock replaces the clock (tests).
func (s *ProjectService) WithClock(now func() time.Time) *ProjectService {
	s.now = now
	return s
}

// CreateProjectRequest is the API-facing project creation request.
type CreateProjectRequest struct {
	ProductID string
	Name      string
	ContextMD string
	Actor     string
}

// CreateProject validates and inserts one project. It never creates a task:
// the project is a container, and the caller decides what work exists.
func (s *ProjectService) CreateProject(ctx context.Context, req CreateProjectRequest) (storev3.Project, error) {
	if strings.TrimSpace(req.ProductID) == "" {
		return storev3.Project{}, invalid("product_id is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return storev3.Project{}, invalid("name is required")
	}
	project, err := storev3.CreateProject(ctx, s.st, storev3.CreateProjectInput{
		ProductID: req.ProductID,
		Name:      req.Name,
		ContextMD: req.ContextMD,
		Actor:     req.Actor,
		Now:       formatTime(s.now()),
	})
	return project, Translate(err)
}

// GetProject loads one project.
func (s *ProjectService) GetProject(ctx context.Context, projectID string) (storev3.Project, error) {
	if strings.TrimSpace(projectID) == "" {
		return storev3.Project{}, invalid("project_id is required")
	}
	project, err := storev3.GetProject(ctx, s.st.Read(), projectID)
	return project, Translate(err)
}

// ProjectFilter narrows ListProjects. ProductID is the product business id
// (empty means "no product filter"); AfterID is a keyset cursor walking towards
// older projects.
type ProjectFilter struct {
	Status    *enum.ProjectStatus
	ProductID string
	AfterID   int64
	Limit     int
}

// ListProjects lists projects newest first.
func (s *ProjectService) ListProjects(ctx context.Context, filter ProjectFilter) ([]storev3.Project, error) {
	projects, err := storev3.ListProjects(ctx, s.st.Read(), filter.Status, filter.ProductID, filter.AfterID, filter.Limit)
	return projects, Translate(err)
}

// UpdateProjectRequest edits the container metadata.
type UpdateProjectRequest struct {
	ProjectID string
	Name      string
	ContextMD string
	Actor     string
}

// UpdateProject edits name and background context.
func (s *ProjectService) UpdateProject(ctx context.Context, req UpdateProjectRequest) (storev3.Project, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return storev3.Project{}, invalid("project_id is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return storev3.Project{}, invalid("name is required")
	}
	if err := storev3.UpdateProjectContext(ctx, s.st, req.ProjectID, req.Name, req.ContextMD, req.Actor, formatTime(s.now())); err != nil {
		return storev3.Project{}, Translate(err)
	}
	return s.GetProject(ctx, req.ProjectID)
}

// CloseProject closes a project. Unfinished tasks are a 409 unless force is
// set; force cancels them and writes a cancelled event per task.
func (s *ProjectService) CloseProject(ctx context.Context, projectID string, force bool, actor string) error {
	if strings.TrimSpace(projectID) == "" {
		return invalid("project_id is required")
	}
	return Translate(storev3.CloseProject(ctx, s.st, projectID, force, actor, formatTime(s.now())))
}

// ReopenProject flips a closed project back to open.
func (s *ProjectService) ReopenProject(ctx context.Context, projectID, actor string) error {
	if strings.TrimSpace(projectID) == "" {
		return invalid("project_id is required")
	}
	return Translate(storev3.ReopenProject(ctx, s.st, projectID, actor, formatTime(s.now())))
}
