package telegram

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"sync"
)

// webhookSecretHeader is the header Telegram attaches to webhook requests when
// a secret_token was registered via setWebhook.
const webhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

var (
	webhookSecretMu sync.RWMutex
	// webhookSecret is the deployment-wide secret registered with Telegram on
	// every SetWebhook call and required on inbound webhook requests. One env
	// var covers all services because they share a single bot.
	webhookSecret = strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET"))
)

// WebhookSecret returns the configured webhook secret ("" when unset).
func WebhookSecret() string {
	webhookSecretMu.RLock()
	defer webhookSecretMu.RUnlock()
	return webhookSecret
}

// SetWebhookSecret overrides the env-derived webhook secret (tests).
func SetWebhookSecret(s string) {
	webhookSecretMu.Lock()
	defer webhookSecretMu.Unlock()
	webhookSecret = strings.TrimSpace(s)
}

// WebhookAuthorized reports whether an inbound webhook request carries the
// secret token registered via SetWebhook. It fails CLOSED when no secret is
// configured: an open webhook endpoint would let anyone forge Telegram
// callback decisions (e.g. fiat-release approvals).
func WebhookAuthorized(r *http.Request) bool {
	secret := WebhookSecret()
	if secret == "" {
		return false
	}
	provided := r.Header.Get(webhookSecretHeader)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) == 1
}
