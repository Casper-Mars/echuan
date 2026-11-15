package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// Worker registry and heartbeat dispatch (tech design v0.8 §2.1 / §4.1).
//
// One row per registered worker. The pointer pair (current_work_item_id,
// current_lease_id) is what makes a worker busy. worker_kinds declares the
// kinds a worker can take and is the only dispatch input: the queue SQL filters
// by it, so a worker that declared nothing claims nothing.

const (
	defaultWorkerTTL              = time.Hour
	defaultWorkerMetadataMaxBytes = 64 * 1024
)

// defaultWorkerHeartbeatInterval is the cadence reported to workers.
const defaultWorkerHeartbeatInterval = 15 * time.Second

// WorkerServiceOptions configures the registry.
type WorkerServiceOptions struct {
	LeaseTTL          time.Duration
	WorkerTTL         time.Duration
	HeartbeatInterval time.Duration
	MetadataMaxBytes  int64
}

// WorkerService implements registration, heartbeat renewal and dispatch.
type WorkerService struct {
	store             *store.Store
	logger            *slog.Logger
	clock             platform.Clock
	leaseTTL          time.Duration
	workerTTL         time.Duration
	heartbeatInterval time.Duration
	metadataMaxBytes  int64
}

// NewWorkerService builds the worker service on top of an open store.
func NewWorkerService(st *store.Store, logger *slog.Logger, opts WorkerServiceOptions) *WorkerService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = defaultLeaseTTL
	}
	if opts.WorkerTTL <= 0 {
		opts.WorkerTTL = defaultWorkerTTL
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = defaultWorkerHeartbeatInterval
	}
	if opts.MetadataMaxBytes <= 0 {
		opts.MetadataMaxBytes = defaultWorkerMetadataMaxBytes
	}
	return &WorkerService{
		store:             st,
		logger:            logger,
		clock:             platform.RealClock{},
		leaseTTL:          opts.LeaseTTL,
		workerTTL:         opts.WorkerTTL,
		heartbeatInterval: opts.HeartbeatInterval,
		metadataMaxBytes:  opts.MetadataMaxBytes,
	}
}

// SetClock replaces the clock. It is meant for tests that need to move the wall
// clock; production always uses platform.RealClock.
func (s *WorkerService) SetClock(clock platform.Clock) {
	if clock != nil {
		s.clock = clock
	}
}

// LeaseTTL returns the configured lease duration.
func (s *WorkerService) LeaseTTL() time.Duration { return s.leaseTTL }

// WorkerTTL returns the configured presence window.
func (s *WorkerService) WorkerTTL() time.Duration { return s.workerTTL }

// HeartbeatInterval returns the heartbeat cadence reported to workers.
func (s *WorkerService) HeartbeatInterval() time.Duration { return s.heartbeatInterval }

// heartbeatIntervalMS renders the heartbeat cadence for the wire.
func (s *WorkerService) heartbeatIntervalMS() int64 { return s.heartbeatInterval.Milliseconds() }

// leaseTTLSeconds renders the lease duration for the wire.
func (s *WorkerService) leaseTTLSeconds() int64 { return int64(s.leaseTTL / time.Second) }

// HeartbeatInput is the body of POST /v1/workers/heartbeat.
//
// WorkerID is empty on the very first call; the server mints a w_-prefixed id.
// Kinds is mandatory on every call and replaces the stored declaration
// wholesale. Metadata is the raw JSON document as received: nil/blank means
// "not supplied", which leaves the stored document untouched.
type HeartbeatInput struct {
	WorkerID string
	State    string
	Kinds    []string
	Metadata []byte
	Actor    string
}

// WorkerSnapshot is the worker part of a heartbeat response.
type WorkerSnapshot struct {
	WorkerID            string
	Metadata            json.RawMessage
	State               string
	Kinds               []string
	Presence            string
	CurrentWorkItemID   *string
	FirstSeen           time.Time
	LastSeen            time.Time
	HeartbeatIntervalMS int64
	LeaseTTLSeconds     int64
}

// WorkerCommand is one instruction returned to the worker. Dispatch is set
// exactly when Kind is CommandDispatch.
type WorkerCommand struct {
	Kind     string
	Dispatch *Dispatch
}

// HeartbeatResult is the body of a 200 heartbeat response. Commands is always a
// non-nil slice: "nothing to do" is an empty array, not null.
type HeartbeatResult struct {
	Worker   WorkerSnapshot
	Commands []WorkerCommand
}

// Heartbeat registers, refreshes and possibly dispatches work for one worker.
func (s *WorkerService) Heartbeat(ctx context.Context, in HeartbeatInput) (HeartbeatResult, error) {
	now := s.clock.Now().UTC()
	updateTime := formatTimestamp(now)

	var (
		result     HeartbeatResult
		reclaimed  []store.ReclaimedWorkItem
		workerID   string
		registered bool

		// leaseLost is set when this heartbeat released the pointer because the
		// lease was gone. It gates dispatch for this round only.
		leaseLost bool

		cleared   bool
		clearedID string
		kinds     []string

		renewedWorkID  string
		renewedLeaseID string
		renewedUntil   time.Time

		dispatched *Dispatch
	)

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		state, normalizedKinds, metadata, err := s.validateHeartbeat(in)
		if err != nil {
			return err
		}
		kinds = normalizedKinds

		// 2. Identify the worker: an empty id registers a new one, a non-empty
		// id must already exist.
		workerID = strings.TrimSpace(in.WorkerID)
		if workerID == "" {
			generated, err := platform.NewWorkerID(now)
			if err != nil {
				return err
			}
			workerID = generated
			registered = true
		} else if _, err := store.GetWorker(ctx, tx, workerID); err != nil {
			if store.IsNotFound(err) {
				return &domain.WorkerUnknownError{WorkerID: workerID}
			}
			return err
		}

		// 3. Register or refresh. The upsert replaces worker_kinds in the same
		// transaction; the pointer pair is left to the steps below.
		if _, err := store.UpsertWorker(ctx, tx, store.WorkerUpsert{
			WorkerID:     workerID,
			MetadataJSON: metadata,
			State:        state,
			Kinds:        kinds,
			LastSeen:     store.FormatWorkerTime(now),
			UpdateTime:   updateTime,
		}); err != nil {
			return err
		}

		beforeReclaim, err := store.GetWorker(ctx, tx, workerID)
		if err != nil {
			return err
		}

		// 4. Reclaim expired leases, which also releases the pointer of every
		// worker that held a reclaimed card.
		reclaimed, err = reclaimExpiredWorkItems(ctx, tx, now)
		if err != nil {
			return err
		}

		worker, err := store.GetWorker(ctx, tx, workerID)
		if err != nil {
			return err
		}
		if beforeReclaim.CurrentWorkItemID != nil && worker.CurrentWorkItemID == nil {
			leaseLost = true
		}

		// 5. Keep or release the held card. Occupancy is the pointer pair, never
		// the declared state: a worker that still holds a card keeps renewing
		// the lease even when it says idle.
		var heldWorkID *string
		if worker.CurrentWorkItemID != nil {
			row, rowErr := store.GetWorkItemByID(ctx, tx, *worker.CurrentWorkItemID)
			if rowErr != nil && !store.IsNotFound(rowErr) {
				return rowErr
			}
			held := rowErr == nil && workerHoldsWorkItem(worker, row)
			if !held {
				if rowErr == nil {
					clearedID = row.WorkID
				}
				if _, err := store.ClearWorkItemByWorkItemID(ctx, tx, *worker.CurrentWorkItemID, updateTime); err != nil {
					return err
				}
				cleared = true
			} else {
				until, ok, err := store.HeartbeatLease(ctx, tx, store.WorkItemHeartbeatRequest{
					WorkID:     row.WorkID,
					LeaseID:    *worker.CurrentLeaseID,
					Now:        formatLeaseTime(now),
					Until:      formatLeaseTime(now.Add(s.leaseTTL)),
					UpdateTime: updateTime,
				})
				if err != nil {
					return err
				}
				if !ok {
					clearedID = row.WorkID
					if _, err := store.ClearWorkItemByWorkItemID(ctx, tx, *worker.CurrentWorkItemID, updateTime); err != nil {
						return err
					}
					cleared = true
					leaseLost = true
				} else {
					parsed, err := parseTimestamp(until)
					if err != nil {
						return err
					}
					renewedWorkID = row.WorkID
					renewedLeaseID = *worker.CurrentLeaseID
					renewedUntil = parsed
					heldWorkID = &row.WorkID
				}
			}
		}

		// 6./7. Dispatch. A busy worker never receives a second card; an idle
		// worker without a card takes the next claimable one — unless it just
		// lost its own pointer in this round.
		commands := make([]WorkerCommand, 0, 1)
		if heldWorkID == nil && state == domain.WorkerStateIdle && !leaseLost {
			dispatch, err := s.dispatch(ctx, tx, workerID, now)
			if err != nil {
				return err
			}
			if dispatch != nil {
				dispatched = dispatch
				workID := dispatch.WorkID
				heldWorkID = &workID
				commands = append(commands, WorkerCommand{Kind: CommandDispatch, Dispatch: dispatch})
			}
		}

		// 8. Snapshot of the row as it now stands (dispatch binds it).
		final, err := store.GetWorker(ctx, tx, workerID)
		if err != nil {
			return err
		}
		storedKinds, err := store.ListWorkerKinds(ctx, tx, workerID)
		if err != nil {
			return err
		}
		firstSeen, err := parseTimestamp(final.FirstSeen)
		if err != nil {
			return err
		}
		lastSeen, err := parseTimestamp(final.LastSeen)
		if err != nil {
			return err
		}
		result = HeartbeatResult{
			Worker: WorkerSnapshot{
				WorkerID:            final.WorkerID,
				Metadata:            json.RawMessage(final.MetadataJSON),
				State:               final.State,
				Kinds:               storedKinds,
				Presence:            domain.Presence(lastSeen, now, s.workerTTL),
				CurrentWorkItemID:   heldWorkID,
				FirstSeen:           firstSeen,
				LastSeen:            lastSeen,
				HeartbeatIntervalMS: s.heartbeatIntervalMS(),
				LeaseTTLSeconds:     s.leaseTTLSeconds(),
			},
			Commands: commands,
		}
		return nil
	})
	if err != nil {
		return HeartbeatResult{}, err
	}

	logReclaimed(s.logger, reclaimed)
	if registered {
		s.logger.Info("worker registered", "worker_id", workerID, "state", result.Worker.State)
	}
	if cleared {
		s.logger.Info("worker pointer cleared", "worker_id", workerID, "work_id", clearedID)
	}
	if leaseLost {
		s.logger.Info("worker lost its lease; dispatch deferred to the next heartbeat",
			"worker_id", workerID)
	}
	if !renewedUntil.IsZero() {
		s.logger.Debug("worker lease renewed",
			"worker_id", workerID,
			"work_id", renewedWorkID,
			"lease_id", renewedLeaseID,
			"lease_until", renewedUntil.Format(time.RFC3339Nano),
		)
	}
	if dispatched != nil {
		s.logger.Info("worker dispatched work item",
			"worker_id", workerID,
			"work_id", dispatched.WorkID,
			"kind", dispatched.Kind,
			"project_id", dispatched.WorkItem.ProjectID,
			"product", dispatched.Context.Product.ID,
			"attempt", dispatched.WorkItem.Attempt,
			"lease_id", dispatched.Lease.LeaseID,
		)
	}
	return result, nil
}

// validateHeartbeat applies step 1 and returns the normalized state, kinds and
// metadata document.
func (s *WorkerService) validateHeartbeat(in HeartbeatInput) (string, []string, string, error) {
	state := strings.TrimSpace(in.State)
	switch state {
	case "":
		state = domain.WorkerStateIdle
	case domain.WorkerStateIdle, domain.WorkerStateBusy:
	default:
		return "", nil, "", invalidField("state", "must be idle or busy")
	}

	kinds, err := normalizeKinds(in.Kinds)
	if err != nil {
		return "", nil, "", err
	}

	metadata, err := s.normalizeMetadata(in.Metadata)
	if err != nil {
		return "", nil, "", err
	}
	return state, kinds, metadata, nil
}

// normalizeKinds validates the mandatory kind declaration.
//
// kinds is required, must contain at least one value and every value must be
// one of the four schema kinds. Duplicates are removed while keeping the
// request order, so worker_kinds stores exactly the union the client meant.
func normalizeKinds(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, invalidField("kinds", "is required and must not be empty")
	}
	kinds := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" || !store.IsValidWorkItemKind(value) {
			return nil, invalidField("kinds", fmt.Sprintf("unsupported kind %q", value))
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		kinds = append(kinds, value)
	}
	if len(kinds) == 0 {
		return nil, invalidField("kinds", "is required and must not be empty")
	}
	return kinds, nil
}

// normalizeMetadata validates the metadata document and returns it trimmed.
func (s *WorkerService) normalizeMetadata(raw []byte) (string, error) {
	doc := bytes.TrimSpace(raw)
	if len(doc) == 0 {
		return "", nil
	}
	if int64(len(doc)) > s.metadataMaxBytes {
		return "", &domain.TooLargeError{
			Field:  "metadata",
			Limit:  s.metadataMaxBytes,
			Actual: int64(len(doc)),
		}
	}
	if doc[0] != '{' || !json.Valid(doc) {
		return "", invalidField("metadata", "must be a JSON object")
	}
	return string(doc), nil
}

// dispatch takes the next claimable card and binds it to the worker.
func (s *WorkerService) dispatch(ctx context.Context, tx *sql.Tx, workerID string, now time.Time) (*Dispatch, error) {
	updateTime := formatTimestamp(now)

	row, claimed, err := store.ClaimNextWorkItem(ctx, tx, store.ClaimNextRequest{
		WorkerID:   workerID,
		LeaseID:    platform.NewLeaseID(),
		LeaseUntil: formatLeaseTime(now.Add(s.leaseTTL)),
		UpdateTime: updateTime,
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, nil
	}

	dispatch, err := buildDispatch(ctx, tx, row, s.leaseTTL, now)
	if err != nil {
		return nil, err
	}

	var leaseID string
	if row.LeaseID != nil {
		leaseID = *row.LeaseID
	}
	if err := store.BindWorkItem(ctx, tx, workerID, row.ID, leaseID, updateTime); err != nil {
		if errors.Is(err, store.ErrUniqueViolation) {
			return nil, &domain.WorkerBusyError{
				WorkerID: workerID,
				WorkID:   row.WorkID,
				Reason:   "the work item is already bound to another worker",
			}
		}
		return nil, err
	}
	return dispatch, nil
}

// workerHoldsWorkItem reports whether the worker's pointer pair still describes
// the live lease of that card.
func workerHoldsWorkItem(worker store.Worker, row store.WorkItem) bool {
	if worker.CurrentWorkItemID == nil || *worker.CurrentWorkItemID != row.ID {
		return false
	}
	if worker.CurrentLeaseID == nil || row.LeaseID == nil || *worker.CurrentLeaseID != *row.LeaseID {
		return false
	}
	if row.State != store.WorkItemStateLeased || row.WorkerID == nil || *row.WorkerID != worker.WorkerID {
		return false
	}
	return true
}

// ListWorkers returns one page of the registry.
func (s *WorkerService) ListWorkers(ctx context.Context, afterID int64, limit int) ([]WorkerView, *int64, error) {
	if limit < 1 || limit > maxListLimit {
		return nil, nil, invalidField("limit", fmt.Sprintf("must be between 1 and %d", maxListLimit))
	}
	rows, err := store.ListWorkers(ctx, s.store.Read(), afterID, limit+1)
	if err != nil {
		return nil, nil, err
	}
	var next *int64
	if len(rows) > limit {
		rows = rows[:limit]
		id := rows[len(rows)-1].ID
		next = &id
	}

	workers := make([]WorkerView, 0, len(rows))
	now := s.clock.Now().UTC()
	for _, row := range rows {
		view, err := s.workerView(ctx, row, now)
		if err != nil {
			return nil, nil, err
		}
		workers = append(workers, view)
	}
	return workers, next, nil
}

// GetWorker returns one registered worker.
func (s *WorkerService) GetWorker(ctx context.Context, workerID string) (WorkerView, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return WorkerView{}, invalidField("worker_id", "is required")
	}
	row, err := store.GetWorker(ctx, s.store.Read(), workerID)
	if err != nil {
		if store.IsNotFound(err) {
			return WorkerView{}, &domain.NotFoundError{Resource: "worker", Key: workerID}
		}
		return WorkerView{}, err
	}
	return s.workerView(ctx, row, s.clock.Now().UTC())
}

// WorkerView is one registry row as the API renders it.
type WorkerView struct {
	WorkerID          string
	Metadata          json.RawMessage
	State             string
	Kinds             []string
	Presence          string
	CurrentWorkItemID *string
	FirstSeen         time.Time
	LastSeen          time.Time
	// HeartbeatIntervalMS and LeaseTTLSeconds mirror the heartbeat payload so
	// registry reads expose the same server configuration.
	HeartbeatIntervalMS int64
	LeaseTTLSeconds     int64
}

func (s *WorkerService) workerView(ctx context.Context, row store.Worker, now time.Time) (WorkerView, error) {
	kinds, err := store.ListWorkerKinds(ctx, s.store.Read(), row.WorkerID)
	if err != nil {
		return WorkerView{}, err
	}
	firstSeen, err := parseTimestamp(row.FirstSeen)
	if err != nil {
		return WorkerView{}, err
	}
	lastSeen, err := parseTimestamp(row.LastSeen)
	if err != nil {
		return WorkerView{}, err
	}
	var current *string
	if row.CurrentWorkItemID != nil {
		item, err := store.GetWorkItemByID(ctx, s.store.Read(), *row.CurrentWorkItemID)
		if err != nil && !store.IsNotFound(err) {
			return WorkerView{}, err
		}
		if err == nil && item.State == store.WorkItemStateLeased {
			current = &item.WorkID
		}
	}
	return WorkerView{
		WorkerID:            row.WorkerID,
		Metadata:            json.RawMessage(row.MetadataJSON),
		State:               row.State,
		Kinds:               kinds,
		Presence:            domain.Presence(lastSeen, now, s.workerTTL),
		CurrentWorkItemID:   current,
		FirstSeen:           firstSeen,
		LastSeen:            lastSeen,
		HeartbeatIntervalMS: s.heartbeatIntervalMS(),
		LeaseTTLSeconds:     s.leaseTTLSeconds(),
	}, nil
}
