package paymentmethods

import (
	"regexp"
	"strings"
)

// SeedRows converts the built-in provider data (providers.go, banks.go,
// crypto.go) into payment_methods table rows. It is used both to seed the
// table on boot (dexUnifiedApi's SeedPaymentMethods, ON CONFLICT DO NOTHING)
// and as the in-process fallback registry before the table has been loaded.
//
// Each provider's normalized aliases become one case-insensitive anchored
// alternation; its raw regex Patterns are appended as extra alternates. The
// traditional-bank list folds into the single National Bank row. MatchOrder
// preserves the specific-before-generic ordering of allProviders(); rows
// inserted later (auto-created unknowns) default to match_order 1000 and are
// scanned last.
func SeedRows() []MethodRow {
	all := allProviders()
	out := make([]MethodRow, 0, len(all))
	for i, p := range all {
		aliases := p.Aliases
		if p.Canonical == CanonicalNationalBank {
			aliases = append(append([]string{}, aliases...), traditionalBankAliases...)
		}
		out = append(out, MethodRow{
			Name:       p.Canonical,
			MergeRegex: buildMergeRegex(aliases, p.Patterns),
			Category:   CategoryDisplayName(p.Category),
			MatchOrder: 100 + i*10,
		})
	}
	return out
}

// buildMergeRegex combines normalized aliases into a (?i)-anchored alternation
// and appends raw patterns (which carry their own flags) as alternates.
// Returns "" when there is nothing to match beyond the row's own name.
func buildMergeRegex(aliases, patterns []string) string {
	var quoted []string
	for _, a := range aliases {
		norm := normalize(a)
		if norm == "" {
			continue
		}
		quoted = append(quoted, regexp.QuoteMeta(norm))
	}
	var parts []string
	if len(quoted) > 0 {
		parts = append(parts, "(?i)^(?:"+strings.Join(quoted, "|")+")$")
	}
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		parts = append(parts, "(?:"+p+")")
	}
	return strings.Join(parts, "|")
}
