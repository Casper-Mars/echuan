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
	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
	"github.com/Casper-Mars/echuan/internal/im"
	"github.com/Casper-Mars/echuan/internal/material"
	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
	storev3 "github.com/Casper-Mars/echuan/internal/store/v3"
	"github.com/Casper-Mars/echuan/internal/web"
)

// shutdownTimeout bounds how long in-flight requests may take to finish.
const shutdownTimeout = 10 * time.Second

// maintenanceInterval is how often one maintenance sweep runs: expired leases
// go back to the pool and retired worker rows are pruned.
const maintenanceInterval = 30 * time.Second

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
	banner := platform.NewBannerLogger(os.Stdout)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", cfg.DataDir, err)
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

	// Products and material downloads keep their legacy services: the tables
	// did not change shape in V3.
	products := app.NewProductService(st)
	materialWriter, err := material.NewWriter(cfg.DataDir, cfg.MaterialMaxBytes)
	if err != nil {
		return fmt.Errorf("prepare material store: %w", err)
	}
	materialService := app.NewMaterialService(st, materialWriter)

	// V3 services. The task service is built by newTaskService so the
	// config → service wiring stays a single testable seam (see
	// cmd/echuan/maxattempts_test.go).
	projectService := appv3.NewProjectService(st)
	taskService := newTaskService(st, cfg)
	workerService := appv3.NewWorkerService(st)

	// Feishu bot: optional. The poller turns in_review tasks into cards and
	// the card callback decides the task through the V3 service.
	imCfg := im.Config{
		Enabled:        cfg.IM.Enabled,
		AppID:          cfg.IM.AppID,
		AppSecret:      cfg.IM.AppSecret,
		AllowedChatIDs: cfg.IM.AllowedChatIDs,
		PollInterval:   cfg.IM.PollInterval,
		QuietHours:     cfg.IM.QuietHours,
		MaxCards:       cfg.IM.MaxCards,
		DocFolderToken: cfg.IM.DocFolderToken,
	}
	notifier := im.New(imCfg, logger)
	if notifier != nil {
		imStore := im.NewV3Store(st)
		poller := im.NewV3Poller(imCfg,
			im.NewV3TaskSource(taskService, projectService), imStore, notifier.Client(), logger, nil)
		// The acceptance card links to the task's latest output material, so the
		// poller needs the same resolver the decision path uses.
		poller.SetMaterials(taskService)
		notifier.SetPoller(poller)
		// An empty folder token switches document import off: cards then carry no
		// "看全文" button instead of a placeholder.
		notifier.SetDocs(im.NewDocService(imCfg, imStore, docMaterialReader{st: st, dataDir: cfg.DataDir}, logger))
		notifier.SetActionHandler(im.NewActionHandler(imCfg, imStore, im.NewV3Decider(taskService), logger, nil))
		if err := notifier.Start(context.Background()); err != nil {
			logger.Warn("im runtime failed to start, continuing without it", "error", err)
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := notifier.Close(closeCtx); err != nil {
				logger.Error("close im runtime", "error", err)
			}
		}()
	}

	reclaimCtx, stopReclaim := context.WithCancel(context.Background())
	defer stopReclaim()
	go maintenanceLoop(reclaimCtx, taskService, workerService, cfg.WorkerRetention, logger)

	handler := api.Chain(
		api.NewRouterV3(logger, web.Handler(), checks, api.V3Services{
			Products:  products,
			Materials: materialService,
			Projects:  projectService,
			Tasks:     taskService,
			Workers:   workerService,
		}),
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	banner.Info("shutdown complete")
	return nil
}

// newTaskService builds the V3 task service the HTTP layer uses. It is the one
// seam where platform.Config meets the task service: the configured
// max_attempts is passed explicitly so every task created at runtime (explicit,
// flow-derived or breakdown child) is stamped with it at insert time. The
// breakdown flow needs the plan reader as well, so the artifact can be
// materialised inside the approval transaction.
//
// The wiring is load-bearing: dropping WithMaxAttempts makes the service fall
// back to the built-in default of 3 and silently ignores the configuration,
// which is exactly what TestConfigMaxAttemptsReachesCreatedTask fails on.
func newTaskService(st *store.Store, cfg platform.Config) *appv3.Service {
	return appv3.New(st).
		WithMaxAttempts(cfg.MaxAttempts).
		WithMaterialReader(planReader{st: st, dataDir: cfg.DataDir})
}

// maintenanceLoop runs one sweep before the first tick, then on a fixed
// interval. Sweeping at startup matters: rows that went stale while the process
// was down would otherwise stay listed for a whole interval (issue #163).
func maintenanceLoop(ctx context.Context, tasks *appv3.Service, workers *appv3.WorkerService,
	retention time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	sweepOnce(ctx, tasks, workers, retention, logger)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepOnce(ctx, tasks, workers, retention, logger)
		}
	}
}

// sweepOnce is one maintenance pass: expired leases go back to the pool, then
// stale worker occupancy is released, then the registry drops the rows of
// workers that stopped reporting longer than retention ago and hold no task.
//
// Reclaim runs first on purpose. It clears current_task_id for workers whose
// lease expired, so a node that just lost its card is not deleted as a busy node
// one interval later: the prune in the same pass already sees it idle. A worker
// that is still holding a card is never pruned, however stale its last_seen is —
// current_task_id is the only record of that occupancy (issue #163).
//
// The release step covers the pointers reclaim cannot: a task cancelled, failed
// or cancelled by a project close before its holder was released, and a task row
// hard-deleted under a live pointer (issue #165 §1). Running it before the prune
// is what makes "holder went away, card is gone" one-pass instead of pending a
// manual fix; rows holding an in_progress card are still never released.
func sweepOnce(ctx context.Context, tasks *appv3.Service, workers *appv3.WorkerService,
	retention time.Duration, logger *slog.Logger) {
	if n, err := tasks.ReclaimExpired(ctx); err != nil {
		logger.Error("reclaim expired leases", "error", err)
	} else if n > 0 {
		logger.Info("reclaimed expired leases", "count", n)
	}

	if n, err := workers.ReleaseStale(ctx); err != nil {
		logger.Error("release stale worker pointers", "error", err)
	} else if n > 0 {
		logger.Info("released stale worker pointers", "count", n)
	}

	pruned, err := workers.PruneStale(ctx, retention)
	if err != nil {
		logger.Error("prune stale workers", "error", err, "retention", retention.String())
		return
	}
	if pruned > 0 {
		logger.Info("pruned stale workers", "count", pruned, "retention", retention.String())
	}
}

// planReader reads a material body from data_dir for the breakdown flow rule.
type planReader struct {
	st      *store.Store
	dataDir string
}

func (p planReader) Read(ctx context.Context, materialID string) ([]byte, error) {
	m, err := storev3.GetMaterial(ctx, p.st.Read(), materialID)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(p.dataDir, filepath.FromSlash(m.Path)))
}

// docMaterialReader reads a material body from data_dir for the doc importer,
// which needs the stored title as the document name.
type docMaterialReader struct {
	st      *store.Store
	dataDir string
}

func (r docMaterialReader) MaterialMarkdown(ctx context.Context, materialID string) (string, []byte, error) {
	m, err := storev3.GetMaterial(ctx, r.st.Read(), materialID)
	if err != nil {
		return "", nil, err
	}
	content, err := os.ReadFile(filepath.Join(r.dataDir, filepath.FromSlash(m.Path)))
	if err != nil {
		return "", nil, err
	}
	return m.Title, content, nil
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
		"log_level", cfg.LogLevel,
	)
}
