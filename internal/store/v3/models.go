// Package v3 is the V3 persistence layer: projects are containers, tasks are
// the only execution unit, and the workflow is a dependency graph.
//
// It lives beside the legacy v2 store (still used by the old services) so the
// refactor can land incrementally. Once the application layer moves to V3 the
// legacy files can be deleted without touching this package.
package v3

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// Sentinel errors. Callers translate them into HTTP/domain errors; they never
// inspect driver strings.
var (
	ErrProjectNotFound   = errors.New("v3: project not found")
	ErrProductNotFound   = errors.New("v3: product not found")
	ErrProjectClosed     = errors.New("v3: project is closed")
	ErrProjectNotEmpty   = errors.New("v3: project still has unfinished tasks")
	ErrTaskNotFound      = errors.New("v3: task not found")
	ErrMaterialNotFound  = errors.New("v3: material not found")
	ErrNoWork            = errors.New("v3: no ready task")
	ErrLeaseLost         = errors.New("v3: lease lost")
	ErrInvalidTransition = errors.New("v3: invalid task transition")
	ErrInvalidAttachment = errors.New("v3: attachment must have exactly one owner")
	// ErrWorkerNotFound reports a worker id the registry has no row for. The
	// detail endpoint translates it to 404 not_found; a heartbeat naming an
	// unknown id is a registration and never produces it.
	ErrWorkerNotFound = errors.New("v3: worker not found")
	// ErrWorkerNotRegistered reports a claim whose worker_id has no registry
	// row: the card would lease to a holder nobody can list, renew or prune.
	// ClaimTask raises it inside its transaction (after the worker projection
	// matched no row), so the lease is rolled back and the caller answers 409 —
	// the recovery is "send a heartbeat first", not "retry the claim".
	ErrWorkerNotRegistered = errors.New("v3: worker not registered")
	// ErrWorkerBusy reports a claim from a worker that still holds a card. One
	// worker, one lease (ux_tasks_worker_lease), so the second claim has to be
	// answered 409 rather than letting the unique index fail the transaction and
	// surface as a 500 (issue #165 D-1). ClaimTask raises it only when a card is
	// actually available to lease: a busy worker facing an empty queue keeps the
	// ordinary 204 "no work".
	ErrWorkerBusy = errors.New("v3: worker already holds a task")
)

// Project is one row of projects.
type Project struct {
	ID          int64              `db:"id"`
	ProjectID   string             `db:"project_id"`
	ProductID   int64              `db:"product_id"`
	ProductCode string             `db:"product_code"`
	SeqNo       int                `db:"seq_no"`
	Name        string             `db:"name"`
	ContextMD   string             `db:"context_md"`
	Status      enum.ProjectStatus `db:"status"`
	CreateTime  string             `db:"create_time"`
	UpdateTime  string             `db:"update_time"`
	ClosedAt    *string            `db:"closed_at"`
	LastActor   *string            `db:"last_actor"`
}

// Task is one row of tasks.
type Task struct {
	ID           int64         `db:"id"`
	TaskID       string        `db:"task_id"`
	ProjectID    int64         `db:"project_id"`
	ParentTaskID *int64        `db:"parent_task_id"`
	ParentCode   *string       `db:"parent_code"`
	Kind         enum.TaskKind `db:"kind"`
	// KindParallel is the "unfinished cards of this kind in one project" budget
	// snapshotted when the card was created (ADR-0003 D4"): 0 = at most one open
	// card per (project, kind), 1 = several may coexist. The partial unique index
	// ux_tasks_open_per_kind only covers kind_parallel = 0.
	KindParallel  int             `db:"kind_parallel"`
	Title         string          `db:"title"`
	DescriptionMD string          `db:"description_md"`
	ContextMD     string          `db:"context_md"`
	Priority      int             `db:"priority"`
	Status        enum.TaskStatus `db:"status"`
	Attempt       int             `db:"attempt"`
	MaxAttempts   int             `db:"max_attempts"`
	NotBefore     *string         `db:"not_before"`
	LeaseID       *string         `db:"lease_id"`
	LeaseUntil    *string         `db:"lease_until"`
	WorkerID      *string         `db:"worker_id"`
	Reason        *string         `db:"reason"`
	CreateTime    string          `db:"create_time"`
	UpdateTime    string          `db:"update_time"`
	ProjectCode   string          `db:"project_code"`
}

// TaskFilter narrows ListTasks. Empty values mean "no filter".
type TaskFilter struct {
	ProjectID string
	Kind      *enum.TaskKind
	Status    *enum.TaskStatus
	Limit     int
}

// Attachment is one row of attachments.
type Attachment struct {
	ID         int64               `db:"id"`
	ProjectID  *int64              `db:"project_id"`
	TaskID     *int64              `db:"task_id"`
	MaterialID int64               `db:"material_id"`
	Role       enum.AttachmentRole `db:"role"`
	SortNo     int                 `db:"sort_no"`
	CreateTime string              `db:"create_time"`
}

// Material is one row of materials.
type Material struct {
	ID                 int64             `db:"id"`
	MaterialID         string            `db:"material_id"`
	Kind               enum.MaterialKind `db:"kind"`
	Version            int               `db:"version"`
	Title              string            `db:"title"`
	Sha256             string            `db:"sha256"`
	Size               int64             `db:"size"`
	Path               string            `db:"path"`
	UploadedBy         string            `db:"uploaded_by"`
	PreviousMaterialID *int64            `db:"previous_material_id"`
	CreateTime         string            `db:"create_time"`
	UpdateTime         string            `db:"update_time"`
}

// Decision is one row of gate_decisions.
type Decision struct {
	ID         int64         `db:"id"`
	TaskID     string        `db:"task_id"`
	Gate       enum.TaskKind `db:"gate"`
	MaterialID *int64        `db:"material_id"`
	Decision   enum.Decision `db:"decision"`
	DecidedBy  string        `db:"decided_by"`
	Reason     *string       `db:"reason"`
	CreateTime string        `db:"create_time"`
}

// Event is one row of task_events.
type Event struct {
	ID         int64      `db:"id"`
	TaskID     int64      `db:"task_id"`
	Event      enum.Event `db:"event"`
	Actor      *string    `db:"actor"`
	DetailJSON string     `db:"detail_json"`
	CreateTime string     `db:"create_time"`
}

// Worker is one row of workers.
type Worker struct {
	ID             int64            `db:"id"`
	WorkerID       string           `db:"worker_id"`
	MetadataJSON   string           `db:"metadata_json"`
	State          enum.WorkerState `db:"state"`
	CurrentTaskID  *int64           `db:"current_task_id"`
	CurrentLeaseID *string          `db:"current_lease_id"`
	FirstSeen      string           `db:"first_seen"`
	LastSeen       string           `db:"last_seen"`
	CreateTime     string           `db:"create_time"`
	UpdateTime     string           `db:"update_time"`

	// CurrentTaskCode is the business id (`t_...`) of CurrentTaskID. Only
	// ListWorkers fills it (LEFT JOIN tasks); GetWorker's single-table query
	// leaves it nil. A dangling pointer (task row gone) stays nil.
	CurrentTaskCode *string `db:"current_task_code"`
}

// CreateProjectInput is the write model for CreateProject.
type CreateProjectInput struct {
	ProductID string
	Name      string
	ContextMD string
	Actor     string
	Now       string
}

// CreateTaskInput is the write model for CreateTask.
type CreateTaskInput struct {
	ProjectID    string
	TaskID       string
	ParentTaskID string
	Kind         enum.TaskKind
	// KindParallel is the open_cards snapshot for this new card: 0 = single,
	// 1 = multiple. Nil means "the derive engine did not decide" and falls back
	// to the built-in default flow, where only dev is multiple (exactly the
	// migration's backfill). The flow-config derivation card sets it explicitly.
	KindParallel  *int
	Title         string
	DescriptionMD string
	ContextMD     string
	Priority      int
	MaxAttempts   int
	DependsOn     []string
	Now           string
}

// ClaimInput is the write model for ClaimTask.
type ClaimInput struct {
	Kind       enum.TaskKind
	WorkerID   string
	LeaseID    string
	Now        string
	LeaseUntil string
}

// DecideInput is the write model for DecideTask.
type DecideInput struct {
	TaskID     string
	Decision   enum.Decision
	MaterialID *int64
	DecidedBy  string
	Reason     string
	Now        string
	// AfterApprove runs inside the approval transaction after the task is
	// done and its direct dependents are promoted. The flow engine uses it to
	// create successor tasks atomically; a hook error rolls the approval back.
	AfterApprove func(context.Context, *sql.Tx, Task) error
}

// AttachInput is the write model for Attach.
type AttachInput struct {
	ProjectID  string
	TaskID     string
	MaterialID int64
	Role       enum.AttachmentRole
	SortNo     int
	Now        string
}

// CreateMaterialInput is the write model for CreateMaterialRecord.
type CreateMaterialInput struct {
	MaterialID         string
	Kind               enum.MaterialKind
	Version            int
	Title              string
	Sha256             string
	Size               int64
	Path               string
	UploadedBy         string
	PreviousMaterialID *int64
	Now                string
}
