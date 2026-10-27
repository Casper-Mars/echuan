// Command echuan starts the 驿传 platform service: a single Go binary that
// serves the HTTP API and the embedded web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Casper-Mars/echuan/internal/api"
	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
	"github.com/Casper-Mars/echuan/internal/web"
)

// shutdownTimeout bounds how long in-flight requests may take to finish.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "echuan: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "./echuan.toml", "path to the TOML config file (optional)")
	flag.Parse()

	cfg, err := platform.Load(*configPath)
	if err != nil {
		return err
	}

	logger := platform.NewLogger(cfg.LogLevel, os.Stdout)
	// The banner logger ignores log_level: the effective listen address and
	// data directories must stay observable even when business logs are
	// configured to warn/error.
	banner := platform.NewBannerLogger(os.Stdout)

	for _, dir := range []string{cfg.DataDir, cfg.WorkspaceDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Database first: open, migrate, then start readers. A failure here
	// returns before net.Listen, so a broken schema never accepts traffic.
	st, err := openStore(cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("close store", "error", err)
		}
	}()

	checks := []api.HealthChecker{
		store.NewDBChecker(st),
		store.NewMigrationChecker(st),
	}

	application := app.New(cfg, logger, st)
	defer func() {
		if err := application.Close(); err != nil {
			logger.Error("close application", "error", err)
		}
	}()

	products := app.NewProductService(st)

	// The material writer owns data_dir/objects; the project service writes
	// requirement rows and the material service serves them back.
	materials, err := material.NewWriter(cfg.DataDir, cfg.MaterialMaxBytes)
	if err != nil {
		return fmt.Errorf("prepare material store: %w", err)
	}
	projects := app.NewProjectService(st, materials)
	// The task attempt budget is configuration, not a constant: every task
	// created through the plan or a rework inherits it.
	projects.SetMaxAttempts(cfg.MaxAttempts)
	materialService := app.NewMaterialService(st, materials)

	// Tasks are reachable through the board and the single-card endpoints; the
	// attempt budget is the same configuration the plan path uses.
	tasks := app.NewTaskService(st)
	tasks.SetMaxAttempts(cfg.MaxAttempts)
	// Reports are material kind=3; the same writer that owns
	// data_dir/objects appends the version chain behind tasks.report_material_id.
	tasks.SetMaterialWriter(materials)

	// Pull mode: workers claim a card, hold a lease and renew it. The reclaimer
	// returns expired leases to the queue even when nobody is claiming.
	claims := app.NewClaimService(st, logger, cfg.LeaseTTL, cfg.WorkspaceDir)

	// The worker registry and worker-side dispatch. Every option is passed
	// explicitly: a zero value silently falls back to the service default, so
	// forgetting one would make the service disagree with the configuration
	// (worker_ttl, heartbeat_interval, worker_metadata_max_bytes).
	workers := app.NewWorkerService(st, logger, app.WorkerServiceOptions{
		LeaseTTL:          cfg.LeaseTTL,
		WorkerTTL:         cfg.WorkerTTL,
		HeartbeatInterval: cfg.HeartbeatInterval,
		MetadataMaxBytes:  cfg.WorkerMetadataMaxBytes,
		WorkspaceDir:      cfg.WorkspaceDir,
	})
	reclaimer := app.NewReclaimer(st, logger, app.DefaultReclaimInterval)
	application.SetReclaimer(reclaimer)

	// The inbox is the single human entry point: it re-reads pending reviews
	// and task decisions on every request, with the stale threshold coming
	// from configuration (review_stale_hours, default 72).
	inboxService := app.NewInboxService(st, logger, cfg.ReviewStaleHours)

	reclaimCtx, stopReclaim := context.WithCancel(context.Background())
	defer stopReclaim()
	go reclaimer.Run(reclaimCtx)

	handler := api.Chain(
		api.NewRouter(logger, web.Handler(), checks, products, projects, tasks, claims, workers, materialService, inboxService),
		api.Recover(logger),
		api.RequestID(),
		api.UnifiedErrors(),
		api.AccessLog(logger),
		api.Timeout(api.DefaultRequestTimeout),
	)

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      api.DefaultRequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	banner.Info("database ready", "path", st.Path(), "schema_version", schemaVersion(st))
	logStartup(banner, cfg, listener.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case sig := <-sigCh:
		banner.Info("shutdown signal received", "signal", sig.String())
	}

	// Stop the reclaim loop before the listener drains: a sweep in flight when
	// the store closes would log a "database is locked" error on the way out.
	stopReclaim()
	reclaimer.Stop()

	// Stop accepting new requests, then wait for in-flight ones (≤10s).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	if err := application.Close(); err != nil {
		logger.Error("close application", "error", err)
	}
	banner.Info("shutdown complete")
	return nil
}

// openStore opens the SQLite file, applies pending migrations and starts the
// read pool. Any failure aborts startup before the listener exists.
func openStore(cfg platform.Config, logger *slog.Logger) (*store.Store, error) {
	dbPath := filepath.Join(cfg.DataDir, "state.db")

	st, err := store.Open(store.Config{
		Path:         dbPath,
		ReadPoolSize: cfg.ReadPoolSize,
		Logger:       logger,
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	applied, err := st.Migrate(context.Background())
	if err != nil {
		// The wrapped error names the failing migration version.
		logger.Error("database migration failed", "db", dbPath, "error", err)
		_ = st.Close()
		return nil, fmt.Errorf("migrate database %s: %w", dbPath, err)
	}
	logger.Info("database migrated", "db", dbPath, "applied", applied)

	if err := st.OpenReadPool(context.Background()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// schemaVersion returns the applied schema version, or 0 when it cannot be
// read; the readiness probe reports such a database as unavailable anyway.
func schemaVersion(st *store.Store) int {
	version, err := st.MigrationVersion(context.Background())
	if err != nil {
		return 0
	}
	return version
}

// logStartup writes the startup banner. It must be called with the banner
// logger so that the actual listen address is logged at every log_level.
func logStartup(banner *slog.Logger, cfg platform.Config, listenAddr string) {
	banner.Info("echuan starting",
		"version", platform.Version,
		"commit", platform.Commit,
		"listen", listenAddr,
		"data_dir", cfg.DataDir,
		"workspace_dir", cfg.WorkspaceDir,
		"log_level", cfg.LogLevel,
	)
}
