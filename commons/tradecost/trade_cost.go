package tradecost

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"commons/bitcoin"
	"commons/exchangecredentials"
	"commons/tradecost/cextrading"
	routing "commons/tradecost/txrouting"
)

const (
	// feeTTL is the refresh interval for the DEX taker fee cache.
	feeTTL = 30 * time.Minute

	// defaultBTCTradeSize is the fallback BTC amount used when an offer has no size set.
	defaultBTCTradeSize = 0.01
)

func DefaultBTCTradeSize() float64 {
	return defaultBTCTradeSize
}

// SupportedCEXes returns the CEXes the cost model knows about (owned by cextrading).
func SupportedCEXes() []string {
	return cextrading.SupportedCEXes()
}

// TakerFeeFallback returns the static taker fee fraction for an exchange.
func TakerFeeFallback(exchange string) float64 {
	return cextrading.TakerFeeFallback(exchange)
}

// WithdrawalFallback returns the static on-chain withdrawal fee (BTC) for an exchange.
func WithdrawalFallback(exchange string) float64 {
	return cextrading.WithdrawalFallback(exchange)
}

// ---------------------------------------------------------------------------
// cachedFee — generic lazy-refresh cache for a single float64 fee value
// (used by the DEX taker fee cache; the CEX fee caches live in cextrading)
// ---------------------------------------------------------------------------

type cachedFee struct {
	mu        sync.RWMutex
	value     float64
	fetchedAt time.Time
	ttl       time.Duration
	inflight  chan struct{}
}

func newCachedFee(ttl time.Duration) *cachedFee { return &cachedFee{ttl: ttl} }

// get returns the cached value if fresh, else calls refresh and caches the result.
// On refresh failure it keeps the last known value (or fallback if never fetched).
func (c *cachedFee) get(fallback float64, refresh func() (float64, error), label string) float64 {
	c.mu.RLock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		v := c.value
		c.mu.RUnlock()
		return v
	}
	if c.inflight != nil {
		ch := c.inflight
		c.mu.RUnlock()
		<-ch
		c.mu.RLock()
		v := c.value
		c.mu.RUnlock()
		return v
	}
	c.mu.RUnlock()

	c.mu.Lock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		v := c.value
		c.mu.Unlock()
		return v
	}
	if c.inflight != nil {
		ch := c.inflight
		c.mu.Unlock()
		<-ch
		c.mu.RLock()
		v := c.value
		c.mu.RUnlock()
		return v
	}
	ch := make(chan struct{})
	c.inflight = ch
	c.mu.Unlock()

	v, err := refresh()
	c.mu.Lock()
	if err == nil {
		c.value = v
		c.fetchedAt = time.Now()
		fmt.Printf("[TradeCost] %s updated: %.8f (live)\n", label, v)
	} else {
		if c.fetchedAt.IsZero() {
			c.value = fallback
			fmt.Printf("[TradeCost] %s fetch failed (%v), using fallback %.8f\n", label, err, fallback)
		} else {
			fmt.Printf("[TradeCost] %s refresh failed (%v), keeping cached %.8f\n", label, err, c.value)
		}
		// Stamp fetchedAt even on failure so we don't retry every call for the TTL duration.
		c.fetchedAt = time.Now()
	}
	result := c.value
	close(ch)
	c.inflight = nil
	c.mu.Unlock()
	return result
}

// ---------------------------------------------------------------------------
// DEX taker fees
// ---------------------------------------------------------------------------

// dexTakerFees are the configured taker fees for each DEX platform (as fraction).
// They act as fallbacks for live sources and as the canonical values for static-fee DEXes.
// Bisq:     1.15% paid in BTC (taker side). Source: bisq.wiki/Trading_fees
// Bisq2:    0% — Bisq Easy has no trade fee for either side; the seller pays only
//
//	the bitcoin mining fee, which is modelled separately as the network fee.
//	Source: bisq.wiki/Bisq_Easy, bisq.wiki/Trading_fees
//
// RoboSats: 0.175% — fetched live from /api/info/; this is the fallback.
// HodlHodl: 0.5% — both sides pay; no API endpoint. Source: hodlhodl.com/pages/faq
// Peach:    2% buyer trading fee. Source: peachbitcoin.com/faq/trading
var dexTakerFees = map[string]float64{
	"bisq":         0.0115,
	"bisq2":        0.0,
	"robosats":     0.00175,
	"hodlhodl":     0.005,
	"peachbitcoin": 0.02,
}

// bisqSecurityDepositFloorBTC is Bisq's minimum security deposit regardless of
// the percentage (0.001 BTC). Source: bisq.wiki/Trading_rules.
const bisqSecurityDepositFloorBTC = 0.001

// dexSecurityDepositPct is the FALLBACK security deposit estimate, as a
// fraction of the trade BTC amount, used only when no real per-offer value is
// available (e.g. an offer snapshotted before the real value was persisted).
// Bisq's default buyer/seller deposit is 15%. The real per-offer value (now
// persisted as offers.security_deposit_pct, computed at snapshot time from
// the bisq gRPC OfferInfo / the RoboSats bond_size) is passed into
// computeCapitalRequirement as realDepositPct and always takes priority over
// this map. DEXes absent from this map lock no deposit when no real value
// is available either.
var dexSecurityDepositPct = map[string]float64{
	"bisq": 0.15,
}

// robosatsOnions are the coordinator onion addresses, tried in order.
// Keep this aligned with the active unified snapshot fetcher.
var robosatsOnions = []string{
	"http://ngdk7ocdzmz5kzsysa3om6du7ycj2evxp2f2olfkyq37htx3gllwp2yd.onion",
	"http://mmhaqzuirth5rx7gl24d4773lknltjhik57k7ahec5iefktezv4b3uid.onion",
	"http://4t4jxmivv6uqej6xzx2jx3fxh75gtt65v3szjoqmc4ugdlhipzdat6yd.onion",
	"http://otmoonrndnrddqdlhu6b36heunmbyw3cgvadqo2oqeau3656wfv7fwad.onion",
}

// newTorHTTPClient returns an HTTP client routed through the local Tor SOCKS5 proxy.
func newTorHTTPClient() (*http.Client, error) {
	torProxyAddr := os.Getenv("TOR_PROXY_ADDR")
	if torProxyAddr == "" {
		torProxyAddr = "127.0.0.1:9050"
	}
	dialer, err := proxy.SOCKS5("tcp", torProxyAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("tor proxy: %w", err)
	}
	return &http.Client{
		Transport: &http.Transport{Dial: dialer.Dial},
		Timeout:   15 * time.Second,
	}, nil
}

// fetchRobosatsTakerFee fetches the taker_fee from GET /api/info/ on each
// coordinator in turn, returning the first successful result.
func fetchRobosatsTakerFee() (float64, error) {
	torHTTP, err := newTorHTTPClient()
	if err != nil {
		return 0, err
	}
	var lastErr error
	for _, onion := range robosatsOnions {
		resp, err := torHTTP.Get(onion + "/api/info/")
		if err != nil {
			lastErr = err
			continue
		}
		var info struct {
			TakerFee float64 `json:"taker_fee"`
		}
		err = json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if info.TakerFee <= 0 {
			lastErr = fmt.Errorf("coordinator returned taker_fee=0")
			continue
		}
		return info.TakerFee, nil
	}
	return 0, fmt.Errorf("all coordinators failed, last: %w", lastErr)
}

// ---------------------------------------------------------------------------
// TradeCostEstimator
// ---------------------------------------------------------------------------

// TradeCostEstimator estimates the total cost of one CEX→DEX arbitrage cycle:
//  1. CEX taker fee — fetched live from exchange API where available, static fallback
//  2. CEX BTC withdrawal fee — fetched live from exchange API, static fallback
//  3. Bitcoin on-chain network fee — congestion-adaptive, dynamic vsize, cached 5 min
//  4. DEX platform fee — static per-exchange taker rate
//
// All components are expressed as a percentage of the trade value so the result
// can be directly compared to / subtracted from the DEX offer premium.
// TradeCostEstimator orchestrates the three cost sources of a CEX→DEX arbitrage
// cycle: the txrouting engine (BTC network fee + Lightning conversion, fixed per
// DEX/side), the cextrading engine (cheapest CEX combo of spot taker + on-chain
// withdrawal + hedge), and the static per-DEX platform fee. It is not per-CEX —
// the cheapest CEX is chosen inside the cextrading engine.
type TradeCostEstimator struct {
	dexFees             map[string]*cachedFee
	btcPredictor        *bitcoin.BitcoinFeePredictor
	withdrawalTxShape   routing.TxShape // canonical CEX on-chain withdrawal shape (CEX-independent)
	txEngine            *routing.Engine
	cexEngine           *cextrading.Engine
	routingCostOverride func(kind routing.Kind, in routing.Input) routing.Cost
	cexCostOverride     func(in cextrading.Input) (cextrading.Selection, bool)
}

func NewTradeCostEstimator() *TradeCostEstimator {
	return NewTradeCostEstimatorWithCredentials(nil)
}

// NewTradeCostEstimatorWithCredentials builds an estimator whose authenticated
// exchange fee lookups resolve credentials from the supplied database provider.
func NewTradeCostEstimatorWithCredentials(provider exchangecredentials.Provider) *TradeCostEstimator {
	torClient, err := newTorHTTPClient()
	if err != nil {
		fmt.Printf("[TradeCost] Tor client unavailable, bitcoin fee fetcher will use direct HTTP: %v\n", err)
		torClient = nil
	}
	predictor := bitcoin.NewBitcoinFeePredictorWithClient(torClient)
	if err := predictor.FetchLatestData(); err != nil {
		fmt.Printf("[TradeCost] initial mempool fetch failed (%v), network fees will be 0 until next retry\n", err)
	} else {
		fmt.Println("[TradeCost] mempool fee data bootstrapped")
	}
	return &TradeCostEstimator{
		dexFees: map[string]*cachedFee{
			"bisq":     newCachedFee(feeTTL),
			"robosats": newCachedFee(feeTTL),
			"hodlhodl": newCachedFee(feeTTL),
		},
		btcPredictor:      predictor,
		withdrawalTxShape: defaultCexProfile.txShape(),
		txEngine:          routing.NewEngine(predictor),
		cexEngine:         cextrading.NewEngine(provider),
	}
}

// fixedRoutingCost returns a deterministic, offline routing.Cost for tests: each
// on-chain hop costs networkFeeBTC (direct=1, hub=2), and the swap method adds a
// fixed Boltz-fallback conversion (0.1% service + 150 sats miner).
func fixedRoutingCost(networkFeeBTC float64) func(routing.Kind, routing.Input) routing.Cost {
	return func(kind routing.Kind, in routing.Input) routing.Cost {
		hops := 1
		if kind == routing.MethodOnChainHubWallet {
			hops = 2
		}
		c := routing.Cost{
			Method:        kind,
			NetworkFeeBTC: networkFeeBTC * float64(hops),
			NumOnchainTxs: hops,
			Tier:          "standard",
			Confidence:    0.875,
			Detail: &routing.NetworkFeeDetail{
				NumTxs:                          hops,
				SelectedFullConfirmationMinutes: 30,
			},
		}
		if kind == routing.MethodOnChainSwap {
			serviceBTC := round8(0.001 * in.BTCAmount)
			minerBTC := round8(150.0 / 1e8)
			c.Conversion = routing.ConversionBreakdown{
				Strategy:         routing.StrategySubmarineSwap,
				ServiceFeeBTC:    serviceBTC,
				ProviderMinerBTC: minerBTC,
				FeeSumBTC:        round8(serviceBTC + minerBTC),
			}
		}
		return c
	}
}

// fixedCexCost builds a deterministic cextrading.Selection override for tests: the
// given CEX wins with a fixed taker/withdrawal fee, and hedgeFn (optional) supplies
// the hedge breakdown. Withdrawal applies on the bid side only.
func fixedCexCost(exchange string, takerFeeFrac, withdrawalFeeBTC float64, hedgeFn func(in cextrading.Input) cextrading.HedgeBreakdown) func(cextrading.Input) (cextrading.Selection, bool) {
	return func(in cextrading.Input) (cextrading.Selection, bool) {
		takerBTC := in.BTCAmount * takerFeeFrac
		withdrawalBTC := 0.0
		if in.Direction != "sell" {
			withdrawalBTC = withdrawalFeeBTC
		}
		sel := cextrading.Selection{
			CEX:              exchange,
			TakerFeeFrac:     takerFeeFrac,
			TakerFeeBTC:      takerBTC,
			WithdrawalFeeBTC: withdrawalBTC,
		}
		if hedgeFn != nil {
			h := hedgeFn(in)
			sel.Hedge = h
			sel.HedgeFeeBTC = h.FeeSumBTC
			sel.Position = h.Position
		}
		sel.ComboBTC = takerBTC + withdrawalBTC + sel.HedgeFeeBTC
		return sel, true
	}
}

// hedgeSideForTestDirection mirrors cextrading's bid→short / ask→long mapping.
func hedgeSideForTestDirection(direction string) string {
	if direction == "sell" {
		return "long"
	}
	return "short"
}

// NewFixedEstimatorForTesting builds a deterministic estimator for handler
// tests outside this package.
func NewFixedEstimatorForTesting(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC float64) *TradeCostEstimator {
	seed := func(value float64) *cachedFee {
		return &cachedFee{value: value, fetchedAt: time.Now(), ttl: feeTTL}
	}
	return &TradeCostEstimator{
		dexFees: map[string]*cachedFee{
			dexExchange: seed(dexFeeFrac),
		},
		withdrawalTxShape:   defaultCexProfile.txShape(),
		routingCostOverride: fixedRoutingCost(networkFeeBTC),
		cexCostOverride:     fixedCexCost(exchange, takerFeeFrac, withdrawalFeeBTC, nil),
	}
}

func NewFixedEstimatorWithHedgeForTesting(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC, hedgeFeeBTC float64) *TradeCostEstimator {
	est := NewFixedEstimatorForTesting(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC)
	hedgeFn := func(in cextrading.Input) cextrading.HedgeBreakdown {
		feePct := 0.0
		if in.BTCAmount > 0 {
			feePct = round4(hedgeFeeBTC / in.BTCAmount * 100)
		}
		return cextrading.HedgeBreakdown{
			FeeSumBTC:       round8(hedgeFeeBTC),
			FeeSumPct:       feePct,
			Side:            hedgeSideForTestDirection(in.Direction),
			Venue:           "coinbase_futures",
			Instrument:      "BTC-PERP",
			TransferMinutes: int(math.Round(in.TransferMinutes)),
		}
	}
	est.cexCostOverride = fixedCexCost(exchange, takerFeeFrac, withdrawalFeeBTC, hedgeFn)
	return est
}

func NewFixedEstimatorWithHedgeRatesForTesting(exchange, dexExchange string, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC float64, openRate, closeRate, fundingRate float64) *TradeCostEstimator {
	est := NewFixedEstimatorForTesting(exchange, dexExchange, takerFeeFrac, withdrawalFeeBTC, dexFeeFrac, networkFeeBTC)
	hedgeFn := func(in cextrading.Input) cextrading.HedgeBreakdown {
		side := hedgeSideForTestDirection(in.Direction)
		fundingCostRate := fundingRate
		if side == "long" {
			fundingCostRate = -fundingRate
		}
		if fundingCostRate < 0 {
			fundingCostRate = 0
		}
		openFeeBTC := in.BTCAmount * openRate
		closeFeeBTC := in.BTCAmount * closeRate
		fundingFeeBTC := in.BTCAmount * fundingCostRate
		feeBTC := openFeeBTC + closeFeeBTC + fundingFeeBTC
		pct := func(b float64) float64 {
			if in.BTCAmount <= 0 || b == 0 {
				return 0
			}
			return round4(b / in.BTCAmount * 100)
		}
		return cextrading.HedgeBreakdown{
			OpenFeePct:      pct(openFeeBTC),
			OpenFeeBTC:      round8(openFeeBTC),
			CloseFeePct:     pct(closeFeeBTC),
			CloseFeeBTC:     round8(closeFeeBTC),
			FundingFeePct:   pct(fundingFeeBTC),
			FundingFeeBTC:   round8(fundingFeeBTC),
			FeeSumPct:       pct(feeBTC),
			FeeSumBTC:       round8(feeBTC),
			Side:            side,
			Venue:           "kraken_futures",
			Instrument:      "PI_XBTUSD",
			TransferMinutes: int(math.Round(in.TransferMinutes)),
		}
	}
	est.cexCostOverride = fixedCexCost(exchange, takerFeeFrac, withdrawalFeeBTC, hedgeFn)
	return est
}

func (e *TradeCostEstimator) SetFixedDexFeeForTesting(dexExchange string, dexFeeFrac float64) {
	e.dexFees[dexExchange] = &cachedFee{value: dexFeeFrac, fetchedAt: time.Now(), ttl: feeTTL}
}

func (e *TradeCostEstimator) getDexFeeFrac(dexExchange string) float64 {
	fallback := dexTakerFees[dexExchange]
	cache := e.dexFees[dexExchange]
	if cache == nil {
		return fallback
	}

	refresh := func() (float64, error) {
		return fallback, nil
	}
	label := dexExchange + " DEX taker fee"

	if dexExchange == "robosats" {
		refresh = fetchRobosatsTakerFee
	}

	return cache.get(fallback, refresh, label)
}

func (e *TradeCostEstimator) TakerFeeBTC(btcAmount float64) float64 {
	if btcAmount <= 0 {
		return 0
	}
	sel, _ := e.cexCost(cextrading.Input{BTCAmount: btcAmount, Direction: "buy"})
	return sel.TakerFeeBTC
}

func (e *TradeCostEstimator) DexFeeBTC(btcAmount float64, dexExchange string) float64 {
	if btcAmount <= 0 {
		return 0
	}
	return btcAmount * e.getDexFeeFrac(dexExchange)
}

func (e *TradeCostEstimator) EstimateHedgeBreakdown(btcAmount float64, refPriceEUR float64, transferMinutes float64, coupledHedge bool) HedgeBreakdown {
	return e.EstimateHedgeBreakdownForDirection(btcAmount, refPriceEUR, transferMinutes, coupledHedge, "buy")
}

func (e *TradeCostEstimator) EstimateHedgeBreakdownForDirection(btcAmount float64, refPriceEUR float64, transferMinutes float64, coupledHedge bool, direction string) HedgeBreakdown {
	sel, _ := e.cexCost(cextrading.Input{
		BTCAmount:       btcAmount,
		RefPriceEUR:     refPriceEUR,
		TransferMinutes: transferMinutes,
		Direction:       direction,
		CoupledHedge:    coupledHedge,
	})
	return sel.Hedge
}

// TradeRouteOptimizer wraps a single orchestrator estimator. The cheapest CEX is
// now chosen inside the cextrading engine, so the optimizer no longer loops a
// per-CEX estimator map.
type TradeRouteOptimizer struct {
	est *TradeCostEstimator
}

func NewTradeRouteOptimizer(estimators map[string]*TradeCostEstimator) *TradeRouteOptimizer {
	return NewOptimizer(SupportedCEXes(), estimators)
}

// NewOptimizer adopts a single orchestrator estimator. The map argument is the
// historical per-CEX signature; post-refactor one estimator serves every CEX (it
// picks the cheapest internally via cextrading), so any non-nil entry is used.
func NewOptimizer(supported []string, estimators map[string]*TradeCostEstimator) *TradeRouteOptimizer {
	var est *TradeCostEstimator
	for _, c := range supported {
		if e := estimators[c]; e != nil {
			est = e
			break
		}
	}
	if est == nil {
		for _, e := range estimators {
			if e != nil {
				est = e
				break
			}
		}
	}
	return &TradeRouteOptimizer{est: est}
}

// BTCNetworkFeeDetail and ConversionBreakdown are owned by the routing package;
// these aliases keep the existing tradecost/API type names valid.
type BTCNetworkFeeDetail = routing.NetworkFeeDetail

type ConversionBreakdown = routing.ConversionBreakdown

func resolveRequiredConfirmations(direction string, requested int) int {
	if requested >= 1 && requested <= 6 {
		return requested
	}
	if direction == "sell" {
		return 4
	}
	return 2
}

func ResolveRequiredConfirmations(direction string, requested int) int {
	return resolveRequiredConfirmations(direction, requested)
}

// CostPct returns the estimated total arbitrage cost as a percentage of btcAmount.
// dexExchange is the name of the DEX the offer is on (e.g. "bisq", "robosats", "hodlhodl").
// A DEX offer premium must exceed this value to be profitable.
func (e *TradeCostEstimator) CostPct(btcAmount float64, dexExchange string) float64 {
	return e.CostPctWithConfirmations(btcAmount, dexExchange, 1)
}

func (e *TradeCostEstimator) CostPctWithConfirmations(btcAmount float64, dexExchange string, requiredConfirmations int) float64 {
	if btcAmount <= 0 {
		return 0
	}
	// CostPct uses the bid side (default), where a CEX withdrawal fee applies.
	requiredConfirmations = resolveRequiredConfirmations("buy", requiredConfirmations)
	dexFeeFrac := e.getDexFeeFrac(dexExchange)
	cost := e.routingCost(dexExchange, "buy", btcAmount, requiredConfirmations, "")
	sel, ok := e.cexCost(cextrading.Input{BTCAmount: btcAmount, Direction: "buy"})
	if !ok {
		return 0
	}

	dexFee := btcAmount * dexFeeFrac
	total := sel.TakerFeeBTC + sel.WithdrawalFeeBTC + cost.NetworkFeeBTC + cost.Conversion.FeeSumBTC + dexFee
	return total / btcAmount * 100
}

// routingCost prices the CEX→DEX transfer leg for a DEX/side using the routing engine
// (or the test override). The method is fixed by the DEX and offer side.
func (e *TradeCostEstimator) routingCost(dexExchange, direction string, btcAmount float64, requiredConfirmations int, feeTier string) routing.Cost {
	side := routing.SideForDirection(direction)
	kind := routing.MethodFor(dexExchange, side)
	in := routing.Input{
		BTCAmount:             btcAmount,
		RequiredConfirmations: requiredConfirmations,
		FeeTier:               feeTier,
		WithdrawalTx:          e.withdrawalTxShape,
	}
	if e.routingCostOverride != nil {
		return e.routingCostOverride(kind, in)
	}
	if e.txEngine == nil {
		return routing.Cost{Method: kind}
	}
	return e.txEngine.Cost(kind, in)
}

// cexCost picks the cheapest CEX fee combo (taker + withdrawal + hedge) for the
// input, using the cextrading engine (or the test override). The bool is false
// when no CEX is available (all excluded).
func (e *TradeCostEstimator) cexCost(in cextrading.Input) (cextrading.Selection, bool) {
	if e.cexCostOverride != nil {
		return e.cexCostOverride(in)
	}
	if e.cexEngine == nil {
		return cextrading.Selection{}, false
	}
	return e.cexEngine.Best(in)
}

// ---------------------------------------------------------------------------
// CostBreakdown — per-component cost details
// ---------------------------------------------------------------------------

// CostBreakdown holds each component of the arbitrage cost, all expressed both
// as absolute BTC amounts and as percentages of the trade value.
type CostBreakdown struct {
	BTCAmount            float64              `json:"btc_amount"`             // trade size used for calculation
	RouteCEX             string               `json:"route_cex"`              // chosen CEX for the cheapest route
	RouteDEX             string               `json:"route_dex"`              // DEX of the evaluated offer
	RouteMethod          string               `json:"route_method"`           // chosen settlement method for the cheapest route
	TradeDirection       string               `json:"trade_direction"`        // "bid" (buy CEX→sell DEX) or "ask" (buy DEX→sell CEX)
	NumNetworkTxs        int                  `json:"num_network_txs"`        // on-chain transactions in the chosen route (1 direct/swap, 2 hub-wallet)
	EstimatedProfitPct   float64              `json:"estimated_profit_pct"`   // net premium after costs as % of trade
	EstimatedProfitBTC   float64              `json:"estimated_profit_btc"`   // estimated profit in BTC for the chosen trade size
	CexTakerFeePct       float64              `json:"cex_taker_fee_pct"`      // CEX taker fee as % of trade
	CexTakerFeeBTC       float64              `json:"cex_taker_fee_btc"`      // CEX taker fee in BTC
	WithdrawalFeePct     float64              `json:"withdrawal_fee_pct"`     // CEX BTC withdrawal fee as % of trade (0 for ask side)
	WithdrawalFeeBTC     float64              `json:"withdrawal_fee_btc"`     // CEX BTC withdrawal fee in BTC (0 for ask side)
	NetworkFeePct        float64              `json:"network_fee_pct"`        // settlement network fee as % of trade
	NetworkFeeBTC        float64              `json:"network_fee_btc"`        // settlement network fee in BTC
	NetworkFeeMethod     string               `json:"network_fee_method"`     // "onchain" or "lightning"
	NetworkFeeTier       string               `json:"network_fee_tier"`       // "economic", "standard", "fast", "express", "lightning"
	NetworkFeeConfidence float64              `json:"network_fee_confidence"` // 0–1.0 estimate reliability
	NetworkFeeDetail     *BTCNetworkFeeDetail `json:"network_fee_detail"`     // full onchain calc context; nil for Lightning
	Conversion           ConversionBreakdown  `json:"conversion"`             // on-chain → Lightning local conversion; zero-value unless RouteMethod is onchain_swap
	Hedge                HedgeBreakdown       `json:"hedge"`                  // perp hedge detail grouped under its own nested object
	DexFeePct            float64              `json:"dex_fee_pct"`            // DEX platform fee as % of trade
	DexFeeBTC            float64              `json:"dex_fee_btc"`            // DEX platform fee in BTC
	TotalCostPct         float64              `json:"total_cost_pct"`         // sum of all components as % of trade
	TotalCostBTC         float64              `json:"total_cost_btc"`         // sum of all components in BTC
	Capital              CapitalRequirement   `json:"capital"`                // capital that must be committed to execute the trade (distinct from costs)
}

type AbortCostComponent struct {
	Pct float64 `json:"pct"`
	BTC float64 `json:"btc"`
}

type AbortCostBreakdown struct {
	BTCAmount      float64            `json:"btc_amount"`
	TradeDirection string             `json:"trade_direction"`
	NumNetworkTxs  int                `json:"num_network_txs"`
	CexBuyTaker    AbortCostComponent `json:"cex_buy_taker"`
	CexSellTaker   AbortCostComponent `json:"cex_sell_taker"`
	Withdrawal     AbortCostComponent `json:"withdrawal"`
	Network        AbortCostComponent `json:"network"`
	Hedge          HedgeBreakdown     `json:"hedge"`
	Total          AbortCostComponent `json:"total"`
}

// HedgeBreakdown and HedgePositionDetail are owned by the cextrading package;
// these aliases keep the existing tradecost/API type names valid.
type HedgeBreakdown = cextrading.HedgeBreakdown

type HedgePositionDetail = cextrading.HedgePositionDetail

// ManualRouteOptions holds all independently selectable CEX-side alternatives
// for a live-offer scenario. It is intentionally separate from CostBreakdown,
// which remains the automatically selected cheapest route.
type ManualRouteOptions = cextrading.ManualRouteOptions

// Trade mode identifiers for live-offer capital / cost variants.
const (
	TradeModeHedgefullSingleCollateral = "hedgefull_single_collateral"
	TradeModeHedgelessDoubleCollateral = "hedgeless_double_collateral"
)

// CapitalRequirement is the capital a trader must commit to *execute* the trade,
// as opposed to the fees consumed by it (CostBreakdown). It is broken out into
// the spot leg (fiat to buy BTC on the CEX), the perp hedge collateral, and the
// DEX security deposit, plus the grand total. All figures are for the BTCAmount
// the enclosing CostBreakdown was built with, so the capped breakdown carries a
// capped capital block. Fiat figures are 0 when the ref price is unknown.
// Amounts are in the offer's fiat currency (see the live-offers fiat filter),
// not hardcoded to EUR.
//
// Hedgeless double-collateral mode additionally reports BTCLiquidityBTC
// (position + security deposit) and FiatCollateral (position×price only);
// TotalFiat is then BTC-pool fiat + fiat position.
type CapitalRequirement struct {
	BTCAmount           float64 `json:"btc_amount"`            // trade size the figures are based on
	BTCPrice            float64 `json:"btc_price"`             // ref price in fiat (0 when unknown)
	SpotNotional        float64 `json:"spot_notional"`         // fiat to buy the BTC leg on the CEX (btcAmount * refPrice)
	HedgeCollateral     float64 `json:"hedge_collateral"`      // = Hedge.Position.RecommendedCollateral (0 if no position / hedgeless)
	SecurityDepositPct  float64 `json:"security_deposit_pct"`  // fraction applied (e.g. 0.15); 0 for non-deposit DEXes
	SecurityDepositSide string  `json:"security_deposit_side"` // "buyer" or "seller" — the side we play on the DEX
	SecurityDepositBTC  float64 `json:"security_deposit_btc"`  // max(btcAmount*pct, floor)
	SecurityDepositFiat float64 `json:"security_deposit_fiat"` // deposit in fiat (0 when refPrice unknown)
	TotalFiat           float64 `json:"total_fiat"`            // hedgefull: spot + hedge + deposit; hedgeless: btc pool fiat + fiat position
	DepositEstimated    bool    `json:"deposit_estimated"`     // true while using the configured %, false once the real per-offer value is plumbed
	TradeMode           string  `json:"trade_mode,omitempty"`  // hedgefull_single_collateral | hedgeless_double_collateral
	BTCLiquidityBTC     float64 `json:"btc_liquidity_btc,omitempty"` // hedgeless: position + security deposit
	FiatCollateral      float64 `json:"fiat_collateral,omitempty"`   // hedgeless: position×price (no deposit on fiat side)
}

func round8(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func Round8(v float64) float64 {
	return round8(v)
}

func round4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func Round4(v float64) float64 {
	return round4(v)
}

func round2(v float64) float64 {
	return math.Round(v*1e2) / 1e2
}

func Round2(v float64) float64 {
	return round2(v)
}

// Breakdown returns a full per-component cost breakdown for a given trade size, DEX, and offer direction.
// direction should be the DEX offer direction: "buy" (bid side) or "sell" (ask side).
func (e *TradeCostEstimator) Breakdown(btcAmount float64, dexExchange string, direction string) CostBreakdown {
	return e.BreakdownWithConfirmations(btcAmount, dexExchange, direction, 0)
}

func (e *TradeCostEstimator) BreakdownWithConfirmations(btcAmount float64, dexExchange string, direction string, requiredConfirmations int) CostBreakdown {
	return e.breakdown(btcAmount, 0, dexExchange, direction, requiredConfirmations, "", nil, nil, false, nil)
}

// breakdown builds a full per-component cost breakdown by combining three cost
// sources: the txrouting leg (BTC network fee + Lightning conversion, fixed by the
// DEX and offer side), the cheapest CEX fee combo (spot taker + on-chain withdrawal
// + hedge, chosen by the cextrading engine honoring excludedCEXes/coupledHedge), and
// the static DEX platform fee.
func (e *TradeCostEstimator) breakdown(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, feeTier string, excludedSpotCEXes, excludedHedgeCEXes []string, coupledHedge bool, realDepositPct *float64) CostBreakdown {
	if btcAmount <= 0 {
		return CostBreakdown{}
	}
	requiredConfirmations = resolveRequiredConfirmations(direction, requiredConfirmations)
	dexFeeFrac := e.getDexFeeFrac(dexExchange)
	cost := e.routingCost(dexExchange, direction, btcAmount, requiredConfirmations, feeTier)

	networkFeeBTC := cost.NetworkFeeBTC
	conversionBTC := cost.Conversion.FeeSumBTC
	transferMinutes := 0.0
	if cost.Detail != nil && cost.Detail.SelectedFullConfirmationMinutes > 0 {
		transferMinutes = float64(cost.Detail.SelectedFullConfirmationMinutes * cost.NumOnchainTxs)
	}

	// Pick the cheapest CEX fee combo. Ask side: BTC is deposited to the CEX, so no
	// withdrawal fee (the cextrading engine side-gates it); bid side withdraws.
	sel, ok := e.cexCost(cextrading.Input{
		BTCAmount:          btcAmount,
		RefPriceEUR:        refPriceEUR,
		TransferMinutes:    transferMinutes,
		Direction:          direction,
		ExcludedSpotCEXes:  excludedSpotCEXes,
		ExcludedHedgeCEXes: excludedHedgeCEXes,
		CoupledHedge:       coupledHedge,
	})
	if !ok {
		return CostBreakdown{}
	}

	isBid := direction != "sell"
	tradeDir := "bid"
	if !isBid {
		tradeDir = "ask"
	}

	takerBTC := sel.TakerFeeBTC
	withdrawalFee := sel.WithdrawalFeeBTC
	hedgeFeeBTC := sel.HedgeFeeBTC
	dexBTC := btcAmount * dexFeeFrac

	pct := func(btc float64) float64 { return round4(btc / btcAmount * 100) }

	totalBTC := takerBTC + withdrawalFee + networkFeeBTC + hedgeFeeBTC + dexBTC + conversionBTC
	totalPct := totalBTC / btcAmount * 100

	return CostBreakdown{
		BTCAmount:            round8(btcAmount),
		RouteCEX:             sel.CEX,
		RouteDEX:             dexExchange,
		RouteMethod:          string(cost.Method),
		TradeDirection:       tradeDir,
		NumNetworkTxs:        cost.NumOnchainTxs,
		CexTakerFeePct:       pct(takerBTC),
		CexTakerFeeBTC:       round8(takerBTC),
		WithdrawalFeePct:     pct(withdrawalFee),
		WithdrawalFeeBTC:     round8(withdrawalFee),
		NetworkFeePct:        pct(networkFeeBTC),
		NetworkFeeBTC:        round8(networkFeeBTC),
		NetworkFeeMethod:     string(cost.Method),
		NetworkFeeTier:       cost.Tier,
		NetworkFeeConfidence: round4(cost.Confidence),
		NetworkFeeDetail:     cost.Detail,
		Hedge:                sel.Hedge,
		Conversion:           cost.Conversion,
		DexFeePct:            pct(dexBTC),
		DexFeeBTC:            round8(dexBTC),
		TotalCostPct:         round4(totalPct),
		TotalCostBTC:         round8(totalBTC),
		Capital:              computeCapitalRequirement(btcAmount, refPriceEUR, dexExchange, isBid, sel.Position, realDepositPct),
	}
}

// computeCapitalRequirement reports the capital a trader must commit to execute a
// trade of btcAmount on dexExchange: the spot leg notional, the perp hedge
// collateral (reused from the already-computed hedge position), and the DEX
// security deposit. isBid is true when we buy BTC on the CEX and sell on the DEX
// (so we post the seller deposit); false on the ask side (buyer deposit). EUR
// figures are 0 when refPriceEUR is unknown (<= 0). realDepositPct is the
// actual per-offer deposit fraction (offers.security_deposit_pct, already
// picked for the taker's side at snapshot time) when known; it always takes
// priority over the flat dexSecurityDepositPct estimate, and clears
// DepositEstimated since it's a measured value, not a guess.
func computeCapitalRequirement(btcAmount, refPriceEUR float64, dexExchange string, isBid bool, hedgePosition *HedgePositionDetail, realDepositPct *float64) CapitalRequirement {
	if btcAmount <= 0 {
		return CapitalRequirement{}
	}

	cap := CapitalRequirement{
		BTCAmount:   round8(btcAmount),
		BTCPrice: round2(refPriceEUR),
		TradeMode:   TradeModeHedgefullSingleCollateral,
	}
	if refPriceEUR > 0 {
		cap.SpotNotional = round2(btcAmount * refPriceEUR)
	}
	if hedgePosition != nil {
		cap.HedgeCollateral = round2(hedgePosition.RecommendedCollateral)
	}

	// On the bid side we sell BTC on the DEX (seller deposit); on the ask
	// side we buy (buyer deposit).
	side := "buyer"
	if isBid {
		side = "seller"
	}

	switch {
	case realDepositPct != nil && *realDepositPct > 0:
		cap.SecurityDepositSide = side
		cap.SecurityDepositPct = *realDepositPct
		cap.SecurityDepositBTC = round8(btcAmount * *realDepositPct)
		if refPriceEUR > 0 {
			cap.SecurityDepositFiat = round2(cap.SecurityDepositBTC * refPriceEUR)
		}
		cap.DepositEstimated = false
	case dexSecurityDepositPct[dexExchange] > 0:
		pct := dexSecurityDepositPct[dexExchange]
		cap.SecurityDepositSide = side
		cap.SecurityDepositPct = pct
		depositBTC := btcAmount * pct
		if depositBTC < bisqSecurityDepositFloorBTC {
			depositBTC = bisqSecurityDepositFloorBTC
		}
		cap.SecurityDepositBTC = round8(depositBTC)
		if refPriceEUR > 0 {
			cap.SecurityDepositFiat = round2(depositBTC * refPriceEUR)
		}
		cap.DepositEstimated = true
	}

	cap.TotalFiat = round2(cap.SpotNotional + cap.HedgeCollateral + cap.SecurityDepositFiat)
	return cap
}

// computeHedgelessCapital builds Hedgeless Double Collateral capital from a
// hedgefull CapitalRequirement: BTC pool = position + deposit, fiat = position
// only (no deposit on the fiat side). TotalFiat = pool fiat + fiat position.
func computeHedgelessCapital(hedgefull CapitalRequirement) CapitalRequirement {
	if hedgefull.BTCAmount <= 0 {
		return CapitalRequirement{}
	}
	cap := hedgefull
	cap.TradeMode = TradeModeHedgelessDoubleCollateral
	cap.HedgeCollateral = 0
	cap.BTCLiquidityBTC = round8(cap.BTCAmount + cap.SecurityDepositBTC)
	if cap.BTCPrice > 0 {
		cap.FiatCollateral = round2(cap.BTCAmount * cap.BTCPrice)
		btcPoolFiat := round2(cap.BTCLiquidityBTC * cap.BTCPrice)
		cap.TotalFiat = round2(btcPoolFiat + cap.FiatCollateral)
	} else {
		cap.FiatCollateral = 0
		cap.TotalFiat = 0
	}
	return cap
}

// DeriveHedgelessDoubleCollateral copies a hedgefull CostBreakdown and removes
// hedge fees from totals (same spot route; no independent re-ranking). Capital
// is replaced with the hedgeless double-collateral requirement.
func DeriveHedgelessDoubleCollateral(bd CostBreakdown) CostBreakdown {
	if bd.BTCAmount <= 0 || bd.RouteCEX == "" {
		return CostBreakdown{}
	}
	out := bd
	hedgeFeeBTC := bd.Hedge.FeeSumBTC
	out.Hedge = HedgeBreakdown{}
	out.TotalCostBTC = round8(bd.TotalCostBTC - hedgeFeeBTC)
	if out.TotalCostBTC < 0 {
		out.TotalCostBTC = 0
	}
	out.TotalCostPct = round4(out.TotalCostBTC / bd.BTCAmount * 100)
	if bd.EstimatedProfitPct != 0 || bd.EstimatedProfitBTC != 0 {
		out.EstimatedProfitPct = round4(bd.EstimatedProfitPct + bd.Hedge.FeeSumPct)
		out.EstimatedProfitBTC = round8(bd.BTCAmount * (out.EstimatedProfitPct / 100))
	}
	out.Capital = computeHedgelessCapital(bd.Capital)
	return out
}

// CapitalRequirementForTrade reports the capital required for a trade using a
// persisted per-offer security deposit when one is available.
func CapitalRequirementForTrade(btcAmount, refPriceEUR float64, dexExchange string, isBid bool, hedgePosition *HedgePositionDetail, realDepositPct *float64) CapitalRequirement {
	return computeCapitalRequirement(btcAmount, refPriceEUR, dexExchange, isBid, hedgePosition, realDepositPct)
}

func AbortCostFromBreakdown(bd CostBreakdown) AbortCostBreakdown {
	if bd.BTCAmount <= 0 {
		return AbortCostBreakdown{}
	}

	pct := func(btc float64) float64 {
		if btc == 0 {
			return 0
		}
		return round4(btc / bd.BTCAmount * 100)
	}

	isAsk := bd.TradeDirection == "ask"
	networkBTC := bd.NetworkFeeBTC
	if isAsk {
		// Normal ask-side trade cost has one BTC transfer; abort risk models two.
		networkBTC = bd.NetworkFeeBTC * 2
	}

	abort := AbortCostBreakdown{
		BTCAmount:      bd.BTCAmount,
		TradeDirection: bd.TradeDirection,
		NumNetworkTxs:  2,
		Network: AbortCostComponent{
			Pct: pct(networkBTC),
			BTC: round8(networkBTC),
		},
	}

	if !isAsk {
		abort.CexBuyTaker = AbortCostComponent{Pct: bd.CexTakerFeePct, BTC: bd.CexTakerFeeBTC}
		abort.CexSellTaker = AbortCostComponent{Pct: bd.CexTakerFeePct, BTC: bd.CexTakerFeeBTC}
		abort.Withdrawal = AbortCostComponent{Pct: bd.WithdrawalFeePct, BTC: bd.WithdrawalFeeBTC}
		abort.Hedge = bd.Hedge
	}

	totalBTC := abort.CexBuyTaker.BTC + abort.CexSellTaker.BTC + abort.Withdrawal.BTC + abort.Network.BTC + abort.Hedge.FeeSumBTC
	abort.Total = AbortCostComponent{
		Pct: pct(totalBTC),
		BTC: round8(totalBTC),
	}
	return abort
}

func (e *TradeCostEstimator) AbortCostFromBreakdown(bd CostBreakdown) AbortCostBreakdown {
	return AbortCostFromBreakdown(bd)
}

// ManualRouteOptionsForTrade exposes all eligible spot/withdrawal fees and all
// hedge venues for local, operator-facing what-if analysis at this exact trade
// size. It does not participate in the automatic route selection.
func (e *TradeCostEstimator) ManualRouteOptionsForTrade(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string) ManualRouteOptions {
	return e.ManualRouteOptionsForTradeWithExclusions(btcAmount, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedCEXes, excludedCEXes, feeTier)
}

func (e *TradeCostEstimator) ManualRouteOptionsForTradeWithExclusions(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedSpotCEXes, excludedHedgeCEXes []string, feeTier string) ManualRouteOptions {
	if e == nil || e.cexEngine == nil || btcAmount <= 0 {
		return ManualRouteOptions{}
	}
	requiredConfirmations = resolveRequiredConfirmations(direction, requiredConfirmations)
	cost := e.routingCost(dexExchange, direction, btcAmount, requiredConfirmations, feeTier)
	transferMinutes := 0.0
	if cost.Detail != nil && cost.Detail.SelectedFullConfirmationMinutes > 0 {
		transferMinutes = float64(cost.Detail.SelectedFullConfirmationMinutes * cost.NumOnchainTxs)
	}
	return e.cexEngine.ManualOptions(cextrading.Input{
		BTCAmount:          btcAmount,
		RefPriceEUR:        refPriceEUR,
		TransferMinutes:    transferMinutes,
		Direction:          direction,
		ExcludedSpotCEXes:  excludedSpotCEXes,
		ExcludedHedgeCEXes: excludedHedgeCEXes,
	})
}

func (o *TradeRouteOptimizer) BestBreakdown(btcAmount float64, dexExchange string, direction string) CostBreakdown {
	return o.BestBreakdownWithConfirmations(btcAmount, dexExchange, direction, 0)
}

func (o *TradeRouteOptimizer) BestBreakdownWithConfirmations(btcAmount float64, dexExchange string, direction string, requiredConfirmations int) CostBreakdown {
	return o.BestBreakdownFull(btcAmount, 0, dexExchange, direction, requiredConfirmations, nil, "", false, nil)
}

// BestBreakdownFull finds the lowest-cost route, optionally excluding certain CEXes
// and/or forcing a specific BTC network fee tier.
// refPriceEUR is the CEX reference price used to size the hedge position
// simulation; pass 0 to skip the simulation.
// coupledHedge forces the hedge venue to match the spot CEX where a pairing exists.
func (o *TradeRouteOptimizer) BestBreakdownFull(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string, coupledHedge bool, realDepositPct *float64) CostBreakdown {
	return o.BestBreakdownFullWithExclusions(btcAmount, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedCEXes, excludedCEXes, feeTier, coupledHedge, realDepositPct)
}

func (o *TradeRouteOptimizer) BestBreakdownFullWithExclusions(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedSpotCEXes, excludedHedgeCEXes []string, feeTier string, coupledHedge bool, realDepositPct *float64) CostBreakdown {
	if o == nil || o.est == nil || btcAmount <= 0 {
		return CostBreakdown{}
	}
	// The routing method is fixed per DEX and offer side; the cheapest non-excluded
	// CEX (with its taker/withdrawal/hedge combo) is chosen inside breakdown via the
	// cextrading engine.
	return o.est.breakdown(btcAmount, refPriceEUR, dexExchange, direction, requiredConfirmations, feeTier, excludedSpotCEXes, excludedHedgeCEXes, coupledHedge, realDepositPct)
}

func (o *TradeRouteOptimizer) ManualRouteOptionsForTrade(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedCEXes []string, feeTier string) ManualRouteOptions {
	return o.ManualRouteOptionsForTradeWithExclusions(btcAmount, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedCEXes, excludedCEXes, feeTier)
}

func (o *TradeRouteOptimizer) ManualRouteOptionsForTradeWithExclusions(btcAmount float64, refPriceEUR float64, dexExchange string, direction string, requiredConfirmations int, excludedSpotCEXes, excludedHedgeCEXes []string, feeTier string) ManualRouteOptions {
	if o == nil || o.est == nil {
		return ManualRouteOptions{}
	}
	return o.est.ManualRouteOptionsForTradeWithExclusions(btcAmount, refPriceEUR, dexExchange, direction, requiredConfirmations, excludedSpotCEXes, excludedHedgeCEXes, feeTier)
}

func (o *TradeRouteOptimizer) AbortCostFromBreakdown(bd CostBreakdown) AbortCostBreakdown {
	return AbortCostFromBreakdown(bd)
}
