package paymentmethods

// Provider describes one canonical payment method and how raw labels map to it.
type Provider struct {
	ID        string
	Canonical string
	Category  string
	Aliases   []string
	Patterns  []string
}

// providers is the tradeflow seed catalog: methods we actually pay/trade with.
// Ordered for matching priority — longer / more specific entries first.
// Long-tail BI labels are not seeded here; they may exist as plain DB rows
// (exact-name match only) or appear as Others via the snapshot writer.
var providers = []Provider{
	// Bank rails — specific before generic
	{ID: "sepa_instant", Canonical: "SEPA Instant", Category: CategoryBankTransfer, Aliases: []string{
		"sepa instant", "instant sepa", "sepainstant",
	}, Patterns: []string{`(?i)\bsepa[\s\-/]*instant\b`}},
	{ID: "sepa", Canonical: "SEPA", Category: CategoryBankTransfer, Aliases: []string{"sepa"}},
	{ID: "national_bank", Canonical: CanonicalNationalBank, Category: CategoryBankTransfer, Aliases: []string{
		"national bank", "national banks", "national_bank", "national transfer",
		"any national bank",
	}},

	// Fiat transmitters used in account setup / tradeflow.
	// PayPal F&F labels fold into PayPal (operator treats F&F as a PayPal alias).
	{ID: "paypal", Canonical: "PayPal", Category: CategoryFiatTransmitter, Aliases: []string{
		"paypal", "pay pal", "paypall",
		"paypal friends & family", "paypal friends and family", "paypal f&f",
		"f&f", "frnds&fam", "frnd&fam", "(frnds&fam)", "(frnd&fam)",
		"paypal(frnd&fam)", "paypal(frnds&fam)",
	}, Patterns: []string{
		`(?i)\bpay\s*pal+l?\b`,
		`(?i)paypal\s*(f&?f|friends)`,
	}},
	{ID: "revolut", Canonical: "Revolut", Category: CategoryFiatTransmitter, Aliases: []string{
		"revolut",
	}, Patterns: []string{`(?i)\brevou?l?t\b`}},
	{ID: "wise", Canonical: "Wise", Category: CategoryFiatTransmitter, Aliases: []string{
		"wise", "transferwise", "transfer wise",
	}},
	{ID: "n26", Canonical: "N26", Category: CategoryFiatTransmitter, Aliases: []string{"n26"}},
}

// allProviders returns bank/fiat providers followed by crypto providers.
func allProviders() []Provider {
	out := make([]Provider, 0, len(providers)+len(cryptoProviders))
	out = append(out, providers...)
	out = append(out, cryptoProviders...)
	return out
}
