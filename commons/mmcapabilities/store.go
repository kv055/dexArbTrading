package mmcapabilities

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) loadAccounts(ctx context.Context) (map[string]accountResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id,
		       a.provider,
		       k.id,
		       k.permissions,
		       k.permissions_checked_at,
		       k.permission_check_error
		FROM accounts a
		LEFT JOIN api_keys k
		  ON k.account_id = a.id
		 AND k.is_active = TRUE
		WHERE a.category = 'CEX'
		  AND a.is_active = TRUE
		ORDER BY a.provider, a.id, k.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query MM CEX capabilities: %w", err)
	}
	defer rows.Close()

	accounts := make(map[string]accountResult)
	for rows.Next() {
		var (
			accountID  int64
			provider   string
			keyID      sql.NullInt64
			raw        []byte
			checkedAt  sql.NullTime
			checkError sql.NullString
		)
		if err := rows.Scan(&accountID, &provider, &keyID, &raw, &checkedAt, &checkError); err != nil {
			return nil, fmt.Errorf("scan MM CEX capabilities: %w", err)
		}
		definition, ok := ProviderByID(provider)
		if !ok {
			continue
		}
		account := accounts[definition.ID]
		account.id = accountID
		if keyID.Valid {
			key := keyResult{permissions: decodePermissions(raw), checkError: strings.TrimSpace(checkError.String)}
			if key.checkError == "" {
				key.checkError = invalidPermissionsError(raw)
			}
			if checkedAt.Valid {
				checked := checkedAt.Time.UTC()
				key.checkedAt = &checked
			}
			account.keys = append(account.keys, key)
		}
		accounts[definition.ID] = account
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate MM CEX capabilities: %w", err)
	}
	return accounts, nil
}

func (s *Store) loadPolicies(ctx context.Context) (map[string]Policy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, spot_enabled, hedge_enabled, updated_at
		FROM mm_cex_provider_policies
	`)
	if err != nil {
		return nil, fmt.Errorf("query MM CEX provider policies: %w", err)
	}
	defer rows.Close()
	policies := make(map[string]Policy)
	for rows.Next() {
		var provider string
		var policy Policy
		var updatedAt sql.NullTime
		if err := rows.Scan(&provider, &policy.SpotEnabled, &policy.HedgeEnabled, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan MM CEX provider policy: %w", err)
		}
		if updatedAt.Valid {
			updated := updatedAt.Time.UTC()
			policy.UpdatedAt = &updated
		}
		if definition, ok := ProviderByID(provider); ok {
			policies[definition.ID] = policy
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate MM CEX provider policies: %w", err)
	}
	return policies, nil
}

// ParseMaxAge reads a deployment duration while preserving the safe default.
func ParseMaxAge(raw string) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return DefaultMaxAge
	}
	return value
}
