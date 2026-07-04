package paymentmethods

import (
	"fmt"
	"regexp"
	"strings"
)

// ibanLength maps ISO 3166-1 alpha-2 country codes to their fixed IBAN length,
// per the IBAN registry (SWIFT/ISO 13616). Only countries that plausibly show
// up in SEPA/European trading-account setups are exhaustive here; unknown
// codes are rejected rather than guessed at.
var ibanLength = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16,
	"BG": 22, "BH": 22, "BR": 29, "BY": 28, "CH": 21, "CR": 22, "CY": 28,
	"CZ": 24, "DE": 22, "DK": 18, "DO": 28, "EE": 20, "EG": 29, "ES": 24,
	"FI": 18, "FO": 18, "FR": 27, "GB": 22, "GE": 22, "GI": 23, "GL": 18,
	"GR": 27, "GT": 28, "HR": 21, "HU": 28, "IE": 22, "IL": 23, "IQ": 23,
	"IS": 26, "IT": 27, "JO": 30, "KW": 30, "KZ": 20, "LB": 28, "LC": 32,
	"LI": 21, "LT": 20, "LU": 20, "LV": 21, "LY": 25, "MC": 27, "MD": 24,
	"ME": 22, "MK": 19, "MR": 27, "MT": 31, "MU": 30, "NL": 18, "NO": 15,
	"PK": 24, "PL": 28, "PS": 29, "PT": 25, "QA": 29, "RO": 24, "RS": 22,
	"SA": 24, "SC": 31, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "ST": 25,
	"SV": 28, "TL": 23, "TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24,
	"XK": 20,
}

// ibanStructure matches the general IBAN shape: a 2-letter country code, a
// 2-digit checksum, then an alphanumeric BBAN.
var ibanStructure = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]+$`)

// ValidateIBAN reports whether raw is a structurally well-formed IBAN: known
// country code, correct length for that country, and a passing ISO 7064
// MOD 97-10 checksum. It cannot and does not confirm the account exists.
func ValidateIBAN(raw string) error {
	iban := normalizeIBAN(raw)
	if iban == "" {
		return fmt.Errorf("iban: empty")
	}
	if !ibanStructure.MatchString(iban) {
		return fmt.Errorf("iban: invalid format %q", raw)
	}

	country := iban[:2]
	wantLen, ok := ibanLength[country]
	if !ok {
		return fmt.Errorf("iban: unknown country code %q", country)
	}
	if len(iban) != wantLen {
		return fmt.Errorf("iban: wrong length for %s (expected %d, got %d)", country, wantLen, len(iban))
	}

	if !ibanChecksumValid(iban) {
		return fmt.Errorf("iban: checksum failed")
	}
	return nil
}

// normalizeIBAN strips spaces/hyphens and uppercases the input.
func normalizeIBAN(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if r == ' ' || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ibanChecksumValid implements ISO 7064 MOD 97-10: move the first four
// characters to the end, expand letters to two-digit numbers (A=10..Z=35),
// and check the resulting numeral is congruent to 1 mod 97. The remainder is
// accumulated digit-by-digit so no big-integer type is needed.
func ibanChecksumValid(iban string) bool {
	rearranged := iban[4:] + iban[:4]

	remainder := 0
	for _, r := range rearranged {
		var digits string
		switch {
		case r >= '0' && r <= '9':
			digits = string(r)
		case r >= 'A' && r <= 'Z':
			digits = fmt.Sprintf("%d", int(r-'A')+10)
		default:
			return false
		}
		for _, d := range digits {
			remainder = (remainder*10 + int(d-'0')) % 97
		}
	}
	return remainder == 1
}
