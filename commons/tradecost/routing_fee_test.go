package tradecost

import (
	"encoding/json"
	"testing"

	routing "commons/tradecost/txrouting"
)

func TestProfileForMethodRoundTrip(t *testing.T) {
	cases := []struct {
		method  string
		profile string
	}{
		{string(routing.MethodOnChainDirect), RoutingProfileDirectMain2Main},
		{string(routing.MethodOnChainHubWallet), RoutingProfileHubMain2Main},
		{string(routing.MethodOnChainSwap), RoutingProfileSwapMain2Lightning},
	}
	for _, tc := range cases {
		got := ProfileForMethod(tc.method)
		if got != tc.profile {
			t.Fatalf("ProfileForMethod(%q) = %q, want %q", tc.method, got, tc.profile)
		}
		if back := MethodForProfile(got); back != tc.method {
			t.Fatalf("MethodForProfile(%q) = %q, want %q", got, back, tc.method)
		}
	}
}

func TestBuildRoutingFeeDetailHubSplitsLegs(t *testing.T) {
	bd := CostBreakdown{
		RouteMethod:    string(routing.MethodOnChainHubWallet),
		NetworkFeeBTC:  0.0002,
		NumNetworkTxs:  2,
		NetworkFeeTier: "standard",
	}
	d := BuildRoutingFeeDetail(bd)
	if d.OnchainFeeBTC != 0.0002 || d.ConversionFeeBTC != 0 || d.NumOnchainTxs != 2 {
		t.Fatalf("detail sums = %+v", d)
	}
	if len(d.Legs) != 2 || d.Legs[0].From != "cex" || d.Legs[0].To != "lnd" || d.Legs[1].To != "dex" {
		t.Fatalf("legs = %+v", d.Legs)
	}
	if d.Legs[0].FeeBTC != 0.0001 || d.Legs[1].FeeBTC != 0.0001 {
		t.Fatalf("leg fees = %+v", d.Legs)
	}
}

func TestBuildRoutingFeeDetailSwapIncludesConversion(t *testing.T) {
	bd := CostBreakdown{
		RouteMethod:   string(routing.MethodOnChainSwap),
		NetworkFeeBTC: 0.00005,
		NumNetworkTxs: 1,
		Conversion: ConversionBreakdown{
			Strategy:  routing.StrategySubmarineSwap,
			FeeSumBTC: 0.00002,
		},
	}
	d := BuildRoutingFeeDetail(bd)
	if d.ConversionFeeBTC != 0.00002 || d.Conversion == nil || d.Conversion.Strategy != routing.StrategySubmarineSwap {
		t.Fatalf("conversion = %+v", d)
	}
	if len(d.Legs) != 1 || d.Legs[0].To != "lnd" {
		t.Fatalf("legs = %+v", d.Legs)
	}
	if sum := RoutingFeeSumBTC(bd); sum != 0.00007 {
		t.Fatalf("RoutingFeeSumBTC = %.8f, want 0.00007", sum)
	}
	raw, err := MarshalRoutingFeeDetail(d)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip["onchain_fee_btc"].(float64) != 0.00005 {
		t.Fatalf("json = %s", raw)
	}
}
