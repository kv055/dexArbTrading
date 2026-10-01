package cextrading

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"commons/hedge"
)

// persistedFeeSnapshotCacheTTL bounds staleness of in-memory CEX fee
// snapshots. Snapshots refresh on the snapshot cadence and stay usable for
// FeeSnapshotStaleAfter (an hour); a minute-scale TTL keeps pricing fresh
// while collapsing the thousands of per-request snapshot reads — one per CEX
// per spot/withdrawal/availability access inside the per-offer pricing loop —
// into one read per CEX per TTL window.
const persistedFeeSnapshotCacheTTL = time.Minute

type cachedFeeSnapshot struct {
	fees      PersistedFees
	fetchedAt time.Time
}

// persistedFeeSnapshotCache coalesces snapshot reads for one CEX.
type persistedFeeSnapshotCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cachedFeeSnapshot
}

func newPersistedFeeSnapshotCache(ttl time.Duration) *persistedFeeSnapshotCache {
	return &persistedFeeSnapshotCache{ttl: ttl, entries: make(map[string]cachedFeeSnapshot)}
}

// get returns the cached snapshot for cex, loading it once per TTL window.
// Load errors are not cached, so a failing database is retried on the next
// access instead of serving a stale error.
func (c *persistedFeeSnapshotCache) get(cex string, load func() (PersistedFees, error)) (PersistedFees, error) {
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.entries[cex]; ok && now.Sub(e.fetchedAt) < c.ttl {
		fees := e.fees
		c.mu.Unlock()
		return fees, nil
	}
	c.mu.Unlock()
	fees, err := load()
	if err != nil {
		return fees, err
	}
	c.mu.Lock()
	c.entries[cex] = cachedFeeSnapshot{fees: fees, fetchedAt: now}
	c.mu.Unlock()
	return fees, nil
}

// Input carries everything the CEX-combo optimizer needs to price and rank the
// active exchanges for one offer.
type Input struct {
	BTCAmount          float64
	RefPriceEUR        float64  // CEX reference price; sizes the hedge position simulation (0 to skip)
	TransferMinutes    float64  // from txrouting; drives the hedge funding-cost window
	Direction          string   // canonical DEX offer direction: "bid" or "ask"
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
	cexes         []string
	takerFrac     func(cex string) float64
	withdrawBTC   func(cex string) float64
	spotAvailable func(cex string, needsWithdrawal bool) bool
	hedgeCost     func(cex string, btcAmount, refPriceEUR, transferMinutes float64, coupled bool, side string) hedgeCostDetails
	hedgeOptions  func(btcAmount, refPriceEUR, transferMinutes float64, side string) []hedgeCostDetails
}

// NewEngine builds an Engine backed exclusively by persisted, exchange-derived
// fee snapshots. A missing or stale snapshot makes that provider unavailable.
func NewEngine(db *sql.DB) *Engine {
	return NewEngineWithWithdrawalResolver(db, nil)
}

// NewEngineWithWithdrawalResolver allows a congestion-aware reserve to replace
// the persisted Coinbase floor/observation at pricing time.
func NewEngineWithWithdrawalResolver(db *sql.DB, resolver func(cex string, persisted float64) float64) *Engine {
	cexes := SupportedCEXes()
	reader := persistedFeeReader{db: db}
	snapshotCache := newPersistedFeeSnapshotCache(persistedFeeSnapshotCacheTTL)
	load := func(cex string) (PersistedFees, error) {
		return snapshotCache.get(cex, func() (PersistedFees, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return reader.get(ctx, cex)
		})
	}
	cache := newCachedHedgeInput(feeTTL, nil, &reader)
	return &Engine{
		cexes: cexes,
		takerFrac: func(cex string) float64 {
			fees, err := load(cex)
			if err != nil || !fees.SpotTakerFeeRate.Valid {
				return 0
			}
			return fees.SpotTakerFeeRate.Float64
		},
		withdrawBTC: func(cex string) float64 {
			fees, err := load(cex)
			if err != nil || !fees.BTCWithdrawalFeeBTC.Valid {
				return 0
			}
			value := fees.BTCWithdrawalFeeBTC.Float64
			if resolver != nil {
				value = resolver(cex, value)
			}
			return value
		},
		spotAvailable: func(cex string, needsWithdrawal bool) bool {
			fees, err := load(cex)
			return err == nil && fees.spotAvailable(time.Now(), needsWithdrawal)
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
	isBid := in.Direction != "ask"
	for _, cex := range e.cexes {
		_, disabled := excludedSpot[cex]
		feesUnavailable := e.spotAvailable != nil && !e.spotAvailable(cex, isBid)
		if feesUnavailable {
			disabled = true
		}
		takerBTC := in.BTCAmount * e.takerFrac(cex)
		spot := SpotTakerFeeOption{Exchange: cex, FeePct: round4(takerBTC / in.BTCAmount * 100), FeeBTC: round8(takerBTC), Disabled: disabled}
		if disabled {
			spot.DisabledReason = "CEX excluded in dashboard settings"
			if feesUnavailable {
				spot.DisabledReason = "CEX fee data is missing or stale"
			}
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
	isBid := in.Direction != "ask"
	side := hedgeSideForDirection(in.Direction)

	best := Selection{}
	found := false
	for _, cex := range e.cexes {
		if _, skip := excludedSpot[cex]; skip {
			continue
		}
		if e.spotAvailable != nil && !e.spotAvailable(cex, isBid) {
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
		if !found || comboBTC < best.ComboBTC || (comboBTC == best.ComboBTC && cex < best.CEX) {
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
		if !found || candidate.feeBTC < best.feeBTC || (candidate.feeBTC == best.feeBTC && candidate.venue < best.venue) {
			best = candidate
			found = true
		}
	}
	return best, found
}
