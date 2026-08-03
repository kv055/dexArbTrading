package paymentmethods

import (
	"strings"
	"testing"
)

// TestSeedRowsRoundTripAliases proves the alias→merge_regex transformation is
// lossless: every alias of every built-in provider still canonicalizes to the
// same name through a registry built from SeedRows.
func TestSeedRowsRoundTripAliases(t *testing.T) {
	reg := NewRegistry(SeedRows())
	for _, p := range allProviders() {
		for _, alias := range p.Aliases {
			got := reg.Canonicalize(alias)
			if len(got) != 1 || got[0] != p.Canonical {
				t.Errorf("alias %q: got %v, want [%s]", alias, got, p.Canonical)
			}
		}
	}
	for _, bank := range traditionalBankAliases {
		got := reg.Canonicalize(bank)
		if len(got) != 1 || got[0] != CanonicalNationalBank {
			t.Errorf("traditional bank %q: got %v, want [%s]", bank, got, CanonicalNationalBank)
		}
	}
}

func TestSeedRowsCategoriesAreDisplayNames(t *testing.T) {
	allowed := make(map[string]bool)
	for _, d := range CategoryDisplayNames() {
		allowed[d] = true
	}
	for _, row := range SeedRows() {
		if !allowed[row.Category] {
			t.Errorf("row %q: category %q is not an allowed payment_methods.category value", row.Name, row.Category)
		}
	}
}

func TestNewRegistrySkipsInvalidRegex(t *testing.T) {
	reg := NewRegistry([]MethodRow{
		{Name: "Broken", MergeRegex: "(unclosed", AliasList: []string{"broken alias"}, Category: "Others", MatchOrder: 100},
		{Name: "Fine", MergeRegex: "(?i)^(?:fine|fein)$", Category: "Others", MatchOrder: 110},
	})
	if len(reg.Warnings()) != 1 || !strings.Contains(reg.Warnings()[0], "Broken") {
		t.Fatalf("want one warning naming Broken, got %v", reg.Warnings())
	}
	// The broken row still matches by exact name...
	if got := reg.Canonicalize("broken"); len(got) != 1 || got[0] != "Broken" {
		t.Errorf("exact-name match for broken row: got %v", got)
	}
	// ...and the valid row is unaffected.
	if got := reg.Canonicalize("FEIN"); len(got) != 1 || got[0] != "Fine" {
		t.Errorf("regex match for valid row: got %v", got)
	}
	if got := reg.Canonicalize("broken_alias"); len(got) != 1 || got[0] != "Broken" {
		t.Errorf("alias match for broken row: got %v", got)
	}
}

// TestRegistryUnknownRowExactNameMatch mirrors the auto-inserted rows the
// snapshot writer creates: no merge_regex, matched by exact name only.
func TestRegistryUnknownRowExactNameMatch(t *testing.T) {
	rows := append(SeedRows(), MethodRow{Name: "Frobnicate Pay", Category: "Others", MatchOrder: 1000})
	reg := NewRegistry(rows)
	if got := reg.Canonicalize("frobnicate_pay"); len(got) != 1 || got[0] != "Frobnicate Pay" {
		t.Errorf("exact-name match: got %v", got)
	}
	if reg.CategoryOf("Frobnicate Pay") != CategoryOthers {
		t.Errorf("category: got %q, want %q", reg.CategoryOf("Frobnicate Pay"), CategoryOthers)
	}
	if !reg.IsRegisteredCanonical("Frobnicate Pay") {
		t.Error("IsRegisteredCanonical should be true for a table row")
	}
}

func TestRegistryAliasListExactMatch(t *testing.T) {
	reg := NewRegistry([]MethodRow{
		{Name: "PayPal", AliasList: []string{"Paypal", "pay pal f & f"}, Category: "Fiat Transmitter", MatchOrder: 100},
		{Name: "SEPA", AliasList: []string{"National Bank", "Sepa Instant"}, Category: "Bank", MatchOrder: 110},
	})
	for _, c := range []struct {
		raw  string
		want string
	}{
		{"Paypal", "PayPal"},
		{"pay_pal_f_&_f", "PayPal"},
		{"National Bank", "SEPA"},
		{"sepa instant", "SEPA"},
	} {
		if got := reg.Canonicalize(c.raw); len(got) != 1 || got[0] != c.want {
			t.Errorf("Canonicalize(%q) = %v, want [%s]", c.raw, got, c.want)
		}
	}
}

func TestRegistryAliasListConflictUsesMatchOrder(t *testing.T) {
	reg := NewRegistry([]MethodRow{
		{ID: 2, Name: "Later", AliasList: []string{"shared alias"}, Category: "Others", MatchOrder: 200},
		{ID: 1, Name: "Earlier", AliasList: []string{"shared alias"}, Category: "Others", MatchOrder: 100},
	})
	if got := reg.Canonicalize("shared alias"); len(got) != 1 || got[0] != "Earlier" {
		t.Errorf("alias conflict = %v, want [Earlier]", got)
	}
}
