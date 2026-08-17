package paymentmethods

import (
	"reflect"
	"testing"
)

func TestCatalogLabelMatches(t *testing.T) {
	aliases := []string{"SEPA Instant", "sepa_instant"}
	tests := []struct {
		raw  string
		want bool
	}{
		{"SEPA", true},
		{"sepa", true},
		{"SEPA Instant", true},
		{"sepa_instant", true},
		{"  Sepa Instant  ", true},
		{"Revolut", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := CatalogLabelMatches("SEPA", aliases, tt.raw); got != tt.want {
			t.Errorf("CatalogLabelMatches(SEPA, aliases, %q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

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
		{"NATIONAL_BANK", []string{"National Bank"}},
		{"Lightning", []string{"Lightning"}},
		{"lnurl", []string{"Lightning"}},

		// PayPal case variants all collapse
		{"PayPal", []string{"PayPal"}},
		{"Paypal", []string{"PayPal"}},
		{"paypal", []string{"PayPal"}},

		// PayPal F&F labels fold into PayPal
		{"Paypal Friends & Family", []string{"PayPal"}},
		{"PayPal(frnd&fam)", []string{"PayPal"}},
		{"(Frnds&Fam)", []string{"PayPal"}},
		{"f&f", []string{"PayPal"}},

		// Regex / typo tolerance
		{"revoult", []string{"Revolut"}},
		{"Revolut (EUR)", []string{"Revolut"}},
		{"SEPA/Instant", []string{"SEPA Instant"}},
		{"paypall", []string{"PayPal"}},

		// Compound free-text combos (CanonicalizeAll sorts output)
		{"Instant SEPA Revolut N26", []string{"N26", "Revolut", "SEPA Instant"}},
		{"Instant SEPA Wise N26", []string{"N26", "SEPA Instant", "Wise"}},
		{"Instant SEPA Paypal Friends & Family", []string{"PayPal", "SEPA Instant"}},
		{"Revolut Instant SEPA Paypal Friends & Family N26", []string{"N26", "PayPal", "Revolut", "SEPA Instant"}},

		// Traditional banks collapse to National Bank
		{"Deutsche Bank", []string{"National Bank"}},
		{"ING", []string{"National Bank"}},
		{"Instant SEPA Deutsche Bank", []string{"SEPA Instant"}},
		{"SEPA Deutsche Bank", []string{"SEPA"}},
		{"Deutsche Bank Revolut", []string{"National Bank", "Revolut"}},

		// Multi-rail providers never become National Bank
		{"Revolut Bank", []string{"Revolut"}},

		// Unknown preserved verbatim (not in tradeflow seed)
		{"Western Union", []string{"Western Union"}},
		{"Sabadell Instant Money", []string{"Sabadell Instant Money"}},
		{"M-Pesa", []string{"M-Pesa"}},
		{"Strike", []string{"Strike"}},
		{"Monero", []string{"Monero"}},

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
	a := CanonicalizeAll([]string{"Instant SEPA Revolut N26"})
	b := CanonicalizeAll([]string{"Revolut N26 Instant SEPA"})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("order-dependent: %#v vs %#v", a, b)
	}
	want := []string{"N26", "Revolut", "SEPA Instant"}
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
		{"Revolut", CategoryFiatTransmitter},
		{"SEPA Instant", CategoryBankTransfer},
		{"National Bank", CategoryBankTransfer},
		{"Lightning", CategoryCrypto},
		{"Unknown Thing", CategoryOthers},
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
