package domain

import "time"

// Project status values (tech design §3.2). The integer is the canonical value
// stored in projects.status; the names are code aliases and are never
// persisted.
const (
	// StatusDraft is a freshly created project: the requirement is its only
	// material and nothing has been planned yet.
	StatusDraft = 0
	// StatusSpecReview waits for a human decision on the submitted spec.
	StatusSpecReview = 1
	// StatusPlanning is being broken down into tasks by the CTO.
	StatusPlanning = 2
	// StatusPlanReview waits for a human decision on the task breakdown.
	StatusPlanReview = 3
	// StatusDeveloping has work in flight.
	StatusDeveloping = 4
	// StatusDeliveryReview waits for acceptance of the delivered work.
	StatusDeliveryReview = 5
	// StatusAccepted is finished and accepted; terminal.
	StatusAccepted = 6
	// StatusCancelled ended without acceptance; terminal.
	StatusCancelled = 7
)

// Material kinds (tech design §3.2).
const (
	// MaterialKindRequirement holds the requirement document (kind=1).
	MaterialKindRequirement = 1
	// MaterialKindSpec holds the implementation spec (kind=2).
	MaterialKindSpec = 2
	// MaterialKindReport holds a task report (kind=3).
	MaterialKindReport = 3
)

// TaskStatusDone is the tasks.status value counted as finished by
// task_stats.done. The full task state machine lands in Task-06; the numeric
// value is fixed by the tech design (§3.2) and the API contract (§6.2).
const TaskStatusDone = 4

// statusNames is the single place that turns projects.status into a name. The
// column only ever stores the integer.
var statusNames = [...]string{
	"draft",
	"spec_review",
	"planning",
	"plan_review",
	"developing",
	"delivery_review",
	"accepted",
	"cancelled",
}

// StatusName maps a projects.status value to its stable name. Unknown values
// render as "unknown" instead of panicking: a row written by a newer binary
// must not take the process down.
func StatusName(status int) string {
	if status < 0 || status >= len(statusNames) {
		return "unknown"
	}
	return statusNames[status]
}

// Material is one version of a material chain. The bytes live in the
// content-addressed object store; SHA256/Size describe them and are what the
// API exposes.
//
// Path is deliberately absent: it is a storage detail owned by the store
// package and the domain layer performs no I/O.
type Material struct {
	MaterialID string
	Kind       int
	Version    int
	Title      string
	SHA256     string
	Size       int64
	UploadedBy string
	CreateTime time.Time
}

// TaskStats summarises the tasks of one project.
//
// ByStatus is only filled in by the detail endpoint (the list stays a single
// query); Done counts tasks whose status is TaskStatusDone.
type TaskStats struct {
	Total    int64
	Done     int64
	ByStatus map[int]int64
}

// Project is one requirement being tracked to acceptance.
//
// ID is the physical row id and stays internal (it only surfaces inside the
// opaque list cursor); ProjectID ("APP-1") is the business id used by the API.
// RequirementMaterialID/SpecMaterialID are physical material ids, 0 when the
// pointer is empty.
type Project struct {
	ID                    int64
	ProjectID             string
	ProductID             string
	SeqNo                 int
	Title                 string
	Status                int
	Reason                string
	Version               int
	RequirementMaterialID int64
	SpecMaterialID        int64
	TaskStats             TaskStats
	CreateTime            time.Time
	UpdateTime            time.Time
}

// ProjectDetail is a project plus the extra material data the detail endpoint
// returns. Requirement/Spec are nil when the pointer is empty; RequirementMD
// is the requirement body read back from the object store.
type ProjectDetail struct {
	Project       Project
	Requirement   *Material
	RequirementMD string
	Spec          *Material
}
