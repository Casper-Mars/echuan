package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Pull-mode dispatch: claim, lease renewal and the lazy reclaim.
//
// The platform dispatches by kind only. A worker declares the kinds it can take
// in worker_kinds, and the queue SQL filters on that declaration; there is no
// product scope any more, so a card may come from any product.

// DefaultReclaimInterval is how often the background reclaimer sweeps expired
// leases (tech design §5.2).
const DefaultReclaimInterval = 30 * time.Second

// defaultLeaseTTL mirrors platform.DefaultConfig().LeaseTTL so a caller that
// forgets to pass the configuration still produces usable leases.
const defaultLeaseTTL = 90 * time.Second

// defaultWorkerRetention mirrors platform.DefaultConfig().WorkerRetention so a
// reclaimer built without configuration still keeps the documented 1 hour.
const defaultWorkerRetention = time.Hour

// leaseTimeLayout is the fixed-width UTC layout of work_items.lease_until.
//
// Fixed width is load-bearing: claim, heartbeat and reclaim compare the column
// as a string, so every writer must produce the same character count.
const leaseTimeLayout = "2006-01-02T15:04:05.000Z"

// formatLeaseTime renders a lease deadline in the fixed-width layout.
func formatLeaseTime(t time.Time) string { return t.UTC().Format(leaseTimeLayout) }

// DispatchProtocol is the protocol version carried by every dispatch payload.
const DispatchProtocol = "v2"

// CommandDispatch is the only worker command in v2.
const CommandDispatch = "dispatch"

// ClaimInput is the use-case input of POST /v1/work-items/claim.
type ClaimInput struct {
	// WorkerID is mandatory: its worker_kinds rows decide what may be claimed.
	WorkerID string
	// WorkID optionally restricts the attempt to that single card.
	WorkID string
	Actor  string
}

// DispatchInput is one material the worker needs to execute the card.
type DispatchInput struct {
	MaterialID string
	Kind       int
	Version    int
}

// DispatchWorkItem is the card part of a dispatch.
type DispatchWorkItem struct {
	ProjectID string
	Title     string
	BodyMD    string
	Priority  int
	Attempt   int
	// MaxAttempts is the budget this card is allowed; Attempt is already
	// incremented for the current lease, so Attempt=1 is the first try.
	MaxAttempts int
	// Reason is the objection that sent this card back (gate rejection or
	// card rejection). Nil on a first attempt and after a successful report.
	Reason *string
	// Rework is true when the dispatch carries an unanswered objection, i.e.
	// the card is being handed out again to fix something specific. It is a
	// derived convenience field so a worker does not have to infer intent from
	// attempt numbers alone (a lease expiry also bumps attempt).
	Rework bool
}

// DispatchLease is the freshly issued (or renewed) lease.
type DispatchLease struct {
	LeaseID string
	Until   time.Time
	TTL     time.Duration
}

// DispatchProduct is the product snapshot handed to the worker so it can clone
// the repository without a second call.
type DispatchProduct struct {
	ID     string
	Name   string
	GitURL string
}

// DispatchContext is the execution context of one dispatch.
type DispatchContext struct {
	SpecVersion int
	SpecDigest  string
	Product     DispatchProduct
}

// Dispatch is the payload of a dispatch command and of a 200 claim response.
type Dispatch struct {
	Protocol string
	WorkID   string
	Kind     string
	Inputs   []DispatchInput
	WorkItem DispatchWorkItem
	Lease    DispatchLease
	Context  DispatchContext
}

// LeaseRenewal is the body of a 200 lease renewal. Until is the renewed
// deadline, not the previous one.
type LeaseRenewal struct {
	LeaseID string
	Until   time.Time
	TTL     time.Duration
}

// ClaimService implements the pull-mode use cases: claim, lease renewal and the
// lazy reclaim that runs before every claim.
type ClaimService struct {
	store    *store.Store
	logger   *slog.Logger
	clock    platform.Clock
	leaseTTL time.Duration
}

// NewClaimService builds the claim service. A non-positive ttl falls back to
// 90s so a misconfigured value never issues an already-expired lease.
func NewClaimService(st *store.Store, logger *slog.Logger, ttl time.Duration) *ClaimService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if ttl <= 0 {
		ttl = defaultLeaseTTL
	}
	return &ClaimService{
		store:    st,
		logger:   logger,
		clock:    platform.RealClock{},
		leaseTTL: ttl,
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

// Claim leases one claimable card for a registered worker.
//
// The lazy reclaim and the queue pick share one write transaction: expired
// leases are returned to the queue first, then the next card of a kind the
// worker declared is taken. A nil result means "no claimable work" — the HTTP
// layer answers 204.
func (s *ClaimService) Claim(ctx context.Context, in ClaimInput) (*Dispatch, error) {
	workerID := strings.TrimSpace(in.WorkerID)
	if workerID == "" {
		return nil, invalidField("worker_id", "is required")
	}
	workID := strings.TrimSpace(in.WorkID)

	now := s.clock.Now().UTC()
	leaseID := platform.NewLeaseID()

	var (
		reclaimed []store.ReclaimedWorkItem
		result    *Dispatch
	)
	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		reclaimed, err = reclaimExpiredWorkItems(ctx, tx, now)
		if err != nil {
			return err
		}

		worker, err := store.GetWorker(ctx, tx, workerID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.WorkerUnknownError{WorkerID: workerID}
			}
			return err
		}
		if worker.CurrentWorkItemID != nil {
			return &domain.WorkerBusyError{WorkerID: workerID, Reason: "already holds a work item"}
		}

		row, claimed, err := store.ClaimNextWorkItem(ctx, tx, store.ClaimNextRequest{
			WorkID:     workID,
			WorkerID:   workerID,
			LeaseID:    leaseID,
			LeaseUntil: formatLeaseTime(now.Add(s.leaseTTL)),
			UpdateTime: formatTimestamp(now),
		})
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}

		if err := store.BindWorkItem(ctx, tx, workerID, row.ID, leaseID, formatTimestamp(now)); err != nil {
			if errors.Is(err, store.ErrUniqueViolation) {
				return &domain.WorkerBusyError{
					WorkerID: workerID,
					WorkID:   row.WorkID,
					Reason:   "the work item is already bound to another worker",
				}
			}
			return err
		}

		result, err = buildDispatch(ctx, tx, row, s.leaseTTL, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	logReclaimed(s.logger, reclaimed)
	if result == nil {
		return nil, nil
	}

	s.logger.Info("work item claimed",
		"work_id", result.WorkID,
		"project_id", result.WorkItem.ProjectID,
		"kind", result.Kind,
		"product", result.Context.Product.ID,
		"attempt", result.WorkItem.Attempt,
		"lease_id", result.Lease.LeaseID,
		"ttl_seconds", int(result.Lease.TTL/time.Second),
		"worker_id", workerID,
	)
	return result, nil
}

// RenewLease extends the lease of one card.
//
// The renewal is one conditional UPDATE: the deadline only moves when the
// caller still holds the live lease. Any other outcome is a lease_lost conflict
// (the card itself missing is a 404, decided by a re-read after the UPDATE
// matched nothing).
func (s *ClaimService) RenewLease(ctx context.Context, workID, leaseID string) (LeaseRenewal, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return LeaseRenewal{}, invalidField("work_id", "is required")
	}
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return LeaseRenewal{}, invalidField("lease_id", "is required")
	}

	now := s.clock.Now().UTC()
	var renewal LeaseRenewal

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		until, ok, err := store.HeartbeatLease(ctx, tx, store.WorkItemHeartbeatRequest{
			WorkID:     workID,
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

		row, err := store.GetWorkItemByWorkID(ctx, tx, workID)
		if err != nil {
			if store.IsNotFound(err) {
				return &domain.NotFoundError{Resource: "work_item", Key: workID}
			}
			return err
		}
		return lostLeaseError(row, leaseID, now)
	})
	if err != nil {
		return LeaseRenewal{}, err
	}

	s.logger.Debug("lease renewed",
		"work_id", workID,
		"lease_id", leaseID,
		"lease_until", renewal.Until.Format(time.RFC3339Nano),
	)
	return renewal, nil
}

// buildDispatch assembles the dispatch payload for one freshly leased card.
//
// It runs inside the claim transaction, so the returned spec is the version the
// project pointed at when the card was leased. Package-level because both the
// pull-mode claim and the worker heartbeat dispatch must return exactly the
// same payload.
func buildDispatch(ctx context.Context, tx *sql.Tx, row store.WorkItem, ttl time.Duration, now time.Time) (*Dispatch, error) {
	project, err := store.GetProjectByProjectID(ctx, tx, row.ProjectCode)
	if err != nil {
		return nil, err
	}
	product, err := store.GetProductByProductID(ctx, tx, project.ProductCode)
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

	inputs, err := store.ListWorkItemInputs(ctx, tx, row.ID)
	if err != nil {
		return nil, err
	}
	dispatchInputs := make([]DispatchInput, 0, len(inputs))
	for _, input := range inputs {
		dispatchInputs = append(dispatchInputs, DispatchInput{
			MaterialID: input.MaterialID,
			Kind:       input.Kind,
			Version:    input.Version,
		})
	}

	leaseID := ""
	if row.LeaseID != nil {
		leaseID = *row.LeaseID
	}
	until := now.Add(ttl)
	if row.LeaseUntil != nil {
		parsed, err := parseTimestamp(*row.LeaseUntil)
		if err != nil {
			return nil, err
		}
		until = parsed
	}

	return &Dispatch{
		Protocol: DispatchProtocol,
		WorkID:   row.WorkID,
		Kind:     row.Kind,
		Inputs:   dispatchInputs,
		WorkItem: DispatchWorkItem{
			ProjectID:   row.ProjectCode,
			Title:       row.Title,
			BodyMD:      row.BodyMD,
			Priority:    row.Priority,
			Attempt:     row.Attempt,
			MaxAttempts: row.MaxAttempts,
			Reason:      row.Reason,
			Rework:      row.Reason != nil && strings.TrimSpace(*row.Reason) != "",
		},
		Lease: DispatchLease{LeaseID: leaseID, Until: until, TTL: ttl},
		Context: DispatchContext{
			SpecVersion: specVersion,
			SpecDigest:  specDigest,
			Product: DispatchProduct{
				ID:     product.ProductID,
				Name:   product.Name,
				GitURL: product.GitURL,
			},
		},
	}, nil
}

// lostLeaseError names the condition that made a renewal fail.
func lostLeaseError(row store.WorkItem, leaseID string, now time.Time) error {
	switch {
	case row.State != store.WorkItemStateLeased:
		return &domain.LeaseLostError{WorkID: row.WorkID, Reason: fmt.Sprintf(
			"work item is in state %s, not leased", row.State)}
	case row.LeaseID == nil || *row.LeaseID != leaseID:
		return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease_id does not match the active lease"}
	case row.LeaseUntil != nil:
		until, err := parseTimestamp(*row.LeaseUntil)
		if err != nil {
			return err
		}
		if !until.After(now) {
			return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease has expired"}
		}
	}
	return &domain.LeaseLostError{WorkID: row.WorkID, Reason: "lease is no longer valid"}
}

// reclaimExpiredWorkItems runs one reclaim sweep inside tx and releases the
// worker pointer of every reclaimed card.
func reclaimExpiredWorkItems(ctx context.Context, tx *sql.Tx, now time.Time) ([]store.ReclaimedWorkItem, error) {
	updateTime := formatTimestamp(now)
	reclaimed, err := store.ReclaimExpiredWorkItems(ctx, tx, formatLeaseTime(now), updateTime)
	if err != nil {
		return nil, err
	}
	for _, row := range reclaimed {
		if _, err := store.ClearWorkItemByWorkItemID(ctx, tx, row.ID, updateTime); err != nil {
			return nil, err
		}
	}
	return reclaimed, nil
}

// logReclaimed emits one line per reclaimed work item.
func logReclaimed(logger *slog.Logger, rows []store.ReclaimedWorkItem) {
	for _, row := range rows {
		reason := ""
		if row.Reason != nil {
			reason = *row.Reason
		}
		logger.Warn("lease expired; work item reclaimed",
			"work_id", row.WorkID,
			"attempt", row.Attempt,
			"to_state", row.State,
			"reason", reason,
		)
	}
}
