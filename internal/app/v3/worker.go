package v3

import (
	"context"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain"
	"github.com/Casper-Mars/echuan/internal/domain/enum"
	"github.com/Casper-Mars/echuan/internal/platform"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// Rhythm is the process-level freshness contract of the worker registry. Both
// valves are configuration, not per-row facts: they describe how often a worker
// is expected to report (HeartbeatInterval) and how long its row, or a lease it
// holds, is trusted without a report (WorkerTTL, LeaseTTL).
type Rhythm struct {
	WorkerTTL         time.Duration
	HeartbeatInterval time.Duration
	LeaseTTL          time.Duration
}

// WorkerService is the V3 worker registry. It owns registration, heartbeat,
// presence and the declared kind list; task binding stays in the
// claim/report path.
type WorkerService struct {
	st     *storedb.Store
	now    func() time.Time
	rhythm Rhythm
}

// NewWorkerService returns a service with the real clock and the platform
// defaults for the freshness contract, so an unwired instance (tests,
// embedders) answers presence exactly like an unconfigured server.
func NewWorkerService(st *storedb.Store) *WorkerService {
	cfg := platform.DefaultConfig()
	return &WorkerService{
		st:  st,
		now: time.Now,
		rhythm: Rhythm{
			WorkerTTL:         cfg.WorkerTTL,
			HeartbeatInterval: cfg.HeartbeatInterval,
			LeaseTTL:          cfg.LeaseTTL,
		},
	}
}

// WithClock replaces the clock (tests).
func (s *WorkerService) WithClock(now func() time.Time) *WorkerService {
	s.now = now
	return s
}

// WithRhythm replaces the freshness contract (production wiring passes
// platform.Config.WorkerTTL / HeartbeatInterval / LeaseTTL). Non-positive
// values are ignored: the config validator rejects them before the wiring runs,
// so a zero here can only be a caller bug and must not silently become "every
// worker is offline" or "every worker is active".
func (s *WorkerService) WithRhythm(workerTTL, heartbeatInterval, leaseTTL time.Duration) *WorkerService {
	if workerTTL > 0 {
		s.rhythm.WorkerTTL = workerTTL
	}
	if heartbeatInterval > 0 {
		s.rhythm.HeartbeatInterval = heartbeatInterval
	}
	if leaseTTL > 0 {
		s.rhythm.LeaseTTL = leaseTTL
	}
	return s
}

// Rhythm reports the freshness contract this service derives presence from and
// publishes on the detail endpoint.
func (s *WorkerService) Rhythm() Rhythm { return s.rhythm }

// Heartbeat registers or refreshes a worker and replaces its kinds.
func (s *WorkerService) Heartbeat(ctx context.Context, workerID, metadata string, kinds []enum.TaskKind) error {
	if strings.TrimSpace(workerID) == "" {
		return invalid("worker_id is required")
	}
	for _, kind := range kinds {
		if !kind.Valid() {
			return invalid("kinds: invalid value %d", kind)
		}
	}
	now := formatTime(s.now())
	if err := storev3.UpsertWorker(ctx, s.st, workerID, metadata, now); err != nil {
		return Translate(err)
	}
	if err := storev3.SetWorkerKinds(ctx, s.st, workerID, kinds); err != nil {
		return Translate(err)
	}
	return nil
}

// List returns worker rows ordered by last_seen.
func (s *WorkerService) List(ctx context.Context, limit int) ([]storev3.Worker, error) {
	workers, err := storev3.ListWorkers(ctx, s.st.Read(), limit)
	return workers, Translate(err)
}

// Get returns one worker's registry row, or a 404 when the id was never
// registered (or was already retired by retention). The row carries the held
// task's business id, resolved by the same LEFT JOIN as List.
func (s *WorkerService) Get(ctx context.Context, workerID string) (storev3.Worker, error) {
	worker, err := storev3.GetWorker(ctx, s.st.Read(), workerID)
	return worker, Translate(err)
}

// PresenceOf derives presence for a page of workers from a single instant, so a
// listing cannot straddle the TTL boundary and report two nodes with the same
// last_seen differently.
func (s *WorkerService) PresenceOf(workers []storev3.Worker) []string {
	now := s.now()
	out := make([]string, len(workers))
	for i, worker := range workers {
		out[i] = presenceAt(worker, now, s.rhythm.WorkerTTL)
	}
	return out
}

// Presence derives one worker's presence with the same rule the listing uses,
// so GET /v1/workers?id= and GET /v1/workers answer identically for the same
// row (AC5).
func (s *WorkerService) Presence(worker storev3.Worker) string {
	return presenceAt(worker, s.now(), s.rhythm.WorkerTTL)
}

// presenceAt is the single derivation both callers share.
//
// An unparseable last_seen is reported offline instead of failing the request:
// a dirty row must not take the detail endpoint or the listing down, and
// "offline" is the conservative answer for a node whose liveness cannot be read
// (the same reason a dangling pointer maps to "no work item" instead of an
// error).
func presenceAt(worker storev3.Worker, now time.Time, ttl time.Duration) string {
	lastSeen, err := time.Parse(time.RFC3339Nano, worker.LastSeen)
	if err != nil {
		return domain.PresenceOffline
	}
	return domain.Presence(lastSeen, now, ttl)
}

// Kinds returns one worker's declared kinds.
func (s *WorkerService) Kinds(ctx context.Context, workerID string) ([]enum.TaskKind, error) {
	kinds, err := storev3.ListWorkerKinds(ctx, s.st.Read(), workerID)
	return kinds, Translate(err)
}

// PruneStale deletes the registry rows of workers that stopped reporting longer
// ago than retention and hold no task, returning how many rows went away.
//
// retention comes from platform.Config.WorkerRetention (the maintenance loop
// injects it). A non-positive value is rejected here instead of being turned
// into "cutoff = now", which would wipe every idle worker on the next sweep.
func (s *WorkerService) PruneStale(ctx context.Context, retention time.Duration) (int, error) {
	if retention <= 0 {
		return 0, invalid("retention must be positive, got %s", retention)
	}
	cutoff := formatTime(s.now().Add(-retention))
	n, err := storev3.PruneStaleWorkers(ctx, s.st, cutoff)
	return n, Translate(err)
}

// ReleaseStale clears the occupancy pointers of workers that no longer hold a
// live card (the task row is gone, or it left in_progress through a path that
// did not release the worker) and returns how many rows were given back.
//
// The maintenance sweep runs it before PruneStale: a pointer is the only thing
// that keeps a stale row out of the prune, so a row that held a card and then
// lost it to a cancel/fail/close must be released in the same pass that tries to
// retire it (issue #165 §1).
func (s *WorkerService) ReleaseStale(ctx context.Context) (int, error) {
	n, err := storev3.ClearStaleWorkerPointers(ctx, s.st, formatTime(s.now()))
	return n, Translate(err)
}
