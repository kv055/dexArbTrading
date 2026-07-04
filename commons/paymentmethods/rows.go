package paymentmethods

import (
	"context"
	"database/sql"
	"fmt"
)

// MethodRow mirrors one payment_methods table row. Category holds the display
// name exactly as stored in the DB (e.g. "Fiat Transmitter"); use
// CategoryIDFromDisplay to get the API category ID.
type MethodRow struct {
	ID              int64
	Name            string
	MergeRegex      string // "" when NULL: exact-name matching only
	Category        string
	BankAccountID   *int64
	AccountID       *int64
	WalletAddressID *int64
	IsActive        bool
	IsLinked        bool
	Priority        int
	MatchOrder      int
}

// LoadRows fetches every payment_methods row ordered for canonicalization
// (match_order, then id). Uses only database/sql so callers on GORM reach it
// via gormDB.DB().
func LoadRows(ctx context.Context, db *sql.DB) ([]MethodRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, COALESCE(merge_regex, ''), category,
		       bank_account_id, account_id, wallet_address_id,
		       is_active, is_linked, priority, match_order
		FROM payment_methods
		ORDER BY match_order, id`)
	if err != nil {
		return nil, fmt.Errorf("load payment_methods: %w", err)
	}
	defer rows.Close()

	var out []MethodRow
	for rows.Next() {
		var r MethodRow
		if err := rows.Scan(&r.ID, &r.Name, &r.MergeRegex, &r.Category,
			&r.BankAccountID, &r.AccountID, &r.WalletAddressID,
			&r.IsActive, &r.IsLinked, &r.Priority, &r.MatchOrder); err != nil {
			return nil, fmt.Errorf("scan payment_methods row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment_methods: %w", err)
	}
	return out, nil
}
