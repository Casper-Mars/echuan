package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// DefaultReclaimInterval is how often the background reclaimer sweeps expired
// leases (tech design §5.2).
const DefaultReclaimInterval = 30 * time.Second

// defaultLeaseTTL mirrors platform.DefaultConfig().LeaseTTL so a caller that
// forgets to pass the configuration still produces usable leases.
const defaultLeaseTTL = 90 * time.Second

// leaseTimeLayout is the fixed-width UTC layout of tasks.lease_until.
//
// Fixed width is load-bearing: claim, heartbeat and reclaim compare the column
// as a string, so every writer must produce the same character count.
// RFC3339Nano trims trailing zeros, which would make "…:00Z" sort after
// "…:00.500Z" and mis-order two leases inside the same second.
const leaseTimeLayout = "2006-01-02T15:04:05.000Z"

// formatLeaseTime renders a lease deadline in the fixed-width layout.
func formatLeaseTime(t time.Time) string { return t.UTC().Format(leaseTimeLayout) }

// ClaimInput is the use-case input of POST /v1/tasks/claim. TaskID is optional:
// when set, only that card is attempted and an unclaimable card yields no work
// instead of another one.
type ClaimInput struct {
	Product string
	TaskID  string
	Actor   string
}

// ClaimedTask is the task part of a claim response.
type ClaimedTask struct {
	ID        string
	ProjectID string
	Title     string
	BodyMD    string
	Attempt   int
	Priority  int
}

// ClaimLease is the freshly issued lease.
type ClaimLease struct {
	LeaseID string
	Until   time.Time
	TTL     time.Duration
}

// ClaimProduct is the product snapshot handed to the worker so it can clone the
// repository without a second call.
type ClaimProduct struct {
	ID     string
	Name   string
	GitURL string
}

// ClaimContext is the execution context of one claim (tech design §4.4).
//
// SpecVersion/SpecDigest describe the project's current spec material; both are
// zero/empty when the project has no spec, which is a normal state and never an
// error.
type ClaimContext struct {
	SpecVersion int
	SpecDigest  string
	Product     ClaimProduct
	Workdir     string
}

// ClaimResult is the body of a 200 claim response.
type ClaimResult struct {
	Task    ClaimedTask
	Lease   ClaimLease
	Context ClaimContext
}

// LeaseRenewal is the body of a 200 heartbeat response. Until is the renewed
// deadline, not the previous one.
type LeaseRenewal struct {
	LeaseID string
	Until   time.Time
	TTL     time.Duration
}

// ClaimService implements the pull-mode use cases: claim, heartbeat and the
// lazy reclaim that runs before every claim.
//
// All time comes from Clock so a test can move the wall clock instead of
// sleeping, and lease deadlines are stored in a fixed-width layout (see
// leaseTimeLayout) so the SQL can compare them as strings.
type ClaimService struct {
	store        *store.Store
	logger       *slog.Logger
	clock        platform.Clock
	leaseTTL     time.Duration
	workspaceDir string
}

// NewClaimService builds the claim service. A non-positive ttl falls back to
// 90s so a misconfigured value never issues an already-expired lease.
func NewClaimService(st *store.Store, logger *slog.Logger, ttl time.Duration, workspaceDir string) *ClaimService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	return &ClaimService{
		store:        st,
		logger:       logger,
		clock:        platform.RealClock{},
		leaseTTL:     ttl,
		workspaceDir: workspaceDir,
	}
}

// SetClock replaces the clock. It is meant for tests that need to move the wall
// clock; production always uses platform.RealClock.
func (s *ClaimService) SetClock(clock platform.Clock) {
	if clock != nil {
		s.clock = clock
	}
}

// LeaseTTL returns the configured lease duration.
func (s *ClaimService) LeaseTTL() time.Duration { return s.leaseTTL }

// Claim leases one task of a product.
//
// The lazy reclaim and the queue pick share one write transaction: expired
// leases are returned to the queue first, then the next claimable card is
// taken. A nil result means "no claimable work" — the HTTP layer answers 204.
//
// Running the reclaim here, instead of waiting for the 30s sweep, is what makes
// a card abandoned a moment ago immediately available again.
func (s *ClaimService) Claim(ctx context.Context, in ClaimInput) (*ClaimResult, error) {
	productCode := strings.TrimSpace(in.Product)
	if productCode == "" {
		return nil, invalidField("product", "is required")
	}
	taskID := strings.TrimSpace(in.TaskID)
	actor := actorOrDefault(in.Actor)

	now := s.clock.Now().UTC()

	var (
		reclaimed []store.ReclaimedTask
		result    *ClaimResult
	)
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		product, err := store.GetProductByProductID(ctx, tx, productCode)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "product", Key: productCode}
			}
			return err
		}

		reclaimed, err = reclaimExpired(ctx, tx, now)
		if err != nil {
			return err
		}

		task, claimed, err := store.ClaimTask(ctx, tx, store.ClaimRequest{
			ProductID:  product.ID,
			TaskID:     taskID,
			LeaseID:    platform.NewLeaseID(),
			LeaseUntil: formatLeaseTime(now.Add(s.leaseTTL)),
			Actor:      actor,
			UpdateTime: formatTimestamp(now),
		})
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}

		result, err = s.executionContext(ctx, tx, task, product)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Reclaim logging happens only after the transaction committed: a rollback
	// must not leave log lines about leases that are still live.
	logReclaimed(s.logger, reclaimed)
	if result == nil {
		return nil, nil
	}

	s.logger.Info("task claimed",
		"task_id", result.Task.ID,
		"project_id", result.Task.ProjectID,
		"product", productCode,
		"attempt", result.Task.Attempt,
		"lease_id", result.Lease.LeaseID,
		"lease_until", result.Lease.Until.Format(time.RFC3339Nano),
		"ttl_seconds", int(result.Lease.TTL/time.Second),
	)
	return result, nil
}

// Heartbeat renews the lease of one task.
//
// The renewal is one conditional UPDATE: the deadline only moves when the
// caller still holds the live lease. Any other outcome is a lease_lost conflict
// (the task itself missing is a 404, decided by a re-read after the UPDATE
// matched nothing).
func (s *ClaimService) Heartbeat(ctx context.Context, taskID, leaseID string) (LeaseRenewal, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return LeaseRenewal{}, invalidField("id", "is required")
	}
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return LeaseRenewal{}, invalidField("lease_id", "is required")
	}

	now := s.clock.Now().UTC()
	var renewal LeaseRenewal

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		until, ok, err := store.HeartbeatLease(ctx, tx, store.HeartbeatRequest{
			TaskID:     taskID,
			LeaseID:    leaseID,
			Now:        formatLeaseTime(now),
			Until:      formatLeaseTime(now.Add(s.leaseTTL)),
			UpdateTime: formatTimestamp(now),
		})
		if err != nil {
			return err
		}
		if ok {
			parsed, err := parseTimestamp(until)
			if err != nil {
				return err
			}
			renewal = LeaseRenewal{LeaseID: leaseID, Until: parsed, TTL: s.leaseTTL}
			return nil
		}

		// The UPDATE matched nothing: re-read the row to tell "no such task"
		// (404) from "you no longer own this lease" (409 lease_lost).
		row, err := store.GetTaskByTaskID(ctx, tx, taskID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "task", Key: taskID}
			}
			return err
		}
		return lostLeaseError(row, leaseID, now)
	})
	if err != nil {
		return LeaseRenewal{}, err
	}

	s.logger.Debug("lease renewed",
		"task_id", taskID,
		"lease_id", leaseID,
		"lease_until", renewal.Until.Format(time.RFC3339Nano),
	)
	return renewal, nil
}

// executionContext loads the project and its spec to build the claim response.
// It runs inside the claim transaction, so the returned spec is the version the
// project pointed at when the card was leased.
func (s *ClaimService) executionContext(ctx context.Context, tx *sql.Tx, task store.Task, product store.Product) (*ClaimResult, error) {
	project, err := store.GetProjectByProjectID(ctx, tx, task.ProjectCode)
	if err != nil {
		return nil, err
	}

	specVersion, specDigest := 0, ""
	if project.SpecMaterialID != nil {
		spec, err := store.GetMaterialByID(ctx, tx, *project.SpecMaterialID)
		if err != nil {
			return nil, err
		}
		specVersion = spec.Version
		specDigest = "sha256:" + spec.SHA256
	}

	leaseID := ""
	if task.LeaseID != nil {
		leaseID = *task.LeaseID
	}
	until := s.clock.Now().UTC().Add(s.leaseTTL)
	if task.LeaseUntil != nil {
		parsed, err := parseTimestamp(*task.LeaseUntil)
		if err != nil {
			return nil, err
		}
		until = parsed
	}

	return &ClaimResult{
		Task: ClaimedTask{
			ID:        task.TaskID,
			ProjectID: task.ProjectCode,
			Title:     task.Title,
			BodyMD:    task.BodyMD,
			Attempt:   task.AttemptCount,
			Priority:  task.Priority,
		},
		Lease: ClaimLease{LeaseID: leaseID, Until: until, TTL: s.leaseTTL},
		Context: ClaimContext{
			SpecVersion: specVersion,
			SpecDigest:  specDigest,
			Product: ClaimProduct{
				ID:     product.ProductID,
				Name:   product.Name,
				GitURL: product.GitURL,
			},
			Workdir: s.workdir(task.ProjectCode),
		},
	}, nil
}

// workdir renders <workspace_dir>/<project_id>. The directory is not created:
// the worker owns its workspace layout.
func (s *ClaimService) workdir(projectID string) string {
	return filepath.Join(s.workspaceDir, projectID)
}

// lostLeaseError names the condition that made a heartbeat fail, from the
// most specific fact to the generic one.
func lostLeaseError(row store.Task, leaseID string, now time.Time) error {
	switch {
	case row.Status != domain.TaskInProgress:
		return &domain.LeaseLostError{TaskID: row.TaskID, Reason: fmt.Sprintf(
			"task is in status %s, not in_progress", domain.TaskStatusName(row.Status))}
	case row.LeaseID == nil || *row.LeaseID != leaseID:
		return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease_id does not match the active lease"}
	case row.LeaseUntil != nil:
		until, err := parseTimestamp(*row.LeaseUntil)
		if err != nil {
			return err
		}
		if !until.After(now) {
			return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease has expired"}
		}
	}
	return &domain.LeaseLostError{TaskID: row.TaskID, Reason: "lease is no longer valid"}
}

// reclaimExpired runs one reclaim sweep inside tx. now is rendered in both the
// lease layout (for the deadline comparison) and the regular timestamp layout
// (for update_time).
func reclaimExpired(ctx context.Context, tx *sql.Tx, now time.Time) ([]store.ReclaimedTask, error) {
	return store.ReclaimExpired(ctx, tx, formatLeaseTime(now), formatTimestamp(now))
}

// logReclaimed emits one line per reclaimed task with the fields the design
// asks for: task_id, attempt, to_status and reason.
func logReclaimed(logger *slog.Logger, rows []store.ReclaimedTask) {
	for _, row := range rows {
		reason := ""
		if row.Reason != nil {
			reason = *row.Reason
		}
		logger.Warn("lease expired; task reclaimed",
			"task_id", row.TaskID,
			"attempt", row.AttemptCount,
			"to_status", row.Status,
			"reason", reason,
		)
	}
}

// Reclaimer sweeps expired leases on a timer (tech design §5.2).
//
// It exists for the case no worker asks for work: a crashed worker's card is
// still expired, and the queue would keep it invisible until the next claim.
// The sweep runs once at startup — which also recovers leases left behind by a
// previous process — and then every Interval.
type Reclaimer struct {
	store    *store.Store
	logger   *slog.Logger
	clock    platform.Clock
	interval time.Duration

	stop     chan struct{}
	stopOnce sync.Once

	mu      sync.Mutex
	running bool
	done    chan struct{}
}

// NewReclaimer builds the background reclaimer. A non-positive interval falls
// back to DefaultReclaimInterval.
func NewReclaimer(st *store.Store, logger *slog.Logger, interval time.Duration) *Reclaimer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if interval <= 0 {
		interval = DefaultReclaimInterval
	}
	return &Reclaimer{
		store:    st,
		logger:   logger,
		clock:    platform.RealClock{},
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// SetClock replaces the clock. It is meant for tests; production uses
// platform.RealClock.
func (r *Reclaimer) SetClock(clock platform.Clock) {
	if clock != nil {
		r.clock = clock
	}
}

// Interval returns the sweep period.
func (r *Reclaimer) Interval() time.Duration { return r.interval }

// Run blocks, sweeping until ctx is cancelled or Stop is called. It is meant to
// be started in its own goroutine; a second concurrent Run returns immediately.
func (r *Reclaimer) Run(ctx context.Context) {
	if !r.begin() {
		return
	}
	defer r.end()

	r.logger.Info("lease reclaimer started", "interval", r.interval.String())
	defer r.logger.Info("lease reclaimer stopped")

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

// Stop asks Run to return and waits for it. It is idempotent and safe to call
// when Run never started, so App.Close can always stop the loop before the
// store closes.
func (r *Reclaimer) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })

	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Reclaim runs one sweep and returns how many tasks moved. It is exported so a
// caller (Task-12's inbox) can force a sweep, and so tests can drive the sweep
// without waiting for a tick.
func (r *Reclaimer) Reclaim(ctx context.Context) (int, error) {
	var reclaimed []store.ReclaimedTask
	err := r.store.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		reclaimed, err = reclaimExpired(ctx, tx, r.clock.Now().UTC())
		return err
	})
	if err != nil {
		return 0, err
	}
	logReclaimed(r.logger, reclaimed)
	return len(reclaimed), nil
}

// sweep runs one pass and keeps a failing database from stopping the loop: the
// next tick retries. A cancelled context or a closed store is silent because
// both are part of an orderly shutdown.
func (r *Reclaimer) sweep(ctx context.Context) {
	n, err := r.Reclaim(ctx)
	switch {
	case err == nil:
		if n > 0 {
			r.logger.Debug("lease reclaim sweep finished", "reclaimed", n)
		}
	case errors.Is(err, context.Canceled), errors.Is(err, store.ErrClosed):
		return
	default:
		r.logger.Error("lease reclaim sweep failed", "error", err)
	}
}

// begin marks the loop running and allocates the done channel Stop waits on.
func (r *Reclaimer) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	r.done = make(chan struct{})
	return true
}

// end closes the done channel and clears the running flag.
func (r *Reclaimer) end() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done != nil {
		close(r.done)
		r.done = nil
	}
	r.running = false
}
