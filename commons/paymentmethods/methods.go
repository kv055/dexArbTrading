// Package paymentmethods canonicalizes free-text payment-method labels into a
// stable set of atomic, canonical method names.
//
// DEX makers (notably Bisq 2) enter payment methods as free text, often
// concatenating several into one label in arbitrary order, e.g.
// "Instant SEPA Revolut Strike MBWay" and "Revolut Instant SEPA Strike MBWay".
// Splitting these reliably is impossible without a vocabulary of known method
// names, so this package keeps a single canonical vocabulary that drives
// splitting, spelling/case normalization, and de-duplication at once.
//
// Canonicalize is order-independent: any permutation of the same methods yields
// the same atoms. Unknown text (bank names, niche services) is preserved
// verbatim so nothing is silently dropped.
package paymentmethods

import (
	"sort"
	"strings"
)

// method is one canonical payment method plus every alias spelling that should
// collapse onto it. Aliases are matched case-insensitively with "_" treated as
// a space (so the Bisq 2 enum form "SEPA_INSTANT" matches "sepa instant").
type method struct {
	canonical string
	aliases   []string
}

// vocabulary lists the atoms that appear inside compound labels plus the common
// spelling/case variants worth collapsing. Order does not matter; matching
// always prefers the longest alias. Only add entries specific enough not to
// mangle legitimate multi-word names (avoid bare words like "cash" or "bank").
var vocabulary = []method{
	// Bank rails
	{"SEPA Instant", []string{"sepa instant", "instant sepa", "sepainstant"}},
	{"SEPA", []string{"sepa"}},
	{"National Bank", []string{"national bank", "national banks"}},
	{"Same Bank", []string{"same bank"}},
	{"SWIFT", []string{"swift"}},
	{"Faster Payments", []string{"faster payments", "faster payment"}},
	{"Cash Deposit", []string{"cash deposit"}},

	// Money transmitters / e-wallets
	{"Revolut", []string{"revolut"}},
	{"Wise", []string{"wise", "transferwise", "transfer wise"}},
	{"N26", []string{"n26"}},
	{"Bizum", []string{"bizum"}},
	{"Strike", []string{"strike"}},
	{"MB Way", []string{"mb way", "mbway"}},
	{"Paysera", []string{"paysera"}},
	{"Vivid", []string{"vivid", "vivid money"}},
	{"Skrill", []string{"skrill"}},
	{"Neteller", []string{"neteller"}},
	{"Satispay", []string{"satispay"}},
	{"BLIK", []string{"blik"}},
	{"Lydia", []string{"lydia"}},
	{"Bunq", []string{"bunq"}},
	{"Monese", []string{"monese"}},
	{"Wero", []string{"wero"}},
	{"Postepay", []string{"postepay"}},
	{"MoneyBeam", []string{"moneybeam", "money beam", "moneybeam (n26)"}},
	{"Western Union", []string{"western union", "western union (cash)"}},
	{"Moneygram", []string{"moneygram", "money gram"}},
	{"Remitly", []string{"remitly"}},
	{"Worldremit", []string{"worldremit"}},
	{"Payoneer", []string{"payoneer"}},

	// PayPal — Friends & Family is matched before plain PayPal (longest alias
	// wins), so "Paypal Friends & Family" collapses to its own canonical.
	{"PayPal F&F", []string{
		"paypal friends & family", "paypal friends and family", "paypal f&f",
		"f&f", "frnds&fam", "frnd&fam", "(frnds&fam)", "(frnd&fam)",
		"paypal(frnd&fam)", "paypal(frnds&fam)",
	}},
	{"PayPal", []string{"paypal", "pay pal"}},

	// Gift cards / prepaid
	{"Amazon Gift Card", []string{
		"amazon gift card", "amazon giftcard", "amazon egift card",
		"amazon es giftcard", "amazon it giftcard", "amazon de giftcard",
		"amazon fr giftcard", "amazon uk giftcard", "amazon us giftcard",
	}},
	{"Paysafecard", []string{"paysafecard", "paysafe card", "paysafe"}},

	// Cash
	{"HalCash", []string{"halcash", "hal cash"}},
	{"Cash by Mail", []string{"cash by mail", "cashbymail"}},
	{"F2F", []string{"f2f", "face to face", "face-to-face"}},

	// Crypto (whole-label variants; tickers also fold so "MONERO XMR" -> one)
	{"Monero", []string{"monero", "xmr", "monero xmr"}},
}

var (
	// aliasToCanonical maps a normalized alias phrase to its canonical name.
	aliasToCanonical = map[string]string{}
	// maxAliasWords is the longest alias measured in whitespace-separated words.
	maxAliasWords = 1
)

func init() {
	for _, m := range vocabulary {
		for _, a := range m.aliases {
			norm := normalize(a)
			if norm == "" {
				continue
			}
			aliasToCanonical[norm] = m.canonical
			if n := len(strings.Fields(norm)); n > maxAliasWords {
				maxAliasWords = n
			}
		}
	}
}

// normalize lowercases s and treats "_" as a space, then collapses whitespace.
// Hyphens are kept so names like "M-Pesa" survive as-is.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), " ")
}

// Canonicalize splits one raw payment-method label into its canonical atoms.
// A clean single method returns a one-element slice; a compound label like
// "Instant SEPA Revolut Strike MBWay" returns each atom. Runs of unrecognized
// words are preserved together as a single verbatim token so nothing is lost.
func Canonicalize(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	// Fast path: the whole label is a known alias.
	if c, ok := aliasToCanonical[normalize(trimmed)]; ok {
		return []string{c}
	}

	// Split on "_"-as-space so Bisq 2 enum compounds tokenize too.
	words := strings.Fields(strings.ReplaceAll(trimmed, "_", " "))
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}

	var out, leftover []string
	flush := func() {
		if len(leftover) > 0 {
			out = append(out, strings.Join(leftover, " "))
			leftover = nil
		}
	}

	for i := 0; i < len(words); {
		matched := false
		// Greedy: try the longest phrase first so "instant sepa" beats "sepa".
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
			// Keep the original-cased word; drop pure punctuation noise.
			if hasAlnum(words[i]) {
				leftover = append(leftover, words[i])
			}
			i++
		}
	}
	flush()

	if len(out) == 0 {
		return []string{trimmed}
	}
	return dedupe(out)
}

// CanonicalizeAll canonicalizes every label and returns the sorted, de-duplicated
// union — the form to store as an offer's payment_methods array so equivalent
// method sets (in any order/spelling) produce identical arrays.
func CanonicalizeAll(raw []string) []string {
	var all []string
	for _, r := range raw {
		all = append(all, Canonicalize(r)...)
	}
	all = dedupe(all)
	sort.Strings(all)
	return all
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
