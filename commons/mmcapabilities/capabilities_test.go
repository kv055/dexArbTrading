package mmcapabilities

import (
	"testing"
	"time"
)

func TestEvaluateCombinesFreshComplementaryKeys(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	state := evaluate(providers[0], accountResult{id: 7, keys: []keyResult{
		{checkedAt: &now, permissions: map[string]string{
			PermFetchDepositAddress: StatusGranted,
			PermSpotTrading:         StatusGranted,
			PermReadBalances:        StatusDenied,
			PermWithdraw:            StatusDenied,
			PermHedgePositions:      StatusGranted,
		}},
		{checkedAt: &now, permissions: map[string]string{
			PermReadBalances: StatusGranted,
			PermWithdraw:     StatusGranted,
		}},
	}}, DefaultPolicy(), now, DefaultMaxAge)

	if !state.Spot.EffectiveEnabled || !state.Hedge.EffectiveEnabled {
		t.Fatalf("fresh complementary keys should enable both legs: %+v", state)
	}
}

func TestEvaluateFailsClosedForStaleOrFailedChecks(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-DefaultMaxAge - time.Second)
	state := evaluate(providers[0], accountResult{id: 7, keys: []keyResult{{
		checkedAt: &stale,
		permissions: map[string]string{
			PermFetchDepositAddress: StatusGranted,
			PermSpotTrading:         StatusGranted,
			PermReadBalances:        StatusGranted,
			PermWithdraw:            StatusGranted,
			PermHedgePositions:      StatusGranted,
		},
	}}}, DefaultPolicy(), now, DefaultMaxAge)

	if state.Spot.EffectiveEnabled || state.Hedge.EffectiveEnabled {
		t.Fatalf("stale permissions must fail closed: %+v", state)
	}
	if len(state.Spot.Blockers) == 0 || state.Spot.Blockers[0].Code != BlockStale {
		t.Fatalf("spot blockers = %+v, want stale", state.Spot.Blockers)
	}
}

func TestEvaluateKeepsPolicySeparateFromCapability(t *testing.T) {
	now := time.Now().UTC()
	state := evaluate(providers[0], accountResult{id: 7, keys: []keyResult{{
		checkedAt: &now,
		permissions: map[string]string{
			PermFetchDepositAddress: StatusGranted,
			PermSpotTrading:         StatusGranted,
			PermReadBalances:        StatusGranted,
			PermWithdraw:            StatusGranted,
			PermHedgePositions:      StatusGranted,
		},
	}}}, Policy{SpotEnabled: false, HedgeEnabled: true}, now, DefaultMaxAge)

	if !state.Spot.Available || state.Spot.EffectiveEnabled {
		t.Fatalf("operator policy should disable an otherwise available spot leg: %+v", state.Spot)
	}
	if err := RequireSpot(state); err == nil {
		t.Fatal("RequireSpot should reject the policy-disabled leg")
	}
}

func TestRegistryIncludesCostOnlyProviderAsUnsupportedForExecution(t *testing.T) {
	bitstamp, ok := ProviderByID("Bitstamp")
	if !ok || !bitstamp.CostSupported || bitstamp.ExecutionSupported {
		t.Fatalf("bitstamp registry entry = %+v, %v", bitstamp, ok)
	}
	state := evaluate(bitstamp, accountResult{}, DefaultPolicy(), time.Now().UTC(), DefaultMaxAge)
	if state.Spot.EffectiveEnabled || len(state.Spot.Blockers) == 0 || state.Spot.Blockers[0].Code != BlockUnsupported {
		t.Fatalf("bitstamp spot state = %+v", state.Spot)
	}
}
