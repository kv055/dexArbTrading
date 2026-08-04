package kraken

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDepositAddressesUsesCorrectGenericPath(t *testing.T) {
	var gotPath string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		if r.Header.Get("API-Key") != "key" || r.Header.Get("API-Sign") == "" {
			t.Error("official SDK did not authenticate request")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":[],"result":[{"address":"bc1qtest"}]}`))}, nil
	})}

	secret := base64.StdEncoding.EncodeToString([]byte("secret"))
	client := New("key", secret, Options{BaseURL: "https://kraken.test", Client: httpClient})
	raw, err := client.PrivatePOST(context.Background(), "deposit addresses", "/0/private/DepositAddresses", map[string]any{"asset": "XBT", "method": "Bitcoin"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/0/private/DepositAddresses" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(string(raw), "bc1qtest") {
		t.Fatalf("response = %s", raw)
	}
}

func TestMutationIsNeverRetried(t *testing.T) {
	calls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":["EAPI:Invalid nonce"],"result":{}}`))}, nil
	})}
	secret := base64.StdEncoding.EncodeToString([]byte("secret"))
	client := New("key", secret, Options{BaseURL: "https://kraken.test", Client: httpClient})
	_, _ = client.PrivatePOST(context.Background(), "add order", "/0/private/AddOrder", map[string]any{"pair": "XBTEUR"})
	if calls != 1 {
		t.Fatalf("calls = %d, mutation must be attempted exactly once", calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
