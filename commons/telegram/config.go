package telegram

import (
	"os"
	"strings"
)

// Config holds Telegram bot credentials and optional webhook URL.
type Config struct {
	Token      string
	ChatID     string
	WebhookURL string
}

// LoadConfig reads TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID, and <prefix>_TELEGRAM_WEBHOOK_URL.
// Pass an empty prefix for the generic names only; pass "PUSHPUSH" or "DEXMM" for service-specific webhooks.
func LoadConfig(prefix string) Config {
	prefix = strings.TrimSpace(prefix)
	webhookKey := "TELEGRAM_WEBHOOK_URL"
	if prefix != "" {
		webhookKey = prefix + "_TELEGRAM_WEBHOOK_URL"
	}
	return Config{
		Token:      strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		ChatID:     strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		WebhookURL: strings.TrimSpace(os.Getenv(webhookKey)),
	}
}

// Enabled reports whether outbound Telegram traffic is configured.
func (c Config) Enabled() bool {
	return c.Token != "" && c.ChatID != ""
}
