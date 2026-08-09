package telegram

import (
	"context"
	"testing"
)

type relayTestChat struct {
	msgs []Message
}

func (f *relayTestChat) SendMessage(context.Context, string, string) error { return nil }

func (f *relayTestChat) Messages(context.Context, string) ([]Message, error) {
	return append([]Message(nil), f.msgs...), nil
}

func TestChatRelaySkipsKnownOutbound(t *testing.T) {
	chat := &relayTestChat{msgs: []Message{{Sender: "us", Text: "payment request"}}}
	relay := NewChatRelay(nil, nil)
	relay.Register("robosats", "order-1", chat, "payment request")
	active := relay.snapshot()[0]
	relay.pollChat(context.Background(), active)
	if len(active.Seen) != 0 || len(active.Outbound) != 1 {
		t.Fatalf("outbound message was treated as inbound: seen=%v outbound=%v", active.Seen, active.Outbound)
	}

	chat.msgs = append(chat.msgs, Message{Sender: "peer", Text: "my IBAN"})
	relay.pollChat(context.Background(), active)
	if len(active.Seen) != 1 {
		t.Fatalf("counterparty message was not recorded: seen=%v", active.Seen)
	}
}

func TestChatRelaySkipsMarkedPaymentRequest(t *testing.T) {
	chat := &relayTestChat{msgs: []Message{{Sender: "us", Text: "Please reply\n" + AutomatedPaymentRequestMarker}}}
	relay := NewChatRelay(nil, nil)
	relay.Register("robosats", "order-1", chat)
	active := relay.snapshot()[0]
	relay.pollChat(context.Background(), active)
	if len(active.Seen) != 0 {
		t.Fatalf("marked payment request was treated as inbound: seen=%v", active.Seen)
	}
}
