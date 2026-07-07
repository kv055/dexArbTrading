// Package telegram provides a shared Telegram Bot API client and trade-chat relay
// utilities used by dexMmController and dexFiatController.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client sends messages and answers callback queries via the Telegram Bot API.
// A nil client (from NewClient when token/chatID empty) disables outbound traffic.
type Client struct {
	token  string
	chatID string
	client *http.Client
}

// NewClient creates a Telegram client. Returns nil when token or chatID is empty.
func NewClient(token, chatID string, client *http.Client) *Client {
	token = strings.TrimSpace(token)
	chatID = strings.TrimSpace(chatID)
	if token == "" || chatID == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{token: token, chatID: chatID, client: client}
}

// Enabled reports whether the client can send messages.
func (c *Client) Enabled() bool {
	return c != nil
}

func (c *Client) api(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.token, method)
}

func (c *Client) post(ctx context.Context, method string, body map[string]any) ([]byte, int, error) {
	if c == nil {
		return nil, 0, fmt.Errorf("telegram: client not configured")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api(method), bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		recordAPICall(method, 0, err)
		return nil, 0, err
	}
	defer resp.Body.Close()
	recordAPICall(method, resp.StatusCode, nil)
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, nil
}

// SendMessage posts text to the configured chat. keyboard is optional.
func (c *Client) SendMessage(ctx context.Context, text string, keyboard *InlineKeyboard) error {
	if c == nil {
		return fmt.Errorf("telegram: client not configured")
	}
	body := map[string]any{
		"chat_id": c.chatID,
		"text":    text,
	}
	if keyboard != nil && len(*keyboard) > 0 {
		body["reply_markup"] = keyboard.Marshal()
	}
	raw, status, err := c.post(ctx, "sendMessage", body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("telegram sendMessage error: %s", raw)
	}
	return nil
}

// Notify sends a one-way alert without inline buttons.
func (c *Client) Notify(ctx context.Context, text string) error {
	return c.SendMessage(ctx, text, nil)
}

// AnswerCallback acknowledges a callback query with a short toast text.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) {
	if c == nil || callbackID == "" {
		return
	}
	_, _, _ = c.post(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	})
}

// SetWebhook registers the bot webhook URL with Telegram.
func (c *Client) SetWebhook(ctx context.Context, url string) error {
	if c == nil {
		return fmt.Errorf("telegram: client not configured")
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	raw, status, err := c.post(ctx, "setWebhook", map[string]any{"url": url})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("telegram setWebhook error: %s", raw)
	}
	return nil
}
