package v3

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// Service is the V3 task use-case layer. It owns validation, time and error
// translation; persistence stays in store/v3.
type Service struct {
	st          *storedb.Store
	now         func() time.Time
	leaseTTL    time.Duration
	flowEnabled bool
	materials   MaterialReader
}

// New returns a service with the real clock, a 60s default lease and the four
// flow rules enabled.
func New(st *storedb.Store) *Service {
	return &Service{st: st, now: time.Now, leaseTTL: 60 * time.Second, flowEnabled: true}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// WithLeaseTTL changes the default lease duration.
func (s *Service) WithLeaseTTL(ttl time.Duration) *Service {
	if ttl > 0 {
		s.leaseTTL = ttl
	}
	return s
}

// CreateTaskRequest is the API-facing task creation request.
type CreateTaskRequest struct {
	ProjectID     string
	TaskID        string
	ParentTaskID  string
	Kind          enum.TaskKind
	Title         string
	DescriptionMD string
	ContextMD     string
	Priority      int
	MaxAttempts   int
	DependsOn     []string
}

// CreateTask validates and inserts one task.
func (s *Service) CreateTask(ctx context.Context, req CreateTaskRequest) (storev3.Task, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return storev3.Task{}, invalid("project_id is required")
	}
	if strings.TrimSpace(req.TaskID) == "" {
		return storev3.Task{}, invalid("task_id is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return storev3.Task{}, invalid("title is required")
	}
	if !req.Kind.Valid() {
		return storev3.Task{}, invalid("kind: invalid value %d", req.Kind)
	}
	for _, dep := range req.DependsOn {
		if strings.TrimSpace(dep) == "" {
			return storev3.Task{}, invalid("depends_on contains an empty task id")
		}
	}

	task, err := storev3.CreateTask(ctx, s.st, storev3.CreateTaskInput{
		ProjectID:     req.ProjectID,
		TaskID:        req.TaskID,
		ParentTaskID:  req.ParentTaskID,
		Kind:          req.Kind,
		Title:         req.Title,
		DescriptionMD: req.DescriptionMD,
		ContextMD:     req.ContextMD,
		Priority:      req.Priority,
		MaxAttempts:   req.MaxAttempts,
		DependsOn:     req.DependsOn,
		Now:           s.nowString(),
	})
	return task, Translate(err)
}

// GetTask loads one task.
func (s *Service) GetTask(ctx context.Context, taskID string) (storev3.Task, error) {
	task, err := storev3.GetTask(ctx, s.st.Read(), taskID)
	return task, Translate(err)
}

// ListTasks lists tasks with filters.
func (s *Service) ListTasks(ctx context.Context, filter storev3.TaskFilter) ([]storev3.Task, error) {
	tasks, err := storev3.ListTasks(ctx, s.st.Read(), filter)
	return tasks, Translate(err)
}

// UpdateTaskRequest edits an unclaimed task.
type UpdateTaskRequest struct {
	TaskID        string
	Title         string
	DescriptionMD string
	ContextMD     string
	Priority      int
}

// UpdateTask edits title/description/context/priority.
func (s *Service) UpdateTask(ctx context.Context, req UpdateTaskRequest) (storev3.Task, error) {
	if strings.TrimSpace(req.TaskID) == "" {
		return storev3.Task{}, invalid("task_id is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return storev3.Task{}, invalid("title is required")
	}
	task, err := storev3.UpdateTaskContent(ctx, s.st, storev3.UpdateTaskContentInput{
		TaskID:        req.TaskID,
		Title:         req.Title,
		DescriptionMD: req.DescriptionMD,
		ContextMD:     req.ContextMD,
		Priority:      req.Priority,
		Now:           s.nowString(),
	})
	return task, Translate(err)
}

// DeleteTask removes an unclaimed task.
func (s *Service) DeleteTask(ctx context.Context, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return invalid("task_id is required")
	}
	return Translate(storev3.DeleteTask(ctx, s.st, taskID))
}

// Claim leases the next ready task for a worker.
func (s *Service) Claim(ctx context.Context, workerID string, kind enum.TaskKind) (storev3.Task, error) {
	if strings.TrimSpace(workerID) == "" {
		return storev3.Task{}, invalid("worker_id is required")
	}
	if !kind.Valid() {
		return storev3.Task{}, invalid("kind: invalid value %d", kind)
	}
	now := s.now()
	ulid, err := platform.NewULID(now)
	if err != nil {
		return storev3.Task{}, err
	}
	task, err := storev3.ClaimTask(ctx, s.st, storev3.ClaimInput{
		Kind:       kind,
		WorkerID:   workerID,
		LeaseID:    "l_" + ulid,
		Now:        formatTime(now),
		LeaseUntil: formatTime(now.Add(s.leaseTTL)),
	})
	return task, Translate(err)
}

// Renew extends a lease by the service TTL and returns the new expiry.
func (s *Service) Renew(ctx context.Context, taskID, leaseID string) (string, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(leaseID) == "" {
		return "", invalid("task_id and lease_id are required")
	}
	now := s.now()
	until := formatTime(now.Add(s.leaseTTL))
	if err := storev3.RenewLease(ctx, s.st, taskID, leaseID, until, formatTime(now)); err != nil {
		return "", Translate(err)
	}
	return until, nil
}

// Report writes the output attachment and moves the task to in_review.
func (s *Service) Report(ctx context.Context, taskID, leaseID, materialID string) (storev3.Task, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(leaseID) == "" || strings.TrimSpace(materialID) == "" {
		return storev3.Task{}, invalid("task_id, lease_id and material_id are required")
	}
	material, err := storev3.GetMaterial(ctx, s.st.Read(), materialID)
	if err != nil {
		return storev3.Task{}, Translate(err)
	}
	if err := storev3.ReportTaskWithOutput(ctx, s.st, storev3.ReportInput{
		TaskID:     taskID,
		LeaseID:    leaseID,
		MaterialID: material.ID,
		Now:        s.nowString(),
	}); err != nil {
		return storev3.Task{}, Translate(err)
	}
	return s.GetTask(ctx, taskID)
}

// DecideRequest is one gate verdict.
type DecideRequest struct {
	TaskID     string
	Decision   enum.Decision
	MaterialID string
	DecidedBy  string
	Reason     string
}

// Decide records a gate verdict.
func (s *Service) Decide(ctx context.Context, req DecideRequest) (storev3.Task, error) {
	if strings.TrimSpace(req.TaskID) == "" {
		return storev3.Task{}, invalid("task_id is required")
	}
	if !req.Decision.Valid() {
		return storev3.Task{}, invalid("decision: invalid value %d", req.Decision)
	}
	if strings.TrimSpace(req.DecidedBy) == "" {
		return storev3.Task{}, invalid("decided_by is required")
	}
	if req.Decision == enum.Reject && strings.TrimSpace(req.Reason) == "" {
		return storev3.Task{}, invalid("reason is required on reject")
	}

	var materialPK *int64
	if strings.TrimSpace(req.MaterialID) != "" {
		material, err := storev3.GetMaterial(ctx, s.st.Read(), req.MaterialID)
		if err != nil {
			return storev3.Task{}, Translate(err)
		}
		materialPK = &material.ID
	}

	var hook func(context.Context, *sql.Tx, storev3.Task) error
	if req.Decision == enum.Approve && s.flowEnabled {
		current, err := storev3.GetTask(ctx, s.st.Read(), req.TaskID)
		if err != nil {
			return storev3.Task{}, Translate(err)
		}
		hook, err = s.approveHook(ctx, current)
		if err != nil {
			return storev3.Task{}, Translate(err)
		}
	}

	task, err := storev3.DecideTask(ctx, s.st, storev3.DecideInput{
		TaskID:       req.TaskID,
		Decision:     req.Decision,
		MaterialID:   materialPK,
		DecidedBy:    req.DecidedBy,
		Reason:       req.Reason,
		Now:          s.nowString(),
		AfterApprove: hook,
	})
	return task, Translate(err)
}

// Reset re-queues a failed task.
func (s *Service) Reset(ctx context.Context, taskID string) (storev3.Task, error) {
	if strings.TrimSpace(taskID) == "" {
		return storev3.Task{}, invalid("task_id is required")
	}
	if err := storev3.ResetTask(ctx, s.st, taskID, s.nowString()); err != nil {
		return storev3.Task{}, Translate(err)
	}
	return s.GetTask(ctx, taskID)
}

// Fail marks a task failed.
func (s *Service) Fail(ctx context.Context, taskID, reason string) error {
	if strings.TrimSpace(taskID) == "" {
		return invalid("task_id is required")
	}
	return Translate(storev3.FailTask(ctx, s.st, taskID, reason, s.nowString()))
}

// Cancel cancels a task.
func (s *Service) Cancel(ctx context.Context, taskID, actor string) error {
	if strings.TrimSpace(taskID) == "" {
		return invalid("task_id is required")
	}
	return Translate(storev3.CancelTask(ctx, s.st, taskID, actor, s.nowString()))
}

// ReclaimExpired returns expired leases to the pool.
func (s *Service) ReclaimExpired(ctx context.Context) (int, error) {
	n, err := storev3.ReclaimExpired(ctx, s.st, s.nowString())
	return n, Translate(err)
}

// CreateMaterialRequest inserts a material metadata row. Object storage is the
// caller's concern; the service only owns the database row and the clock.
type CreateMaterialRequest struct {
	MaterialID         string
	Kind               enum.MaterialKind
	Version            int
	Title              string
	Sha256             string
	Size               int64
	Path               string
	UploadedBy         string
	PreviousMaterialID *int64
}

// CreateMaterial inserts one material row and returns its business id.
func (s *Service) CreateMaterial(ctx context.Context, req CreateMaterialRequest) (string, error) {
	if strings.TrimSpace(req.MaterialID) == "" {
		return "", invalid("material_id is required")
	}
	if !req.Kind.Valid() {
		return "", invalid("kind: invalid value %d", req.Kind)
	}
	if strings.TrimSpace(req.Title) == "" {
		return "", invalid("title is required")
	}
	_, err := storev3.CreateMaterialRecord(ctx, s.st, storev3.CreateMaterialInput{
		MaterialID:         req.MaterialID,
		Kind:               req.Kind,
		Version:            req.Version,
		Title:              req.Title,
		Sha256:             req.Sha256,
		Size:               req.Size,
		Path:               req.Path,
		UploadedBy:         req.UploadedBy,
		PreviousMaterialID: req.PreviousMaterialID,
		Now:                s.nowString(),
	})
	if err != nil {
		return "", Translate(err)
	}
	return req.MaterialID, nil
}

// AttachRequest links a material to a project or a task.
type AttachRequest struct {
	ProjectID  string
	TaskID     string
	MaterialID string
	Role       enum.AttachmentRole
	SortNo     int
}

// Attach links a material to exactly one owner.
func (s *Service) Attach(ctx context.Context, req AttachRequest) (storev3.Attachment, error) {
	if (strings.TrimSpace(req.ProjectID) == "") == (strings.TrimSpace(req.TaskID) == "") {
		return storev3.Attachment{}, invalid("exactly one of project_id / task_id is required")
	}
	if strings.TrimSpace(req.MaterialID) == "" {
		return storev3.Attachment{}, invalid("material_id is required")
	}
	if !req.Role.Valid() {
		return storev3.Attachment{}, invalid("role: invalid value %d", req.Role)
	}
	material, err := storev3.GetMaterial(ctx, s.st.Read(), req.MaterialID)
	if err != nil {
		return storev3.Attachment{}, Translate(err)
	}
	attachment, err := storev3.Attach(ctx, s.st, storev3.AttachInput{
		ProjectID:  req.ProjectID,
		TaskID:     req.TaskID,
		MaterialID: material.ID,
		Role:       req.Role,
		SortNo:     req.SortNo,
		Now:        s.nowString(),
	})
	return attachment, Translate(err)
}

// LatestOutput returns the artifact a gate reviews.
func (s *Service) LatestOutput(ctx context.Context, taskID string) (storev3.Attachment, error) {
	attachment, err := storev3.LatestOutput(ctx, s.st.Read(), taskID)
	return attachment, Translate(err)
}

// ListEvents returns the task timeline.
func (s *Service) ListEvents(ctx context.Context, taskID string, limit int) ([]storev3.Event, error) {
	events, err := storev3.ListEvents(ctx, s.st.Read(), taskID, limit)
	return events, Translate(err)
}

// ListDecisions returns the task gate decisions.
func (s *Service) ListDecisions(ctx context.Context, taskID string, limit int) ([]storev3.Decision, error) {
	decisions, err := storev3.ListDecisions(ctx, s.st.Read(), taskID, limit)
	return decisions, Translate(err)
}

// CheckInvariants exposes the cross-row invariant check to operators.
func (s *Service) CheckInvariants(ctx context.Context) error {
	return storev3.CheckInvariants(ctx, s.st.Read())
}

func (s *Service) nowString() string { return formatTime(s.now()) }

func formatTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Dependencies returns the business ids a task depends on.
func (s *Service) Dependencies(ctx context.Context, taskID string) ([]string, error) {
	deps, err := storev3.ListDependencies(ctx, s.st.Read(), taskID)
	return deps, Translate(err)
}

// Attachments returns every attachment of one task.
func (s *Service) Attachments(ctx context.Context, taskID string) ([]storev3.MaterialRef, error) {
	task, err := storev3.GetTask(ctx, s.st.Read(), taskID)
	if err != nil {
		return nil, Translate(err)
	}
	refs, err := storev3.ListTaskAttachmentRefs(ctx, s.st.Read(), task.ID)
	return refs, Translate(err)
}

// ProjectMaterials returns every material produced inside a project, optionally
// filtered by attachment role. It is derived from the tasks of the project: a
// project owns no attachment of its own. Each row carries the task_id that
// produced it (the project view groups by task).
func (s *Service) ProjectMaterials(ctx context.Context, projectID string, role *enum.AttachmentRole) ([]storev3.ProjectMaterialRef, error) {
	project, err := storev3.GetProject(ctx, s.st.Read(), projectID)
	if err != nil {
		return nil, Translate(err)
	}
	refs, err := storev3.ListProjectMaterialRefs(ctx, s.st.Read(), project.ID)
	if err != nil {
		return nil, Translate(err)
	}
	if role == nil {
		return refs, nil
	}
	filtered := make([]storev3.ProjectMaterialRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Role == *role {
			filtered = append(filtered, ref)
		}
	}
	return filtered, nil
}

// LatestOutputMaterialID returns the business id of the task's latest output
// material. IM decisions need the business id, not the attachment's physical id.
func (s *Service) LatestOutputMaterialID(ctx context.Context, taskID string) (string, error) {
	attachment, err := storev3.LatestOutput(ctx, s.st.Read(), taskID)
	if err != nil {
		return "", Translate(err)
	}
	material, err := storev3.GetMaterialByID(ctx, s.st.Read(), attachment.MaterialID)
	if err != nil {
		return "", Translate(err)
	}
	return material.MaterialID, nil
}
