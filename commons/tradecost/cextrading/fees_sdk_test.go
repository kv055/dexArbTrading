package cextrading

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	binanceclient "commons/cexclient/binance"
	krakenclient "commons/cexclient/kraken"
)

// krakenFeeServer spins up a fake Kraken Spot API answering the given body for
// the expected private path, so the fetchers exercise the real SDK connector
// (signing, nonce handling) against a deterministic backend.
func krakenFeeServer(t *testing.T, path string, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("path = %s, want %s", r.URL.Path, path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("API-Key") == "" || r.Header.Get("API-Sign") == "" {
			t.Error("expected authenticated Kraken request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func overrideKrakenBaseURL(t *testing.T, url string) func() {
	t.Helper()
	previous := krakenClientOptions
	krakenClientOptions = func() krakenclient.Options {
		opts := previous()
		opts.BaseURL = url
		return opts
	}
	return func() { krakenClientOptions = previous }
}

func overrideBinanceBaseURL(t *testing.T, url string) func() {
	t.Helper()
	previous := binanceClientOptions
	binanceClientOptions = func() binanceclient.Options {
		opts := previous()
		opts.BaseURL = url
		return opts
	}
	return func() { binanceClientOptions = previous }
}

func testKrakenSecret(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
}

func TestKrakenTakerFeeViaSDKConnector(t *testing.T) {
	server := krakenFeeServer(t, "/0/private/TradeVolume", `{"error":[],"result":{"fees":{"XXBTZEUR":{"fee":"0.26"}}}}`)
	defer server.Close()

	restore := overrideKrakenBaseURL(t, server.URL)
	defer restore()

	got, err := (&takerFeeFetcher{apiKey: "api-key", apiSecret: testKrakenSecret(t)}).fetchKraken()
	if err != nil {
		t.Fatalf("fetchKraken: %v", err)
	}
	const want = 0.0026 // 0.26% as fraction
	if got != want {
		t.Fatalf("taker fee = %.6f, want %.6f", got, want)
	}
}

func TestKrakenWithdrawalFeeViaSDKConnector(t *testing.T) {
	server := krakenFeeServer(t, "/0/private/WithdrawMethods",
		`{"error":[],"result":[{"asset":"XXBT","network":"Bitcoin","fee":"0.00015"}]}`)
	defer server.Close()

	restore := overrideKrakenBaseURL(t, server.URL)
	defer restore()

	got, err := (&withdrawalFeeFetcher{apiKey: "api-key", apiSecret: testKrakenSecret(t)}).fetchKraken()
	if err != nil {
		t.Fatalf("fetchKraken: %v", err)
	}
	if got != 0.00015 {
		t.Fatalf("withdrawal fee = %.8f, want 0.00015", got)
	}
}

func TestBinanceTakerFeeViaSpotSDK(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("X-MBX-APIKEY") == "" {
			t.Error("expected X-MBX-APIKEY header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","standardCommission":{"maker":"0.0010","taker":"0.0010"}}`))
	}))
	defer server.Close()

	restore := overrideBinanceBaseURL(t, server.URL)
	defer restore()

	rate, err := (&takerFeeFetcher{apiKey: "api-key", apiSecret: "secret"}).fetchBinance()
	if err != nil {
		t.Fatalf("fetchBinance: %v", err)
	}
	if rate != 0.001 {
		t.Fatalf("taker fee = %.5f, want 0.00100", rate)
	}
	if !strings.HasSuffix(gotPath, "/api/v3/account/commission") {
		t.Fatalf("endpoint = %s, want /api/v3/account/commission", gotPath)
	}
}

func TestBinanceTakerFeeSurfacesAPIErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`))
	}))
	defer server.Close()

	restore := overrideBinanceBaseURL(t, server.URL)
	defer restore()

	_, err := (&takerFeeFetcher{apiKey: "bad-key", apiSecret: "secret"}).fetchBinance()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "-2015") || !strings.Contains(err.Error(), "Invalid API-key") {
		t.Fatalf("error = %v, want -2015 with exchange msg", err)
	}
}

func TestBinanceWithdrawalFeeViaWalletSDK(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"coin":"BTC","networkList":[
			{"network":"BNB Smart Chain (BEP20)","isDefault":false,"withdrawFee":"0.0000031"},
			{"network":"BTC","isDefault":true,"withdrawFee":"0.0000046"}]}]`))
	}))
	defer server.Close()

	restore := overrideBinanceBaseURL(t, server.URL)
	defer restore()

	fee, err := (&withdrawalFeeFetcher{apiKey: "api-key", apiSecret: "secret"}).fetchBinance()
	if err != nil {
		t.Fatalf("fetchBinance: %v", err)
	}
	if fee != 0.0000046 {
		t.Fatalf("withdrawal fee = %.10f, want 0.00000460", fee)
	}
	if !strings.HasSuffix(gotPath, "/sapi/v1/capital/config/getall") {
		t.Fatalf("endpoint = %s, want /sapi/v1/capital/config/getall", gotPath)
	}
}

func TestParseKrakenWithdrawalFeeAcceptsCurrentResponseShapes(t *testing.T) {
	for _, raw := range []string{`"0.00015"`, `0.00015`, `{"fee":"0.00015"}`, `{"withdrawal_fee":0.00015}`} {
		got, err := parseKrakenWithdrawalFee(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if got != 0.00015 {
			t.Fatalf("parse %s = %.8f, want 0.00015", raw, got)
		}
	}
}

func TestParseKrakenTakerFeeAcceptsCurrentResponseShapes(t *testing.T) {
	for _, raw := range []string{`"0.26"`, `0.26`, `{"fee":"0.26"}`, `{"taker":0.26}`} {
		got, err := parseKrakenTakerFee(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if got != 0.26 {
			t.Fatalf("parse %s = %.8f, want 0.26", raw, got)
		}
	}
}
