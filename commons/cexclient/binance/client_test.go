package binance

import "testing"

func TestConfigurationDisablesRetries(t *testing.T) {
	cfg := configuration("key", "secret", Options{BaseURL: "https://example.test/"})
	if cfg.Retries != 0 {
		t.Fatalf("Retries = %d, want 0", cfg.Retries)
	}
	if cfg.BasePath != "https://example.test" {
		t.Fatalf("BasePath = %q", cfg.BasePath)
	}
}

func TestFuturesCombinedStreamsUsesConfiguredBaseURL(t *testing.T) {
	streams, err := NewFuturesCombinedStreams(Options{WebsocketStreamsURL: "wss://testnet.example/"})
	if err != nil {
		t.Fatalf("NewFuturesCombinedStreams: %v", err)
	}
	if got, want := streams.Cfg.BasePath, "wss://testnet.example/stream"; got != want {
		t.Fatalf("BasePath = %q, want %q", got, want)
	}
}
