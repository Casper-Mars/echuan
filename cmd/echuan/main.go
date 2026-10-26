// Command echuan starts the 驿传 platform service: a single Go binary that
// serves the HTTP API and the embedded web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Casper-Mars/echuan/internal/api"
	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/platform"
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

	for _, dir := range []string{cfg.DataDir, cfg.WorkspaceDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	application := app.New(cfg, logger)
	defer func() {
		if err := application.Close(); err != nil {
			logger.Error("close application", "error", err)
		}
	}()

	// TODO(Task-02): open the SQLite store (WAL, busy_timeout, foreign_keys),
	// apply pending migrations and start the lease reclaim loop before the
	// listener is created. A migration failure must abort without listening.
	checks := []api.HealthChecker{
		api.NewPendingChecker("db"),
		api.NewPendingChecker("migration"),
	}

	handler := api.Chain(
		api.NewRouter(logger, web.Handler(), checks),
		api.Recover(logger),
		api.RequestID(),
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

	logger.Info("echuan starting",
		"version", platform.Version,
		"commit", platform.Commit,
		"listen", listener.Addr().String(),
		"data_dir", cfg.DataDir,
		"workspace_dir", cfg.WorkspaceDir,
		"log_level", cfg.LogLevel,
	)

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
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	// Stop accepting new requests, then wait for in-flight ones (≤10s).
	// TODO(Task-02): afterwards stop the reclaim loop and close the store.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}
