package v3

import (
	"context"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
	storedb "github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
)

// WorkerService is the V3 worker registry. It owns registration, heartbeat and
// the declared kind list; task binding stays in the claim/report path.
type WorkerService struct {
	st  *storedb.Store
	now func() time.Time
}

// NewWorkerService returns a service with the real clock.
func NewWorkerService(st *storedb.Store) *WorkerService {
	return &WorkerService{st: st, now: time.Now}
}

// WithClock replaces the clock (tests).
func (s *WorkerService) WithClock(now func() time.Time) *WorkerService {
	s.now = now
	return s
}

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

// CurrentWorkItemID is the business id (`t_...`) of the task a worker holds,
// or nil when it holds nothing.
//
// It is already resolved by store/v3.ListWorkers' LEFT JOIN (Worker.CurrentTaskCode),
// so the listing stays one query for the whole page instead of one per worker
// (issue #89 C1/E4 review: N+1). A dangling pointer (task row gone) maps to nil,
// exactly like "no work item", and never fails the listing.
func (s *WorkerService) CurrentWorkItemID(worker storev3.Worker) *string {
	return worker.CurrentTaskCode
}
