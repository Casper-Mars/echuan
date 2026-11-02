package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

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
	// workerRetention is how long an offline, work-free worker row is kept.
	workerRetention time.Duration

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
		store:           st,
		logger:          logger,
		clock:           platform.RealClock{},
		interval:        interval,
		workerRetention: defaultWorkerRetention,
		stop:            make(chan struct{}),
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

// SetWorkerRetention sets how long an offline worker that holds no card is
// kept before the maintenance sweep deletes its row.
func (r *Reclaimer) SetWorkerRetention(d time.Duration) {
	if d <= 0 {
		d = defaultWorkerRetention
	}
	r.workerRetention = d
}

// WorkerRetention returns the configured retention window.
func (r *Reclaimer) WorkerRetention() time.Duration { return r.workerRetention }

// Run blocks, sweeping until ctx is cancelled or Stop is called.
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

// Stop asks Run to return and waits for it. It is idempotent.
func (r *Reclaimer) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })

	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Reclaim runs one sweep and returns how many work items moved.
func (r *Reclaimer) Reclaim(ctx context.Context) (int, error) {
	var reclaimed []store.ReclaimedWorkItem
	err := r.store.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		reclaimed, err = reclaimExpiredWorkItems(ctx, tx, r.clock.Now().UTC())
		return err
	})
	if err != nil {
		return 0, err
	}
	logReclaimed(r.logger, reclaimed)
	return len(reclaimed), nil
}

// maintenanceResult reports how many rows the two worker maintenance
// statements touched in one sweep.
type maintenanceResult struct {
	Dangling int64
	Pruned   int64
}

// maintain runs the worker-registry maintenance statements in one transaction.
func (r *Reclaimer) maintain(ctx context.Context, now time.Time) (maintenanceResult, error) {
	var out maintenanceResult
	updateTime := formatTimestamp(now)
	before := store.FormatWorkerTime(now.Add(-r.workerRetention))

	err := r.store.Tx(ctx, func(tx *sql.Tx) error {
		dangling, err := store.ClearDanglingPointers(ctx, tx, updateTime)
		if err != nil {
			return err
		}
		out.Dangling = dangling

		pruned, err := store.PruneStaleWorkers(ctx, tx, before)
		if err != nil {
			return err
		}
		out.Pruned = pruned
		return nil
	})
	if err != nil {
		return maintenanceResult{}, err
	}
	return out, nil
}

// sweep runs one pass and keeps a failing database from stopping the loop.
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
		return
	}

	stats, err := r.maintain(ctx, r.clock.Now().UTC())
	switch {
	case err == nil:
		if stats.Pruned > 0 {
			r.logger.Info("worker registry pruned",
				"count", stats.Pruned,
				"retention", r.workerRetention.String(),
			)
		}
		if stats.Dangling > 0 {
			r.logger.Debug("worker maintenance cleared dangling pointers", "count", stats.Dangling)
		}
	case errors.Is(err, context.Canceled), errors.Is(err, store.ErrClosed):
		return
	default:
		r.logger.Error("worker maintenance sweep failed", "error", err)
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
