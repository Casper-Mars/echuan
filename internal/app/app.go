// Package app wires the long-lived components of the service together.
//
// App owns the process-scoped resources that must be released on shutdown: the
// SQLite store (Task-02) and the lease reclaim loop (Task-10).
package app

import (
	"log/slog"
	"sync"

	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// App is the process-scoped container of service components.
type App struct {
	cfg       platform.Config
	logger    *slog.Logger
	store     *store.Store
	reclaimer *Reclaimer

	closeOnce sync.Once
	closeErr  error
}

// New builds an App from the resolved configuration, logger and an already
// opened store. The caller keeps ownership of store until App.Close runs;
// App.Close closes it exactly once.
func New(cfg platform.Config, logger *slog.Logger, st *store.Store) *App {
	return &App{cfg: cfg, logger: logger, store: st}
}

// Config returns the resolved configuration.
func (a *App) Config() platform.Config { return a.cfg }

// Logger returns the process logger.
func (a *App) Logger() *slog.Logger { return a.logger }

// Store returns the SQLite store.
func (a *App) Store() *store.Store { return a.store }

// SetReclaimer attaches the lease reclaim loop so Close can stop it. It must be
// called before the loop's goroutine is started (and before Close).
func (a *App) SetReclaimer(r *Reclaimer) { a.reclaimer = r }

// Close releases process-scoped resources. It is safe to call more than once.
//
// The reclaim loop is stopped first and awaited: closing the database under a
// running sweep is what produces "database is locked" during shutdown.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		if a.reclaimer != nil {
			a.reclaimer.Stop()
		}
		if a.store != nil {
			a.closeErr = a.store.Close()
		}
	})
	return a.closeErr
}
