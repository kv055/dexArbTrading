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
	BaseURL string
	Timeout time.Duration
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
	return futures.NewBinanceDerivativesTradingUsdsFuturesClient(futures.WithRestAPI(configuration(apiKey, apiSecret, opts)))
}
