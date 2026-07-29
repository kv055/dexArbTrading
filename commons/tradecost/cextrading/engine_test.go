package cextrading

import (
	"math"
	"testing"
)

func eq(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

// testEngine builds an Engine with injected, deterministic fee sources so the
// optimizer can be exercised offline.
func testEngine(taker, withdraw map[string]float64, hedge func(cex string, btc, ref, tm float64, coupled bool, side string) hedgeCostDetails) *Engine {
	if hedge == nil {
		hedge = func(cex string, btc, ref, tm float64, coupled bool, side string) hedgeCostDetails {
			return hedgeCostDetails{}
		}
	}
	return &Engine{
		cexes:       []string{"kraken", "binance", "bitstamp", "coinbase"},
		takerFrac:   func(c string) float64 { return taker[c] },
		withdrawBTC: func(c string) float64 { return withdraw[c] },
		hedgeCost:   hedge,
	}
}

func TestBestPicksCheapestCombo(t *testing.T) {
	taker := map[string]float64{"kraken": 0.002, "binance": 0.001, "bitstamp": 0.005, "coinbase": 0.006}
	withdraw := map[string]float64{"kraken": 0.0001, "binance": 0.0002, "bitstamp": 0.0003, "coinbase": 0.0}
	eng := testEngine(taker, withdraw, nil)

	sel, ok := eng.Best(Input{BTCAmount: 1.0, Direction: "buy"})
	if !ok {
		t.Fatal("expected a selection")
	}
	// binance combo = 0.001 + 0.0002 = 0.0012 is the lowest.
	if sel.CEX != "binance" {
		t.Fatalf("cheapest CEX = %q, want binance", sel.CEX)
	}
	eq(t, sel.TakerFeeBTC, 0.001)
	eq(t, sel.WithdrawalFeeBTC, 0.0002)
	eq(t, sel.ComboBTC, 0.0012)
}

func TestBestSkipsExcludedCEXes(t *testing.T) {
	taker := map[string]float64{"kraken": 0.002, "binance": 0.001, "bitstamp": 0.005, "coinbase": 0.006}
	withdraw := map[string]float64{"kraken": 0.0001, "binance": 0.0002, "bitstamp": 0.0003, "coinbase": 0.0}
	eng := testEngine(taker, withdraw, nil)

	sel, ok := eng.Best(Input{BTCAmount: 1.0, Direction: "buy", ExcludedCEXes: []string{"binance"}})
	if !ok {
		t.Fatal("expected a selection")
	}
	// With binance excluded, kraken (0.002 + 0.0001 = 0.0021) wins.
	if sel.CEX != "kraken" {
		t.Fatalf("cheapest CEX = %q, want kraken", sel.CEX)
	}
	eq(t, sel.ComboBTC, 0.0021)
}

func TestBestReturnsFalseWhenAllExcluded(t *testing.T) {
	eng := testEngine(map[string]float64{}, map[string]float64{}, nil)
	if _, ok := eng.Best(Input{BTCAmount: 1.0, Direction: "buy", ExcludedCEXes: []string{"kraken", "binance", "bitstamp", "coinbase"}}); ok {
		t.Fatal("expected no selection when every CEX is excluded")
	}
}

func TestBestAskSideHasNoWithdrawalFee(t *testing.T) {
	taker := map[string]float64{"kraken": 0.002, "binance": 0.001, "bitstamp": 0.005, "coinbase": 0.006}
	withdraw := map[string]float64{"kraken": 0.0001, "binance": 0.0002, "bitstamp": 0.0003, "coinbase": 0.0}
	eng := testEngine(taker, withdraw, nil)

	sel, ok := eng.Best(Input{BTCAmount: 1.0, Direction: "sell"})
	if !ok {
		t.Fatal("expected a selection")
	}
	// Ask side: no withdrawal fee, so the combo is taker only — binance still wins.
	if sel.CEX != "binance" {
		t.Fatalf("cheapest CEX = %q, want binance", sel.CEX)
	}
	eq(t, sel.WithdrawalFeeBTC, 0)
	eq(t, sel.ComboBTC, 0.001)
}

func TestManualOptionsExposeEligibleSpotAndWithdrawalFees(t *testing.T) {
	taker := map[string]float64{"kraken": 0.002, "binance": 0.001, "bitstamp": 0.005, "coinbase": 0.006}
	withdraw := map[string]float64{"kraken": 0.0001, "binance": 0.0002, "bitstamp": 0.0003, "coinbase": 0}
	eng := testEngine(taker, withdraw, nil)

	options := eng.ManualOptions(Input{BTCAmount: 1, Direction: "buy", ExcludedCEXes: []string{"binance"}})
	if len(options.SpotTakerFees) != 4 || len(options.WithdrawalFees) != 4 {
		t.Fatalf("options = %d spot / %d withdrawal, want 4 each", len(options.SpotTakerFees), len(options.WithdrawalFees))
	}
	if options.SpotTakerFees[0].Exchange != "kraken" || options.SpotTakerFees[0].FeeBTC != 0.002 {
		t.Fatalf("first spot option = %+v, want kraken at 0.002 BTC", options.SpotTakerFees[0])
	}
	if !options.WithdrawalFees[0].Applicable || options.WithdrawalFees[0].FeeBTC != 0.0001 {
		t.Fatalf("first withdrawal option = %+v, want applicable kraken withdrawal", options.WithdrawalFees[0])
	}
	if !options.SpotTakerFees[1].Disabled || !options.WithdrawalFees[1].Disabled {
		t.Fatalf("excluded binance options must remain visible and disabled")
	}

	ask := eng.ManualOptions(Input{BTCAmount: 1, Direction: "sell"})
	if ask.WithdrawalFees[0].Applicable || ask.WithdrawalFees[0].FeeBTC != 0 {
		t.Fatalf("ask withdrawal option = %+v, want not applicable and zero", ask.WithdrawalFees[0])
	}
}

func TestBestHedgeParticipatesInComboAndThreadsCoupledFlag(t *testing.T) {
	// Equal taker/withdrawal across CEXes so the hedge dominates the choice.
	taker := map[string]float64{"kraken": 0.001, "binance": 0.001, "bitstamp": 0.001, "coinbase": 0.001}
	withdraw := map[string]float64{"kraken": 0.0001, "binance": 0.0001, "bitstamp": 0.0001, "coinbase": 0.0001}

	var sawCoupled bool
	hedge := func(cex string, btc, ref, tm float64, coupled bool, side string) hedgeCostDetails {
		sawCoupled = coupled
		// Emulate coupled venue pinning: coupled kraken gets an expensive pinned
		// venue; every other case shares a cheap venue.
		if coupled && cex == "kraken" {
			return hedgeCostDetails{feeBTC: 0.01, venue: "kraken_futures", side: side}
		}
		return hedgeCostDetails{feeBTC: 0.0001, venue: "binance_futures", side: side}
	}
	eng := testEngine(taker, withdraw, hedge)

	sel, ok := eng.Best(Input{BTCAmount: 1.0, Direction: "buy", CoupledHedge: true})
	if !ok {
		t.Fatal("expected a selection")
	}
	if !sawCoupled {
		t.Fatal("coupled flag was not threaded into the hedge estimator")
	}
	// kraken's pinned hedge (0.01) makes it lose to a cheap-venue CEX.
	if sel.Hedge.Venue != "binance_futures" {
		t.Fatalf("hedge venue = %q, want binance_futures", sel.Hedge.Venue)
	}
	// combo = taker 0.001 + withdrawal 0.0001 + hedge 0.0001 = 0.0012.
	eq(t, sel.HedgeFeeBTC, 0.0001)
	eq(t, sel.ComboBTC, 0.0012)
}

func TestBestZeroAmountReturnsFalse(t *testing.T) {
	eng := testEngine(map[string]float64{"kraken": 0.001}, map[string]float64{"kraken": 0.0001}, nil)
	if _, ok := eng.Best(Input{BTCAmount: 0, Direction: "buy"}); ok {
		t.Fatal("expected no selection for a zero amount")
	}
}

func TestCoupledHedgePinsPairedVenueMap(t *testing.T) {
	// Guards the coupling data: kraken/coinbase have paired perps; binance/bitstamp
	// do not (so they fall back to evaluating all venues).
	if cexToHedgeVenue["kraken"] == "" || cexToHedgeVenue["coinbase"] == "" {
		t.Fatal("kraken and coinbase must have paired hedge venues")
	}
	if _, ok := cexToHedgeVenue["binance"]; ok {
		t.Fatal("binance must not have a paired hedge venue")
	}
}
