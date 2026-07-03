package paymentmethods

import (
	"reflect"
	"testing"
)

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		raw  string
		want []string
	}{
		// Clean single methods (fast path)
		{"Revolut", []string{"Revolut"}},
		{"Instant SEPA", []string{"SEPA Instant"}},
		{"SEPA Instant", []string{"SEPA Instant"}},
		{"SEPA_INSTANT", []string{"SEPA Instant"}},
		{"SEPA", []string{"SEPA"}},
		{"WISE", []string{"Wise"}},
		{"AMAZON_GIFT_CARD", []string{"Amazon Gift Card"}},
		{"NATIONAL_BANK", []string{"National Bank"}},

		// PayPal case variants all collapse
		{"PayPal", []string{"PayPal"}},
		{"Paypal", []string{"PayPal"}},
		{"paypal", []string{"PayPal"}},

		// PayPal F&F kept distinct from plain PayPal
		{"Paypal Friends & Family", []string{"PayPal F&F"}},
		{"PayPal(frnd&fam)", []string{"PayPal F&F"}},
		{"(Frnds&Fam)", []string{"PayPal F&F"}},
		{"f&f", []string{"PayPal F&F"}},

		// Paysafe variants
		{"paysafecard", []string{"Paysafecard"}},
		{"paysafe card", []string{"Paysafecard"}},
		{"Paysafe", []string{"Paysafecard"}},

		// Regex / typo tolerance
		{"revoult", []string{"Revolut"}},
		{"Revolut (EUR)", []string{"Revolut"}},
		{"SEPA/Instant", []string{"SEPA Instant"}},
		{"paypall", []string{"PayPal"}},

		// Compound free-text combos (CanonicalizeAll sorts output)
		{"Instant SEPA Bizum", []string{"Bizum", "SEPA Instant"}},
		{"Instant SEPA Revolut Strike MBWay", []string{"MB Way", "Revolut", "SEPA Instant", "Strike"}},
		{"Instant SEPA Wise N26 Paysera Vivid", []string{"N26", "Paysera", "SEPA Instant", "Vivid", "Wise"}},
		{"Instant SEPA Bizum Amazon ES GiftCard", []string{"Amazon Gift Card", "Bizum", "SEPA Instant"}},
		{"Instant SEPA Paypal Friends & Family", []string{"PayPal F&F", "SEPA Instant"}},
		{"Revolut Instant SEPA Paypal Friends & Family N26", []string{"N26", "PayPal F&F", "Revolut", "SEPA Instant"}},
		{"HalCash Amazon ES GiftCard Revolut Instant SEPA", []string{"Amazon Gift Card", "HalCash", "Revolut", "SEPA Instant"}},
		{"MONERO XMR", []string{"Monero"}},

		// Crypto coins and rails
		{"Other coins", []string{"Other Coins"}},
		{"SOL XRP XLM", []string{"Solana", "Stellar", "XRP"}},
		{"USDT LTC", []string{"Litecoin", "USDT"}},
		{"Altcoins Instant", []string{"Altcoins Instant"}},
		{"ETH", []string{"Ethereum"}},
		{"L-BTC", []string{"Liquid Bitcoin"}},
		{"Liquid", []string{"Liquid Bitcoin"}},
		{"TUSD", []string{"True USD"}},
		{"True USD", []string{"True USD"}},
		{"USDC FDUSD", []string{"FDUSD", "USDC"}},

		// Traditional banks collapse to National Bank
		{"Deutsche Bank", []string{"National Bank"}},
		{"ING", []string{"National Bank"}},
		{"Instant SEPA Deutsche Bank", []string{"SEPA Instant"}},
		{"SEPA Deutsche Bank", []string{"SEPA"}},
		{"Deutsche Bank Revolut", []string{"National Bank", "Revolut"}},

		// Multi-rail providers never become National Bank
		{"Revolut Bank", []string{"Revolut"}},
		{"Monzo Bank", []string{"Monzo"}},

		// Unknown preserved verbatim
		{"Western Union", []string{"Western Union"}},
		{"Sabadell Instant Money", []string{"Sabadell Instant Money"}},
		{"M-Pesa", []string{"M-Pesa"}},

		// False positive guard: bare "instant" must not become SEPA Instant
		{"instant", []string{"instant"}},

		// Edge cases
		{"", nil},
		{"  Revolut  ", []string{"Revolut"}},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := CanonicalizeAll([]string{tt.raw})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CanonicalizeAll(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCanonicalizeOrderIndependent(t *testing.T) {
	a := CanonicalizeAll([]string{"Instant SEPA Revolut Strike MBWay"})
	b := CanonicalizeAll([]string{"Revolut Strike Instant SEPA MBWay"})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("order-dependent: %#v vs %#v", a, b)
	}
	want := []string{"MB Way", "Revolut", "SEPA Instant", "Strike"}
	if !reflect.DeepEqual(a, want) {
		t.Errorf("CanonicalizeAll = %#v, want %#v", a, want)
	}
}

func TestCanonicalizeDistinctDoesNotSuppressAcrossLabels(t *testing.T) {
	// Distinct list from DB: SEPA Instant from one offer, Deutsche Bank from another.
	got := CanonicalizeDistinct([]string{"SEPA Instant", "Deutsche Bank"})
	want := []string{"National Bank", "SEPA Instant"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CanonicalizeDistinct = %#v, want %#v", got, want)
	}
}

func TestCanonicalizeAllDedup(t *testing.T) {
	in := []string{"Instant SEPA", "SEPA_INSTANT", "Revolut", "REVOLUT", "Instant SEPA Revolut"}
	got := CanonicalizeAll(in)
	want := []string{"Revolut", "SEPA Instant"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CanonicalizeAll = %#v, want %#v", got, want)
	}
}

func TestSuppressRedundantNationalBank(t *testing.T) {
	tests := []struct {
		in, want []string
	}{
		{[]string{"National Bank"}, []string{"National Bank"}},
		{[]string{"National Bank", "Revolut"}, []string{"National Bank", "Revolut"}},
		{[]string{"SEPA Instant", "National Bank"}, []string{"SEPA Instant"}},
		{[]string{"SEPA", "National Bank", "Revolut"}, []string{"SEPA", "Revolut"}},
	}
	for _, tt := range tests {
		got := SuppressRedundantNationalBank(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SuppressRedundantNationalBank(%#v) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestCategoryOf(t *testing.T) {
	tests := []struct {
		canonical string
		want      string
	}{
		{"Revolut", CategoryMoneyTransmitter},
		{"SEPA Instant", CategoryBankTransfer},
		{"National Bank", CategoryBankTransfer},
		{"Monero", CategoryCrypto},
		{"Litecoin", CategoryCrypto},
		{"Other Coins", CategoryCrypto},
		{"Altcoins", CategoryCrypto},
		{"Unknown Thing", CategoryOther},
	}
	for _, tt := range tests {
		if got := CategoryOf(tt.canonical); got != tt.want {
			t.Errorf("CategoryOf(%q) = %q, want %q", tt.canonical, got, tt.want)
		}
	}
}

func TestUnmatchedFromDistinct(t *testing.T) {
	raw := []string{"Deutsche Bank", "Sabadell Instant Money", "Revolut"}
	got := UnmatchedFromDistinct(raw)
	want := []string{"Sabadell Instant Money"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnmatchedFromDistinct = %#v, want %#v", got, want)
	}
}
