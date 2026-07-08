package paymentmethods

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry canonicalizes raw payment-method labels against a set of
// MethodRows (normally loaded from the payment_methods table via LoadRows;
// SeedRows provides the built-in fallback). Build one with NewRegistry and
// either pass it around or install it process-wide with SetDefault.
type Registry struct {
	exactToName  map[string]string // normalize(row.Name) -> row.Name
	catIDByName  map[string]string // row.Name -> category ID (e.g. "fiat_transmitter")
	known        map[string]bool   // row.Name set
	rows         []compiledRow     // scan order: match_order, id
	maxNameWords int
	warnings     []string
	phraseCache  sync.Map // normalized phrase -> string (resolved name, "" = no match)
}

type compiledRow struct {
	name   string
	reFull *regexp.Regexp // ^(?:merge_regex)$ — whole-phrase semantics
	reSub  *regexp.Regexp // merge_regex as-is — substring semantics
}

// minPhraseWords is the floor for the greedy longest-phrase tokenizer window.
// Regex-only aliases are hidden inside compiled regexes, so the window cannot
// be derived from them; the longest current built-in regex alias is 4 words.
const minPhraseWords = 6

// explicitBankRails are seeded canonical names that make a bare "National
// Bank" entry redundant on the same offer. Policy, not merge data — renaming
// these rows in the panel would disable suppression for them.
var explicitBankRails = map[string]bool{
	"SEPA":            true,
	"SEPA Instant":    true,
	"SWIFT":           true,
	"Faster Payments": true,
	"Cash Deposit":    true,
	"ACH":             true,
	"Interac":         true,
	"Same Bank":       true,
}

// NewRegistry compiles rows into a Registry. It never fails: a row whose
// merge_regex does not compile is kept for exact-name matching and the
// problem is recorded in Warnings.
func NewRegistry(rows []MethodRow) *Registry {
	sorted := make([]MethodRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].MatchOrder != sorted[j].MatchOrder {
			return sorted[i].MatchOrder < sorted[j].MatchOrder
		}
		return sorted[i].ID < sorted[j].ID
	})

	r := &Registry{
		exactToName:  make(map[string]string, len(sorted)),
		catIDByName:  make(map[string]string, len(sorted)),
		known:        make(map[string]bool, len(sorted)),
		maxNameWords: minPhraseWords,
	}
	for _, row := range sorted {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			continue
		}
		norm := normalize(name)
		if _, exists := r.exactToName[norm]; !exists && norm != "" {
			r.exactToName[norm] = name
		}
		if n := len(strings.Fields(norm)); n > r.maxNameWords {
			r.maxNameWords = n
		}
		for _, alias := range row.AliasList {
			aliasNorm := normalize(alias)
			if aliasNorm == "" {
				continue
			}
			if _, exists := r.exactToName[aliasNorm]; !exists {
				r.exactToName[aliasNorm] = name
			}
			if n := len(strings.Fields(aliasNorm)); n > r.maxNameWords {
				r.maxNameWords = n
			}
		}
		r.catIDByName[name] = CategoryIDFromDisplay(row.Category)
		r.known[name] = true

		cr := compiledRow{name: name}
		if row.MergeRegex != "" {
			sub, err := regexp.Compile(row.MergeRegex)
			if err != nil {
				r.warnings = append(r.warnings,
					fmt.Sprintf("payment method %q: merge_regex does not compile, using exact-name match only: %v", name, err))
			} else {
				cr.reSub = sub
				// The full-match variant anchors the whole expression so
				// substring patterns don't swallow surrounding words during
				// phrase matching.
				if full, err := regexp.Compile("^(?:" + row.MergeRegex + ")$"); err == nil {
					cr.reFull = full
				}
			}
		}
		if cr.reSub != nil {
			r.rows = append(r.rows, cr)
		}
	}
	return r
}

// Warnings lists rows whose merge_regex failed to compile.
func (r *Registry) Warnings() []string {
	out := make([]string, len(r.warnings))
	copy(out, r.warnings)
	return out
}

// defaultRegistry starts as the built-in seed registry and is replaced with a
// DB-backed one via SetDefault once the payment_methods table is loaded.
var defaultRegistry atomic.Pointer[Registry]

func init() {
	defaultRegistry.Store(NewRegistry(SeedRows()))
}

// Default returns the process-wide registry.
func Default() *Registry { return defaultRegistry.Load() }

// SetDefault installs r as the process-wide registry used by the package-level
// Canonicalize*/CategoryOf/... wrappers. Safe for concurrent use.
func SetDefault(r *Registry) {
	if r != nil {
		defaultRegistry.Store(r)
	}
}

// CategoryOf returns the category ID for a canonical payment method name.
func (r *Registry) CategoryOf(canonical string) string {
	if cat, ok := r.catIDByName[canonical]; ok {
		return cat
	}
	return CategoryOthers
}

// IsRegisteredCanonical reports whether canonical is a known registry entry.
func (r *Registry) IsRegisteredCanonical(canonical string) bool {
	return r.known[canonical]
}

// matchPhrase resolves one normalized phrase to a canonical name: exact name
// hit first, then an anchored merge_regex scan in match order. Results are
// memoized — distinct phrases per run number in the hundreds, so the linear
// regex scan amortizes away.
func (r *Registry) matchPhrase(norm string) (string, bool) {
	if norm == "" {
		return "", false
	}
	if name, ok := r.exactToName[norm]; ok {
		return name, true
	}
	if cached, ok := r.phraseCache.Load(norm); ok {
		name := cached.(string)
		return name, name != ""
	}
	for _, row := range r.rows {
		if row.reFull != nil && row.reFull.MatchString(norm) {
			r.phraseCache.Store(norm, row.name)
			return row.name, true
		}
	}
	r.phraseCache.Store(norm, "")
	return "", false
}

// matchSubstring scans merge_regexes with substring semantics — the last
// resort for leftover chunks (catches typo patterns like "Revoult transfer").
func (r *Registry) matchSubstring(label string) (string, bool) {
	for _, row := range r.rows {
		if row.reSub != nil && row.reSub.MatchString(label) {
			return row.name, true
		}
	}
	return "", false
}

// resolveUnknownChunk maps a leftover token run to a canonical name, or
// returns it verbatim when nothing matches.
func (r *Registry) resolveUnknownChunk(chunk string) string {
	trimmed := strings.TrimSpace(chunk)
	if trimmed == "" {
		return ""
	}
	norm := normalize(trimmed)
	if name, ok := r.matchPhrase(norm); ok {
		return name
	}
	if name, ok := r.matchSubstring(trimmed); ok {
		return name
	}
	if name, ok := r.matchSubstring(norm); ok {
		return name
	}
	return trimmed
}

// SuppressRedundantNationalBank removes National Bank when an explicit bank
// rail is already present in the method list.
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

// UnmatchedFromDistinct returns raw DB method strings that canonicalize to
// unregistered verbatim values (candidates for the payment_methods table —
// the snapshot writer auto-inserts these as Others/inactive).
func (r *Registry) UnmatchedFromDistinct(raw []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range raw {
		for _, atom := range r.Canonicalize(s) {
			if atom == "" || r.IsRegisteredCanonical(atom) {
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

// Package-level wrappers over the Default() registry, keeping existing
// call sites source-compatible.

// CategoryOf returns the category ID for a canonical payment method name.
func CategoryOf(canonical string) string { return Default().CategoryOf(canonical) }

// IsRegisteredCanonical reports whether canonical is a known registry entry.
func IsRegisteredCanonical(canonical string) bool { return Default().IsRegisteredCanonical(canonical) }

// UnmatchedFromDistinct returns raw DB method strings that canonicalize to
// unregistered verbatim values.
func UnmatchedFromDistinct(raw []string) []string { return Default().UnmatchedFromDistinct(raw) }
