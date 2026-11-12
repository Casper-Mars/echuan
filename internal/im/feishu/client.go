// Package feishu adapts the Lark/Feishu SDK to the small surface the platform
// needs: sending and patching interactive cards, and holding the long-lived
// WebSocket connection. The SDK is imported here and nowhere else.
package feishu

import (
	"context"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// receiveIDTypeChatID is the stable identifier for a conversation. open_id is
// scoped to a single app, so it is not usable as a cross-app allow-list key.
const receiveIDTypeChatID = "chat_id"

// Client wraps the OpenAPI calls used by the card channel.
type Client struct {
	api *lark.Client
}

// New returns a client that authenticates with the app credentials. The SDK
// fetches and refreshes the tenant access token on its own.
func New(appID, appSecret string) *Client {
	return &Client{api: lark.NewClient(appID, appSecret)}
}

// SendCard posts an interactive card to chatID and returns the message id,
// which is the handle for later patches.
func (c *Client) SendCard(ctx context.Context, chatID, cardJSON string) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDTypeChatID).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("interactive").
			Content(cardJSON).
			Build()).
		Build()

	resp, err := c.api.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("send card: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("send card: code=%d msg=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", fmt.Errorf("send card: response has no message id")
	}
	return *resp.Data.MessageId, nil
}

// PatchCard replaces the content of a card the app already sent. Feishu only
// allows this within 14 days of the original message.
func (c *Client) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(cardJSON).
			Build()).
		Build()

	resp, err := c.api.Im.V1.Message.Patch(ctx, req)
	if err != nil {
		return fmt.Errorf("patch card %s: %w", messageID, err)
	}
	if !resp.Success() {
		return fmt.Errorf("patch card %s: code=%d msg=%s", messageID, resp.Code, resp.Msg)
	}
	return nil
}
