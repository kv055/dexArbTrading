package paymentmethods

import "testing"

// TestRequiredAccountFieldsProviders pins the provider keys to values allowed
// by the accounts table's CHECK constraint (01_accounts.sql) — a typo here
// would make the pre-trade field check silently pass for that provider.
func TestRequiredAccountFieldsProviders(t *testing.T) {
	allowed := map[string]bool{
		"WISE": true, "PayPal": true, "Revolut": true, "N26": true,
		"Robosats": true, "Bisq": true, "Bisq2": true, "HodlHodl": true,
		"Kraken": true, "Coinbase": true, "Bitstamp": true, "Binance": true,
		"ByBit": true, "KuCoin": true, "Huobi": true, "LND wallet": true,
	}
	for provider := range RequiredAccountFields {
		if !allowed[provider] {
			t.Errorf("RequiredAccountFields key %q is not an allowed accounts.provider value", provider)
		}
	}
	if fields := RequiredAccountFields["Revolut"]; len(fields) != 1 || fields[0] != "payment_tag" {
		t.Errorf("Revolut fields = %v, want [payment_tag]", fields)
	}
	if fields := RequiredAccountFields["WISE"]; len(fields) != 1 || fields[0] != "email" {
		t.Errorf("WISE fields = %v, want [email]", fields)
	}
}
