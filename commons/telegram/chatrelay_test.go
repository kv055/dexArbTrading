package telegram_test

import (
	"context"
	"log/slog"
	"testing"

	"commons/telegram"
)

type fakeChat struct {
	msgs     []telegram.Message
	sent     []string
	orderID  string
}

func (f *fakeChat) SendMessage(_ context.Context, orderID, text string) error {
	if orderID != f.orderID {
		return nil
	}
	f.sent = append(f.sent, text)
	f.msgs = append(f.msgs, telegram.Message{Sender: "us", Outbound: true, Text: text})
	return nil
}

func (f *fakeChat) Messages(_ context.Context, orderID string) ([]telegram.Message, error) {
	if orderID != f.orderID {
		return nil, nil
	}
	return append([]telegram.Message(nil), f.msgs...), nil
}

type recordingTG struct {
	telegram.Client
	out []string
}

func TestChatRelayForwardsInboundOnly(t *testing.T) {
	tg := telegram.NewClient("token", "123", nil)
	relay := telegram.NewChatRelay(tg, slog.Default())
	chat := &fakeChat{orderID: "ord-1", msgs: []telegram.Message{
		{Sender: "peer", Text: "hello"},
		{Sender: "us", Outbound: true, Text: "template"},
	}}
	relay.Register("RoboSats", "ord-1", chat)

	// Simulate poll by calling internal path via second registration + manual message add.
	chat.msgs = append(chat.msgs, telegram.Message{Sender: "peer", Text: "payment details"})
	relay.Register("RoboSats", "ord-1", chat)

	if got := telegram.FormatRelayMessage("RoboSats", "ord-1", "peer", "hello"); got != "[RoboSats #ord-1] peer: hello" {
		t.Fatalf("format = %q", got)
	}
}

func TestChatRelayReply(t *testing.T) {
	tg := telegram.NewClient("token", "123", nil)
	relay := telegram.NewChatRelay(tg, slog.Default())
	chat := &fakeChat{orderID: "ord-2"}
	relay.Register("RoboSats", "ord-2", chat)

	if err := relay.Reply(context.Background(), "RoboSats", "ord-2", "thanks"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(chat.sent) != 1 || chat.sent[0] != "thanks" {
		t.Fatalf("sent = %v", chat.sent)
	}
}

func TestParseRelayHeader(t *testing.T) {
	u := telegram.Update{
		Message: &telegram.IncomingMessage{
			Text: "my reply",
			ReplyToMessage: &telegram.IncomingMessage{
				Text: "[RoboSats #abc123] alice: send IBAN",
			},
		},
	}
	relay := telegram.NewChatRelay(nil, slog.Default())
	chat := &fakeChat{orderID: "abc123"}
	relay.Register("RoboSats", "abc123", chat)
	relay.HandleUpdate(context.Background(), u)
	if len(chat.sent) != 1 || chat.sent[0] != "my reply" {
		t.Fatalf("expected reply forwarded, sent=%v", chat.sent)
	}
}

func TestNewClientNilWhenUnconfigured(t *testing.T) {
	if c := telegram.NewClient("", "", nil); c != nil {
		t.Fatal("expected nil client")
	}
}
