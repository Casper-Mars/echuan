package v3

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// defaultMaxAttempts is the attempt budget used when the service was built
// without WithMaxAttempts (tests, embedders). It mirrors
// platform.DefaultConfig().MaxAttempts so an unwired service behaves exactly
// like an unconfigured instance; production wiring passes
// platform.Config.MaxAttempts explicitly.
const defaultMaxAttempts = 3

// Service is the V3 task use-case layer. It owns validation, time and error
// translation; persistence stays in store/v3.
type Service struct {
	st          *storedb.Store
	now         func() time.Time
	leaseTTL    time.Duration
	flow        platform.FlowConfig
	logger      *slog.Logger
	materials   MaterialReader
	maxAttempts int
}

// New returns a service with the real clock, a 60s default lease, the built-in
// default flow and the default attempt budget.
//
// The default flow is the same value a configuration without a flow section
// resolves to, so a service built without WithFlow behaves exactly like the
// platform always has (AC7 / AC10). The logger starts as a discard handler: a
// service used by a test or an embedder must not write to the process logger.
func New(st *storedb.Store) *Service {
	return &Service{
		st:          st,
		now:         time.Now,
		leaseTTL:    60 * time.Second,
		flow:        platform.DefaultFlowProfile(),
		logger:      slog.New(slog.DiscardHandler),
		maxAttempts: defaultMaxAttempts,
	}
}

// WithFlow installs the flow snapshot the service derives from. It is wired once
// at startup (cmd/echuan), so a running process keeps the configuration it
// started with even if the file changes on disk. A zero flow is ignored: it
// would make every kind lookup fail.
func (s *Service) WithFlow(flow platform.FlowConfig) *Service {
	if len(flow.Stages) > 0 {
		s.flow = flow
	}
	return s
}

// WithLogger installs the process logger used by the derivation warnings and by
// the post-commit derivation line.
func (s *Service) WithLogger(logger *slog.Logger) *Service {
	if logger != nil {
		s.logger = logger
	}
	return s
}

// WithMaxAttempts sets the attempt budget written into every task this service
// creates, including the tasks the flow rules derive. Non-positive values are
// ignored: platform.Config.normalize rejects them before the wiring runs, so a
// zero here can only be a caller bug and must not silently become unlimited.
func (s *Service) WithMaxAttempts(n int) *Service {
	if n > 0 {
		s.maxAttempts = n
	}
	return s
}

// attemptLimit resolves the budget of a new task: an explicit request value
// wins, otherwise the configured limit applies.
func (s *Service) attemptLimit(requested int) int {
	if requested > 0 {
		return requested
	}
	if s.maxAttempts > 0 {
		return s.maxAttempts
	}
	return defaultMaxAttempts
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
	// MaxAttempts is optional. A value <= 0 means "not provided" and resolves
	// to the configured limit (platform.Config.MaxAttempts, wired through
	// Service.WithMaxAttempts); it is deliberately not a validation error, so
	// an omitted JSON field and an explicit 0 behave the same. Only a positive
	// value overrides the configuration.
	MaxAttempts int
	DependsOn   []string
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
		ProjectID:    req.ProjectID,
		TaskID:       req.TaskID,
		ParentTaskID: req.ParentTaskID,
		Kind:         req.Kind,
		// The open_cards snapshot must come from the same flow that derives
		// cards (§12 integration point). An id the configuration does not know
		// leaves the column to the store's built-in default, which is what a
		// card created from a removed stage would do.
		KindParallel:  s.kindParallel(req.Kind),
		Title:         req.Title,
		DescriptionMD: req.DescriptionMD,
		ContextMD:     req.ContextMD,
		Priority:      req.Priority,
		MaxAttempts:   s.attemptLimit(req.MaxAttempts),
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
//
// The registry is checked before the queue, deliberately: a claim with an
// unregistered worker_id is a 409 even when no card is waiting, so a stale id
// cannot spin forever on "no work" while the operator has no way to learn it is
// not registered. The transaction repeats the check with its own projection
// guard, which covers the row disappearing between this read and the lease.
func (s *Service) Claim(ctx context.Context, workerID string, kind enum.TaskKind) (storev3.Task, error) {
	if strings.TrimSpace(workerID) == "" {
		return storev3.Task{}, invalid("worker_id is required")
	}
	if !kind.Valid() {
		return storev3.Task{}, invalid("kind: invalid value %d", kind)
	}
	if _, err := storev3.GetWorker(ctx, s.st.Read(), workerID); err != nil {
		if errors.Is(err, storev3.ErrWorkerNotFound) {
			return storev3.Task{}, workerNotRegistered(workerID)
		}
		return storev3.Task{}, Translate(err)
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
	if errors.Is(err, storev3.ErrWorkerNotRegistered) {
		// The row was deleted after the pre-check (retention sweep). Same
		// answer, same recovery: send a heartbeat, then claim again.
		return storev3.Task{}, workerNotRegistered(workerID)
	}
	if errors.Is(err, storev3.ErrWorkerBusy) {
		// One worker, one lease: the registry pointer is still set and the
		// unique index ux_tasks_worker_lease would reject the second lease.
		// The transaction already rolled back, so the queued card is untouched
		// and the caller gets a conflict instead of a 500 (issue #165 D-1).
		return storev3.Task{}, workerBusy(workerID)
	}
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

	// The derivation plan is built outside the approval transaction: reading a
	// plan artifact can fail on operator data, and that failure must answer 409
	// with the card untouched in in_review (§8.5). The hook then runs inside the
	// transaction, so a failure while creating the batch rolls the approval back
	// with the cards.
	var plan *derivation
	if req.Decision == enum.Approve {
		current, err := storev3.GetTask(ctx, s.st.Read(), req.TaskID)
		if err != nil {
			return storev3.Task{}, Translate(err)
		}
		plan, err = s.planDerivation(ctx, current)
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
		AfterApprove: plan.hook(s),
	})
	if err != nil {
		return task, Translate(err)
	}
	// Logged after the commit: a line about cards that a rolled-back
	// transaction did not create would be a lie.
	plan.log(s)
	return task, nil
}

// kindParallel is the kind_parallel snapshot of a new card: 1 when the stage
// allows several unfinished cards per project, 0 when it allows one. A nil
// result means "the flow does not know this id" and leaves the store default.
func (s *Service) kindParallel(kind enum.TaskKind) *int {
	stage, ok := s.flow.ByID(int(kind))
	if !ok {
		return nil
	}
	parallel := 0
	if stage.OpenCards == platform.OpenCardsMultiple {
		parallel = 1
	}
	return &parallel
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
