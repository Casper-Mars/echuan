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

// Worker registration and heartbeat (tech design v0.6 §4.1 / §4.3).
//
// A heartbeat is a worker's single round trip to the control plane: it
// registers on the first call, refreshes liveness, renews the lease it holds,
// and — when it has nothing to do — receives the next card. The eight steps of
// §4.1 all run inside one write transaction, so two heartbeats of the same
// worker or a heartbeat racing the background reclaimer can never dispatch the
// same card twice: the write pool is a single connection and the DSN sets
// _txlock=immediate, which serialises the whole step list.
//
// The transaction is also why the dispatch is safe without a second
// "is it still free?" read: store.ClaimNextTask picks and updates the card in
// one statement, store.BindTask re-checks the worker pointer under the same
// write lock, and both roll back together with everything else if anything in
// the eight steps fails.

// CommandRunTask is the only command a heartbeat can carry: execute the task
// attached to it (tech design v0.6 §4.3).
const CommandRunTask = "run_task"

// defaultWorkerTTL mirrors platform.DefaultConfig().WorkerTTL so a service
// built without an explicit configuration still reports presence sensibly.
const defaultWorkerTTL = 45 * time.Second

// defaultWorkerHeartbeatInterval mirrors
// platform.DefaultConfig().HeartbeatInterval.
const defaultWorkerHeartbeatInterval = 15 * time.Second

// defaultWorkerMetadataMaxBytes mirrors
// platform.DefaultConfig().WorkerMetadataMaxBytes.
const defaultWorkerMetadataMaxBytes = 8192

// WorkerServiceOptions is the subset of the configuration the worker use cases
// need. Zero values fall back to the defaults above, except WorkspaceDir which
// is used as given (an empty root yields a relative workdir, which is what the
// claim service does too).
type WorkerServiceOptions struct {
	// LeaseTTL is the lease duration handed out on dispatch and renewal. It
	// should match the claim service's TTL: both write tasks.lease_until.
	LeaseTTL time.Duration
	// WorkerTTL is how long a worker stays "active" after its last heartbeat.
	WorkerTTL time.Duration
	// HeartbeatInterval is the cadence workers are told to heartbeat at. It is
	// reported back on every heartbeat so a worker never has to hard-code it.
	HeartbeatInterval time.Duration
	// MetadataMaxBytes caps the free-form metadata document.
	MetadataMaxBytes int64
	// WorkspaceDir is the root of the per-project workdirs reported back.
	WorkspaceDir string
}

// WorkerService implements registration, heartbeat and worker-side dispatch.
//
// It deliberately does not own HTTP concerns (Task-05 maps the results) and it
// does not touch the pull-mode claim endpoint (Task-06).
type WorkerService struct {
	store             *store.Store
	logger            *slog.Logger
	clock             platform.Clock
	leaseTTL          time.Duration
	workerTTL         time.Duration
	heartbeatInterval time.Duration
	metadataMaxBytes  int64
	workspaceDir      string
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
		workspaceDir:      opts.WorkspaceDir,
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

// HeartbeatInput is the body of POST /v1/workers/heartbeat (§4.1).
//
// WorkerID is empty on the very first call; the server mints a w_-prefixed id
// and the worker must reuse it afterwards. State defaults to idle. Metadata is
// the raw JSON document as received: nil/blank means "not supplied", which
// leaves the stored document untouched, while a supplied document replaces it.
type HeartbeatInput struct {
	WorkerID string
	State    string
	Metadata []byte
	Actor    string
}

// WorkerSnapshot is the worker part of a heartbeat response.
//
// CurrentTaskID is the business task id the worker holds, nil when idle. State
// is the persisted value, i.e. the client's last declaration: a worker that
// declares idle while a card is still bound keeps the pointer (§4.1 step 5
// renews only a busy one) and the card is reclaimed once the lease expires.
// The pointer, not State, is what makes a worker occupied. Presence is derived
// from last_seen and the configured TTL and is never stored.
type WorkerSnapshot struct {
	WorkerID      string
	Metadata      json.RawMessage
	State         string
	Presence      string
	CurrentTaskID *string
	FirstSeen     time.Time
	LastSeen      time.Time
}

// WorkerCommand is one instruction returned to the worker. Task/Lease/Context
// are set exactly when Kind is CommandRunTask; they reuse the pull-mode claim
// types so a worker parses one payload shape no matter how it got the card.
type WorkerCommand struct {
	Kind    string
	Task    *ClaimedTask
	Lease   *ClaimLease
	Context *ClaimContext
}

// HeartbeatResult is the body of a 200 heartbeat response. Commands is always
// a non-nil slice: "nothing to do" is an empty array, not null.
type HeartbeatResult struct {
	Worker   WorkerSnapshot
	Commands []WorkerCommand
}

// Heartbeat registers, refreshes and possibly dispatches work for one worker.
//
// The eight steps of §4.1 map to the transaction body below, in order:
//
//  1. validate state and metadata;
//  2. mint a worker id or reject an unknown one (worker_unknown);
//  3. upsert metadata/state/last_seen (first_seen on the insert);
//  4. reclaim expired leases and release their worker pointers;
//  5. keep or release the held card and renew its lease when still busy;
//  6. a busy worker gets no new card;
//  7. an idle free worker gets the next claimable card;
//  8. read the worker back and return the snapshot plus the commands.
func (s *WorkerService) Heartbeat(ctx context.Context, in HeartbeatInput) (HeartbeatResult, error) {
	now := s.clock.Now().UTC()
	updateTime := formatTimestamp(now)

	var (
		result     HeartbeatResult
		reclaimed  []store.ReclaimedTask
		workerID   string
		registered bool

		cleared       bool
		clearedTaskID string

		renewedTaskID  string
		renewedLeaseID string
		renewedUntil   time.Time

		dispatched *ClaimResult
	)

	err := s.store.Tx(ctx, func(tx *sql.Tx) error {
		// 1. Request validation. It runs inside the transaction only because
		// the design puts the whole sequence there; it touches no rows.
		state, metadata, err := s.validateHeartbeat(in)
		if err != nil {
			return err
		}
		actor := actorOrDefault(in.Actor)

		// 2. Identify the worker: an empty id registers a new one, a non-empty
		// id must already exist. A client cannot pick its own id, which is why
		// an unknown one is an error instead of an implicit insert.
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

		// 3. Register or refresh. The upsert never touches the pointer pair;
		// only the steps below own the binding.
		if _, err := store.UpsertWorker(ctx, tx, store.WorkerUpsert{
			WorkerID:     workerID,
			MetadataJSON: metadata,
			State:        state,
			LastSeen:     store.FormatWorkerTime(now),
			UpdateTime:   updateTime,
		}); err != nil {
			return err
		}

		// 4. Reclaim expired leases. reclaimExpired also releases the pointer of
		// each worker that held a reclaimed card, so this step and the claim /
		// background sweeps share one implementation.
		reclaimed, err = reclaimExpired(ctx, tx, now)
		if err != nil {
			return err
		}

		// Step 4 may have cleared this very worker's pointer, so the row read
		// by the upsert is stale: re-read before deciding what it holds.
		worker, err := store.GetWorker(ctx, tx, workerID)
		if err != nil {
			return err
		}

		// 5. Keep or release the held card.
		var heldTaskID *string
		if worker.CurrentTaskID != nil {
			task, taskErr := store.GetTaskByID(ctx, tx, *worker.CurrentTaskID)
			if taskErr != nil && !store.IsNotFound(taskErr) {
				return taskErr
			}
			held := taskErr == nil && workerHoldsTask(worker, task)
			switch {
			case !held:
				// The card was reported, reclaimed or re-leased elsewhere: the
				// pointer is stale. Clearing is what lets the next step dispatch
				// a fresh card in this same heartbeat.
				if taskErr == nil {
					clearedTaskID = task.TaskID
				}
				if _, err := store.ClearTaskByTaskID(ctx, tx, *worker.CurrentTaskID, updateTime); err != nil {
					return err
				}
				cleared = true
			case state == domain.WorkerStateBusy:
				until, ok, err := store.HeartbeatLease(ctx, tx, store.HeartbeatRequest{
					TaskID:     task.TaskID,
					LeaseID:    *worker.CurrentLeaseID,
					Now:        formatLeaseTime(now),
					Until:      formatLeaseTime(now.Add(s.leaseTTL)),
					UpdateTime: updateTime,
				})
				if err != nil {
					return err
				}
				if !ok {
					// Defensive: step 4 already swept expired leases, so a live
					// binding should renew. Treat a miss as a lost lease rather
					// than keep a pointer we cannot prove.
					clearedTaskID = task.TaskID
					if _, err := store.ClearTaskByTaskID(ctx, tx, *worker.CurrentTaskID, updateTime); err != nil {
						return err
					}
					cleared = true
					break
				}
				parsed, err := parseTimestamp(until)
				if err != nil {
					return err
				}
				renewedTaskID = task.TaskID
				renewedLeaseID = *worker.CurrentLeaseID
				renewedUntil = parsed
				heldTaskID = &task.TaskID
			default:
				// idle while still holding: the worker is declaring it has
				// nothing to renew, so the lease is left to expire.
				heldTaskID = &task.TaskID
			}
		}

		// 6./7. Dispatch. A busy worker never receives a second card; an idle
		// worker without a card takes the next claimable one.
		commands := make([]WorkerCommand, 0, 1)
		if heldTaskID == nil && state == domain.WorkerStateIdle {
			claim, err := s.dispatch(ctx, tx, workerID, actor, now)
			if err != nil {
				return err
			}
			if claim != nil {
				dispatched = claim
				taskID := claim.Task.ID
				heldTaskID = &taskID
				commands = append(commands, WorkerCommand{
					Kind:    CommandRunTask,
					Task:    &claim.Task,
					Lease:   &claim.Lease,
					Context: &claim.Context,
				})
			}
		}

		// 8. Snapshot of the row as it now stands (dispatch binds it).
		final, err := store.GetWorker(ctx, tx, workerID)
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
				WorkerID:      final.WorkerID,
				Metadata:      json.RawMessage(final.MetadataJSON),
				State:         final.State,
				Presence:      domain.Presence(lastSeen, now, s.workerTTL),
				CurrentTaskID: heldTaskID,
				FirstSeen:     firstSeen,
				LastSeen:      lastSeen,
			},
			Commands: commands,
		}
		return nil
	})
	if err != nil {
		return HeartbeatResult{}, err
	}

	// Logging happens only after the commit: a rolled-back heartbeat must not
	// leave lines about a registration, a renewal or a dispatch that never was.
	logReclaimed(s.logger, reclaimed)
	if registered {
		s.logger.Info("worker registered",
			"worker_id", workerID,
			"state", result.Worker.State,
		)
	}
	if cleared {
		s.logger.Info("worker pointer cleared",
			"worker_id", workerID,
			"task_id", clearedTaskID,
		)
	}
	if !renewedUntil.IsZero() {
		s.logger.Debug("worker lease renewed",
			"worker_id", workerID,
			"task_id", renewedTaskID,
			"lease_id", renewedLeaseID,
			"lease_until", renewedUntil.Format(time.RFC3339Nano),
		)
	}
	if dispatched != nil {
		s.logger.Info("worker dispatched task",
			"worker_id", workerID,
			"task_id", dispatched.Task.ID,
			"project_id", dispatched.Task.ProjectID,
			"product", dispatched.Context.Product.ID,
			"attempt", dispatched.Task.Attempt,
			"lease_id", dispatched.Lease.LeaseID,
			"lease_until", dispatched.Lease.Until.Format(time.RFC3339Nano),
		)
	}
	return result, nil
}

// validateHeartbeat applies step 1 of §4.1 and returns the normalized state and
// metadata document. An empty metadata document means "not supplied".
func (s *WorkerService) validateHeartbeat(in HeartbeatInput) (string, string, error) {
	state := strings.TrimSpace(in.State)
	switch state {
	case "":
		state = domain.WorkerStateIdle
	case domain.WorkerStateIdle, domain.WorkerStateBusy:
	default:
		return "", "", invalidField("state", "must be idle or busy")
	}

	metadata, err := s.normalizeMetadata(in.Metadata)
	if err != nil {
		return "", "", err
	}
	return state, metadata, nil
}

// normalizeMetadata validates the metadata document and returns it trimmed.
//
// A metadata document is free-form, but it must be a JSON object: the column is
// documented as an object and an array or a bare string would make every reader
// special-case it. The size cap is checked first so an oversized body is
// answered with 413 before any parsing work is done.
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
	// A leading '{' plus json.Valid rejects every non-object: arrays, strings,
	// numbers, null and malformed documents (including trailing garbage). It
	// also avoids allocating a decode map for a document the store only ever
	// copies verbatim.
	if doc[0] != '{' || !json.Valid(doc) {
		return "", invalidField("metadata", "must be a JSON object")
	}
	return string(doc), nil
}

// dispatch takes the next claimable card of any product and binds it to the
// worker (steps 7 of §4.1). A nil result means the queue is empty.
//
// The task row, the lease columns and the worker pointer are written by three
// statements inside the caller's transaction; there is no window in which the
// card is in progress without an owner or owned by a worker that was not told.
func (s *WorkerService) dispatch(ctx context.Context, tx *sql.Tx, workerID, actor string, now time.Time) (*ClaimResult, error) {
	updateTime := formatTimestamp(now)

	task, claimed, err := store.ClaimNextTask(ctx, tx, store.ClaimNextRequest{
		LeaseID:    platform.NewLeaseID(),
		LeaseUntil: formatLeaseTime(now.Add(s.leaseTTL)),
		Actor:      actor,
		UpdateTime: updateTime,
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, nil
	}

	// The response carries the product snapshot, so resolve the card's project
	// and then its product. store.Project only exposes the product's business
	// id (the physical foreign key stays inside the store), and both rows exist
	// by foreign key.
	project, err := store.GetProjectByProjectID(ctx, tx, task.ProjectCode)
	if err != nil {
		return nil, err
	}
	product, err := store.GetProductByProductID(ctx, tx, project.ProductCode)
	if err != nil {
		return nil, err
	}

	claim, err := buildClaimResult(ctx, tx, task, product, s.leaseTTL, now, s.workspaceDir)
	if err != nil {
		return nil, err
	}

	var leaseID string
	if task.LeaseID != nil {
		leaseID = *task.LeaseID
	}
	if err := store.BindTask(ctx, tx, workerID, task.ID, leaseID, updateTime); err != nil {
		// The unique index on current_task_id is the authority: a stale pointer
		// on another worker can make a card that looks ready already owned. The
		// caller maps this to 409 worker_busy; the transaction rolls back, so
		// the claim itself is undone too.
		if errors.Is(err, store.ErrUniqueViolation) {
			return nil, &domain.WorkerBusyError{
				WorkerID: workerID,
				TaskID:   task.TaskID,
				Reason:   "the card is already bound to another worker",
			}
		}
		return nil, err
	}
	return claim, nil
}

// workerHoldsTask reports whether the worker's pointer pair still describes the
// live lease of that task. Status 2 plus a matching lease id is the whole
// proof: a report, a reclaim or a re-lease breaks at least one of them.
func workerHoldsTask(worker store.Worker, task store.Task) bool {
	return task.Status == domain.TaskInProgress &&
		task.LeaseID != nil &&
		worker.CurrentLeaseID != nil &&
		*task.LeaseID == *worker.CurrentLeaseID
}

// WorkerPage is one page of the worker registry. NextID is the physical id of
// the last returned row and is zero on the last page; it is what the HTTP layer
// turns into an opaque cursor.
type WorkerPage struct {
	Items  []WorkerSnapshot
	NextID int64
}

// ListWorkers returns one page of the registry ordered by last_seen DESC.
//
// The ordering is the store's (keyset on last_seen, id); the API layer must not
// re-sort. "Online first" is a presentation concern of the operator UI
// (Task-10), so the page only reports the derived presence of each row.
//
// limit+1 rows are read to learn whether another page exists, exactly like
// ListProducts. A worker whose pointer names a task that no longer exists is
// reported with a nil CurrentTaskID: the maintenance sweep owns clearing the
// column, and a dangling pointer must not make the listing fail.
func (s *WorkerService) ListWorkers(ctx context.Context, afterID int64, limit int) (WorkerPage, error) {
	if limit < 1 || limit > maxListLimit {
		return WorkerPage{}, invalidField("limit", fmt.Sprintf("must be between 1 and %d", maxListLimit))
	}
	if afterID < 0 {
		return WorkerPage{}, invalidField("cursor", "is invalid")
	}

	db := s.store.Read()
	rows, err := store.ListWorkers(ctx, db, afterID, limit+1)
	if err != nil {
		return WorkerPage{}, err
	}

	now := s.clock.Now().UTC()
	page := WorkerPage{Items: make([]WorkerSnapshot, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			page.NextID = rows[limit-1].ID
			break
		}
		snapshot, err := s.snapshotFromRow(ctx, db, row, now)
		if err != nil {
			return WorkerPage{}, err
		}
		page.Items = append(page.Items, snapshot)
	}
	return page, nil
}

// GetWorker returns one worker by business id. A missing row is the domain
// not_found error (404 at the HTTP layer), not worker_unknown: worker_unknown
// is the heartbeat telling a worker its own id is stale.
func (s *WorkerService) GetWorker(ctx context.Context, workerID string) (WorkerSnapshot, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return WorkerSnapshot{}, invalidField("id", "is required")
	}
	db := s.store.Read()
	row, err := store.GetWorker(ctx, db, workerID)
	if err != nil {
		if store.IsNotFound(err) {
			return WorkerSnapshot{}, &domain.NotFoundError{Resource: "worker", Key: workerID}
		}
		return WorkerSnapshot{}, err
	}
	return s.snapshotFromRow(ctx, db, row, s.clock.Now().UTC())
}

// snapshotFromRow renders one persisted row as the read model the API exposes.
//
// CurrentTaskID is resolved from the physical pointer to the business task id;
// occupancy is decided by the pointer alone (a worker that declares idle while
// still holding a card stays occupied, §4.1 step 5), never by State.
func (s *WorkerService) snapshotFromRow(ctx context.Context, q store.Querier, row store.Worker, now time.Time) (WorkerSnapshot, error) {
	firstSeen, err := parseTimestamp(row.FirstSeen)
	if err != nil {
		return WorkerSnapshot{}, err
	}
	lastSeen, err := parseTimestamp(row.LastSeen)
	if err != nil {
		return WorkerSnapshot{}, err
	}

	var currentTaskID *string
	if row.CurrentTaskID != nil {
		task, taskErr := store.GetTaskByID(ctx, q, *row.CurrentTaskID)
		switch {
		case taskErr == nil:
			id := task.TaskID
			currentTaskID = &id
		case store.IsNotFound(taskErr):
			// The pointer outlived its task. Report free capacity instead of a
			// dangling id; the sweep clears the column.
		default:
			return WorkerSnapshot{}, taskErr
		}
	}

	return WorkerSnapshot{
		WorkerID:      row.WorkerID,
		Metadata:      json.RawMessage(row.MetadataJSON),
		State:         row.State,
		Presence:      domain.Presence(lastSeen, now, s.workerTTL),
		CurrentTaskID: currentTaskID,
		FirstSeen:     firstSeen,
		LastSeen:      lastSeen,
	}, nil
}
