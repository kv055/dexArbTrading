package telegram_test

import (
	"net/http"
	"testing"

	"commons/telegram"
)

func TestStoreRebuildsClientOnlyOnChange(t *testing.T) {
	s := telegram.NewStore(telegram.Config{Token: "tok", ChatID: "1"}, &http.Client{Timeout: 0})
	if !s.Enabled() {
		t.Fatal("expected enabled with token+chatID")
	}
	first := s.Client()
	v1 := s.Version()

	// Identical config: no rebuild, no version bump.
	s.Set(telegram.Config{Token: "tok", ChatID: "1"})
	if s.Version() != v1 {
		t.Fatalf("version bumped on no-op Set: %d", s.Version())
	}
	if s.Client() != first {
		t.Fatal("client pointer changed on no-op Set")
	}

	// Token changes: rebuild + version bump.
	s.Set(telegram.Config{Token: "tok2", ChatID: "1"})
	if s.Version() != v1+1 {
		t.Fatalf("version = %d, want %d", s.Version(), v1+1)
	}
	if s.Client() == first {
		t.Fatal("client pointer unchanged after token swap")
	}
}

func TestStoreDisabledWhenTokenMissing(t *testing.T) {
	s := telegram.NewStore(telegram.Config{ChatID: "1"}, nil)
	if s.Enabled() {
		t.Fatal("expected disabled with empty token")
	}
	if c := s.Client(); c != nil {
		t.Fatalf("expected nil client, got %v", c)
	}

	// Configure later (hot reload).
	s.Set(telegram.Config{Token: "tok", ChatID: "1"})
	if !s.Enabled() {
		t.Fatal("expected enabled after Set")
	}
}
