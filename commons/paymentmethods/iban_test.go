package paymentmethods

import "testing"

func TestValidateIBANValid(t *testing.T) {
	valid := []string{
		"DE89 3704 0044 0532 0130 00", // published IBAN example, spaced
		"GB29NWBK60161331926819",
		"FR1420041010050500013M02606",
		"NL91ABNA0417164300",
		"BE68539007547034",
		"IT60X0542811101000000123456",
		"ES9121000418450200051332",
		"CH9300762011623852957",
	}
	for _, iban := range valid {
		if err := ValidateIBAN(iban); err != nil {
			t.Errorf("ValidateIBAN(%q) = %v, want nil", iban, err)
		}
	}
}

func TestValidateIBANInvalid(t *testing.T) {
	cases := []struct {
		name string
		iban string
	}{
		{"empty", ""},
		{"garbage", "not-an-iban"},
		{"bad checksum", "DE89370400440532013001"},        // last digit of DE example flipped
		{"wrong length for country", "DE8937040044053201"}, // too short for DE (22)
		{"unknown country", "ZZ1234567890"},
	}
	for _, tc := range cases {
		if err := ValidateIBAN(tc.iban); err == nil {
			t.Errorf("%s: ValidateIBAN(%q) = nil, want error", tc.name, tc.iban)
		}
	}
}

func TestValidateIBANAcceptsSeparatorsAndCase(t *testing.T) {
	spaced := "DE89 3704 0044 0532 0130 00"
	hyphenated := "DE89-3704-0044-0532-0130-00"
	plain := "DE89370400440532013000"
	lowercase := "de89370400440532013000"
	for _, iban := range []string{spaced, hyphenated, plain, lowercase} {
		if err := ValidateIBAN(iban); err != nil {
			t.Errorf("ValidateIBAN(%q) = %v, want nil", iban, err)
		}
	}
}
