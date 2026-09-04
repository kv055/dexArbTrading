package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"
)

// onChainSwap withdraws BTC on-chain from the CEX into our LND, then converts it to
// spendable Lightning locally so a Lightning DEX (RoboSats) can be paid. One on-chain
// transaction (CEX → our LND) plus a Boltz submarine swap into the manually
// managed long-lived channel. Channel opening remains an informational comparison.
type onChainSwap struct{ eng *Engine }

func (onChainSwap) Kind() Kind { return MethodOnChainSwap }

func (m onChainSwap) Cost(in Input) Cost {
	c := m.eng.onchainCost(MethodOnChainSwap, in, 1)
	feeRateSatVB := 0.0
	if c.Detail != nil {
		feeRateSatVB = float64(c.Detail.SelectedSatVB)
	}
	c.Conversion = m.eng.conversion(in.BTCAmount, feeRateSatVB)
	return c
}

// conversion prices the local on-chain → Lightning conversion for one payment.
func (e *Engine) conversion(amountBTC, feeRateSatVB float64) ConversionBreakdown {
	bf := e.boltz.get()
	routeFrac := 0.0
	if amountBTC > 0 && e.feeSrc != nil {
		routeFrac = e.feeSrc.EstimateLightningRoutingFee(amountBTC) / amountBTC
	}
	p := defaultSwapParams()
	p.FeeRate = feeRateSatVB
	p.ServiceFrac = bf.serviceFrac
	p.ClaimMinerSats = bf.minerSats
	p.RouteFrac = routeFrac
	return p.conversionBreakdown(amountBTC)
}

// ---------------------------------------------------------------------------
// Conversion cost model (ported from the standalone crossover analysis)
// ---------------------------------------------------------------------------

// Default virtual sizes (vBytes) of the on-chain txs in each conversion strategy.
const (
	defaultOpenVB   = 153.0 // channel funding tx
	defaultCloseVB  = 140.0 // cooperative close tx
	defaultLockupVB = 150.0 // swap lockup tx (we broadcast this)
)

// Boltz submarine-swap fee fallbacks, used when the live API is unavailable.
const (
	boltzServiceFracFallback = 0.001 // 0.1% of amount
	boltzMinerSatsFallback   = 150.0 // provider per-swap miner charge, sats
)

const boltzSubmarineURL = "https://api.boltz.exchange/v2/swap/submarine"

// Conversion strategy labels, also surfaced in the API breakdown.
const (
	StrategySubmarineSwap = "submarine_swap"
	StrategyChannelOpen   = "channel_open"
)

// ConversionBreakdown details the local on-chain → Lightning conversion. It is only
// populated for the OnChainSwap method; Strategy is "" otherwise. Execution
// always selects submarine_swap; ChannelOpenBTC remains comparison-only.
type ConversionBreakdown struct {
	Strategy           string  `json:"strategy"`             // "submarine_swap" | "channel_open" | "" (n/a)
	ServiceFeePct      float64 `json:"service_fee_pct"`      // Boltz service fee as % of trade (swap strategy)
	ServiceFeeBTC      float64 `json:"service_fee_btc"`      // Boltz service fee in BTC
	ProviderMinerBTC   float64 `json:"provider_miner_btc"`   // Boltz per-swap miner charge in BTC
	LockupFeeBTC       float64 `json:"lockup_fee_btc"`       // on-chain lockup tx fee we pay in BTC
	RoutingFeePct      float64 `json:"routing_fee_pct"`      // LN routing fee as % of trade
	RoutingFeeBTC      float64 `json:"routing_fee_btc"`      // LN routing fee in BTC
	ChannelOpenBTC     float64 `json:"channel_open_btc"`     // cost of the channel-open alternative in BTC
	FeeSumPct          float64 `json:"fee_sum_pct"`          // chosen strategy total as % of trade
	FeeSumBTC          float64 `json:"fee_sum_btc"`          // chosen strategy total in BTC
	CrossoverAmountBTC float64 `json:"crossover_amount_btc"` // payment size where swap and channel-open cost the same
}

// swapParams holds the inputs to the conversion cost model. Fractions are fractions
// (0.001 = 0.1%); sizes are virtual bytes; sats are sats.
type swapParams struct {
	FeeRate        float64 // on-chain fee rate, sat/vB
	OpenVB         float64
	CloseVB        float64
	LockupVB       float64
	ServiceFrac    float64 // Boltz service fee, fraction of amount
	ClaimMinerSats float64 // Boltz per-swap miner charge, sats
	RouteFrac      float64 // LN routing fee, fraction of amount
	CloseChannels  bool    // count an eventual cooperative close per channel?
}

func defaultSwapParams() swapParams {
	return swapParams{
		OpenVB:        defaultOpenVB,
		CloseVB:       defaultCloseVB,
		LockupVB:      defaultLockupVB,
		CloseChannels: true,
	}
}

// channelOpenCostSats is the on-chain cost of opening one channel (plus close if counted).
func (p swapParams) channelOpenCostSats() float64 {
	c := p.OpenVB * p.FeeRate
	if p.CloseChannels {
		c += p.CloseVB * p.FeeRate
	}
	return c
}

// swapPerPaymentCostSats is the cost of converting one payment of amountSats via a
// submarine swap into an already-open (reused) channel.
func (p swapParams) swapPerPaymentCostSats(amountSats float64) float64 {
	return p.LockupVB*p.FeeRate + p.ServiceFrac*amountSats + p.ClaimMinerSats + p.RouteFrac*amountSats
}

// crossoverSteadyStateSats is the per-payment amount at which the two strategies cost
// the same (one-time channel terms amortised away). Below it the swap is cheaper;
// above it opening a fresh channel per payment is cheaper. ok=false when there is no
// percentage fee to solve against.
func (p swapParams) crossoverSteadyStateSats() (sats float64, ok bool) {
	denom := p.ServiceFrac + p.RouteFrac
	if denom == 0 {
		return 0, false
	}
	num := p.channelOpenCostSats() - p.LockupVB*p.FeeRate - p.ClaimMinerSats
	return num / denom, true
}

// conversionBreakdown prices executable submarine-swap conversion while retaining
// channel-open cost and crossover as informational comparison fields.
func (p swapParams) conversionBreakdown(amountBTC float64) ConversionBreakdown {
	amountSats := amountBTC * 1e8
	serviceSats := p.ServiceFrac * amountSats
	routingSats := p.RouteFrac * amountSats
	lockupSats := p.LockupVB * p.FeeRate
	swapSats := lockupSats + serviceSats + p.ClaimMinerSats + routingSats
	channelSats := p.channelOpenCostSats()
	crossoverSats, _ := p.crossoverSteadyStateSats()

	totalSats := swapSats
	strategy := StrategySubmarineSwap

	pct := func(sats float64) float64 {
		if amountBTC <= 0 {
			return 0
		}
		return round4(sats / 1e8 / amountBTC * 100)
	}
	return ConversionBreakdown{
		Strategy:           strategy,
		ServiceFeePct:      pct(serviceSats),
		ServiceFeeBTC:      round8(serviceSats / 1e8),
		ProviderMinerBTC:   round8(p.ClaimMinerSats / 1e8),
		LockupFeeBTC:       round8(lockupSats / 1e8),
		RoutingFeePct:      pct(routingSats),
		RoutingFeeBTC:      round8(routingSats / 1e8),
		ChannelOpenBTC:     round8(channelSats / 1e8),
		FeeSumPct:          pct(totalSats),
		FeeSumBTC:          round8(totalSats / 1e8),
		CrossoverAmountBTC: round8(crossoverSats / 1e8),
	}
}

func round8(v float64) float64 { return math.Round(v*1e8) / 1e8 }
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// ---------------------------------------------------------------------------
// Boltz submarine-swap fee fetcher (clearnet, no API key)
// ---------------------------------------------------------------------------

var httpClient = &http.Client{Timeout: 10 * time.Second}

type boltzFees struct {
	serviceFrac float64
	minerSats   float64
}

// boltzFeeCache is a single-flight, TTL-guarded cache of the live Boltz submarine fees.
type boltzFeeCache struct {
	mu        sync.Mutex
	value     boltzFees
	fetchedAt time.Time
	ttl       time.Duration
	inflight  chan struct{}
}

func newBoltzFeeCache(ttl time.Duration) *boltzFeeCache { return &boltzFeeCache{ttl: ttl} }

func (c *boltzFeeCache) get() boltzFees {
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
		c.mu.Lock()
		v := c.value
		c.mu.Unlock()
		return v
	}
	ch := make(chan struct{})
	c.inflight = ch
	c.mu.Unlock()

	v, err := fetchBoltzSubmarineFees()
	c.mu.Lock()
	if err == nil {
		c.value = v
		c.fetchedAt = time.Now()
		fmt.Printf("[routing] boltz submarine fees updated: service=%.4f%% miner=%.0f sats (live)\n", v.serviceFrac*100, v.minerSats)
	} else {
		if c.fetchedAt.IsZero() {
			c.value = boltzFees{serviceFrac: boltzServiceFracFallback, minerSats: boltzMinerSatsFallback}
			fmt.Printf("[routing] boltz submarine fees fetch failed (%v), using fallback service=%.4f%% miner=%.0f sats\n",
				err, c.value.serviceFrac*100, c.value.minerSats)
		} else {
			fmt.Printf("[routing] boltz submarine fees refresh failed (%v), keeping cached\n", err)
		}
		// Stamp fetchedAt even on failure so we don't retry every call for the TTL.
		c.fetchedAt = time.Now()
	}
	result := c.value
	close(ch)
	c.inflight = nil
	c.mu.Unlock()
	return result
}

// This read-only shared cost estimator consumes public fee metadata. Swap
// creation, signing, claiming and refunding live exclusively in mm-controller
// and use the official Boltz client library there.
func fetchBoltzSubmarineFees() (boltzFees, error) {
	req, err := http.NewRequest(http.MethodGet, boltzSubmarineURL, nil)
	if err != nil {
		return boltzFees{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return boltzFees{}, fmt.Errorf("boltz: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return boltzFees{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return boltzFees{}, fmt.Errorf("boltz GET %s: status %d", boltzSubmarineURL, resp.StatusCode)
	}
	var parsed map[string]map[string]struct {
		Fees struct {
			Percentage float64         `json:"percentage"`
			MinerFees  json.RawMessage `json:"minerFees"`
		} `json:"fees"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return boltzFees{}, fmt.Errorf("boltz: decode: %w", err)
	}
	pair, ok := parsed["BTC"]["BTC"]
	if !ok {
		return boltzFees{}, errors.New("boltz response missing BTC->BTC submarine pair")
	}
	return boltzFees{
		serviceFrac: pair.Fees.Percentage / 100.0,
		minerSats:   parseBoltzMinerFees(pair.Fees.MinerFees),
	}, nil
}

func parseBoltzMinerFees(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var obj map[string]float64
	if err := json.Unmarshal(raw, &obj); err == nil {
		sum := 0.0
		for _, v := range obj {
			sum += v
		}
		return sum
	}
	return 0
}
