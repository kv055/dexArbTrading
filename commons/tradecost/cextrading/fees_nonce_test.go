package cextrading

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestNextKrakenNonceIsStrictlyIncreasing(t *testing.T) {
	krakenNonce.Store(0)
	previous := int64(0)
	for range 100 {
		nonce, err := strconv.ParseInt(nextKrakenNonce(), 10, 64)
		if err != nil {
			t.Fatalf("parse nonce: %v", err)
		}
		if nonce <= previous {
			t.Fatalf("nonce %d is not greater than %d", nonce, previous)
		}
		previous = nonce
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

func TestBinanceSpotTakerFeeReportsAPIErrorObject(t *testing.T) {
	previousClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/sapi/v1/asset/tradeFee" {
			t.Fatalf("path = %s", req.URL.Path)
		}
		if req.Header.Get("X-MBX-APIKEY") != "api-key" || req.URL.Query().Get("signature") == "" {
			t.Fatal("expected signed Binance request")
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`)),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { httpClient = previousClient })

	_, err := (&takerFeeFetcher{apiKey: "api-key", apiSecret: "secret"}).fetchBinance()
	if err == nil || !strings.Contains(err.Error(), "binance API -2015: Invalid API-key, IP, or permissions for action.") {
		t.Fatalf("error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
