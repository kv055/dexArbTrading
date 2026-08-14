package cextrading

import (
	"database/sql"
	"testing"
	"time"
)

func TestPersistedFeesRequireFreshCompleteData(t *testing.T) {
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
	fees.FetchedAt.Time = now.Add(-time.Hour - time.Nanosecond)
	if fees.spotAvailable(now, true) || fees.hedgeAvailable(now) {
		t.Fatal("one-hour-old snapshot must be stale")
	}
	fees.FetchedAt.Time = now
	fees.BTCWithdrawalFeeBTC.Valid = false
	if fees.spotAvailable(now, true) {
		t.Fatal("bid route must require a stored withdrawal fee")
	}
}
