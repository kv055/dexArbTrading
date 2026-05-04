package tradecost

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seededCachedFee(value float64) *cachedFee {
	return &cachedFee{
		value:     value,
		fetchedAt: time.Now(),
		ttl:       feeTTL,
	}
}

func fixedCostEstimator(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC float64) *TradeCostEstimator {
	withdrawal := newWithdrawalFeeFetcher(exchange)
	withdrawal.caches[MethodOnchain] = seededCachedFee(withdrawalFeeBTC)
	withdrawal.caches[MethodLightning] = seededCachedFee(withdrawalFeeBTC)

	return &TradeCostEstimator{
		exchange:   exchange,
		taker:      &takerFeeFetcher{exchange: exchange, cache: seededCachedFee(takerFeeFrac)},
		withdrawal: withdrawal,
		dexFees: map[string]*cachedFee{
			dexExchange: seededCachedFee(dexFeeFrac),
		},
		cexProfile: cexProfileFor(exchange),
		networkFeeOverride: func(method WithdrawalMethod, btcAmount float64) networkFeeDetails {
			return networkFeeDetails{
				feeBTC:     networkFeeBTC,
				method:     method,
				tier:       "standard",
				confidence: 0.875,
				detail: &BTCNetworkFeeDetail{
					SelectedFullConfirmationMinutes: 30,
				},
			}
		},
		hedgeCostOverride: func(btcAmount float64, refPriceEUR float64, transferMinutes float64, side string) hedgeCostDetails {
			return hedgeCostDetails{}
		},
	}
}

func fixedCostEstimatorWithHedge(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC, hedgeFeeBTC float64) *TradeCostEstimator {
	est := fixedCostEstimator(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC)
	est.hedgeCostOverride = func(btcAmount float64, refPriceEUR float64, transferMinutes float64, side string) hedgeCostDetails {
		return hedgeCostDetails{
			feeBTC:          hedgeFeeBTC,
			feePct:          hedgeFeeBTC / btcAmount * 100,
			side:            side,
			venue:           "coinbase_futures",
			instrument:      "BTC-PERP",
			transferMinutes: transferMinutes,
		}
	}
	return est
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

	bd := est.Breakdown(0.1, "bisq", "buy")

	assertFloat(t, bd.BTCAmount, 0.1)
	assertFloat(t, bd.CexTakerFeeBTC, 0.00025)
	assertFloat(t, bd.CexTakerFeePct, 0.25)
	assertFloat(t, bd.WithdrawalFeeBTC, 0.00015)
	assertFloat(t, bd.WithdrawalFeePct, 0.15)
	assertFloat(t, bd.NetworkFeeBTC, 0.0001) // 2 txs × 0.00005
	assertFloat(t, bd.NetworkFeePct, 0.1)
	assertFloat(t, bd.DexFeeBTC, 0.0001)
	assertFloat(t, bd.DexFeePct, 0.1)
	assertFloat(t, bd.TotalCostBTC, 0.0006)
	assertFloat(t, bd.TotalCostPct, 0.6)
	if bd.NetworkFeeMethod != "onchain" {
		t.Fatalf("network method = %q, want onchain", bd.NetworkFeeMethod)
	}
	if bd.NetworkFeeConfidence != 0.875 {
		t.Fatalf("network confidence = %.12f, want 0.875", bd.NetworkFeeConfidence)
	}
}

func TestTradeCostEstimatorBreakdownIncludesHedgeCostOnBidSide(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "buy")

	assertFloat(t, bd.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, bd.Hedge.FeeSumPct, 0.2)
	if bd.Hedge.Side != "short" {
		t.Fatalf("hedge side = %q, want short", bd.Hedge.Side)
	}
	assertFloat(t, bd.TotalCostBTC, 0.0008)
	assertFloat(t, bd.TotalCostPct, 0.8)
	if bd.Hedge.Venue != "coinbase_futures" {
		t.Fatalf("hedge venue = %q, want coinbase_futures", bd.Hedge.Venue)
	}
	if bd.Hedge.TransferMinutes != 60 {
		t.Fatalf("hedge transfer minutes = %d, want 60", bd.Hedge.TransferMinutes)
	}
}

func TestAbortCostFromBreakdownIncludesBidUnwindComponents(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "buy")
	abort := est.AbortCostFromBreakdown(bd)

	assertFloat(t, abort.BTCAmount, 0.1)
	assertFloat(t, abort.CexBuyTaker.BTC, 0.00025)
	assertFloat(t, abort.CexSellTaker.BTC, 0.00025)
	assertFloat(t, abort.Withdrawal.BTC, 0.00015)
	assertFloat(t, abort.Network.BTC, 0.0001)
	assertFloat(t, abort.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, abort.Total.BTC, 0.00095)
	assertFloat(t, abort.Total.Pct, 0.95)
	if abort.TradeDirection != "bid" {
		t.Fatalf("trade direction = %q, want bid", abort.TradeDirection)
	}
	if abort.NumNetworkTxs != 2 {
		t.Fatalf("network txs = %d, want 2", abort.NumNetworkTxs)
	}
}

func TestTradeCostEstimatorBreakdownIncludesLongHedgeCostOnAskSide(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	bd := est.Breakdown(0.1, "bisq", "sell")

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

	bd := est.Breakdown(0.1, "bisq", "sell")
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

func TestTradeCostEstimatorAskSideLongHedgeClampsFundingCredit(t *testing.T) {
	est := NewFixedEstimatorWithHedgeRatesForTesting("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.001, 0.002, 0.003)

	bid := est.Breakdown(0.1, "bisq", "buy")
	ask := est.Breakdown(0.1, "bisq", "sell")

	assertFloat(t, bid.Hedge.FundingFeeBTC, 0.0003)
	assertFloat(t, bid.Hedge.FeeSumBTC, 0.0006)
	assertFloat(t, ask.Hedge.FundingFeeBTC, 0)
	assertFloat(t, ask.Hedge.FeeSumBTC, 0.0003)
	if ask.Hedge.Side != "long" {
		t.Fatalf("hedge side = %q, want long", ask.Hedge.Side)
	}
}

func TestAbortCostFromBreakdownRecalculatesPercentForCappedSize(t *testing.T) {
	est := fixedCostEstimatorWithHedge("kraken", "bisq", 0.0025, 0.00015, 0.001, 0.00005, 0.0002)

	abort := est.AbortCostFromBreakdown(est.Breakdown(0.05, "bisq", "buy"))

	assertFloat(t, abort.BTCAmount, 0.05)
	assertFloat(t, abort.CexBuyTaker.BTC, 0.000125)
	assertFloat(t, abort.CexSellTaker.BTC, 0.000125)
	assertFloat(t, abort.Withdrawal.BTC, 0.00015)
	assertFloat(t, abort.Network.BTC, 0.0001)
	assertFloat(t, abort.Hedge.FeeSumBTC, 0.0002)
	assertFloat(t, abort.Total.BTC, 0.0007)
	assertFloat(t, abort.Total.Pct, 1.4)
}

func TestTradeCostEstimatorBreakdownRoundsBTCAndPercentFields(t *testing.T) {
	est := fixedCostEstimator("kraken", "bisq", 0.00123456789, 0.000000004, 0.00098765432, 0.000000006)

	bd := est.Breakdown(0.123456789, "bisq", "buy")

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
	if bd := est.Breakdown(0, "bisq", "buy"); bd != (CostBreakdown{}) {
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
