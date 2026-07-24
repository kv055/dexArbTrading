package cextrading

import (
	"testing"

	"commons/exchangecredentials"
)

func TestFeeFetchersResolveDatabaseCredentials(t *testing.T) {
	provider := func(exchange string) (exchangecredentials.Credentials, error) {
		return exchangecredentials.Credentials{
			APIKey:    exchange + "-key",
			APISecret: exchange + "-secret",
		}, nil
	}

	taker := newTakerFeeFetcher("kraken", provider)
	if err := taker.refreshCredentials(); err != nil {
		t.Fatalf("refresh taker credentials: %v", err)
	}
	if taker.apiKey != "kraken-key" || taker.apiSecret != "kraken-secret" {
		t.Fatalf("taker credentials = %q/%q", taker.apiKey, taker.apiSecret)
	}

	withdrawal := newWithdrawalFeeFetcher("binance", provider)
	if err := withdrawal.refreshCredentials(); err != nil {
		t.Fatalf("refresh withdrawal credentials: %v", err)
	}
	if withdrawal.apiKey != "binance-key" || withdrawal.apiSecret != "binance-secret" {
		t.Fatalf("withdrawal credentials = %q/%q", withdrawal.apiKey, withdrawal.apiSecret)
	}
}
