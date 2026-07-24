package cextrading

import (
	"strings"

	"commons/exchangecredentials"
)

// Input carries everything the CEX-combo optimizer needs to price and rank the
// active exchanges for one offer.
type Input struct {
	BTCAmount       float64
	RefPriceEUR     float64 // CEX reference price; sizes the hedge position simulation (0 to skip)
	TransferMinutes float64 // from txrouting; drives the hedge funding-cost window
	Direction       string  // DEX offer direction: "buy" (bid) or "sell" (ask)
	ExcludedCEXes   []string
	CoupledHedge    bool // pin the hedge venue to the spot CEX where a pairing exists
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

// Engine ranks the active CEXes by their fee combo. The fee sources are function
// fields so the optimizer can be unit-tested offline with deterministic inputs.
type Engine struct {
	cexes       []string
	takerFrac   func(cex string) float64
	withdrawBTC func(cex string) float64
	hedgeCost   func(cex string, btcAmount, refPriceEUR, transferMinutes float64, coupled bool, side string) hedgeCostDetails
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
		hedgeCost: newHedgeCostEstimator(provider),
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
	excluded := make(map[string]struct{}, len(in.ExcludedCEXes))
	for _, c := range in.ExcludedCEXes {
		excluded[strings.TrimSpace(strings.ToLower(c))] = struct{}{}
	}
	isBid := in.Direction != "sell"
	side := hedgeSideForDirection(in.Direction)

	best := Selection{}
	found := false
	for _, cex := range e.cexes {
		if _, skip := excluded[cex]; skip {
			continue
		}
		takerFrac := e.takerFrac(cex)
		withdrawalBTC := 0.0
		if isBid {
			withdrawalBTC = e.withdrawBTC(cex)
		}
		hc := e.hedgeCost(cex, in.BTCAmount, in.RefPriceEUR, in.TransferMinutes, in.CoupledHedge, side)
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
