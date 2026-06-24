package cextrading

import (
	"fmt"
	"math"
	"sync"
	"time"

	"commons/hedge"
)

func round8(v float64) float64 { return math.Round(v*1e8) / 1e8 }
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
func round2(v float64) float64 { return math.Round(v*1e2) / 1e2 }

// ---------------------------------------------------------------------------
// Hedge perp-input cache (live funding/fee inputs per venue)
// ---------------------------------------------------------------------------

type cachedHedgeInput struct {
	mu       sync.Mutex
	values   map[hedge.Venue]hedge.LivePerpInputs
	fetched  map[hedge.Venue]time.Time
	inflight map[hedge.Venue]chan struct{}
	ttl      time.Duration
}

func newCachedHedgeInput(ttl time.Duration) *cachedHedgeInput {
	return &cachedHedgeInput{
		values:   map[hedge.Venue]hedge.LivePerpInputs{},
		fetched:  map[hedge.Venue]time.Time{},
		inflight: map[hedge.Venue]chan struct{}{},
		ttl:      ttl,
	}
}

func (c *cachedHedgeInput) get(venue hedge.Venue) (hedge.LivePerpInputs, error) {
	c.mu.Lock()
	if fetchedAt, ok := c.fetched[venue]; ok && time.Since(fetchedAt) < c.ttl {
		value := c.values[venue]
		c.mu.Unlock()
		return value, nil
	}
	if ch, ok := c.inflight[venue]; ok {
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
		value, ok := c.values[venue]
		c.mu.Unlock()
		if ok {
			return value, nil
		}
		return hedge.LivePerpInputs{}, fmt.Errorf("hedge inputs unavailable for %s", venue)
	}
	ch := make(chan struct{})
	c.inflight[venue] = ch
	c.mu.Unlock()

	inputs, err := hedge.FetchDefaultVenueInputs(venue)
	if err != nil {
		inputs, err = hedge.FallbackVenueInputs(venue)
	}

	c.mu.Lock()
	if err == nil {
		c.values[venue] = inputs
		c.fetched[venue] = time.Now()
	}
	close(ch)
	delete(c.inflight, venue)
	c.mu.Unlock()
	return inputs, err
}

var hedgeInputCache = newCachedHedgeInput(feeTTL)

// ---------------------------------------------------------------------------
// Hedge cost model
// ---------------------------------------------------------------------------

type hedgeCostDetails struct {
	feeBTC          float64
	feePct          float64
	openFeeBTC      float64
	openFeePct      float64
	closeFeeBTC     float64
	closeFeePct     float64
	fundingFeeBTC   float64
	fundingFeePct   float64
	side            string
	venue           string
	instrument      string
	transferMinutes float64
	position        *HedgePositionDetail
}

// HedgeSideForDirection maps a DEX offer direction to the perp hedge side: a bid
// (we buy spot, sell on the DEX) is hedged short; an ask is hedged long.
func HedgeSideForDirection(direction string) string {
	return hedgeSideForDirection(direction)
}

func hedgeSideForDirection(direction string) string {
	if direction == "sell" {
		return "long"
	}
	return "short"
}

func nonNegativeFundingCostRate(fundingRate float64, side string) float64 {
	costRate := fundingRate
	if side == "long" {
		costRate = -fundingRate
	}
	return math.Max(costRate, 0)
}

// cexToHedgeVenue maps a spot CEX name to its paired perp hedge venue.
// Used in coupled mode to ensure the same exchange is used for both legs.
var cexToHedgeVenue = map[string]hedge.Venue{
	"kraken":   hedge.VenueKrakenFutures,
	"coinbase": hedge.VenueCoinbaseFutures,
}

// estimateBestHedgeCost picks the cheapest perp hedge for the given spot CEX and
// side. In coupled mode the hedge venue is pinned to the spot CEX where a pairing
// exists (kraken→krakenFutures, coinbase→coinbaseFutures); otherwise — and for CEXes
// with no paired perp (binance/bitstamp) — all venues are evaluated and the cheapest
// is chosen.
func estimateBestHedgeCost(cex string, btcAmount float64, refPriceEUR float64, transferMinutes float64, coupledHedge bool, side string) hedgeCostDetails {
	if btcAmount <= 0 {
		return hedgeCostDetails{}
	}

	allCandidates := []hedge.Venue{hedge.VenueKrakenFutures, hedge.VenueCoinbaseFutures, hedge.VenueBinanceFutures}
	candidates := allCandidates
	if coupledHedge {
		if paired, ok := cexToHedgeVenue[cex]; ok {
			candidates = []hedge.Venue{paired}
		}
	}
	best := hedgeCostDetails{}
	found := false

	for _, venue := range candidates {
		profile, err := hedge.DefaultVenueProfile(venue)
		if err != nil {
			continue
		}

		inputs, err := hedgeInputCache.get(venue)
		if err != nil {
			continue
		}

		cfg := hedge.Config{TransferMinutes: transferMinutes}
		cfg = hedge.ApplyVenueProfile(cfg, profile)
		cfg = hedge.ApplyLivePerpInputs(cfg, inputs)
		cfg.FundingCostRate = nonNegativeFundingCostRate(cfg.FundingCostRate, side)
		openFeeBTC := btcAmount * cfg.PerpOpenFeeRate
		closeFeeBTC := btcAmount * cfg.PerpCloseFeeRate
		fundingFeeBTC := btcAmount * cfg.FundingCostRate
		feeBTC := openFeeBTC + closeFeeBTC + fundingFeeBTC
		rate := cfg.PerpOpenFeeRate + cfg.PerpCloseFeeRate + cfg.FundingCostRate

		var position *HedgePositionDetail
		if refPriceEUR > 0 {
			sim := hedge.SimulatePositionWithSide(cfg, btcAmount, refPriceEUR, side)
			position = &HedgePositionDetail{
				Side:                         sim.Side,
				HedgeRatio:                   round4(sim.HedgeRatio),
				LeverageUsed:                 round4(sim.LeverageUsed),
				HedgeNotionalEUR:             round2(sim.HedgeNotionalEUR),
				InitialMarginEUR:             round2(sim.InitialMarginEUR),
				RecommendedCollateralEUR:     round2(sim.RecommendedCollateralEUR),
				StressMovePct:                round4(sim.StressMovePct),
				StressLossEUR:                round2(sim.StressLossEUR),
				MaintenanceRequirementEUR:    round2(sim.MaintenanceRequirementEUR),
				FreeCollateralAfterStressEUR: round2(sim.FreeCollateralAfterStressEUR),
				LiquidationMovePct:           round4(sim.LiquidationMovePct),
				BTCPriceEUR:                  round2(sim.BTCPriceEUR),
			}
		}

		details := hedgeCostDetails{
			feeBTC:          feeBTC,
			feePct:          rate * 100,
			openFeeBTC:      openFeeBTC,
			openFeePct:      cfg.PerpOpenFeeRate * 100,
			closeFeeBTC:     closeFeeBTC,
			closeFeePct:     cfg.PerpCloseFeeRate * 100,
			fundingFeeBTC:   fundingFeeBTC,
			fundingFeePct:   cfg.FundingCostRate * 100,
			side:            side,
			venue:           inputs.Venue,
			instrument:      inputs.Instrument,
			transferMinutes: transferMinutes,
			position:        position,
		}
		if !found || details.feeBTC < best.feeBTC {
			best = details
			found = true
		}
	}

	if !found {
		return hedgeCostDetails{}
	}
	return best
}

// ---------------------------------------------------------------------------
// Hedge result types (owned here; tradecost aliases them for API stability)
// ---------------------------------------------------------------------------

// HedgeBreakdown is the perp-hedge component of an arbitrage cost breakdown.
type HedgeBreakdown struct {
	OpenFeePct      float64              `json:"open_fee_pct"`     // perp opening fee as % of trade
	OpenFeeBTC      float64              `json:"open_fee_btc"`     // perp opening fee in BTC
	CloseFeePct     float64              `json:"close_fee_pct"`    // perp closing fee as % of trade
	CloseFeeBTC     float64              `json:"close_fee_btc"`    // perp closing fee in BTC
	FundingFeePct   float64              `json:"funding_fee_pct"`  // perp funding cost over transfer window as % of trade
	FundingFeeBTC   float64              `json:"funding_fee_btc"`  // perp funding cost over transfer window in BTC
	FeeSumPct       float64              `json:"fee_sum_pct"`      // perp open+close+funding as % of trade
	FeeSumBTC       float64              `json:"fee_sum_btc"`      // perp open+close+funding in BTC
	Side            string               `json:"side"`             // "short" for bid-side hedges, "long" for ask-side hedges
	Venue           string               `json:"venue"`            // chosen hedge venue
	Instrument      string               `json:"instrument"`       // chosen perp instrument
	TransferMinutes int                  `json:"transfer_minutes"` // transfer-time input used for hedge costing
	Position        *HedgePositionDetail `json:"position"`         // position sizing simulation; nil when unavailable (e.g. missing ref price)
}

// HedgePositionDetail is the per-offer simulation of how the perp hedge
// would be executed: hedge notional, leverage, collateral, and liquidation
// headroom. All EUR amounts are rounded to 2 decimals; rates are unrounded.
type HedgePositionDetail struct {
	Side                         string  `json:"side"`                             // "short" or "long"
	HedgeRatio                   float64 `json:"hedge_ratio"`                      // fraction of spot transfer that is hedged (1.0 = full)
	LeverageUsed                 float64 `json:"leverage_used"`                    // leverage assumed for sizing (venue max)
	HedgeNotionalEUR             float64 `json:"hedge_notional_eur"`               // perp notional in EUR
	InitialMarginEUR             float64 `json:"initial_margin_eur"`               // required initial margin = notional / leverage
	RecommendedCollateralEUR     float64 `json:"recommended_collateral_eur"`       // collateral to post so the position survives the stress move
	StressMovePct                float64 `json:"stress_move_pct"`                  // adverse BTC move assumed in the stress scenario (e.g. 0.10)
	StressLossEUR                float64 `json:"stress_loss_eur"`                  // unrealized loss on the short under the stress move
	MaintenanceRequirementEUR    float64 `json:"maintenance_requirement_eur"`      // venue maintenance margin on the notional
	FreeCollateralAfterStressEUR float64 `json:"free_collateral_after_stress_eur"` // equity left above maintenance after the stress move
	LiquidationMovePct           float64 `json:"liquidation_move_pct"`             // adverse BTC move that would liquidate the position at the chosen leverage
	BTCPriceEUR                  float64 `json:"btc_price_eur"`                    // CEX ref price used to size the notional
	// Historical is true when this position was reconstructed from persisted
	// per-cycle averages (order_lifecycle) for a closed offer, rather than
	// freshly simulated for a live one. Only Side, HedgeRatio, LeverageUsed,
	// HedgeNotionalEUR, RecommendedCollateralEUR, and BTCPriceEUR are
	// populated in that case — the other fields were never persisted and are
	// left zero, so consumers should hide them rather than display a fake 0.
	Historical bool `json:"historical,omitempty"`
}

// buildHedgeBreakdown converts the internal hedge cost detail into the rounded,
// display-ready HedgeBreakdown.
func buildHedgeBreakdown(hc hedgeCostDetails, btcAmount float64) HedgeBreakdown {
	pct := func(btc float64) float64 {
		if btcAmount <= 0 || btc == 0 {
			return 0
		}
		return round4(btc / btcAmount * 100)
	}
	return HedgeBreakdown{
		OpenFeePct:      pct(hc.openFeeBTC),
		OpenFeeBTC:      round8(hc.openFeeBTC),
		CloseFeePct:     pct(hc.closeFeeBTC),
		CloseFeeBTC:     round8(hc.closeFeeBTC),
		FundingFeePct:   pct(hc.fundingFeeBTC),
		FundingFeeBTC:   round8(hc.fundingFeeBTC),
		FeeSumPct:       pct(hc.feeBTC),
		FeeSumBTC:       round8(hc.feeBTC),
		Side:            hc.side,
		Venue:           hc.venue,
		Instrument:      hc.instrument,
		TransferMinutes: int(math.Round(hc.transferMinutes)),
		Position:        hc.position,
	}
}
