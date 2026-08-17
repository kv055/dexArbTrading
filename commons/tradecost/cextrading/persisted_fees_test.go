package cextrading

import (
	"database/sql"
	"testing"
	"time"
)

func TestPersistedFeesUseCompleteLastKnownData(t *testing.T) {
	now := time.Now()
	fees := PersistedFees{
		SpotTakerFeeRate:       sql.NullFloat64{Float64: 0.001, Valid: true},
		BTCWithdrawalFeeBTC:    sql.NullFloat64{Float64: 0.00001, Valid: true},
		FuturesOpenTakerRate:   sql.NullFloat64{Float64: 0.0004, Valid: true},
		FuturesCloseTakerRate:  sql.NullFloat64{Float64: 0.0004, Valid: true},
		FuturesFundingRate:     sql.NullFloat64{Float64: 0.0001, Valid: true},
		FundingIntervalSeconds: sql.NullInt64{Int64: 28800, Valid: true},
		FetchedAt:              sql.NullTime{Time: now.Add(-59 * time.Minute), Valid: true},
	}
	if !fees.spotAvailable(now, true) || !fees.hedgeAvailable(now) {
		t.Fatal("fresh complete fee snapshot should be usable")
	}
	fees.FetchedAt.Time = now.Add(-14 * time.Hour)
	if !fees.spotAvailable(now, true) || !fees.hedgeAvailable(now) {
		t.Fatal("complete last-known snapshot should remain usable")
	}
	if !fees.Stale(now) {
		t.Fatal("old snapshot should still be reported as stale")
	}
	fees.FetchedAt.Time = now
	fees.BTCWithdrawalFeeBTC.Valid = false
	if fees.spotAvailable(now, true) {
		t.Fatal("bid route must require a stored withdrawal fee")
	}
}
