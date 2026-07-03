// Package paymentmethods canonicalizes free-text payment-method labels into a
// stable set of atomic, canonical method names.
//
// DEX makers (notably Bisq 2) enter payment methods as free text, often
// concatenating several into one label in arbitrary order. A provider registry
// drives splitting, spelling normalization, regex matching, traditional-bank
// collapse, and category assignment.
//
// Canonicalize is order-independent: any permutation of the same methods yields
// the same atoms after CanonicalizeAll.
package paymentmethods

import (
	"sort"
	"strings"
)

// normalize lowercases s and treats "_" as a space, then collapses whitespace.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), " ")
}

func splitFields(s string) []string {
	return strings.Fields(s)
}

// Canonicalize splits one raw payment-method label into its canonical atoms.
func Canonicalize(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	norm := normalize(trimmed)
	if c, ok := aliasToCanonical[norm]; ok {
		return []string{c}
	}
	if isTraditionalBank(norm) {
		return []string{CanonicalNationalBank}
	}

	// Tokenize compound labels; also split on / and parentheses for "SEPA/Instant", "Revolut (EUR)".
	tokenSource := strings.ReplaceAll(trimmed, "/", " ")
	tokenSource = strings.ReplaceAll(tokenSource, "(", " ")
	tokenSource = strings.ReplaceAll(tokenSource, ")", " ")
	words := strings.Fields(strings.ReplaceAll(tokenSource, "_", " "))
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}

	var out, leftover []string
	flush := func() {
		if len(leftover) == 0 {
			return
		}
		chunk := strings.Join(leftover, " ")
		leftover = nil
		if resolved := resolveUnknownChunk(chunk); resolved != "" && !isNoiseChunk(resolved) {
			out = append(out, resolved)
		}
	}

	for i := 0; i < len(words); {
		matched := false
		for n := min(maxAliasWords, len(words)-i); n >= 1; n-- {
			phrase := strings.Join(lower[i:i+n], " ")
			if c, ok := aliasToCanonical[phrase]; ok {
				flush()
				out = append(out, c)
				i += n
				matched = true
				break
			}
		}
		if !matched {
			if hasAlnum(words[i]) {
				leftover = append(leftover, words[i])
			}
			i++
		}
	}
	flush()

	if len(out) == 0 {
		if resolved := resolveUnknownChunk(trimmed); resolved != "" && resolved != trimmed && !isNoiseChunk(resolved) {
			return []string{resolved}
		}
		return []string{trimmed}
	}
	return dedupe(out)
}

func isNoiseChunk(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bank", "eur", "usd", "gbp":
		return true
	default:
		return false
	}
}

// CanonicalizeAll canonicalizes every label and returns the sorted, de-duplicated
// union — the form to store as an offer's payment_methods array.
func CanonicalizeAll(raw []string) []string {
	var all []string
	for _, r := range raw {
		all = append(all, Canonicalize(r)...)
	}
	all = dedupe(all)
	all = SuppressRedundantNationalBank(all)
	sort.Strings(all)
	return all
}

// CanonicalizeDistinct canonicalizes each raw string independently and returns the
// sorted union. Unlike CanonicalizeAll, this does not apply per-offer National
// Bank suppression across unrelated labels — use when building global method lists
// from DISTINCT unnest(payment_methods).
func CanonicalizeDistinct(raw []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range raw {
		for _, c := range Canonicalize(r) {
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}
