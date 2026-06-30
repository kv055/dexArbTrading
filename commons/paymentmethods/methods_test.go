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

		// PayPal case variants all collapse (the "listed 3 times" bug)
		{"PayPal", []string{"PayPal"}},
		{"Paypal", []string{"PayPal"}},
		{"paypal", []string{"PayPal"}},

		// PayPal F&F kept distinct from plain PayPal
		{"Paypal Friends & Family", []string{"PayPal F&F"}},
		{"PayPal(frnd&fam)", []string{"PayPal F&F"}},
		{"(Frnds&Fam)", []string{"PayPal F&F"}},
		{"f&f", []string{"PayPal F&F"}},

		// Paysafe variants — the very first example from the user
		{"paysafecard", []string{"Paysafecard"}},
		{"paysafe card", []string{"Paysafecard"}},
		{"Paysafe", []string{"Paysafecard"}},

		// Compound free-text combos split into ordered atoms
		{"Instant SEPA Bizum", []string{"SEPA Instant", "Bizum"}},
		{"Instant SEPA Revolut Strike MBWay", []string{"SEPA Instant", "Revolut", "Strike", "MB Way"}},
		{"Instant SEPA Wise N26 Paysera Vivid", []string{"SEPA Instant", "Wise", "N26", "Paysera", "Vivid"}},
		{"Instant SEPA Bizum Amazon ES GiftCard", []string{"SEPA Instant", "Bizum", "Amazon Gift Card"}},
		{"Instant SEPA Paypal Friends & Family", []string{"SEPA Instant", "PayPal F&F"}},
		{"Revolut Instant SEPA Paypal Friends & Family N26", []string{"Revolut", "SEPA Instant", "PayPal F&F", "N26"}},
		{"HalCash Amazon ES GiftCard Revolut Instant SEPA", []string{"HalCash", "Amazon Gift Card", "Revolut", "SEPA Instant"}},
		{"MONERO XMR", []string{"Monero"}},

		// Unknown / long-tail single methods preserved verbatim
		{"Western Union", []string{"Western Union"}},
		{"Deutsche Bank", []string{"Deutsche Bank"}},
		{"Monzo Bank", []string{"Monzo Bank"}},
		{"Sabadell Instant Money", []string{"Sabadell Instant Money"}},
		{"M-Pesa", []string{"M-Pesa"}},

		// Edge cases
		{"", nil},
		{"  Revolut  ", []string{"Revolut"}},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := Canonicalize(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Canonicalize(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestCanonicalizeOrderIndependent verifies that permutations of the same
// methods produce the same atom set — the core property that collapses the
// "same methods, different order" explosion.
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

func TestCanonicalizeAllDedup(t *testing.T) {
	// A whole offer's worth of array elements collapses to a sorted unique set.
	in := []string{"Instant SEPA", "SEPA_INSTANT", "Revolut", "REVOLUT", "Instant SEPA Revolut"}
	got := CanonicalizeAll(in)
	want := []string{"Revolut", "SEPA Instant"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CanonicalizeAll = %#v, want %#v", got, want)
	}
}
