package telegram

import (
	"net/http/httptest"
	"testing"
)

func TestWebhookAuthorized(t *testing.T) {
	orig := WebhookSecret()
	t.Cleanup(func() { SetWebhookSecret(orig) })

	SetWebhookSecret("")
	r := httptest.NewRequest("POST", "/telegram-webhook", nil)
	if WebhookAuthorized(r) {
		t.Error("empty secret must fail closed")
	}

	SetWebhookSecret("tok")
	r = httptest.NewRequest("POST", "/telegram-webhook", nil)
	if WebhookAuthorized(r) {
		t.Error("missing header must be rejected")
	}
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	if WebhookAuthorized(r) {
		t.Error("wrong token must be rejected")
	}
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", "tok")
	if !WebhookAuthorized(r) {
		t.Error("matching token must be accepted")
	}
}
