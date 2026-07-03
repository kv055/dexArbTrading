package paymentmethods

import (
	"testing"
)

func TestPopularCryptoTickersReference(t *testing.T) {
	if len(PopularCryptoTickers) < 30 {
		t.Fatalf("expected at least 30 popular crypto tickers, got %d", len(PopularCryptoTickers))
	}
	seen := make(map[string]bool)
	for _, tick := range PopularCryptoTickers {
		if tick.Ticker == "" || tick.Name == "" {
			t.Fatalf("empty ticker or name: %#v", tick)
		}
		if seen[tick.Ticker] {
			t.Fatalf("duplicate ticker %q", tick.Ticker)
		}
		seen[tick.Ticker] = true
		if !IsPopularCryptoTicker(tick.Ticker) {
			t.Fatalf("IsPopularCryptoTicker(%q) = false", tick.Ticker)
		}
	}
	if !IsPopularCryptoTicker("btc") {
		t.Fatal("IsPopularCryptoTicker should be case-insensitive")
	}
	if IsPopularCryptoTicker("SEPA") {
		t.Fatal("fiat rail should not be a popular crypto ticker")
	}
}

func TestCryptoProvidersBuiltFromTickers(t *testing.T) {
	for _, tick := range PopularCryptoTickers {
		got := CanonicalizeAll([]string{tick.Ticker})
		if len(got) != 1 || got[0] != tick.Name {
			t.Errorf("CanonicalizeAll(%q) = %#v, want [%q]", tick.Ticker, got, tick.Name)
		}
	}
}
