// Package routing models how BTC physically moves from a CEX to a DEX. The route
// is dictated by the DEX's wallet mechanics and the offer side (bid/ask), not by
// which option is cheapest, so each DEX×side maps deterministically to exactly one
// of three methods:
//
//   - OnChainDirect   — CEX → a final destination we control / can whitelist (1 tx)
//   - OnChainHubWallet — CEX → our LND on-chain wallet → final destination (2 txs)
//   - OnChainSwap      — CEX → our LND, then a submarine swap into Lightning (1 tx + conversion)
//
// No CEX (Binance/Coinbase/Kraken) supports a Lightning withdrawal via API, so there
// is no "CEX pays over Lightning" method anywhere; Lightning DEXes (RoboSats) are
// reached only through OnChainSwap.
package routing

import (
	"math"
	"time"

	"commons/bitcoin"
)

// feeDataTTL is how long fetched on-chain/Boltz fee data is considered fresh.
const feeDataTTL = 30 * time.Minute

// Kind identifies a CEX→DEX routing method.
type Kind string

const (
	MethodOnChainDirect    Kind = "onchain_direct"
	MethodOnChainHubWallet Kind = "onchain_hub"
	MethodOnChainSwap      Kind = "onchain_swap"
)

// Side is the side of a DEX offer from our perspective.
type Side string

const (
	SideBid Side = "bid" // we sell BTC on the DEX (BTC leaves toward the counterparty/escrow)
	SideAsk Side = "ask" // we buy BTC on the DEX (BTC arrives at our own CEX deposit)
)

// SideForDirection maps a canonical DEX offer direction to our trade side.
// Anything other than "ask" is treated as the bid side, matching the rest of the
// cost model.
func SideForDirection(direction string) Side {
	if direction == "ask" {
		return SideAsk
	}
	return SideBid
}

// TxShape describes an on-chain transaction's structure for virtual-size estimation.
type TxShape struct {
	TxType  string // "P2WPKH", "P2TR", ...
	Inputs  int
	Outputs int
}

// Input carries everything a routing method needs to price a CEX→DEX transfer.
type Input struct {
	BTCAmount             float64
	RequiredConfirmations int
	FeeTier               string  // "", "economic", "standard", "fast", "express"
	WithdrawalTx          TxShape // shape of the on-chain hop(s); the hub forward hop is assumed the same native-segwit shape
}

// NetworkFeeDetail carries the full on-chain fee calculation context for display.
type NetworkFeeDetail struct {
	TxVBytes                        int64   `json:"tx_vbytes"` // calculated virtual size (per tx)
	TxType                          string  `json:"tx_type"`   // "P2WPKH", "P2TR", etc.
	TxInputs                        int     `json:"tx_inputs"`
	TxOutputs                       int     `json:"tx_outputs"`
	NumTxs                          int     `json:"num_txs"`         // number of on-chain transactions in this route
	SelectedSatVB                   int64   `json:"selected_sat_vb"` // fee rate for the chosen tier
	RequiredConfirmations           int     `json:"required_confirmations"`
	SelectedInclusionMinutes        int     `json:"selected_inclusion_minutes"`
	SelectedFullConfirmationMinutes int     `json:"selected_full_confirmation_minutes"`
	EconomicSatVB                   int64   `json:"economic_sat_vb"` // all-tier rates for comparison
	EconomicInclusionMinutes        int     `json:"economic_inclusion_minutes"`
	EconomicFullConfirmationMinutes int     `json:"economic_full_confirmation_minutes"`
	StandardSatVB                   int64   `json:"standard_sat_vb"`
	StandardInclusionMinutes        int     `json:"standard_inclusion_minutes"`
	StandardFullConfirmationMinutes int     `json:"standard_full_confirmation_minutes"`
	FastSatVB                       int64   `json:"fast_sat_vb"`
	FastInclusionMinutes            int     `json:"fast_inclusion_minutes"`
	FastFullConfirmationMinutes     int     `json:"fast_full_confirmation_minutes"`
	ExpressSatVB                    int64   `json:"express_sat_vb"`
	ExpressInclusionMinutes         int     `json:"express_inclusion_minutes"`
	ExpressFullConfirmationMinutes  int     `json:"express_full_confirmation_minutes"`
	CongestionLevel                 string  `json:"congestion_level"`
	MempoolSizeMB                   float64 `json:"mempool_size_mb"`
	UnconfirmedTxCount              int64   `json:"unconfirmed_tx_count"`
	DataAgeMinutes                  int     `json:"data_age_minutes"`
}

// Cost is the priced transfer produced by a routing method.
type Cost struct {
	Method        Kind
	NetworkFeeBTC float64 // total on-chain miner fee across all hops
	NumOnchainTxs int
	Tier          string
	Confidence    float64
	Detail        *NetworkFeeDetail
	Conversion    ConversionBreakdown // zero-value unless Method is OnChainSwap
}

// Method prices one routing method for a given Input.
type Method interface {
	Kind() Kind
	Cost(in Input) Cost
}

// onchainFeeSource is the subset of the bitcoin fee predictor the routing methods
// rely on. It keeps the package unit-testable with a fake.
type onchainFeeSource interface {
	IsDataStale(maxAge time.Duration) bool
	FetchLatestData() error
	GetDetailedRecommendationForTier(inputs, outputs int, txType string, requiredConfirmations int, tier string) (*bitcoin.DetailedFeeContext, error)
	EstimateLightningRoutingFee(amountBTC float64) float64
}

// Engine prices any routing Kind using a shared on-chain fee source and Boltz cache.
type Engine struct {
	feeSrc  onchainFeeSource
	boltz   *boltzFeeCache
	methods map[Kind]Method
}

// NewEngine builds an Engine backed by the bitcoin fee predictor.
func NewEngine(predictor *bitcoin.BitcoinFeePredictor) *Engine {
	return newEngineWithSource(predictor)
}

func newEngineWithSource(src onchainFeeSource) *Engine {
	e := &Engine{feeSrc: src, boltz: newBoltzFeeCache(feeDataTTL)}
	e.methods = map[Kind]Method{
		MethodOnChainDirect:    onChainDirect{eng: e},
		MethodOnChainHubWallet: onChainHubWallet{eng: e},
		MethodOnChainSwap:      onChainSwap{eng: e},
	}
	return e
}

// Cost prices the given routing Kind. An unknown Kind falls back to OnChainDirect.
func (e *Engine) Cost(kind Kind, in Input) Cost {
	m, ok := e.methods[kind]
	if !ok {
		m = e.methods[MethodOnChainDirect]
	}
	return m.Cost(in)
}

// onchainCost prices `hops` identical on-chain transactions (native-segwit shape) at
// the selected tier. It is the shared core of the direct/hub/swap methods.
func (e *Engine) onchainCost(kind Kind, in Input, hops int) Cost {
	if e.feeSrc == nil || in.BTCAmount <= 0 {
		return Cost{Method: kind, NumOnchainTxs: hops}
	}
	if e.feeSrc.IsDataStale(feeDataTTL) {
		_ = e.feeSrc.FetchLatestData() // best-effort; proceed with whatever is cached
	}

	ctx, err := e.feeSrc.GetDetailedRecommendationForTier(
		in.WithdrawalTx.Inputs,
		in.WithdrawalTx.Outputs,
		in.WithdrawalTx.TxType,
		in.RequiredConfirmations,
		in.FeeTier,
	)
	if err != nil || ctx == nil || ctx.Recommendation == nil {
		return Cost{Method: kind, NumOnchainTxs: hops}
	}

	detail := buildDetail(ctx, hops, in.RequiredConfirmations)
	confidence := 0.0
	if ctx.Estimate != nil {
		confidence = ctx.Estimate.EstimatedConfidence
	}
	return Cost{
		Method:        kind,
		NetworkFeeBTC: ctx.Recommendation.TotalFeeBTC * float64(hops),
		NumOnchainTxs: hops,
		Tier:          ctx.Recommendation.Tier,
		Confidence:    confidence,
		Detail:        detail,
	}
}

// buildDetail translates a predictor recommendation context into a NetworkFeeDetail.
func buildDetail(ctx *bitcoin.DetailedFeeContext, hops, requiredConfirmations int) *NetworkFeeDetail {
	if ctx.Estimate == nil {
		return nil
	}
	d := &NetworkFeeDetail{
		TxVBytes:                        ctx.Estimate.TransactionSize,
		TxType:                          ctx.TxType,
		TxInputs:                        ctx.TxInputs,
		TxOutputs:                       ctx.TxOutputs,
		NumTxs:                          hops,
		SelectedSatVB:                   ctx.Recommendation.FeeRateSatVB,
		RequiredConfirmations:           requiredConfirmations,
		SelectedInclusionMinutes:        int(ctx.Recommendation.EstimatedInclusionTime / time.Minute),
		SelectedFullConfirmationMinutes: int(ctx.Recommendation.EstimatedFullConfirmationTime / time.Minute),
		CongestionLevel:                 ctx.Estimate.NetworkCongestion,
		DataAgeMinutes:                  ctx.DataAgeMinutes,
	}
	if ctx.FeeRates != nil {
		d.EconomicSatVB = ctx.FeeRates.EconomicFee
		d.EconomicInclusionMinutes = int(ctx.Estimate.EconomicInclusionTime / time.Minute)
		d.EconomicFullConfirmationMinutes = int(ctx.Estimate.EconomicTime / time.Minute)
		d.StandardSatVB = ctx.FeeRates.StandardFee
		d.StandardInclusionMinutes = int(ctx.Estimate.StandardInclusionTime / time.Minute)
		d.StandardFullConfirmationMinutes = int(ctx.Estimate.StandardTime / time.Minute)
		d.FastSatVB = ctx.FeeRates.FastFee
		d.FastInclusionMinutes = int(ctx.Estimate.FastInclusionTime / time.Minute)
		d.FastFullConfirmationMinutes = int(ctx.Estimate.FastTime / time.Minute)
		d.ExpressSatVB = ctx.FeeRates.ExpressFee
		d.ExpressInclusionMinutes = int(ctx.Estimate.ExpressInclusionTime / time.Minute)
		d.ExpressFullConfirmationMinutes = int(ctx.Estimate.ExpressTime / time.Minute)
	}
	if ctx.NetworkStats != nil {
		d.MempoolSizeMB = math.Round(float64(ctx.NetworkStats.MempoolSize)/(1024*1024)*100) / 100
		d.UnconfirmedTxCount = ctx.NetworkStats.TransactionCount
	}
	return d
}
