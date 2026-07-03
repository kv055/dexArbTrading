package paymentmethods

import (
	"regexp"
	"sort"
)

type compiledProvider struct {
	Provider
	patterns []*regexp.Regexp
}

var (
	aliasToCanonical    map[string]string
	canonicalToCategory map[string]string
	registeredCanonical map[string]bool
	maxAliasWords       int
	compiledProviders   []compiledProvider
	traditionalBanks    map[string]bool
	explicitBankRails   map[string]bool
)

func init() {
	buildRegistry()
}

func buildRegistry() {
	aliasToCanonical = make(map[string]string)
	canonicalToCategory = make(map[string]string)
	registeredCanonical = make(map[string]bool)
	traditionalBanks = make(map[string]bool)
	maxAliasWords = 1

	explicitBankRails = map[string]bool{
		"SEPA":              true,
		"SEPA Instant":      true,
		"SWIFT":             true,
		"Faster Payments":   true,
		"Cash Deposit":      true,
		"ACH":               true,
		"Interac":           true,
		"Same Bank":         true,
	}

	for _, p := range allProviders() {
		registerProvider(p)
	}
	for _, alias := range traditionalBankAliases {
		norm := normalize(alias)
		if norm == "" {
			continue
		}
		traditionalBanks[norm] = true
		registerAlias(norm, CanonicalNationalBank, CategoryBankTransfer)
	}
}

func registerProvider(p Provider) {
	cp := compiledProvider{Provider: p}
	for _, pat := range p.Patterns {
		cp.patterns = append(cp.patterns, regexp.MustCompile(pat))
	}
	compiledProviders = append(compiledProviders, cp)

	canonicalToCategory[p.Canonical] = p.Category
	registeredCanonical[p.Canonical] = true
	for _, alias := range p.Aliases {
		registerAlias(normalize(alias), p.Canonical, p.Category)
	}
}

func registerAlias(norm, canonical, category string) {
	if norm == "" {
		return
	}
	if _, exists := aliasToCanonical[norm]; !exists {
		aliasToCanonical[norm] = canonical
	}
	if _, exists := canonicalToCategory[canonical]; !exists {
		canonicalToCategory[canonical] = category
	}
	registeredCanonical[canonical] = true
	if n := len(stringsFields(norm)); n > maxAliasWords {
		maxAliasWords = n
	}
}

// CategoryOf returns the category for a canonical payment method name.
func CategoryOf(canonical string) string {
	if cat, ok := canonicalToCategory[canonical]; ok {
		return cat
	}
	return CategoryOther
}

// IsRegisteredCanonical reports whether canonical is a known registry entry.
func IsRegisteredCanonical(canonical string) bool {
	return registeredCanonical[canonical]
}

func matchProviderPatterns(label string) (string, bool) {
	for _, p := range compiledProviders {
		for _, re := range p.patterns {
			if re.MatchString(label) {
				return p.Canonical, true
			}
		}
	}
	return "", false
}

func isTraditionalBank(norm string) bool {
	if traditionalBanks[norm] {
		return true
	}
	return false
}

func resolveUnknownChunk(chunk string) string {
	trimmed := stringsTrimSpace(chunk)
	if trimmed == "" {
		return ""
	}
	norm := normalize(trimmed)

	if c, ok := aliasToCanonical[norm]; ok {
		return c
	}
	if c, ok := matchProviderPatterns(trimmed); ok {
		return c
	}
	if c, ok := matchProviderPatterns(norm); ok {
		return c
	}
	if isTraditionalBank(norm) {
		return CanonicalNationalBank
	}
	return trimmed
}

// SuppressRedundantNationalBank removes National Bank when an explicit bank rail
// is already present in the method list.
func SuppressRedundantNationalBank(methods []string) []string {
	hasRail := false
	for _, m := range methods {
		if explicitBankRails[m] {
			hasRail = true
			break
		}
	}
	if !hasRail {
		return methods
	}
	out := methods[:0]
	for _, m := range methods {
		if m == CanonicalNationalBank {
			continue
		}
		out = append(out, m)
	}
	return out
}

// stringsFields is a tiny helper to avoid importing strings in registry for word count only.
func stringsFields(s string) []string {
	return splitFields(s)
}

func stringsTrimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// UnmatchedFromDistinct returns raw DB method strings that canonicalize to
// unregistered verbatim values (candidates for registry growth).
func UnmatchedFromDistinct(raw []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range raw {
		for _, atom := range Canonicalize(r) {
			if atom == "" || IsRegisteredCanonical(atom) {
				continue
			}
			if !seen[atom] {
				seen[atom] = true
				out = append(out, atom)
			}
		}
	}
	sort.Strings(out)
	return out
}
