// Package cextrading owns the CEX side of the arbitrage cost model: per-exchange
// spot taker fees, on-chain BTC withdrawal fees, and the perp hedge. Its Engine
// picks the cheapest CEX fee combo (taker + withdrawal + hedge) across the active
// exchanges, honoring the user's CEX activation/deactivation (ExcludedCEXes) and
// hedge coupling (CoupledHedge) choices. It is the CEX-side counterpart to the
// txrouting package and must NOT import tradecost (that would cycle).
//
// Transport policy: Kraken and Binance requests go through the shared official
// SDK connectors in commons/cexclient (nonce handling, signing, error mapping).
// Bitstamp and Coinbase remain hand-signed here because no official Go SDK
// exists for those APIs.
package cextrading

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	binanceclient "commons/cexclient/binance"
	krakenclient "commons/cexclient/kraken"
	"commons/exchangecredentials"
	"commons/mmcapabilities"

	"github.com/google/uuid"
)

// feeTTL is the single refresh interval for all CEX fee components (taker + withdrawal).
const feeTTL = 30 * time.Minute

// SupportedCEXes returns the canonical list of CEXes the cost model knows about.
func SupportedCEXes() []string {
	return mmcapabilities.CostSupportedProviderIDs()
}

// httpClient is shared across all clearnet CEX fetchers.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// krakenClientOptions and binanceClientOptions build the official-SDK connector
// options used by the fee fetchers below. They are variables so tests can route
// the connectors at a deterministic local backend without touching call sites.
var (
	krakenClientOptions  = func() krakenclient.Options { return krakenclient.Options{Timeout: httpClient.Timeout} }
	binanceClientOptions = func() binanceclient.Options { return binanceclient.Options{Timeout: httpClient.Timeout} }
)

// krakenSpotPOST executes an authenticated Kraken Spot request through the
// shared official SDK connector. Nonce generation (microsecond monotonic stream)
// and invalid-nonce recovery live in commons/cexclient/kraken; both fee
// endpoints below are read-only, so a rejected nonce is retried there.
func krakenSpotPOST(apiKey, apiSecret, operation, path string, body map[string]any) ([]byte, error) {
	client := krakenclient.New(apiKey, apiSecret, krakenClientOptions())
	return client.PrivatePOST(context.Background(), operation, path, body)
}

// ---------------------------------------------------------------------------
// cachedFee — generic lazy-refresh cache for a single float64 fee value
// (self-contained, mirroring txrouting's per-package cache pattern)
// ---------------------------------------------------------------------------

type cachedFee struct {
	mu        sync.RWMutex
	value     float64
	fetchedAt time.Time
	ttl       time.Duration
	inflight  chan struct{}
}

func newCachedFee(ttl time.Duration) *cachedFee { return &cachedFee{ttl: ttl} }

// get returns the cached value if fresh, else calls refresh and caches the result.
// On refresh failure it keeps the last known value (or fallback if never fetched).
func (c *cachedFee) get(fallback float64, refresh func() (float64, error), label string) float64 {
	c.mu.RLock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		v := c.value
		c.mu.RUnlock()
		return v
	}
	if c.inflight != nil {
		ch := c.inflight
		c.mu.RUnlock()
		<-ch
		c.mu.RLock()
		v := c.value
		c.mu.RUnlock()
		return v
	}
	c.mu.RUnlock()

	c.mu.Lock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		v := c.value
		c.mu.Unlock()
		return v
	}
	if c.inflight != nil {
		ch := c.inflight
		c.mu.Unlock()
		<-ch
		c.mu.RLock()
		v := c.value
		c.mu.RUnlock()
		return v
	}
	ch := make(chan struct{})
	c.inflight = ch
	c.mu.Unlock()

	v, err := refresh()
	c.mu.Lock()
	if err == nil {
		c.value = v
		c.fetchedAt = time.Now()
		fmt.Printf("[TradeCost] %s updated: %.8f (live)\n", label, v)
	} else {
		if c.fetchedAt.IsZero() {
			c.value = fallback
			fmt.Printf("[TradeCost] %s fetch failed (%v), using fallback %.8f\n", label, err, fallback)
		} else {
			fmt.Printf("[TradeCost] %s refresh failed (%v), keeping cached %.8f\n", label, err, c.value)
		}
		// Stamp fetchedAt even on failure so we don't retry every call for the TTL duration.
		c.fetchedAt = time.Now()
	}
	result := c.value
	close(ch)
	c.inflight = nil
	c.mu.Unlock()
	return result
}

// ---------------------------------------------------------------------------
// Withdrawal fee fetcher
// ---------------------------------------------------------------------------

type withdrawalFeeFetcher struct {
	exchange  string
	apiKey    string
	apiSecret string
	provider  exchangecredentials.Provider
	cache     *cachedFee
}

func newWithdrawalFeeFetcher(exchange string, provider exchangecredentials.Provider) *withdrawalFeeFetcher {
	return &withdrawalFeeFetcher{
		exchange: exchange,
		provider: provider,
		cache:    newCachedFee(feeTTL),
	}
}

// get returns the CEX's on-chain BTC withdrawal fee. Every routing method withdraws
// on-chain, so there is a single fee per exchange.
func (f *withdrawalFeeFetcher) get() float64 {
	if f.cache == nil {
		return 0
	}
	label := fmt.Sprintf("%s onchain withdrawal fee BTC", f.exchange)
	return f.cache.get(0, f.fetchLive, label)
}

func (f *withdrawalFeeFetcher) fetchLive() (float64, error) {
	if err := f.refreshCredentials(); err != nil {
		return 0, err
	}
	switch f.exchange {
	case "kraken":
		return f.fetchKraken()
	case "binance":
		return f.fetchBinance()
	case "coinbase":
		return 0, fmt.Errorf("coinbase withdrawal fee requires an address-specific estimate and is not available from these credentials")
	case "bitstamp":
		return f.fetchBitstamp()
	default:
		return 0, fmt.Errorf("unsupported exchange: %s", f.exchange)
	}
}

func (f *withdrawalFeeFetcher) refreshCredentials() error {
	if f.provider == nil {
		return fmt.Errorf("no database credential provider for %s", f.exchange)
	}
	credentials, err := f.provider(f.exchange)
	if err != nil {
		return err
	}
	f.apiKey = credentials.APIKey
	f.apiSecret = credentials.APISecret
	if f.apiKey == "" || f.apiSecret == "" {
		return fmt.Errorf("no active API credentials for %s", f.exchange)
	}
	return nil
}

func (f *withdrawalFeeFetcher) fetchKraken() (float64, error) {
	raw, err := krakenSpotPOST(f.apiKey, f.apiSecret, "WithdrawMethods", "/0/private/WithdrawMethods",
		map[string]any{"asset": "XBT"})
	if err != nil {
		return 0, err
	}

	var result struct {
		Error  []string `json:"error"`
		Result []struct {
			Asset   string          `json:"asset"`
			Network string          `json:"network"`
			Fee     json.RawMessage `json:"fee"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, fmt.Errorf("kraken: decode: %w", err)
	}
	if len(result.Error) > 0 {
		return 0, fmt.Errorf("kraken API: %v", result.Error)
	}
	for _, m := range result.Result {
		if m.Asset != "XXBT" {
			continue
		}
		network := strings.ToLower(m.Network)
		if network == "bitcoin" || network == "btc" || strings.Contains(network, "segwit") {
			return parseKrakenWithdrawalFee(m.Fee)
		}
	}
	return 0, fmt.Errorf("kraken: onchain withdrawal method not found")
}

func parseKrakenWithdrawalFee(raw json.RawMessage) (float64, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strconv.ParseFloat(text, 64)
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return 0, fmt.Errorf("kraken withdrawal fee: decode %s: %w", string(raw), err)
	}
	for _, key := range []string{"fee", "amount", "withdrawal_fee"} {
		if value, ok := object[key]; ok {
			return parseKrakenWithdrawalFee(value)
		}
	}
	return 0, fmt.Errorf("kraken withdrawal fee: unsupported response %s", string(raw))
}

// fetchBinance fetches the on-chain BTC withdrawal fee via the official Wallet
// SDK (GET /sapi/v1/capital/config/getall) and picks the BTC network
// (btc/bitcoin/isDefault).
func (f *withdrawalFeeFetcher) fetchBinance() (float64, error) {
	client := binanceclient.NewWallet(f.apiKey, f.apiSecret, binanceClientOptions())
	resp, err := client.RestApi.CapitalAPI.AllCoinsInformation(context.Background()).Execute()
	if err != nil {
		return 0, binanceclient.WrapError("CapitalConfigGetAll", false, err)
	}
	for _, coin := range resp.Data.Items {
		if coin.GetCoin() != "BTC" {
			continue
		}
		for _, net := range coin.GetNetworkList() {
			network := strings.ToLower(net.GetNetwork())
			if network == "btc" || network == "bitcoin" || net.GetIsDefault() {
				fee := net.GetWithdrawFee()
				if fee == "" {
					return 0, fmt.Errorf("binance: BTC %s network reports no withdrawal fee", net.GetNetwork())
				}
				return strconv.ParseFloat(fee, 64)
			}
		}
	}
	return 0, fmt.Errorf("binance: onchain withdrawal fee not found")
}

func (f *withdrawalFeeFetcher) fetchBitstamp() (float64, error) {
	const (
		urlHost = "www.bitstamp.net"
		urlPath = "/api/v2/fees/withdrawal/"
	)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.New().String()
	xAuth := "BITSTAMP " + f.apiKey
	message := xAuth + "POST" + urlHost + urlPath + "" + nonce + timestamp + "v2" + ""
	mac := hmac.New(sha256.New, []byte(f.apiSecret))
	mac.Write([]byte(message))

	req, _ := http.NewRequest(http.MethodPost, "https://"+urlHost+urlPath, nil)
	req.Header.Set("X-Auth", xAuth)
	req.Header.Set("X-Auth-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Auth-Nonce", nonce)
	req.Header.Set("X-Auth-Timestamp", timestamp)
	req.Header.Set("X-Auth-Version", "v2")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitstamp: %w", err)
	}
	defer resp.Body.Close()

	var entries []struct {
		Currency string `json:"currency"`
		Fee      string `json:"fee"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return 0, fmt.Errorf("bitstamp: decode: %w", err)
	}
	for _, e := range entries {
		if strings.EqualFold(e.Currency, "BTC") {
			return strconv.ParseFloat(e.Fee, 64)
		}
	}
	return 0, fmt.Errorf("bitstamp: BTC withdrawal fee not found")
}

// ---------------------------------------------------------------------------
// Taker fee fetcher
// ---------------------------------------------------------------------------
//
// Supported: Kraken (TradeVolume), Binance (tradeFee), Bitstamp (fees/trading).
// Coinbase requires Advanced Trade JWT credentials and is handled by the
// persisted-snapshot refresher rather than this legacy key format.

type takerFeeFetcher struct {
	exchange  string
	apiKey    string
	apiSecret string
	provider  exchangecredentials.Provider
	cache     *cachedFee
}

func newTakerFeeFetcher(exchange string, provider exchangecredentials.Provider) *takerFeeFetcher {
	return &takerFeeFetcher{
		exchange: exchange,
		provider: provider,
		cache:    newCachedFee(feeTTL),
	}
}

func (f *takerFeeFetcher) get() float64 {
	label := f.exchange + " taker fee fraction"
	return f.cache.get(0, f.fetchLive, label)
}

func (f *takerFeeFetcher) fetchLive() (float64, error) {
	if err := f.refreshCredentials(); err != nil {
		return 0, err
	}
	switch f.exchange {
	case "kraken":
		return f.fetchKraken()
	case "binance":
		return f.fetchBinance()
	case "coinbase":
		return 0, fmt.Errorf("coinbase taker fee requires Advanced Trade JWT credentials")
	case "bitstamp":
		return f.fetchBitstamp()
	default:
		return 0, fmt.Errorf("unsupported exchange: %s", f.exchange)
	}
}

func (f *takerFeeFetcher) refreshCredentials() error {
	if f.provider == nil {
		return fmt.Errorf("no database credential provider for %s", f.exchange)
	}
	credentials, err := f.provider(f.exchange)
	if err != nil {
		return err
	}
	f.apiKey = credentials.APIKey
	f.apiSecret = credentials.APISecret
	if f.apiKey == "" || f.apiSecret == "" {
		return fmt.Errorf("no active API credentials for %s", f.exchange)
	}
	return nil
}

// fetchKraken fetches the user's current taker fee via POST /0/private/TradeVolume.
// The API returns the fee as a percent string (e.g. "0.26" = 0.26%), which is
// divided by 100 to produce a fraction.
func (f *takerFeeFetcher) fetchKraken() (float64, error) {
	raw, err := krakenSpotPOST(f.apiKey, f.apiSecret, "TradeVolume", "/0/private/TradeVolume",
		map[string]any{"pair": "XBTEUR"})
	if err != nil {
		return 0, err
	}

	var result struct {
		Error  []string `json:"error"`
		Result struct {
			Fees map[string]struct {
				Fee json.RawMessage `json:"fee"` // percent string, e.g. "0.26"
			} `json:"fees"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, fmt.Errorf("kraken: decode: %w", err)
	}
	if len(result.Error) > 0 {
		return 0, fmt.Errorf("kraken API: %v", result.Error)
	}
	// Kraken uses its internal pair name "XXBTZEUR" in the response.
	for _, key := range []string{"XXBTZEUR", "XBTEUR"} {
		if entry, ok := result.Result.Fees[key]; ok {
			pct, err := parseKrakenTakerFee(entry.Fee)
			if err != nil {
				return 0, err
			}
			return pct / 100, nil // convert percent to fraction
		}
	}
	return 0, fmt.Errorf("kraken: XBTEUR taker fee not found in TradeVolume response")
}

func parseKrakenTakerFee(raw json.RawMessage) (float64, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		pct, parseErr := strconv.ParseFloat(text, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("kraken: parse taker fee %q: %w", text, parseErr)
		}
		return pct, nil
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return 0, fmt.Errorf("kraken: decode taker fee %s: %w", string(raw), err)
	}
	for _, key := range []string{"fee", "taker", "rate"} {
		if value, ok := object[key]; ok {
			return parseKrakenTakerFee(value)
		}
	}
	return 0, fmt.Errorf("kraken: unsupported taker fee response %s", string(raw))
}

// fetchBinance fetches the user's taker fee via the official Spot SDK
// (GET /api/v3/account/commission) for BTCUSDT. The API returns the fee already
// as a fraction (e.g. "0.001" = 0.1%); the effective per-symbol rate includes
// BNB discounts and matches what the account actually pays.
func (f *takerFeeFetcher) fetchBinance() (float64, error) {
	client := binanceclient.NewSpot(f.apiKey, f.apiSecret, binanceClientOptions())
	resp, err := client.RestApi.AccountAPI.AccountCommission(context.Background()).Symbol("BTCUSDT").Execute()
	if err != nil {
		return 0, binanceclient.WrapError("AccountCommission", false, err)
	}
	standard := resp.Data.GetStandardCommission()
	taker := standard.GetTaker()
	rate, err := strconv.ParseFloat(taker, 64)
	if err != nil {
		return 0, fmt.Errorf("binance: parse taker commission %q: %w", taker, err)
	}
	return rate, nil
}

// fetchBitstamp fetches the user's taker fee via POST /api/v2/fees/trading/.
// The API returns the fee as a percent string (e.g. "0.50" = 0.50%), which is
// divided by 100 to produce a fraction.
func (f *takerFeeFetcher) fetchBitstamp() (float64, error) {
	const (
		urlHost = "www.bitstamp.net"
		urlPath = "/api/v2/fees/trading/"
	)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.New().String()
	xAuth := "BITSTAMP " + f.apiKey
	message := xAuth + "POST" + urlHost + urlPath + "" + nonce + timestamp + "v2" + ""
	mac := hmac.New(sha256.New, []byte(f.apiSecret))
	mac.Write([]byte(message))

	req, _ := http.NewRequest(http.MethodPost, "https://"+urlHost+urlPath, nil)
	req.Header.Set("X-Auth", xAuth)
	req.Header.Set("X-Auth-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Auth-Nonce", nonce)
	req.Header.Set("X-Auth-Timestamp", timestamp)
	req.Header.Set("X-Auth-Version", "v2")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("bitstamp: %w", err)
	}
	defer resp.Body.Close()

	var entries []struct {
		Market string `json:"market"`
		Fees   struct {
			Taker string `json:"taker"` // percent string, e.g. "0.50"
		} `json:"fees"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return 0, fmt.Errorf("bitstamp: decode: %w", err)
	}
	for _, e := range entries {
		if e.Market == "BTC/EUR" {
			pct, err := strconv.ParseFloat(e.Fees.Taker, 64)
			if err != nil {
				return 0, fmt.Errorf("bitstamp: parse taker fee %q: %w", e.Fees.Taker, err)
			}
			return pct / 100, nil // convert percent to fraction
		}
	}
	return 0, fmt.Errorf("bitstamp: BTC/EUR taker fee not found")
}
