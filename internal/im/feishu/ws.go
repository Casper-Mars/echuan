package feishu

import (
	"context"
	"log/slog"
	"sync"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"github.com/larksuite/oapi-sdk-go/v3/ws"
)

// CardAction is one card interaction decoded from the SDK payload, so the IM
// package never has to import SDK types.
type CardAction struct {
	OpenID    string
	ChatID    string
	MessageID string
	// Action is the clicked button name: approve or reject.
	Action string
	// Form carries the submitted form fields, keyed by component name.
	Form map[string]string
}

// CardActionResult is what the user sees immediately after clicking.
type CardActionResult struct {
	Toast     string
	ToastType string // success | info | warning | error
}

// CardActionFunc handles one card action. It must return within three seconds:
// Feishu fails the interaction when the callback stalls.
type CardActionFunc func(ctx context.Context, action CardAction) CardActionResult

// Connection owns the WebSocket client and its event dispatcher.
type Connection struct {
	appID     string
	appSecret string
	logger    *slog.Logger

	mu   sync.Mutex
	cli  *ws.Client
	card CardActionFunc
}

// NewConnection prepares a long connection. Nothing happens on the wire until
// Start is called.
func NewConnection(appID, appSecret string, logger *slog.Logger, card CardActionFunc) *Connection {
	if logger == nil {
		logger = slog.Default()
	}
	if card == nil {
		card = DefaultCardHandler(logger)
	}
	return &Connection{appID: appID, appSecret: appSecret, logger: logger, card: card}
}

// SetCardHandler replaces the card handler. It must be called before Start.
func (c *Connection) SetCardHandler(card CardActionFunc) {
	if card == nil {
		return
	}
	c.mu.Lock()
	c.card = card
	c.mu.Unlock()
}

// Start blocks until the connection stops. Reconnection is handled by the SDK
// (2 minute interval, unlimited retries); callers run this in a goroutine.
func (c *Connection) Start(ctx context.Context) error {
	d := dispatcher.NewEventDispatcher("", "")
	d.OnP2CardActionTrigger(c.dispatch)

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

// dispatch adapts the SDK event to CardAction and back to a toast response.
func (c *Connection) dispatch(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	action := CardAction{Form: map[string]string{}}
	if event != nil && event.Event != nil {
		body := event.Event
		if body.Operator != nil {
			action.OpenID = body.Operator.OpenID
		}
		if body.Context != nil {
			action.ChatID = body.Context.OpenChatID
			action.MessageID = body.Context.OpenMessageID
		}
		if body.Action != nil {
			action.Action = body.Action.Name
			for key, value := range body.Action.FormValue {
				if s, ok := value.(string); ok {
					action.Form[key] = s
				}
			}
		}
	}

	c.mu.Lock()
	handler := c.card
	c.mu.Unlock()

	result := handler(ctx, action)
	if result.Toast == "" {
		return &callback.CardActionTriggerResponse{}, nil
	}
	toastType := result.ToastType
	if toastType == "" {
		toastType = "info"
	}
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{Type: toastType, Content: result.Toast},
	}, nil
}

// DefaultCardHandler acknowledges card actions until a real handler is wired.
func DefaultCardHandler(logger *slog.Logger) CardActionFunc {
	return func(ctx context.Context, action CardAction) CardActionResult {
		logger.Info("im card action received", "action", action.Action, "chat", action.ChatID)
		return CardActionResult{Toast: "驿传：卡片通道已接通，决策逻辑待接入", ToastType: "info"}
	}
}
