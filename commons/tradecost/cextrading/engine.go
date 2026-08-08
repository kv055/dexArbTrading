package cextrading

import (
	"strings"

	"commons/exchangecredentials"
	"commons/hedge"
)

// Input carries everything the CEX-combo optimizer needs to price and rank the
// active exchanges for one offer.
type Input struct {
	BTCAmount          float64
	RefPriceEUR        float64  // CEX reference price; sizes the hedge position simulation (0 to skip)
	TransferMinutes    float64  // from txrouting; drives the hedge funding-cost window
	Direction          string   // DEX offer direction: "buy" (bid) or "sell" (ask)
	ExcludedCEXes      []string // legacy: excludes both spot and hedge
	ExcludedSpotCEXes  []string
	ExcludedHedgeCEXes []string
	CoupledHedge       bool // pin the hedge venue to the spot CEX where a pairing exists
	// AllowNoHedge lets Best return a spot+withdrawal-only selection when every
	// hedge venue is excluded. Used for hedgeless double-collateral pricing.
	AllowNoHedge bool
}

// Selection is the cheapest CEX fee combo for an Input. BTC fields are raw
// (unrounded) so the orchestrator can do exact total arithmetic; the Hedge field
// is already rounded for display.
type Selection struct {
	CEX              string
	TakerFeeFrac     float64
	TakerFeeBTC      float64 // raw spot taker fee in BTC
	WithdrawalFeeBTC float64 // raw on-chain withdrawal fee in BTC, side-gated (0 on ask)
	HedgeFeeBTC      float64 // raw perp open+close+funding sum in BTC
	Hedge            HedgeBreakdown
	Position         *HedgePositionDetail // hedge position sizing, reused by the capital requirement
	ComboBTC         float64              // taker + withdrawal + hedge (raw) — the minimized quantity
}

// ManualRouteOptions contains independently selectable CEX-side components
// for live-offer what-if analysis. The automatic optimizer still uses Best.
type ManualRouteOptions struct {
	SpotTakerFees  []SpotTakerFeeOption  `json:"spot_taker_fees"`
	WithdrawalFees []WithdrawalFeeOption `json:"withdrawal_fees"`
	HedgeCosts     []HedgeCostOption     `json:"hedge_costs"`
}

type SpotTakerFeeOption struct {
	Exchange       string  `json:"exchange"`
	FeePct         float64 `json:"fee_pct"`
	FeeBTC         float64 `json:"fee_btc"`
	Disabled       bool    `json:"disabled"`
	DisabledReason string  `json:"disabled_reason,omitempty"`
}

type WithdrawalFeeOption struct {
	Exchange   string  `json:"exchange"`
	FeePct     float64 `json:"fee_pct"`
	FeeBTC     float64 `json:"fee_btc"`
	Applicable bool    `json:"applicable"`
	Disabled   bool    `json:"disabled"`
}

type HedgeCostOption struct {
	Exchange       string         `json:"exchange"`
	Hedge          HedgeBreakdown `json:"hedge"`
	Disabled       bool           `json:"disabled"`
	DisabledReason string         `json:"disabled_reason,omitempty"`
}

// Engine ranks the active CEXes by their fee combo. The fee sources are function
// fields so the optimizer can be unit-tested offline with deterministic inputs.
type Engine struct {
	cexes        []string
	takerFrac    func(cex string) float64
	withdrawBTC  func(cex string) float64
	hedgeCost    func(cex string, btcAmount, refPriceEUR, transferMinutes float64, coupled bool, side string) hedgeCostDetails
	hedgeOptions func(btcAmount, refPriceEUR, transferMinutes float64, side string) []hedgeCostDetails
}

type cexFees struct {
	taker      *takerFeeFetcher
	withdrawal *withdrawalFeeFetcher
}

// NewEngine builds an Engine backed by database-credentialed live CEX fee
// fetchers (with static fallbacks) and the perp hedge estimator.
func NewEngine(providers ...exchangecredentials.Provider) *Engine {
	var provider exchangecredentials.Provider
	if len(providers) > 0 {
		provider = providers[0]
	}
	cexes := SupportedCEXes()
	fetchers := make(map[string]*cexFees, len(cexes))
	for _, c := range cexes {
		fetchers[c] = &cexFees{
			taker:      newTakerFeeFetcher(c, provider),
			withdrawal: newWithdrawalFeeFetcher(c, provider),
		}
	}
	cache := newCachedHedgeInput(feeTTL, provider)
	return &Engine{
		cexes: cexes,
		takerFrac: func(cex string) float64 {
			if f := fetchers[cex]; f != nil {
				return f.taker.get()
			}
			return takerFeeFallbacks[cex]
		},
		withdrawBTC: func(cex string) float64 {
			if f := fetchers[cex]; f != nil {
				return f.withdrawal.get()
			}
			return withdrawalFallbacks[cex]
		},
		hedgeCost: func(cex string, btcAmount, refPriceEUR, transferMinutes float64, coupled bool, side string) hedgeCostDetails {
			return estimateBestHedgeCost(cache, cex, btcAmount, refPriceEUR, transferMinutes, coupled, side)
		},
		hedgeOptions: func(btcAmount, refPriceEUR, transferMinutes float64, side string) []hedgeCostDetails {
			return estimateHedgeCosts(cache, []hedge.Venue{hedge.VenueKrakenFutures, hedge.VenueCoinbaseFutures, hedge.VenueBinanceFutures}, btcAmount, refPriceEUR, transferMinutes, side)
		},
	}
}

// ManualOptions returns all spot and withdrawal fees plus all hedge venues.
// Dashboard-excluded choices remain visible but disabled for operator context.
func (e *Engine) ManualOptions(in Input) ManualRouteOptions {
	options := ManualRouteOptions{}
	if e == nil || in.BTCAmount <= 0 {
		return options
	}
	excludedSpot, excludedHedge := exclusionSets(in)
	isBid := in.Direction != "sell"
	for _, cex := range e.cexes {
		_, disabled := excludedSpot[cex]
		takerBTC := in.BTCAmount * e.takerFrac(cex)
		spot := SpotTakerFeeOption{Exchange: cex, FeePct: round4(takerBTC / in.BTCAmount * 100), FeeBTC: round8(takerBTC), Disabled: disabled}
		if disabled {
			spot.DisabledReason = "CEX excluded in dashboard settings"
		}
		options.SpotTakerFees = append(options.SpotTakerFees, spot)
		withdrawBTC := 0.0
		if isBid {
			withdrawBTC = e.withdrawBTC(cex)
		}
		options.WithdrawalFees = append(options.WithdrawalFees, WithdrawalFeeOption{Exchange: cex, FeePct: round4(withdrawBTC / in.BTCAmount * 100), FeeBTC: round8(withdrawBTC), Applicable: isBid, Disabled: disabled})
	}
	if e.hedgeOptions == nil {
		return options
	}
	for _, h := range e.hedgeOptions(in.BTCAmount, in.RefPriceEUR, in.TransferMinutes, hedgeSideForDirection(in.Direction)) {
		exchange := hedgeExchange(h.venue)
		_, disabled := excludedHedge[exchange]
		option := HedgeCostOption{Exchange: exchange, Hedge: buildHedgeBreakdown(h, in.BTCAmount), Disabled: disabled}
		if disabled {
			option.DisabledReason = "CEX excluded in dashboard settings"
		}
		options.HedgeCosts = append(options.HedgeCosts, option)
	}
	return options
}

func hedgeExchange(venue string) string {
	switch venue {
	case string(hedge.VenueKrakenFutures):
		return "kraken"
	case string(hedge.VenueCoinbaseFutures):
		return "coinbase"
	case string(hedge.VenueBinanceFutures):
		return "binance"
	default:
		return venue
	}
}

// Best returns the cheapest CEX fee combo for the Input, skipping any excluded
// CEXes. The bool is false when no CEX is available (all excluded). Withdrawal fee
// applies on the bid side only (we withdraw from the CEX); on the ask side BTC is
// deposited to the CEX, so no withdrawal fee is charged.
func (e *Engine) Best(in Input) (Selection, bool) {
	if in.BTCAmount <= 0 {
		return Selection{}, false
	}
	excludedSpot, excludedHedge := exclusionSets(in)
	isBid := in.Direction != "sell"
	side := hedgeSideForDirection(in.Direction)

	best := Selection{}
	found := false
	for _, cex := range e.cexes {
		if _, skip := excludedSpot[cex]; skip {
			continue
		}
		takerFrac := e.takerFrac(cex)
		withdrawalBTC := 0.0
		if isBid {
			withdrawalBTC = e.withdrawBTC(cex)
		}
		hc, hedgeFound := e.bestEnabledHedge(in, cex, side, excludedHedge)
		if !hedgeFound {
			if !in.AllowNoHedge {
				continue
			}
			hc = hedgeCostDetails{}
		}
		takerBTC := in.BTCAmount * takerFrac
		comboBTC := takerBTC + withdrawalBTC + hc.feeBTC
		if !found || comboBTC < best.ComboBTC {
			best = Selection{
				CEX:              cex,
				TakerFeeFrac:     takerFrac,
				TakerFeeBTC:      takerBTC,
				WithdrawalFeeBTC: withdrawalBTC,
				HedgeFeeBTC:      hc.feeBTC,
				Hedge:            buildHedgeBreakdown(hc, in.BTCAmount),
				Position:         hc.position,
				ComboBTC:         comboBTC,
			}
			found = true
		}
	}
	return best, found
}

func exclusionSets(in Input) (map[string]struct{}, map[string]struct{}) {
	spot := make(map[string]struct{}, len(in.ExcludedCEXes)+len(in.ExcludedSpotCEXes))
	hedge := make(map[string]struct{}, len(in.ExcludedCEXes)+len(in.ExcludedHedgeCEXes))
	add := func(target map[string]struct{}, values []string) {
		for _, value := range values {
			value = strings.TrimSpace(strings.ToLower(value))
			if value != "" {
				target[value] = struct{}{}
			}
		}
	}
	add(spot, in.ExcludedCEXes)
	add(hedge, in.ExcludedCEXes)
	add(spot, in.ExcludedSpotCEXes)
	add(hedge, in.ExcludedHedgeCEXes)
	return spot, hedge
}

func (e *Engine) bestEnabledHedge(in Input, spotCEX, side string, excluded map[string]struct{}) (hedgeCostDetails, bool) {
	if in.CoupledHedge {
		if _, paired := cexToHedgeVenue[spotCEX]; paired {
			if _, disabled := excluded[spotCEX]; disabled {
				return hedgeCostDetails{}, false
			}
			return e.hedgeCost(spotCEX, in.BTCAmount, in.RefPriceEUR, in.TransferMinutes, true, side), true
		}
	}
	if len(excluded) == 0 {
		return e.hedgeCost(spotCEX, in.BTCAmount, in.RefPriceEUR, in.TransferMinutes, false, side), true
	}
	if e.hedgeOptions == nil {
		candidate := e.hedgeCost(spotCEX, in.BTCAmount, in.RefPriceEUR, in.TransferMinutes, false, side)
		if exchange := hedgeExchange(candidate.venue); exchange != "" {
			if _, disabled := excluded[exchange]; disabled {
				return hedgeCostDetails{}, false
			}
		}
		return candidate, true
	}
	best := hedgeCostDetails{}
	found := false
	for _, candidate := range e.hedgeOptions(in.BTCAmount, in.RefPriceEUR, in.TransferMinutes, side) {
		if _, disabled := excluded[hedgeExchange(candidate.venue)]; disabled {
			continue
		}
		if !found || candidate.feeBTC < best.feeBTC {
			best = candidate
			found = true
		}
	}
	return best, found
}
