package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestInteractiveMessageReturnsIdentityAndEditRemovesKeyboard(t *testing.T) {
	var methods []string
	var bodies []map[string]any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.URL.Path)
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		response := `{"ok":true,"result":{"message_id":77,"chat":{"id":123}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(response)),
			Header:     make(http.Header),
		}, nil
	})}
	tg := NewClient("token", "123", client)
	keyboard := InlineKeyboard{}
	keyboard.Row(Button("✅ Yes", "pay:yes:1"), Button("❌ No", "pay:no:1"))

	sent, err := tg.SendInteractiveMessage(context.Background(), "question", &keyboard)
	if err != nil {
		t.Fatal(err)
	}
	if sent.MessageID != 77 || sent.ChatID != "123" {
		t.Fatalf("sent=%+v", sent)
	}
	if err := tg.EditMessage(context.Background(), sent.ChatID, sent.MessageID, "answered", nil); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || !strings.HasSuffix(methods[0], "/sendMessage") ||
		!strings.HasSuffix(methods[1], "/editMessageText") {
		t.Fatalf("methods=%v", methods)
	}
	replyMarkup, ok := bodies[1]["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("edit reply_markup=%T", bodies[1]["reply_markup"])
	}
	if keyboard, ok := replyMarkup["inline_keyboard"].([]any); !ok || len(keyboard) != 0 {
		t.Fatalf("edit keyboard=%#v", replyMarkup["inline_keyboard"])
	}
}
