package tradecost

import (
	"testing"

	"commons/nostrreq"
)

func TestParseMostroInfoFeeTakesHighestPositiveFee(t *testing.T) {
	fee, ok := parseMostroInfoFee([]nostrreq.Event{
		{Tags: [][]string{{"y", "mostro"}, {"fee", "0.003"}}},
		{Tags: [][]string{{"fee", "0"}, {"fee", "0.006"}}},
		{Tags: [][]string{{"fee", "nope"}}},
	})
	if !ok || fee != 0.006 {
		t.Fatalf("parseMostroInfoFee = (%v, %v), want 0.006", fee, ok)
	}
}

func TestParseMostroInfoFeeMissing(t *testing.T) {
	if _, ok := parseMostroInfoFee(nil); ok {
		t.Fatal("expected no fee")
	}
}
