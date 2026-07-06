package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Message is a venue-agnostic trade-chat message.
type Message struct {
	Time     string
	Sender   string
	Outbound bool
	Text     string
}

// TradeChat is a venue's trade-chat transport (RoboSats, Bisq2 Easy, HodlHodl, …).
type TradeChat interface {
	SendMessage(ctx context.Context, orderID, text string) error
	Messages(ctx context.Context, orderID string) ([]Message, error)
}

const defaultRelayPoll = 15 * time.Second
const defaultRelayTTL = 48 * time.Hour

var relayHeader = regexp.MustCompile(`^\[([^\]]+)\s+#([^\]]+)\]`)

// ActiveChat tracks one open trade chat relayed to Telegram.
type ActiveChat struct {
	Exchange   string
	OrderID    string
	Chat       TradeChat
	Seen       map[string]struct{}
	Registered time.Time
}

func sessionKey(exchange, orderID string) string {
	return strings.ToLower(strings.TrimSpace(exchange)) + ":" + strings.TrimSpace(orderID)
}

// ChatRelay polls active trade chats and forwards new inbound messages to Telegram.
// Operator replies (Telegram reply-to or reply: callbacks) are sent back to the venue.
//
// The outbound Telegram client is mutable at runtime via SetClient so the relay
// picks up a hot-reloaded bot token/chat ID without a restart.
type ChatRelay struct {
	tg    *Client
	tgMu  sync.RWMutex
	mu    sync.RWMutex
	active map[string]*ActiveChat

	pollInterval time.Duration
	ttl          time.Duration
	log          *slog.Logger
}

// NewChatRelay creates a relay. tg may be nil to disable outbound Telegram.
func NewChatRelay(tg *Client, log *slog.Logger) *ChatRelay {
	if log == nil {
		log = slog.Default()
	}
	return &ChatRelay{
		tg:           tg,
		active:       make(map[string]*ActiveChat),
		pollInterval: defaultRelayPoll,
		ttl:          defaultRelayTTL,
		log:          log,
	}
}

// SetClient swaps the outbound Telegram client. Safe to call concurrently with
// Run; the next poll uses the new client. Pass nil to disable outbound traffic.
func (r *ChatRelay) SetClient(tg *Client) {
	r.tgMu.Lock()
	r.tg = tg
	r.tgMu.Unlock()
}

// client returns the current outbound Telegram client under a read lock.
func (r *ChatRelay) client() *Client {
	r.tgMu.RLock()
	defer r.tgMu.RUnlock()
	return r.tg
}

// WithPollInterval overrides the default poll interval.
func (r *ChatRelay) WithPollInterval(d time.Duration) *ChatRelay {
	if d > 0 {
		r.pollInterval = d
	}
	return r
}

// Register starts relaying chat for exchange/orderID using the given TradeChat transport.
func (r *ChatRelay) Register(exchange, orderID string, chat TradeChat) {
	exchange = strings.TrimSpace(exchange)
	orderID = strings.TrimSpace(orderID)
	if exchange == "" || orderID == "" || chat == nil {
		return
	}
	key := sessionKey(exchange, orderID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.active[key]; ok {
		existing.Chat = chat
		return
	}
	r.active[key] = &ActiveChat{
		Exchange:   exchange,
		OrderID:    orderID,
		Chat:       chat,
		Seen:       make(map[string]struct{}),
		Registered: time.Now().UTC(),
	}
	r.log.Info("telegram relay: registered chat", "exchange", exchange, "order_id", orderID)
}

// Unregister stops relaying a chat session.
func (r *ChatRelay) Unregister(exchange, orderID string) {
	key := sessionKey(exchange, orderID)
	r.mu.Lock()
	delete(r.active, key)
	r.mu.Unlock()
}

// Run polls active chats until ctx is cancelled. A nil/unconfigured client at
// startup is not fatal: the loop keeps ticking and picks up a client swapped in
// via SetClient so hot-reloaded config takes effect without a restart.
func (r *ChatRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		r.pollOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *ChatRelay) pollOnce(ctx context.Context) {
	r.expireStale()
	snapshot := r.snapshot()
	for _, ac := range snapshot {
		r.pollChat(ctx, ac)
	}
}

func (r *ChatRelay) expireStale() {
	cutoff := time.Now().UTC().Add(-r.ttl)
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, ac := range r.active {
		if ac.Registered.Before(cutoff) {
			delete(r.active, key)
		}
	}
}

func (r *ChatRelay) snapshot() []*ActiveChat {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ActiveChat, 0, len(r.active))
	for _, ac := range r.active {
		out = append(out, ac)
	}
	return out
}

func (r *ChatRelay) pollChat(ctx context.Context, ac *ActiveChat) {
	msgs, err := ac.Chat.Messages(ctx, ac.OrderID)
	if err != nil {
		r.log.Warn("telegram relay: read messages", "exchange", ac.Exchange, "order_id", ac.OrderID, "error", err)
		return
	}
	tg := r.client()
	for _, m := range msgs {
		if m.Outbound || strings.TrimSpace(m.Text) == "" {
			continue
		}
		fp := messageFingerprint(m)
		if r.alreadySeen(ac, fp) {
			continue
		}
		r.markSeen(ac, fp)
		if tg == nil {
			continue
		}
		text := FormatRelayMessage(ac.Exchange, ac.OrderID, m.Sender, m.Text)
		if err := tg.Notify(ctx, text); err != nil {
			r.log.Warn("telegram relay: forward message", "exchange", ac.Exchange, "order_id", ac.OrderID, "error", err)
		}
	}
}

func messageFingerprint(m Message) string {
	return strings.TrimSpace(m.Time) + "|" + strings.TrimSpace(m.Sender) + "|" + strings.TrimSpace(m.Text)
}

func (r *ChatRelay) alreadySeen(ac *ActiveChat, fp string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := ac.Seen[fp]
	return ok
}

func (r *ChatRelay) markSeen(ac *ActiveChat, fp string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ac.Seen == nil {
		ac.Seen = make(map[string]struct{})
	}
	ac.Seen[fp] = struct{}{}
}

// FormatRelayMessage builds the Telegram body for an inbound trade-chat message.
func FormatRelayMessage(exchange, orderID, sender, text string) string {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		sender = "counterparty"
	}
	return fmt.Sprintf("[%s #%s] %s: %s", exchange, orderID, sender, text)
}

// Reply sends operator text back to the venue chat.
func (r *ChatRelay) Reply(ctx context.Context, exchange, orderID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("telegram relay: empty reply")
	}
	key := sessionKey(exchange, orderID)
	r.mu.RLock()
	ac, ok := r.active[key]
	r.mu.RUnlock()
	if !ok || ac.Chat == nil {
		return fmt.Errorf("telegram relay: no active chat for %s #%s", exchange, orderID)
	}
	if err := ac.Chat.SendMessage(ctx, ac.OrderID, text); err != nil {
		return err
	}
	r.markSeen(ac, messageFingerprint(Message{Sender: "operator", Text: text, Outbound: true}))
	return nil
}

// HandleUpdate routes operator replies from a Telegram webhook update.
func (r *ChatRelay) HandleUpdate(ctx context.Context, u Update) {
	if u.CallbackQuery != nil {
		r.handleCallback(ctx, u.CallbackQuery)
		return
	}
	if u.Message != nil {
		r.handleMessage(ctx, u.Message)
	}
}

func (r *ChatRelay) handleCallback(ctx context.Context, cq *CallbackQuery) {
	parts := strings.SplitN(cq.Data, ":", 3)
	if len(parts) != 3 || parts[0] != "reply" {
		if tg := r.client(); tg != nil {
			tg.AnswerCallback(ctx, cq.ID, "Unknown action.")
		}
		return
	}
	// reply:<exchange>:<orderID> — operator must send text as a follow-up message for now.
	if tg := r.client(); tg != nil {
		tg.AnswerCallback(ctx, cq.ID, fmt.Sprintf("Reply in chat to [%s #%s] messages.", parts[1], parts[2]))
	}
}

func (r *ChatRelay) handleMessage(ctx context.Context, msg *IncomingMessage) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}
	exchange, orderID := parseRelayHeader(msg.ReplyToMessage)
	if exchange == "" || orderID == "" {
		return
	}
	tg := r.client()
	if err := r.Reply(ctx, exchange, orderID, text); err != nil {
		r.log.Warn("telegram relay: operator reply failed", "exchange", exchange, "order_id", orderID, "error", err)
		if tg != nil {
			_ = tg.Notify(ctx, fmt.Sprintf("Failed to send reply to %s #%s: %v", exchange, orderID, err))
		}
		return
	}
	if tg != nil {
		_ = tg.Notify(ctx, fmt.Sprintf("Sent to %s #%s.", exchange, orderID))
	}
}

func parseRelayHeader(replyTo *IncomingMessage) (exchange, orderID string) {
	if replyTo == nil {
		return "", ""
	}
	m := relayHeader.FindStringSubmatch(strings.TrimSpace(replyTo.Text))
	if len(m) != 3 {
		return "", ""
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
}
