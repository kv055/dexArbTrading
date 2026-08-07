package tradecost

import (
	"encoding/json"

	routing "commons/tradecost/txrouting"
)

// Routing profile labels persisted on offers.routing_profile.
const (
	RoutingProfileDirectMain2Main    = "Direct_main2main"
	RoutingProfileHubMain2Main       = "Hub_main2main"
	RoutingProfileSwapMain2Lightning = "Swap_main2lightning"
)

// RoutingFeeLeg is one physical hop in a BTC route.
type RoutingFeeLeg struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	FeeBTC float64 `json:"fee_btc"`
}

// RoutingFeeDetail is the JSON shape stored in offers.routing_fee_detail.
type RoutingFeeDetail struct {
	OnchainFeeBTC    float64               `json:"onchain_fee_btc"`
	ConversionFeeBTC float64               `json:"conversion_fee_btc"`
	NumOnchainTxs    int                   `json:"num_onchain_txs"`
	Legs             []RoutingFeeLeg       `json:"legs"`
	Onchain          *BTCNetworkFeeDetail  `json:"onchain,omitempty"`
	Conversion       *ConversionBreakdown  `json:"conversion,omitempty"`
	Tier             string                `json:"tier,omitempty"`
	Confidence       float64               `json:"confidence,omitempty"`
}

// ProfileForMethod maps a txrouting Kind to the persisted routing_profile enum.
func ProfileForMethod(method string) string {
	switch routing.Kind(method) {
	case routing.MethodOnChainHubWallet:
		return RoutingProfileHubMain2Main
	case routing.MethodOnChainSwap:
		return RoutingProfileSwapMain2Lightning
	case routing.MethodOnChainDirect:
		return RoutingProfileDirectMain2Main
	default:
		return ""
	}
}

// MethodForProfile maps a persisted routing_profile back to a txrouting Kind.
func MethodForProfile(profile string) string {
	switch profile {
	case RoutingProfileHubMain2Main:
		return string(routing.MethodOnChainHubWallet)
	case RoutingProfileSwapMain2Lightning:
		return string(routing.MethodOnChainSwap)
	case RoutingProfileDirectMain2Main:
		return string(routing.MethodOnChainDirect)
	default:
		return ""
	}
}

// RoutingFeeSumBTC is on-chain miner fees plus Lightning conversion fees.
func RoutingFeeSumBTC(bd CostBreakdown) float64 {
	return round8(bd.NetworkFeeBTC + bd.Conversion.FeeSumBTC)
}

// BuildRoutingFeeDetail builds the persisted JSON detail for a cost breakdown.
func BuildRoutingFeeDetail(bd CostBreakdown) RoutingFeeDetail {
	onchain := round8(bd.NetworkFeeBTC)
	conversion := round8(bd.Conversion.FeeSumBTC)
	n := bd.NumNetworkTxs
	if n <= 0 {
		n = 1
	}

	detail := RoutingFeeDetail{
		OnchainFeeBTC:    onchain,
		ConversionFeeBTC: conversion,
		NumOnchainTxs:    n,
		Legs:             routingLegs(bd.RouteMethod, onchain, n),
		Onchain:          bd.NetworkFeeDetail,
		Tier:             bd.NetworkFeeTier,
		Confidence:       bd.NetworkFeeConfidence,
	}
	if bd.Conversion.Strategy != "" || conversion > 0 {
		c := bd.Conversion
		detail.Conversion = &c
	}
	return detail
}

func routingLegs(method string, onchainFeeBTC float64, numTxs int) []RoutingFeeLeg {
	switch routing.Kind(method) {
	case routing.MethodOnChainHubWallet:
		per := round8(onchainFeeBTC / 2)
		if numTxs < 2 {
			numTxs = 2
		}
		// Equal split matches the current 2× identical-hop pricing model.
		_ = numTxs
		return []RoutingFeeLeg{
			{From: "cex", To: "lnd", FeeBTC: per},
			{From: "lnd", To: "dex", FeeBTC: per},
		}
	case routing.MethodOnChainSwap:
		return []RoutingFeeLeg{
			{From: "cex", To: "lnd", FeeBTC: onchainFeeBTC},
		}
	default: // direct / unknown
		return []RoutingFeeLeg{
			{From: "cex", To: "dex", FeeBTC: onchainFeeBTC},
		}
	}
}

// MarshalRoutingFeeDetail JSON-encodes a routing fee detail for DB storage.
func MarshalRoutingFeeDetail(d RoutingFeeDetail) ([]byte, error) {
	return json.Marshal(d)
}

// UnmarshalRoutingFeeDetail parses offers.routing_fee_detail JSON.
func UnmarshalRoutingFeeDetail(raw []byte) (RoutingFeeDetail, error) {
	if len(raw) == 0 {
		return RoutingFeeDetail{}, nil
	}
	var d RoutingFeeDetail
	err := json.Unmarshal(raw, &d)
	return d, err
}
