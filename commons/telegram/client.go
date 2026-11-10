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

// SentMessage identifies a Telegram message created by SendInteractiveMessage.
type SentMessage struct {
	MessageID int64
	ChatID    string
}

// BotInfo is the public subset returned by Telegram's getMe endpoint.
type BotInfo struct {
	Username string `json:"username"`
}

// GetBotInfo validates token and returns the bot's public username. It does not
// require a destination chat, so it is safe to use while pairing a new bot.
func GetBotInfo(ctx context.Context, token string, client *http.Client) (BotInfo, error) {
	raw, status, err := postWithToken(ctx, token, "getMe", map[string]any{}, client)
	if err != nil {
		return BotInfo{}, err
	}
	var response struct {
		OK     bool    `json:"ok"`
		Result BotInfo `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return BotInfo{}, fmt.Errorf("decode Telegram getMe response: %w", err)
	}
	if status != http.StatusOK || !response.OK || strings.TrimSpace(response.Result.Username) == "" {
		return BotInfo{}, fmt.Errorf("telegram getMe error: %s", raw)
	}
	return response.Result, nil
}

// SetWebhookWithToken registers a webhook before a destination chat has been
// paired. The normal Client method remains the preferred path after pairing.
func SetWebhookWithToken(ctx context.Context, token, url string, client *http.Client) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("telegram webhook URL is empty")
	}
	body := map[string]any{"url": url}
	if secret := WebhookSecret(); secret != "" {
		body["secret_token"] = secret
	}
	raw, status, err := postWithToken(ctx, token, "setWebhook", body, client)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("telegram setWebhook error: %s", raw)
	}
	return nil
}

func postWithToken(ctx context.Context, token, method string, body map[string]any, client *http.Client) ([]byte, int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, 0, fmt.Errorf("telegram: bot token not configured")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method), bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, nil
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
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, nil
}

// SendMessage posts text to the configured chat. keyboard is optional.
func (c *Client) SendMessage(ctx context.Context, text string, keyboard *InlineKeyboard) error {
	_, err := c.sendMessage(ctx, text, keyboard)
	return err
}

// SendInteractiveMessage posts text and returns the created message identity so
// callers can persist it and later replace the inline keyboard with a result.
func (c *Client) SendInteractiveMessage(ctx context.Context, text string, keyboard *InlineKeyboard) (SentMessage, error) {
	raw, err := c.sendMessage(ctx, text, keyboard)
	if err != nil {
		return SentMessage{}, err
	}
	var response struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID json.Number `json:"id"`
			} `json:"chat"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return SentMessage{}, fmt.Errorf("decode Telegram sendMessage response: %w", err)
	}
	if !response.OK || response.Result.MessageID == 0 {
		return SentMessage{}, fmt.Errorf("telegram sendMessage returned no message identity")
	}
	chatID := response.Result.Chat.ID.String()
	if chatID == "" {
		chatID = c.chatID
	}
	return SentMessage{MessageID: response.Result.MessageID, ChatID: chatID}, nil
}

func (c *Client) sendMessage(ctx context.Context, text string, keyboard *InlineKeyboard) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("telegram: client not configured")
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
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("telegram sendMessage error: %s", raw)
	}
	return raw, nil
}

// Notify sends a one-way alert without inline buttons.
func (c *Client) Notify(ctx context.Context, text string) error {
	return c.SendMessage(ctx, text, nil)
}

// EditMessage replaces an existing message and its inline keyboard. Passing a
// nil keyboard removes all buttons.
func (c *Client) EditMessage(ctx context.Context, chatID string, messageID int64, text string, keyboard *InlineKeyboard) error {
	if c == nil {
		return fmt.Errorf("telegram: client not configured")
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		chatID = c.chatID
	}
	if messageID == 0 {
		return fmt.Errorf("telegram: message id is required")
	}
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if keyboard == nil {
		body["reply_markup"] = map[string]any{"inline_keyboard": []any{}}
	} else {
		body["reply_markup"] = keyboard.Marshal()
	}
	raw, status, err := c.post(ctx, "editMessageText", body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("telegram editMessageText error: %s", raw)
	}
	return nil
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
	body := map[string]any{"url": url}
	// Register the shared secret so Telegram echoes it back on every webhook
	// request; inbound handlers require it via WebhookAuthorized.
	if secret := WebhookSecret(); secret != "" {
		body["secret_token"] = secret
	}
	raw, status, err := c.post(ctx, "setWebhook", body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("telegram setWebhook error: %s", raw)
	}
	return nil
}
