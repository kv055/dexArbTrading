package paymentmethods

import "strings"

// CryptoTicker is one entry in the hardcoded popular-crypto reference list.
// Ticker is the uppercase market symbol; Name is the canonical payment-method label.
type CryptoTicker struct {
	Ticker  string
	Name    string
	Aliases []string // optional extra lowercase aliases beyond ticker and name
}

// PopularCryptoTickers is the hardcoded reference of widely traded crypto tickers.
// Used to build crypto payment-method providers and to classify free-text labels
// in DEX offers. Extend this list when observability shows missing tickers in Other.
var PopularCryptoTickers = []CryptoTicker{
	// Top market-cap / most traded
	{Ticker: "BTC", Name: "Bitcoin", Aliases: []string{"on-chain", "onchain", "on chain"}},
	{Ticker: "ETH", Name: "Ethereum"},
	{Ticker: "USDT", Name: "USDT", Aliases: []string{"tether", "tether usdt"}},
	{Ticker: "USDC", Name: "USDC", Aliases: []string{"usd coin"}},
	{Ticker: "BNB", Name: "BNB", Aliases: []string{"binance coin"}},
	{Ticker: "SOL", Name: "Solana"},
	{Ticker: "XRP", Name: "XRP", Aliases: []string{"ripple"}},
	{Ticker: "ADA", Name: "Cardano"},
	{Ticker: "DOGE", Name: "Dogecoin", Aliases: []string{"doge"}},
	{Ticker: "TRX", Name: "Tron"},
	{Ticker: "AVAX", Name: "Avalanche"},
	{Ticker: "LINK", Name: "Chainlink"},
	{Ticker: "DOT", Name: "Polkadot"},
	{Ticker: "MATIC", Name: "Polygon", Aliases: []string{"pol"}},
	{Ticker: "LTC", Name: "Litecoin"},
	{Ticker: "BCH", Name: "Bitcoin Cash"},
	{Ticker: "XMR", Name: "Monero", Aliases: []string{"monero xmr"}},
	{Ticker: "SHIB", Name: "Shiba Inu", Aliases: []string{"shiba"}},
	{Ticker: "UNI", Name: "Uniswap"},
	{Ticker: "ATOM", Name: "Cosmos"},
	{Ticker: "XLM", Name: "Stellar"},
	{Ticker: "ETC", Name: "Ethereum Classic"},
	{Ticker: "ZEC", Name: "Zcash", Aliases: []string{"zcash"}},
	{Ticker: "DASH", Name: "Dash"},
	{Ticker: "DAI", Name: "DAI"},
	{Ticker: "TUSD", Name: "True USD", Aliases: []string{"trueusd"}},
	{Ticker: "USDD", Name: "USDD"},
	{Ticker: "BUSD", Name: "BUSD", Aliases: []string{"binance usd"}},
	{Ticker: "FDUSD", Name: "FDUSD", Aliases: []string{"first digital usd"}},
	{Ticker: "PYUSD", Name: "PYUSD", Aliases: []string{"paypal usd"}},
	{Ticker: "USDP", Name: "Pax Dollar", Aliases: []string{"pax dollar", "paxos usd"}},
	{Ticker: "GUSD", Name: "Gemini Dollar", Aliases: []string{"gemini usd"}},
	{Ticker: "FRAX", Name: "Frax"},
	{Ticker: "LUSD", Name: "Liquity USD", Aliases: []string{"liquity usd"}},
	{Ticker: "USDE", Name: "Ethena USDe", Aliases: []string{"usde", "ethena usde"}},
	{Ticker: "EURC", Name: "Euro Coin", Aliases: []string{"euro coin", "circle euro"}},
	{Ticker: "EURT", Name: "Tether EUR", Aliases: []string{"tether eur", "euro tether"}},
	{Ticker: "RLUSD", Name: "RLUSD", Aliases: []string{"ripple usd"}},
	{Ticker: "USDS", Name: "USDS", Aliases: []string{"sky dollar", "maker usds"}},
	{Ticker: "UST", Name: "TerraUSD", Aliases: []string{"terra usd", "terrausd"}},
	{Ticker: "NEAR", Name: "NEAR Protocol", Aliases: []string{"near"}},
	{Ticker: "APT", Name: "Aptos"},
	{Ticker: "ARB", Name: "Arbitrum"},
	{Ticker: "OP", Name: "Optimism"},
	{Ticker: "FIL", Name: "Filecoin"},
	{Ticker: "HBAR", Name: "Hedera"},
	{Ticker: "VET", Name: "VeChain"},
	{Ticker: "ALGO", Name: "Algorand"},
	{Ticker: "EOS", Name: "EOS"},
	{Ticker: "AAVE", Name: "Aave"},
	{Ticker: "MKR", Name: "Maker", Aliases: []string{"makerdao"}},
	{Ticker: "BSV", Name: "Bitcoin SV"},

	// Bisq / privacy / L2-specific
	{Ticker: "BSQ", Name: "BSQ", Aliases: []string{"bisq sq"}},
	{Ticker: "LBTC", Name: "Liquid Bitcoin", Aliases: []string{"l-btc", "lbtc", "liquid", "liquid btc", "liquid network", "blockstream liquid"}},
}

// PopularCryptoTickerSet is a fast lookup of uppercase tickers from PopularCryptoTickers.
var PopularCryptoTickerSet map[string]bool

func init() {
	PopularCryptoTickerSet = make(map[string]bool, len(PopularCryptoTickers))
	for _, t := range PopularCryptoTickers {
		PopularCryptoTickerSet[strings.ToUpper(t.Ticker)] = true
	}
}

// IsPopularCryptoTicker reports whether symbol is in PopularCryptoTickers (case-insensitive).
func IsPopularCryptoTicker(symbol string) bool {
	return PopularCryptoTickerSet[strings.ToUpper(strings.TrimSpace(symbol))]
}
