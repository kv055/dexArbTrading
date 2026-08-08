package paymentmethods

// cryptoProviders lists cryptocurrency settlement methods used in tradeflow.
// Coin tickers and generic altcoin labels are not seeded; they may exist as
// plain DB rows or appear as Others via the snapshot writer.
var cryptoProviders = []Provider{
	{ID: "lightning", Canonical: "Lightning", Category: CategoryCrypto, Aliases: []string{
		"lightning", "lightning network", "ln", "lnurl",
	}},
}
