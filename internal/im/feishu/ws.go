package feishu

import (
	"context"
	"log/slog"
	"sync"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"github.com/larksuite/oapi-sdk-go/v3/ws"
)

// CardHandler handles one card action callback. It must return within three
// seconds: Feishu fails the interaction when the callback stalls.
type CardHandler func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error)

// Connection owns the WebSocket client and its event dispatcher.
type Connection struct {
	appID     string
	appSecret string
	logger    *slog.Logger
	card      CardHandler

	mu  sync.Mutex
	cli *ws.Client
}

// NewConnection prepares a long connection. Nothing happens on the wire until
// Start is called.
func NewConnection(appID, appSecret string, logger *slog.Logger, card CardHandler) *Connection {
	if logger == nil {
		logger = slog.Default()
	}
	if card == nil {
		card = DefaultCardHandler(logger)
	}
	return &Connection{appID: appID, appSecret: appSecret, logger: logger, card: card}
}

// Start blocks until the connection stops. Reconnection is handled by the SDK
// (2 minute interval, unlimited retries); callers run this in a goroutine.
func (c *Connection) Start(ctx context.Context) error {
	d := dispatcher.NewEventDispatcher("", "")
	d.OnP2CardActionTrigger(c.card)

	cli := ws.NewClient(c.appID, c.appSecret,
		ws.WithEventHandler(d),
		ws.WithAutoReconnect(true),
		ws.WithOnReady(func() { c.logger.Info("im long connection ready") }),
		ws.WithOnReconnecting(func() { c.logger.Warn("im long connection reconnecting") }),
		ws.WithOnReconnected(func() { c.logger.Info("im long connection reconnected") }),
		ws.WithOnDisconnected(func() { c.logger.Warn("im long connection disconnected") }),
		ws.WithOnError(func(err error) { c.logger.Error("im long connection error", "error", err) }),
	)

	c.mu.Lock()
	c.cli = cli
	c.mu.Unlock()

	if err := cli.Start(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// Close stops the connection and waits for in-flight handlers, bounded by ctx.
func (c *Connection) Close(ctx context.Context) error {
	c.mu.Lock()
	cli := c.cli
	c.mu.Unlock()
	if cli == nil {
		return nil
	}
	return cli.CloseAndWait(ctx)
}

// DefaultCardHandler acknowledges card actions until the decision flow lands in
// 任务-05. Returning a toast proves the inbound path works end to end.
func DefaultCardHandler(logger *slog.Logger) CardHandler {
	return func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		logger.Info("im card action received")
		return &callback.CardActionTriggerResponse{
			Toast: &callback.Toast{Type: "info", Content: "驿传：卡片通道已接通，决策逻辑待接入"},
		}, nil
	}
}
