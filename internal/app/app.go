// Package app wires the long-lived components of the service together.
//
// Task-01 keeps App minimal: it carries configuration and the logger and owns
// nothing else yet. Task-02 extends it with the SQLite store, the migration
// runner and the lease reclaim loop.
package app

import (
	"log/slog"
	"sync"

	"github.com/Casper-Mars/echuan/internal/platform"
)

// App is the process-scoped container of service components.
type App struct {
	cfg    platform.Config
	logger *slog.Logger

	closeOnce sync.Once
	closeErr  error
}

// New builds an App from the resolved configuration and logger.
func New(cfg platform.Config, logger *slog.Logger) *App {
	return &App{cfg: cfg, logger: logger}
}

// Config returns the resolved configuration.
func (a *App) Config() platform.Config { return a.cfg }

// Logger returns the process logger.
func (a *App) Logger() *slog.Logger { return a.logger }

// Close releases process-scoped resources. It is safe to call more than once.
//
// TODO(Task-02): stop the reclaim loop and close the SQLite store here.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.closeErr = nil
	})
	return a.closeErr
}
