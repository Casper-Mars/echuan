// Package im is the boundary between the platform and the Feishu bot
// integration. Everything Feishu specific lives behind this package so the
// rest of the platform never imports the SDK.
package im

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Casper-Mars/echuan/internal/im/feishu"
)

// Config mirrors the resolved configuration of the integration.
type Config struct {
	Enabled        bool
	AppID          string
	AppSecret      string
	AllowedChatIDs []string
	PollInterval   time.Duration
	QuietHours     string
	MaxCards       int
	DocFolderToken string
}

// Runtime owns the long connection and, once 任务-04 lands, the inbox poller.
type Runtime struct {
	cfg    Config
	logger *slog.Logger
	client *feishu.Client
	conn   *feishu.Connection
	poller *Poller
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup
}

// New returns the runtime, or nil when the integration is disabled. Callers
// treat nil as "feature off" and keep the platform unchanged.
func New(cfg Config, logger *slog.Logger) *Runtime {
	if !cfg.Enabled {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{
		cfg:    cfg,
		logger: logger,
		client: feishu.New(cfg.AppID, cfg.AppSecret),
		conn:   feishu.NewConnection(cfg.AppID, cfg.AppSecret, logger, nil),
	}
}

// Client exposes the card API for the callers that will wire it in 任务-03/04.
func (r *Runtime) Client() *feishu.Client {
	if r == nil {
		return nil
	}
	return r.client
}

// SetDocs attaches the material → document importer to the poller.
func (r *Runtime) SetDocs(docs DocProvider) {
	if r == nil || r.poller == nil {
		return
	}
	r.poller.SetDocs(docs)
}

// SetActionHandler wires the card callback to the decision handler. It must be
// called before Start.
func (r *Runtime) SetActionHandler(h *ActionHandler) {
	if r == nil || h == nil || r.conn == nil {
		return
	}
	r.conn.SetCardHandler(h.Handle)
}

// SetPoller attaches the outbound poller. It must be called before Start.
func (r *Runtime) SetPoller(p *Poller) {
	if r == nil {
		return
	}
	r.poller = p
}

// Start launches the long connection and returns immediately: a slow or failed
// connection must never delay the HTTP listener. Errors surface through the
// lifecycle callbacks and the connection goroutine.
func (r *Runtime) Start(ctx context.Context) error {
	if r.cfg.AppID == "" || r.cfg.AppSecret == "" {
		return errors.New("im: app_id and app_secret are required")
	}
	if r.conn == nil {
		return errors.New("im: connection not initialised")
	}

	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(r.done)
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Error("im runtime panicked", "panic", rec)
			}
		}()
		if err := r.conn.Start(runCtx); err != nil {
			r.logger.Error("im long connection stopped with error", "error", err)
			return
		}
		r.logger.Info("im long connection stopped")
	}()

	if r.poller != nil {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					r.logger.Error("im poller panicked", "panic", rec)
				}
			}()
			r.poller.Run(runCtx)
		}()
	}

	r.logger.Info("im runtime starting",
		"app_id", r.cfg.AppID,
		"chats", len(r.cfg.AllowedChatIDs),
		"poll_interval", r.cfg.PollInterval.String(),
	)
	return nil
}

// Close stops the connection and waits for the goroutine, bounded by ctx.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	var err error
	if r.conn != nil {
		err = r.conn.Close(ctx)
	}
	waited := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	}
	r.logger.Info("im runtime stopped")
	return err
}
