package tradecost

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"commons/tradecost/cextrading"
	routing "commons/tradecost/txrouting"
)

func fixedCostEstimator(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC float64) *TradeCostEstimator {
	return NewFixedEstimatorForTesting(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC)
}

func fixedCostEstimatorWithHedge(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC, hedgeFeeBTC float64) *TradeCostEstimator {
	return NewFixedEstimatorWithHedgeForTesting(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC, hedgeFeeBTC)
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func TestTradeCostEstimatorCostPctUsesAllComponents(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)

	got := est.CostPct(0.1, "bisq")

	assertFloat(t, got, 0.55)
}

func TestTradeCostEstimatorBreakdownMatchesComponentSum(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)

	bd := est.Breakdown(0.1, "bisq", "bid")

	assertFloat(t, bd.BTCAmount, 0.1)
	assertFloat(t, bd.CexTakerFeeBTC, 0.00025)
	assertFloat(t, bd.CexTakerFeePct, 0.25)
	assertFloat(t, bd.WithdrawalFeeBTC, 0.00015)
	assertFloat(t, bd.WithdrawalFeePct, 0.15)
	// bisq bid routes on-chain direct (1 tx) into our Bisq wallet.
	assertFloat(t, bd.NetworkFeeBTC, 0.00005)
	assertFloat(t, bd.NetworkFeePct, 0.05)
	assertFloat(t, bd.DexFeeBTC, 0.0001)
	assertFloat(t, bd.DexFeePct, 0.1)
	assertFloat(t, bd.TotalCostBTC, 0.00055)
	assertFloat(t, bd.TotalCostPct, 0.55)
	if bd.RouteMethod != string(routing.MethodOnChainDirect) {
		t.Fatalf("route method = %q, want onchain_direct", bd.RouteMethod)
	}
	if bd.NetworkFeeMethod != string(routing.MethodOnChainDirect) {
		t.Fatalf("network method = %q, want onchain_direct", bd.NetworkFeeMethod)
	}
	if bd.NumNetworkTxs != 1 {
		t.Fatalf("network txs = %d, want 1", bd.NumNetworkTxs)
	}
	if bd.NetworkFeeConfidence != 0.875 {
		t.Fatalf("network confidence = %.12f, want 0.875", bd.NetworkFeeConfidence)
	}
}

func TestTradeCostEstimatorBreakdownIncludesHedgeCostOnBidSide(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "bid")

	assertFloat(t, bd.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, bd.Hedge.FeeSumPct, 0.2)
	if bd.Hedge.Side != "short" {
		t.Fatalf("hedge side = %q, want short", bd.Hedge.Side)
	}
	assertFloat(t, bd.TotalCostBTC, 0.00075)
	assertFloat(t, bd.TotalCostPct, 0.75)
	if bd.Hedge.Venue != "coinbase_futures" {
		t.Fatalf("hedge venue = %q, want coinbase_futures", bd.Hedge.Venue)
	}
	if bd.Hedge.TransferMinutes != 30 {
		t.Fatalf("hedge transfer minutes = %d, want 30", bd.Hedge.TransferMinutes)
	}
}

func TestBisq2BidUsesHubWalletTwoTxs(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq2", 0.0025, 0.00015, 0.0, 0.00005)

	bd := est.Breakdown(0.1, "bisq2", "bid")

	if bd.RouteMethod != string(routing.MethodOnChainHubWallet) {
		t.Fatalf("route method = %q, want onchain_hub", bd.RouteMethod)
	}
	if bd.NumNetworkTxs != 2 {
		t.Fatalf("network txs = %d, want 2", bd.NumNetworkTxs)
	}
	// Hub wallet routes two on-chain txs: 2 × 0.00005.
	assertFloat(t, bd.NetworkFeeBTC, 0.0001)
}

func TestBisq2AskSendsDirectToCex(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq2", 0.0025, 0.00015, 0.0, 0.00005)

	bd := est.Breakdown(0.1, "bisq2", "ask")

	if bd.RouteMethod != string(routing.MethodOnChainDirect) {
		t.Fatalf("ask route method = %q, want onchain_direct", bd.RouteMethod)
	}
	if bd.NumNetworkTxs != 1 {
		t.Fatalf("network txs = %d, want 1", bd.NumNetworkTxs)
	}
	// Ask side withdraws nothing from the CEX.
	assertFloat(t, bd.WithdrawalFeeBTC, 0)
}

func TestRobosatsRoutesViaSwapBothSides(t *testing.T) {
	est := fixedCostEstimator("kraken", "robosats", 0.0025, 0.00015, 0.00175, 0.00005)

	for _, dir := range []string{"bid", "ask"} {
		bd := est.Breakdown(0.01, "robosats", dir)
		if bd.RouteMethod != string(routing.MethodOnChainSwap) {
			t.Fatalf("%s route method = %q, want onchain_swap", dir, bd.RouteMethod)
		}
		if bd.Conversion.Strategy != routing.StrategySubmarineSwap {
			t.Fatalf("%s conversion strategy = %q, want submarine_swap", dir, bd.Conversion.Strategy)
		}
		// service 0.1% of 0.01 BTC + 150 sats miner.
		assertFloat(t, bd.Conversion.ServiceFeeBTC, 0.00001)
		assertFloat(t, bd.Conversion.ProviderMinerBTC, 0.0000015)
		assertFloat(t, bd.Conversion.FeeSumBTC, 0.0000115)
	}
}

func TestComputeCapitalRequirementBidBisq(t *testing.T) {
	pos := &HedgePositionDetail{RecommendedCollateral: 900}
	cap := computeCapitalRequirement(0.1, 45000, "bisq", true, pos, nil)

	assertFloat(t, cap.BTCAmount, 0.1)
	assertFloat(t, cap.BTCPrice, 45000)
	assertFloat(t, cap.SpotNotional, 4500)   // 0.1 * 45000
	assertFloat(t, cap.HedgeCollateral, 900) // reused from the hedge position
	assertFloat(t, cap.SecurityDepositPct, 0.15)
	assertFloat(t, cap.SecurityDepositBTC, 0.015) // 0.1 * 0.15, above the 0.001 floor
	assertFloat(t, cap.SecurityDepositFiat, 675)  // 0.015 * 45000
	assertFloat(t, cap.TotalFiat, 6075)           // 4500 + 900 + 675
	if cap.TradeMode != TradeModeHedgefullSingleCollateral {
		t.Fatalf("TradeMode = %q, want %q", cap.TradeMode, TradeModeHedgefullSingleCollateral)
	}
	if cap.SecurityDepositSide != "seller" {
		t.Fatalf("deposit side = %q, want seller", cap.SecurityDepositSide)
	}
	if !cap.DepositEstimated {
		t.Fatalf("DepositEstimated = false, want true")
	}
}

func TestComputeHedgelessCapitalDoubleCollateral(t *testing.T) {
	pos := &HedgePositionDetail{RecommendedCollateral: 900}
	hedgefull := computeCapitalRequirement(0.1, 45000, "bisq", true, pos, nil)
	cap := computeHedgelessCapital(hedgefull)

	if cap.TradeMode != TradeModeHedgelessDoubleCollateral {
		t.Fatalf("TradeMode = %q, want %q", cap.TradeMode, TradeModeHedgelessDoubleCollateral)
	}
	assertFloat(t, cap.HedgeCollateral, 0)
	assertFloat(t, cap.BTCLiquidityBTC, 0.115)    // 0.1 + 0.015
	assertFloat(t, cap.FiatCollateral, 4500)      // position only
	assertFloat(t, cap.SecurityDepositBTC, 0.015) // preserved from hedgefull
	// total = (pos+deposit)*price + pos*price = 0.115*45000 + 4500 = 5175 + 4500 = 9675
	assertFloat(t, cap.TotalFiat, 9675)
	assertFloat(t, cap.TotalFiat, 2*cap.SpotNotional+cap.SecurityDepositFiat)
}

func TestDeriveHedgelessDoubleCollateralStripsHedgeFees(t *testing.T) {
	bd := CostBreakdown{
		BTCAmount:          0.1,
		RouteCEX:           "kraken",
		RouteDEX:           "bisq",
		TotalCostBTC:       0.0012,
		TotalCostPct:       1.2,
		EstimatedProfitPct: 2.5,
		EstimatedProfitBTC: 0.0025,
		Hedge:              HedgeBreakdown{FeeSumBTC: 0.0002, FeeSumPct: 0.2, Venue: "kraken_futures"},
		Capital:            computeCapitalRequirement(0.1, 45000, "bisq", true, &HedgePositionDetail{RecommendedCollateral: 900}, nil),
	}
	out := DeriveHedgelessDoubleCollateral(bd)

	assertFloat(t, out.TotalCostBTC, 0.001) // 0.0012 - 0.0002
	assertFloat(t, out.TotalCostPct, 1.0)
	assertFloat(t, out.EstimatedProfitPct, 2.7) // 2.5 + 0.2
	assertFloat(t, out.EstimatedProfitBTC, 0.0027)
	assertFloat(t, out.Hedge.FeeSumBTC, 0)
	if out.Hedge.Venue != "" {
		t.Fatalf("hedge venue = %q, want empty", out.Hedge.Venue)
	}
	if out.Capital.TradeMode != TradeModeHedgelessDoubleCollateral {
		t.Fatalf("capital trade mode = %q", out.Capital.TradeMode)
	}
	assertFloat(t, out.Capital.TotalFiat, 9675)
}

func TestDeriveHedgelessDoubleCollateralBisqUsesTwoOnChainLegs(t *testing.T) {
	bd := CostBreakdown{
		BTCAmount: 0.1, RouteCEX: "kraken", RouteDEX: "bisq", RouteMethod: "onchain_direct",
		TradeDirection: "bid", NumNetworkTxs: 1, NetworkFeeBTC: 0.0001, NetworkFeePct: 0.1,
		TotalCostBTC: 0.001, TotalCostPct: 1, EstimatedProfitBTC: 0.001, EstimatedProfitPct: 1,
		Capital: CapitalRequirement{BTCAmount: 0.1},
	}
	out := DeriveHedgelessDoubleCollateral(bd)
	if out.RouteMethod != "onchain_hub" || out.NumNetworkTxs != 2 {
		t.Fatalf("route = %s/%d, want onchain_hub/2", out.RouteMethod, out.NumNetworkTxs)
	}
	if out.NetworkFeeBTC != 0.0002 || out.TotalCostBTC != 0.0011 {
		t.Fatalf("network/total = %.8f/%.8f", out.NetworkFeeBTC, out.TotalCostBTC)
	}
	if out.EstimatedProfitPct != 0.9 {
		t.Fatalf("estimated profit = %.4f, want 0.9", out.EstimatedProfitPct)
	}
}

func TestComputeCapitalRequirementAskSideUsesBuyerDeposit(t *testing.T) {
	cap := computeCapitalRequirement(0.1, 45000, "bisq", false, nil, nil)

	if cap.SecurityDepositSide != "buyer" {
		t.Fatalf("deposit side = %q, want buyer", cap.SecurityDepositSide)
	}
	assertFloat(t, cap.HedgeCollateral, 0) // nil position
}

func TestComputeCapitalRequirementAppliesDepositFloor(t *testing.T) {
	cap := computeCapitalRequirement(0.001, 45000, "bisq", true, nil, nil)

	// 0.001 * 0.15 = 0.00015 is below the 0.001 BTC floor.
	assertFloat(t, cap.SecurityDepositBTC, 0.001)
	assertFloat(t, cap.SecurityDepositFiat, 45) // 0.001 * 45000
}

func TestComputeCapitalRequirementUnknownRefPriceZeroesEUR(t *testing.T) {
	pos := &HedgePositionDetail{RecommendedCollateral: 900}
	cap := computeCapitalRequirement(0.1, 0, "bisq", true, pos, nil)

	assertFloat(t, cap.SpotNotional, 0)
	assertFloat(t, cap.SecurityDepositFiat, 0)
	assertFloat(t, cap.SecurityDepositBTC, 0.015) // BTC deposit still known without a price
	assertFloat(t, cap.HedgeCollateral, 900)
	assertFloat(t, cap.TotalFiat, 900) // only the hedge collateral, which is already EUR
}

func TestComputeCapitalRequirementNonDepositDEX(t *testing.T) {
	cap := computeCapitalRequirement(0.1, 45000, "robosats", true, nil, nil)

	assertFloat(t, cap.SecurityDepositPct, 0)
	assertFloat(t, cap.SecurityDepositBTC, 0)
	assertFloat(t, cap.SecurityDepositFiat, 0)
	if cap.SecurityDepositSide != "" {
		t.Fatalf("deposit side = %q, want empty", cap.SecurityDepositSide)
	}
	if cap.DepositEstimated {
		t.Fatalf("DepositEstimated = true, want false for non-deposit DEX")
	}
}

func TestComputeCapitalRequirementRealDepositBeatsEstimate(t *testing.T) {
	realPct := 0.18 // measured per-offer deposit, different from the 15% bisq estimate
	cap := computeCapitalRequirement(0.1, 45000, "bisq", true, nil, &realPct)

	assertFloat(t, cap.SecurityDepositPct, 0.18)
	assertFloat(t, cap.SecurityDepositBTC, 0.018) // 0.1 * 0.18, no estimate floor applied
	assertFloat(t, cap.SecurityDepositFiat, 810)  // 0.018 * 45000
	if cap.SecurityDepositSide != "seller" {
		t.Fatalf("deposit side = %q, want seller", cap.SecurityDepositSide)
	}
	if cap.DepositEstimated {
		t.Fatalf("DepositEstimated = true, want false for a real measured deposit")
	}
}

func TestComputeCapitalRequirementRealDepositAppliesToRoboSats(t *testing.T) {
	// robosats has no entry in dexSecurityDepositPct (no flat estimate), but a
	// real measured bond should still produce a deposit.
	realPct := 0.03
	cap := computeCapitalRequirement(0.1, 45000, "robosats", false, nil, &realPct)

	assertFloat(t, cap.SecurityDepositPct, 0.03)
	assertFloat(t, cap.SecurityDepositBTC, 0.003) // 0.1 * 0.03
	if cap.SecurityDepositSide != "buyer" {
		t.Fatalf("deposit side = %q, want buyer", cap.SecurityDepositSide)
	}
	if cap.DepositEstimated {
		t.Fatalf("DepositEstimated = true, want false")
	}
}

// fixedCostEstimatorWithHedgePosition mirrors fixedCostEstimatorWithHedge but
// also attaches a position whose recommended collateral scales with the trade
// notional, so capped vs full breakdowns can be compared.
func fixedCostEstimatorWithHedgePosition(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC, hedgeFeeBTC float64) *TradeCostEstimator {
	est := fixedCostEstimator(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC)
	est.cexCostOverride = func(in cextrading.Input) (cextrading.Selection, bool) {
		takerBTC := in.BTCAmount * takerFeeFrac
		withdrawalBTC := 0.0
		if in.Direction != "ask" {
			withdrawalBTC = withdrawalFeeBTC
		}
		side := "short"
		if in.Direction == "ask" {
			side = "long"
		}
		pos := &cextrading.HedgePositionDetail{RecommendedCollateral: in.BTCAmount * in.RefPriceEUR * 0.1}
		feePct := 0.0
		if in.BTCAmount > 0 {
			feePct = round4(hedgeFeeBTC / in.BTCAmount * 100)
		}
		h := cextrading.HedgeBreakdown{
			FeeSumBTC:       round8(hedgeFeeBTC),
			FeeSumPct:       feePct,
			Side:            side,
			Venue:           "coinbase_futures",
			Instrument:      "BTC-PERP",
			TransferMinutes: int(math.Round(in.TransferMinutes)),
			Position:        pos,
		}
		return cextrading.Selection{
			CEX:              exchange,
			TakerFeeFrac:     takerFeeFrac,
			TakerFeeBTC:      takerBTC,
			WithdrawalFeeBTC: withdrawalBTC,
			HedgeFeeBTC:      h.FeeSumBTC,
			Hedge:            h,
			Position:         pos,
			ComboBTC:         takerBTC + withdrawalBTC + h.FeeSumBTC,
		}, true
	}
	return est
}

func TestBreakdownCapitalRespectsFullAndCappedVolume(t *testing.T) {
	est := fixedCostEstimatorWithHedgePosition("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	full := est.breakdown(0.1, 45000, "bisq", "bid", 0, "", nil, nil, false, nil)
	capped := est.breakdown(0.04, 45000, "bisq", "bid", 0, "", nil, nil, false, nil)

	// Full size.
	assertFloat(t, full.Capital.SpotNotional, 4500)        // 0.1 * 45000
	assertFloat(t, full.Capital.HedgeCollateral, 450)      // 0.1 * 45000 * 0.1
	assertFloat(t, full.Capital.SecurityDepositBTC, 0.015) // 0.1 * 0.15
	assertFloat(t, full.Capital.TotalFiat, 5625)           // 4500 + 450 + 675

	// Capped size scales every component down with the smaller btc amount.
	assertFloat(t, capped.Capital.SpotNotional, 1800)        // 0.04 * 45000
	assertFloat(t, capped.Capital.HedgeCollateral, 180)      // 0.04 * 45000 * 0.1
	assertFloat(t, capped.Capital.SecurityDepositBTC, 0.006) // 0.04 * 0.15
	assertFloat(t, capped.Capital.TotalFiat, 2250)           // 1800 + 180 + 270

	if capped.Capital.TotalFiat >= full.Capital.TotalFiat {
		t.Fatalf("capped total %.2f should be below full total %.2f", capped.Capital.TotalFiat, full.Capital.TotalFiat)
	}
}

func TestBreakdownRealDepositPctScalesWithCappedVolume(t *testing.T) {
	est := fixedCostEstimatorWithHedgePosition("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)
	realPct := 0.18

	full := est.breakdown(0.1, 45000, "bisq", "bid", 0, "", nil, nil, false, &realPct)
	capped := est.breakdown(0.04, 45000, "bisq", "bid", 0, "", nil, nil, false, &realPct)

	// The real pct is fixed, but since SecurityDepositBTC = btcAmount * pct, the
	// absolute deposit still scales down correctly with the smaller capped size.
	assertFloat(t, full.Capital.SecurityDepositPct, 0.18)
	assertFloat(t, full.Capital.SecurityDepositBTC, 0.018) // 0.1 * 0.18
	if full.Capital.DepositEstimated {
		t.Fatalf("full DepositEstimated = true, want false")
	}
	assertFloat(t, capped.Capital.SecurityDepositPct, 0.18)
	assertFloat(t, capped.Capital.SecurityDepositBTC, 0.0072) // 0.04 * 0.18
	if capped.Capital.DepositEstimated {
		t.Fatalf("capped DepositEstimated = true, want false")
	}
}

func TestAbortCostFromBreakdownIncludesBidUnwindComponents(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "bid")
	abort := est.AbortCostFromBreakdown(bd)

	assertFloat(t, abort.BTCAmount, 0.1)
	assertFloat(t, abort.CexBuyTaker.BTC, 0.00025)
	assertFloat(t, abort.CexSellTaker.BTC, 0.00025)
	assertFloat(t, abort.Withdrawal.BTC, 0.00015)
	assertFloat(t, abort.Network.BTC, 0.00005) // bisq bid is a single on-chain tx
	assertFloat(t, abort.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, abort.Total.BTC, 0.0009)
	assertFloat(t, abort.Total.Pct, 0.9)
	if abort.TradeDirection != "bid" {
		t.Fatalf("trade direction = %q, want bid", abort.TradeDirection)
	}
	if abort.NumNetworkTxs != 2 {
		t.Fatalf("network txs = %d, want 2", abort.NumNetworkTxs)
	}
}

func TestTradeCostEstimatorBreakdownIncludesLongHedgeCostOnAskSide(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "ask")

	assertFloat(t, bd.WithdrawalFeeBTC, 0)
	assertFloat(t, bd.NetworkFeeBTC, 0.00005)
	assertFloat(t, bd.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, bd.Hedge.FeeSumPct, 0.2)
	if bd.Hedge.Side != "long" {
		t.Fatalf("hedge side = %q, want long", bd.Hedge.Side)
	}
	assertFloat(t, bd.TotalCostBTC, 0.0006)
	assertFloat(t, bd.TotalCostPct, 0.6)
}

func TestAbortCostFromBreakdownAskSideOnlyIncludesTwoBTCTransfers(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "ask")
	abort := est.AbortCostFromBreakdown(bd)

	assertFloat(t, abort.CexBuyTaker.BTC, 0)
	assertFloat(t, abort.CexSellTaker.BTC, 0)
	assertFloat(t, abort.Withdrawal.BTC, 0)
	assertFloat(t, abort.Hedge.FeeSumBTC, 0)
	assertFloat(t, abort.Network.BTC, 0.0001)
	assertFloat(t, abort.Total.BTC, 0.0001)
	assertFloat(t, abort.Total.Pct, 0.1)
	if abort.TradeDirection != "ask" {
		t.Fatalf("trade direction = %q, want ask", abort.TradeDirection)
	}
	if abort.NumNetworkTxs != 2 {
		t.Fatalf("network txs = %d, want 2", abort.NumNetworkTxs)
	}
}

func TestTradeCostEstimatorDoesNotProjectFundingWithoutEventDuration(t *testing.T) {
	est := NewFixedEstimatorWithHedgeRatesForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.001, 0.002, 0.003)

	bid := est.Breakdown(0.1, "bisq", "bid")
	ask := est.Breakdown(0.1, "bisq", "ask")

	assertFloat(t, bid.Hedge.FundingFeeBTC, 0)
	assertFloat(t, bid.Hedge.FeeSumBTC, 0.0003)
	assertFloat(t, ask.Hedge.FundingFeeBTC, 0)
	assertFloat(t, ask.Hedge.FeeSumBTC, 0.0003)
	if ask.Hedge.Side != "long" {
		t.Fatalf("hedge side = %q, want long", ask.Hedge.Side)
	}
}

func TestAbortCostFromBreakdownRecalculatesPercentForCappedSize(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	abort := est.AbortCostFromBreakdown(est.Breakdown(0.05, "bisq", "bid"))

	assertFloat(t, abort.BTCAmount, 0.05)
	assertFloat(t, abort.CexBuyTaker.BTC, 0.000125)
	assertFloat(t, abort.CexSellTaker.BTC, 0.000125)
	assertFloat(t, abort.Withdrawal.BTC, 0.00015)
	assertFloat(t, abort.Network.BTC, 0.00005) // bisq bid is a single on-chain tx
	assertFloat(t, abort.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, abort.Total.BTC, 0.00065)
	assertFloat(t, abort.Total.Pct, 1.3)
}

func TestTradeCostEstimatorBreakdownRoundsBTCAndPercentFields(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.00123456789, 0.000000004, 0.00098765432, 0.000000006)

	bd := est.Breakdown(0.123456789, "bisq", "bid")

	assertFloat(t, bd.BTCAmount, 0.12345679)
	assertFloat(t, bd.CexTakerFeeBTC, 0.00015242)
	assertFloat(t, bd.WithdrawalFeeBTC, 0)
	assertFloat(t, bd.NetworkFeeBTC, 0.00000001)
	assertFloat(t, bd.DexFeeBTC, 0.00012193)
	assertFloat(t, bd.CexTakerFeePct, 0.1235)
	assertFloat(t, bd.DexFeePct, 0.0988)
	assertFloat(t, bd.TotalCostPct, 0.2222)
}

func TestTradeCostEstimatorZeroAmountReturnsEmptyCost(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005)

	assertFloat(t, est.CostPct(0, "bisq"), 0)
	if bd := est.Breakdown(0, "bisq", "bid"); !reflect.DeepEqual(bd, CostBreakdown{}) {
		t.Fatalf("zero amount breakdown = %#v, want empty breakdown", bd)
	}
}

func TestCachedFeeUsesFallbackThenKeepsCachedValueOnFailure(t *testing.T) {
	cache := newCachedFee(time.Hour)
	refreshErr := errors.New("boom")

	got := cache.get(0.123, func() (float64, error) {
		return 0, refreshErr
	}, "test fee")
	assertFloat(t, got, 0.123)

	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	got = cache.get(0.123, func() (float64, error) {
		return 0.456, nil
	}, "test fee")
	assertFloat(t, got, 0.456)

	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	got = cache.get(0.123, func() (float64, error) {
		return 0, refreshErr
	}, "test fee")
	assertFloat(t, got, 0.456)
}

func TestCachedFeeSharesConcurrentRefresh(t *testing.T) {
	cache := newCachedFee(time.Hour)
	start := make(chan struct{})
	var calls int32
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got := cache.get(0.123, func() (float64, error) {
				atomic.AddInt32(&calls, 1)
				time.Sleep(20 * time.Millisecond)
				return 0.789, nil
			}, "test fee")
			assertFloat(t, got, 0.789)
		}()
	}

	close(start)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

// TestWarmDexTakerFeesNilEstimator guards the startup pre-warm wiring: a nil
// estimator must be a no-op, never a panic. Live-source fetch behavior itself
// needs Tor/relays and is verified out of band (see the capped-pricing speed
// probe), not in unit tests.
func TestWarmDexTakerFeesNilEstimator(t *testing.T) {
	var e *TradeCostEstimator
	e.WarmDexTakerFees()
	e.RunDexTakerFeeRefreshLoop(nil)
}
