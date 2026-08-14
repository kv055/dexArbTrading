package cextrading

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PersistedFees is the last successful exchange-derived fee snapshot for one
// provider. FundingRate is expressed per FundingInterval.
type PersistedFees struct {
	SpotTakerFeeRate       sql.NullFloat64
	BTCWithdrawalFeeBTC    sql.NullFloat64
	FuturesOpenTakerRate   sql.NullFloat64
	FuturesCloseTakerRate  sql.NullFloat64
	FuturesFundingRate     sql.NullFloat64
	FundingIntervalSeconds sql.NullInt64
	FetchedAt              sql.NullTime
	LastError              sql.NullString
}

const feeMaxAge = time.Hour

func (f PersistedFees) fresh(now time.Time) bool {
	return f.FetchedAt.Valid && now.Sub(f.FetchedAt.Time) <= feeMaxAge
}

func (f PersistedFees) spotAvailable(now time.Time, needsWithdrawal bool) bool {
	return f.fresh(now) && f.SpotTakerFeeRate.Valid && (!needsWithdrawal || f.BTCWithdrawalFeeBTC.Valid)
}

func (f PersistedFees) hedgeAvailable(now time.Time) bool {
	return f.fresh(now) && f.FuturesOpenTakerRate.Valid && f.FuturesCloseTakerRate.Valid &&
		f.FuturesFundingRate.Valid && f.FundingIntervalSeconds.Valid && f.FundingIntervalSeconds.Int64 > 0
}

type persistedFeeReader struct{ db *sql.DB }

func (r persistedFeeReader) get(ctx context.Context, provider string) (PersistedFees, error) {
	var fees PersistedFees
	if r.db == nil {
		return fees, fmt.Errorf("CEX fee database is not configured")
	}
	err := r.db.QueryRowContext(ctx, `
		SELECT spot_taker_fee_percent, withdrawal_fee_btc,
		       futures_open_taker_fee_percent, futures_close_taker_fee_percent,
		       futures_funding_fee_percent, futures_funding_interval_seconds,
		       fees_fetched_at, fees_last_error
		FROM mm_cex_provider_policies WHERE provider = $1
	`, provider).Scan(
		&fees.SpotTakerFeeRate, &fees.BTCWithdrawalFeeBTC,
		&fees.FuturesOpenTakerRate, &fees.FuturesCloseTakerRate,
		&fees.FuturesFundingRate, &fees.FundingIntervalSeconds,
		&fees.FetchedAt, &fees.LastError,
	)
	if err != nil {
		return fees, fmt.Errorf("load persisted CEX fees for %s: %w", provider, err)
	}
	return fees, nil
}
