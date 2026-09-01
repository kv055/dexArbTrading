package cextrading

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"commons/exchangecredentials"
)

type coinbaseRoundTripFunc func(*http.Request) (*http.Response, error)

func (f coinbaseRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFetchCoinbaseSendFeeObservationsFiltersRecentBTCSends(t *testing.T) {
	previous := httpClient
	httpClient = &http.Client{Transport: coinbaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") == "" {
			t.Fatal("missing Coinbase JWT")
		}
		var body any
		switch req.URL.Path {
		case "/v2/accounts/BTC":
			body = map[string]any{"data": map[string]any{"id": "btc-wallet", "currency": map[string]any{"code": "BTC"}}}
		case "/v2/accounts/btc-wallet/transactions":
			body = map[string]any{"data": []any{
				map[string]any{"id": "send-new", "type": "send", "created_at": time.Now().UTC().Format(time.RFC3339), "network": map[string]any{"hash": "hash-1", "transaction_fee": map[string]any{"amount": "-0.000123", "currency": "BTC"}}},
				map[string]any{"id": "buy", "type": "buy", "created_at": time.Now().UTC().Format(time.RFC3339), "network": map[string]any{"transaction_fee": map[string]any{"amount": "0.9", "currency": "BTC"}}},
				map[string]any{"id": "send-old", "type": "send", "created_at": time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339), "network": map[string]any{"transaction_fee": map[string]any{"amount": "0.5", "currency": "BTC"}}},
			}}
		default:
			t.Fatalf("unexpected path %s", req.URL.RequestURI())
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { httpClient = previous })

	apiKey := strings.Join([]string{"organizations", "org", "apiKeys", "key"}, "/")
	observations, err := FetchCoinbaseSendFeeObservations(exchangecredentials.Credentials{APIKey: apiKey, APISecret: coinbaseTestKey(t)}, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("FetchCoinbaseSendFeeObservations: %v", err)
	}
	if len(observations) != 1 || observations[0].TransactionID != "send-new" || observations[0].AmountBTC != 0.000123 {
		t.Fatalf("observations = %#v", observations)
	}
}

func coinbaseTestKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw}))
}
