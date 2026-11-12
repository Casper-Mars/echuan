// Package im is the boundary between the platform and the Feishu bot
// integration. Everything Feishu specific lives behind this package so the
// rest of the platform never imports the SDK.
package im

import (
	"context"
	"log/slog"
	"time"
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

// Runtime owns the long connection and the inbox poller.
type Runtime struct {
	cfg    Config
	logger *slog.Logger
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
	return &Runtime{cfg: cfg, logger: logger}
}

// Start opens the long connection and starts polling the inbox.
//
// TODO(任务-02): wire the larkws client, the tenant_access_token refresh and
// the inbox poller here.
func (r *Runtime) Start(ctx context.Context) error {
	r.logger.Info("im runtime starting",
		"app_id", r.cfg.AppID,
		"chats", len(r.cfg.AllowedChatIDs),
		"poll_interval", r.cfg.PollInterval.String(),
	)
	return nil
}

// Close stops the poller and closes the long connection.
func (r *Runtime) Close(ctx context.Context) error {
	r.logger.Info("im runtime stopped")
	return nil
}
