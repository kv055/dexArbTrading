package telegram

import (
	"net/http"
	"sync"
	"time"
)

// Store is a concurrency-safe, hot-reloadable holder for Telegram bot Config and
// the derived *Client. It is the runtime replacement for the env-only Config
// loaded once at startup: both dexMmController and dexFiatController read it, and
// the settings poller calls Set whenever the shared telegram_settings row changes
// so a config swap propagates without a restart.
//
// The cached *Client is rebuilt only when the token or chat ID actually changes
// (version-gated), so frequent polls are cheap and callers can invoke Client() on
// every send without rebuilding.
type Store struct {
	mu         sync.RWMutex
	cfg        Config
	version    int64
	client     *Client
	httpClient *http.Client
}

// NewStore creates a store seeded with cfg. httpClient is reused for every
// rebuilt client; nil falls back to the Client default (10s timeout).
func NewStore(cfg Config, httpClient *http.Client) *Store {
	s := &Store{httpClient: httpClient}
	s.setLocked(cfg)
	return s
}

// Get returns the current config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Version returns the internal version, incremented on every Set that changes
// the token or chat ID. Useful for tests and observers.
func (s *Store) Version() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Set replaces the current config and rebuilds the cached client when the token
// or chat ID changed. A no-op when cfg is identical to the current value.
func (s *Store) Set(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLocked(cfg)
}

func (s *Store) setLocked(cfg Config) {
	cfg.Token = trim(cfg.Token)
	cfg.ChatID = trim(cfg.ChatID)
	cfg.WebhookURL = trim(cfg.WebhookURL)
	if cfg == s.cfg {
		return
	}
	s.cfg = cfg
	s.version++
	s.client = NewClient(cfg.Token, cfg.ChatID, s.httpClient)
}

// Client returns the current *Client, or nil when the bot is not configured
// (empty token or chat ID). The returned pointer is stable until the next Set
// that changes token/chat ID, so callers may cache it briefly.
func (s *Store) Client() *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Enabled reports whether outbound Telegram traffic is currently possible.
func (s *Store) Enabled() bool {
	c := s.Client()
	return c != nil && c.Enabled()
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// PollInterval is the default cadence at which controllers poll the shared
// telegram_settings row for hot-reload changes.
const PollInterval = 10 * time.Second
