// Package binance owns construction of the official Binance SDK clients.
package binance

import (
	"strings"
	"time"

	futures "github.com/binance/binance-connector-go/clients/derivativestradingusdsfutures"
	spot "github.com/binance/binance-connector-go/clients/spot"
	wallet "github.com/binance/binance-connector-go/clients/wallet"
	"github.com/binance/binance-connector-go/common/v2/common"
)

const defaultTimeout = 20 * time.Second

// Options configures official SDK clients. Retries are always disabled because
// MM Controller owns reconciliation of potentially mutating requests.
type Options struct {
	BaseURL             string
	WebsocketAPIBaseURL string
	WebsocketStreamsURL string
	Timeout             time.Duration
}

func configuration(apiKey, apiSecret string, opts Options) *common.ConfigurationRestAPI {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	configOpts := []common.ConfigurationRestAPIOption{
		common.WithApiKey(apiKey),
		common.WithApiSecret(apiSecret),
		common.WithTimeout(timeout),
		common.WithRetries(0),
	}
	if baseURL := strings.TrimRight(opts.BaseURL, "/"); baseURL != "" {
		configOpts = append(configOpts, common.WithBasePath(baseURL))
	}
	return common.NewConfigurationRestAPI(configOpts...)
}

func NewSpot(apiKey, apiSecret string, opts Options) *spot.BinanceSpotClient {
	return spot.NewBinanceSpotClient(spot.WithRestAPI(configuration(apiKey, apiSecret, opts)))
}

func NewWallet(apiKey, apiSecret string, opts Options) *wallet.BinanceWalletClient {
	return wallet.NewBinanceWalletClient(wallet.WithRestAPI(configuration(apiKey, apiSecret, opts)))
}

func NewFutures(apiKey, apiSecret string, opts Options) *futures.BinanceDerivativesTradingUsdsFuturesClient {
	wsAPIBaseURL := strings.TrimRight(opts.WebsocketAPIBaseURL, "/")
	if wsAPIBaseURL == "" {
		wsAPIBaseURL = common.DerivativesTradingUsdsFuturesWebsocketApiProdUrl
	}
	wsAPIOptions := []common.ConfigurationWebsocketApiOption{
		common.WithWsApiKey(apiKey),
		common.WithWsApiSecret(apiSecret),
		common.WithWsTimeout(timeout(opts)),
	}
	wsAPIOptions = append(wsAPIOptions, common.WithWsAPIBasePath(wsAPIBaseURL))
	wsStreamBaseURL := strings.TrimRight(opts.WebsocketStreamsURL, "/")
	if wsStreamBaseURL == "" {
		wsStreamBaseURL = common.DerivativesTradingUsdsFuturesWebsocketStreamsProdUrl
	}
	wsStreamOptions := []common.ConfigurationWebsocketStreamsOption{
		common.WithWsStreamsBasePath(wsStreamBaseURL),
	}
	return futures.NewBinanceDerivativesTradingUsdsFuturesClient(
		futures.WithRestAPI(configuration(apiKey, apiSecret, opts)),
		futures.WithWebsocketAPI(common.NewConfigurationWebsocketApi(wsAPIOptions...)),
		futures.WithWebsocketStreams(common.NewConfigurationWebsocketStreams(wsStreamOptions...)),
	)
}

// NewFuturesCombinedStreams creates the official SDK transport for Binance's
// legacy combined-stream endpoint. The generated futures facade splits market
// and user-data streams across separate paths, while this connector needs both
// streams on one connection.
func NewFuturesCombinedStreams(opts Options) (*common.WebsocketStreams, error) {
	baseURL := strings.TrimRight(opts.WebsocketStreamsURL, "/")
	if baseURL == "" {
		baseURL = common.DerivativesTradingUsdsFuturesWebsocketStreamsProdUrl
	}
	return common.NewWebsocketStreams(common.NewConfigurationWebsocketStreams(
		common.WithWsStreamsBasePath(baseURL + "/stream"),
	))
}

func timeout(opts Options) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return defaultTimeout
}
