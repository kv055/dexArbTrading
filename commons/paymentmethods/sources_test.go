package paymentmethods

import (
	"strings"
	"testing"
)

func TestNormalizeSources(t *testing.T) {
	t.Parallel()

	got, err := NormalizeSources([]string{" Bisq ", "ROBOSATS", "bisq", "", "hodlhodl"})
	if err != nil {
		t.Fatalf("NormalizeSources: %v", err)
	}
	want := []string{"bisq", "robosats", "hodlhodl"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeSources = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeSources = %v, want %v", got, want)
		}
	}

	if _, err := NormalizeSources([]string{"kraken"}); err == nil {
		t.Fatal("expected error for unknown source")
	} else if !strings.Contains(err.Error(), "kraken") {
		t.Fatalf("error %q should mention kraken", err)
	}

	empty, err := NormalizeSources(nil)
	if err != nil {
		t.Fatalf("NormalizeSources(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("NormalizeSources(nil) = %#v, want empty non-nil slice", empty)
	}
}

func TestNormalizeCurrencies(t *testing.T) {
	t.Parallel()

	got, err := NormalizeCurrencies([]string{" eur ", "USD", "eur", "", "chf"})
	if err != nil {
		t.Fatalf("NormalizeCurrencies: %v", err)
	}
	want := []string{"EUR", "USD", "CHF"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeCurrencies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeCurrencies = %v, want %v", got, want)
		}
	}

	if _, err := NormalizeCurrencies([]string{"XYZ"}); err == nil {
		t.Fatal("expected error for unknown currency")
	} else if !strings.Contains(err.Error(), "XYZ") {
		t.Fatalf("error %q should mention XYZ", err)
	}

	empty, err := NormalizeCurrencies(nil)
	if err != nil {
		t.Fatalf("NormalizeCurrencies(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("NormalizeCurrencies(nil) = %#v, want empty non-nil slice", empty)
	}
}
