package paymentmethods

import "strings"

// cryptoProviders lists cryptocurrency settlement methods. Coin entries are
// generated from PopularCryptoTickers; non-ticker labels are defined below.
var cryptoProviders = buildCryptoProviders()

func buildCryptoProviders() []Provider {
	out := []Provider{
		// Bisq v1 altcoin payment method names
		{ID: "altcoins_instant", Canonical: "Altcoins Instant", Category: CategoryCrypto, Aliases: []string{
			"altcoins instant", "altcoin instant", "altcoins_instant",
		}},
		{ID: "altcoins", Canonical: "Altcoins", Category: CategoryCrypto, Aliases: []string{
			"altcoins", "altcoin",
		}},
		// Bitcoin L2 / payment rails (not single-asset tickers)
		{ID: "lightning", Canonical: "Lightning", Category: CategoryCrypto, Aliases: []string{
			"lightning", "lightning network", "ln", "lnurl",
		}},
	}

	for _, t := range PopularCryptoTickers {
		out = append(out, tickerToProvider(t))
	}

	out = append(out,
		Provider{ID: "other_coins", Canonical: "Other Coins", Category: CategoryCrypto, Aliases: []string{
			"other coins", "other crypto", "other cryptos", "mixed coins", "various coins",
		}},
		Provider{ID: "crypto", Canonical: "Crypto", Category: CategoryCrypto, Aliases: []string{
			"crypto", "cryptocurrency", "cryptocurrencies",
		}},
	)
	return out
}

func tickerToProvider(t CryptoTicker) Provider {
	id := strings.ToLower(t.Ticker)
	aliases := []string{strings.ToLower(t.Ticker)}
	name := strings.ToLower(t.Name)
	if name != "" && name != aliases[0] {
		aliases = append(aliases, name)
	}
	aliases = append(aliases, t.Aliases...)
	return Provider{
		ID:        id,
		Canonical: t.Name,
		Category:  CategoryCrypto,
		Aliases:   aliases,
	}
}
