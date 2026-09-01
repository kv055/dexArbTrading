package cextrading

import (
	"fmt"
	"strings"
	"time"

	"commons/exchangecredentials"
	"commons/hedge"
)

// LiveFeeSnapshot is a complete, exchange-derived fee snapshot. Rates are
// decimal fractions; FundingRate applies once per FundingInterval.
type LiveFeeSnapshot struct {
	SpotTakerFeeRate       *float64
	BTCWithdrawalFeeBTC    *float64
	FuturesOpenTakerRate   *float64
	FuturesCloseTakerRate  *float64
	FuturesFundingRate     *float64
	FundingIntervalSeconds *int64
	FetchedAt              time.Time
}

// FetchLiveSpotTakerRate refreshes only the spot component, so a withdrawal or
// hedge outage cannot erase the component's own success timestamp.
func FetchLiveSpotTakerRate(exchange string, credentials exchangecredentials.Credentials) (float64, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if credentials.APIKey == "" || credentials.APISecret == "" {
		return 0, fmt.Errorf("%s API credentials are not configured", exchange)
	}
	if exchange == "coinbase" {
		return fetchCoinbaseAdvancedSpotTakerRate(credentials)
	}
	return (&takerFeeFetcher{exchange: exchange, apiKey: credentials.APIKey, apiSecret: credentials.APISecret}).fetchLiveWithCredentials()
}

// FetchLiveWithdrawalFee refreshes exchange-reported withdrawal fees. Coinbase
// intentionally uses observed sends plus a mempool reserve instead.
func FetchLiveWithdrawalFee(exchange string, credentials exchangecredentials.Credentials) (float64, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if credentials.APIKey == "" || credentials.APISecret == "" {
		return 0, fmt.Errorf("%s API credentials are not configured", exchange)
	}
	return (&withdrawalFeeFetcher{exchange: exchange, apiKey: credentials.APIKey, apiSecret: credentials.APISecret}).fetchLiveWithCredentials()
}

// FetchLiveFeeSnapshot obtains fees without applying any fallback. Bitstamp is
// spot-only, so its futures fields are nil.
func FetchLiveFeeSnapshot(exchange string, credentials exchangecredentials.Credentials) (LiveFeeSnapshot, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	if credentials.APIKey == "" || credentials.APISecret == "" {
		return LiveFeeSnapshot{}, fmt.Errorf("%s API credentials are not configured", exchange)
	}
	if exchange == "coinbase" {
		spotRate, err := fetchCoinbaseAdvancedSpotTakerRate(credentials)
		if err != nil {
			return LiveFeeSnapshot{}, err
		}
		return LiveFeeSnapshot{SpotTakerFeeRate: &spotRate, FetchedAt: time.Now().UTC()}, nil
	}
	taker := &takerFeeFetcher{exchange: exchange, apiKey: credentials.APIKey, apiSecret: credentials.APISecret}
	withdrawal := &withdrawalFeeFetcher{exchange: exchange, apiKey: credentials.APIKey, apiSecret: credentials.APISecret}
	spotRate, err := taker.fetchLiveWithCredentials()
	if err != nil {
		return LiveFeeSnapshot{}, err
	}
	withdrawRate, err := withdrawal.fetchLiveWithCredentials()
	if err != nil {
		return LiveFeeSnapshot{}, err
	}
	snapshot := LiveFeeSnapshot{SpotTakerFeeRate: &spotRate, BTCWithdrawalFeeBTC: &withdrawRate, FetchedAt: time.Now().UTC()}
	_, ok := hedgeVenueForExchange(exchange)
	if !ok {
		return snapshot, nil
	}
	hedgeSnapshot, err := FetchLiveHedgeFeeSnapshot(exchange, credentials)
	if err != nil {
		// Spot and withdrawal credentials are often distinct from futures
		// credentials (notably Kraken). Preserve the verified spot snapshot;
		// hedge pricing remains unavailable until its own data is refreshed.
		return snapshot, nil
	}
	snapshot.FuturesOpenTakerRate = hedgeSnapshot.FuturesOpenTakerRate
	snapshot.FuturesCloseTakerRate = hedgeSnapshot.FuturesCloseTakerRate
	snapshot.FuturesFundingRate = hedgeSnapshot.FuturesFundingRate
	snapshot.FundingIntervalSeconds = hedgeSnapshot.FundingIntervalSeconds
	return snapshot, nil
}

// FetchLiveHedgeFeeSnapshot obtains only the derivatives fields. It is useful
// when an exchange account has a futures-only credential separate from spot.
func FetchLiveHedgeFeeSnapshot(exchange string, credentials exchangecredentials.Credentials) (LiveFeeSnapshot, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	venue, ok := hedgeVenueForExchange(exchange)
	if !ok {
		return LiveFeeSnapshot{}, fmt.Errorf("%s does not have a configured hedge venue", exchange)
	}
	inputs, err := hedge.FetchDefaultVenueInputs(venue, credentials)
	if err != nil {
		return LiveFeeSnapshot{}, err
	}
	interval := int64(inputs.FundingInterval.Seconds())
	if interval <= 0 {
		return LiveFeeSnapshot{}, fmt.Errorf("%s returned an invalid funding interval", exchange)
	}
	open, close, funding := inputs.OpenFeeRate, inputs.CloseFeeRate, inputs.FundingRate
	return LiveFeeSnapshot{
		FuturesOpenTakerRate:   &open,
		FuturesCloseTakerRate:  &close,
		FuturesFundingRate:     &funding,
		FundingIntervalSeconds: &interval,
		FetchedAt:              time.Now().UTC(),
	}, nil
}

func (f *takerFeeFetcher) fetchLiveWithCredentials() (float64, error) {
	switch f.exchange {
	case "kraken":
		return f.fetchKraken()
	case "binance":
		return f.fetchBinance()
	case "bitstamp":
		return f.fetchBitstamp()
	case "coinbase":
		return 0, fmt.Errorf("coinbase spot taker fee requires Advanced Trade JWT credentials")
	default:
		return 0, fmt.Errorf("unsupported exchange: %s", f.exchange)
	}
}

func (f *withdrawalFeeFetcher) fetchLiveWithCredentials() (float64, error) {
	switch f.exchange {
	case "kraken":
		return f.fetchKraken()
	case "binance":
		return f.fetchBinance()
	case "bitstamp":
		return f.fetchBitstamp()
	case "coinbase":
		return 0, fmt.Errorf("coinbase withdrawal fee requires an address-specific Exchange estimate")
	default:
		return 0, fmt.Errorf("unsupported exchange: %s", f.exchange)
	}
}

func hedgeVenueForExchange(exchange string) (hedge.Venue, bool) {
	switch exchange {
	case "kraken":
		return hedge.VenueKrakenFutures, true
	case "binance":
		return hedge.VenueBinanceFutures, true
	case "coinbase":
		return hedge.VenueCoinbaseFutures, true
	default:
		return "", false
	}
}
