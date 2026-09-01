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
	SpotTakerFeeRate          sql.NullFloat64
	BTCWithdrawalFeeBTC       sql.NullFloat64
	FuturesOpenTakerRate      sql.NullFloat64
	FuturesCloseTakerRate     sql.NullFloat64
	FuturesFundingRate        sql.NullFloat64
	FundingIntervalSeconds    sql.NullInt64
	FetchedAt                 sql.NullTime
	LastAttemptedAt           sql.NullTime
	LastError                 sql.NullString
	SpotFetchedAt             sql.NullTime
	SpotLastAttemptedAt       sql.NullTime
	SpotLastError             sql.NullString
	WithdrawalFetchedAt       sql.NullTime
	WithdrawalLastAttemptedAt sql.NullTime
	WithdrawalLastError       sql.NullString
	WithdrawalFeeSource       sql.NullString
	WithdrawalFeeObservedBTC  sql.NullFloat64
	WithdrawalFeeObservedAt   sql.NullTime
	HedgeFetchedAt            sql.NullTime
	HedgeLastAttemptedAt      sql.NullTime
	HedgeLastError            sql.NullString
}

// FeeSnapshotStaleAfter is an operator-facing freshness threshold. Complete
// exchange-derived snapshots remain usable after this age; refresh failures are
// surfaced as warnings instead of removing an otherwise valid route.
const FeeSnapshotStaleAfter = time.Hour

func (f PersistedFees) Stale(now time.Time) bool {
	return f.FetchedAt.Valid && now.Sub(f.FetchedAt.Time) > FeeSnapshotStaleAfter
}

func componentStale(at sql.NullTime, now time.Time) bool {
	return !at.Valid || now.Sub(at.Time) > FeeSnapshotStaleAfter
}

func fallbackTime(component, aggregate sql.NullTime) sql.NullTime {
	if component.Valid {
		return component
	}
	return aggregate
}

func (f PersistedFees) SpotStale(now time.Time) bool {
	return componentStale(fallbackTime(f.SpotFetchedAt, f.FetchedAt), now)
}
func (f PersistedFees) WithdrawalStale(now time.Time) bool {
	return componentStale(fallbackTime(f.WithdrawalFetchedAt, f.FetchedAt), now)
}
func (f PersistedFees) HedgeStale(now time.Time) bool {
	return componentStale(fallbackTime(f.HedgeFetchedAt, f.FetchedAt), now)
}

func (f PersistedFees) spotAvailable(_ time.Time, needsWithdrawal bool) bool {
	return fallbackTime(f.SpotFetchedAt, f.FetchedAt).Valid && f.SpotTakerFeeRate.Valid && (!needsWithdrawal || (fallbackTime(f.WithdrawalFetchedAt, f.FetchedAt).Valid && f.BTCWithdrawalFeeBTC.Valid))
}

func (f PersistedFees) hedgeAvailable(_ time.Time) bool {
	return fallbackTime(f.HedgeFetchedAt, f.FetchedAt).Valid && f.FuturesOpenTakerRate.Valid && f.FuturesCloseTakerRate.Valid &&
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
		       fees_fetched_at, fees_last_attempted_at, fees_last_error
		       , COALESCE(spot_fees_fetched_at, fees_fetched_at), COALESCE(spot_fees_last_attempted_at, fees_last_attempted_at), spot_fees_last_error
		       , COALESCE(withdrawal_fees_fetched_at, fees_fetched_at), COALESCE(withdrawal_fees_last_attempted_at, fees_last_attempted_at), withdrawal_fees_last_error
		       , withdrawal_fee_source, withdrawal_fee_observed_btc, withdrawal_fee_observed_at
		       , COALESCE(hedge_fees_fetched_at, fees_fetched_at), COALESCE(hedge_fees_last_attempted_at, fees_last_attempted_at), hedge_fees_last_error
		FROM mm_cex_provider_policies WHERE provider = $1
	`, provider).Scan(
		&fees.SpotTakerFeeRate, &fees.BTCWithdrawalFeeBTC,
		&fees.FuturesOpenTakerRate, &fees.FuturesCloseTakerRate,
		&fees.FuturesFundingRate, &fees.FundingIntervalSeconds,
		&fees.FetchedAt, &fees.LastAttemptedAt, &fees.LastError,
		&fees.SpotFetchedAt, &fees.SpotLastAttemptedAt, &fees.SpotLastError,
		&fees.WithdrawalFetchedAt, &fees.WithdrawalLastAttemptedAt, &fees.WithdrawalLastError,
		&fees.WithdrawalFeeSource, &fees.WithdrawalFeeObservedBTC, &fees.WithdrawalFeeObservedAt,
		&fees.HedgeFetchedAt, &fees.HedgeLastAttemptedAt, &fees.HedgeLastError,
	)
	if err != nil {
		return fees, fmt.Errorf("load persisted CEX fees for %s: %w", provider, err)
	}
	return fees, nil
}

// ReadPersistedFees exposes the persisted snapshot and its operational metadata
// to API status handlers without duplicating the optimizer's database contract.
func ReadPersistedFees(ctx context.Context, db *sql.DB, provider string) (PersistedFees, error) {
	return persistedFeeReader{db: db}.get(ctx, provider)
}
