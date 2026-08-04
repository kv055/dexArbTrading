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
