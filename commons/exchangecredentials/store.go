// Package exchangecredentials loads active exchange API credentials from the
// shared accounts/api_keys tables.
package exchangecredentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"commons/credentialcrypto"
)

const queryTimeout = 5 * time.Second

// Credentials contains one active exchange API credential set.
type Credentials struct {
	APIKey     string
	APISecret  string
	Passphrase string
}

// Provider resolves credentials for a canonical exchange name such as
// "Kraken", "Binance", or "Coinbase".
type Provider func(exchange string) (Credentials, error)

// Store resolves encrypted credentials from the shared Postgres schema.
type Store struct {
	db *sql.DB
}

// NewStore creates a database-backed credential store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Resolve returns the most recently updated active credential set. Coinbase
// prefers a credential carrying a passphrase so the same resolver can serve
// INTX futures operations.
func (s *Store) Resolve(exchange string) (Credentials, error) {
	credentials, err := s.ResolveAll(exchange)
	if err != nil {
		return Credentials{}, err
	}
	for _, credential := range credentials {
		if !strings.EqualFold(exchange, "Coinbase") || credential.Passphrase != "" {
			return credential, nil
		}
	}
	return credentials[0], nil
}

// ResolveAll returns all active credential sets, newest first. Exchanges may
// use distinct keys for spot and derivatives permissions.
func (s *Store) ResolveAll(exchange string) ([]Credentials, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("exchange credential database is not configured")
	}
	provider := strings.TrimSpace(exchange)
	if provider == "" {
		return nil, errors.New("exchange provider is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT api_keys.api_key,
		       COALESCE(api_keys.api_secret, ''),
		       COALESCE(api_keys.passphrase, '')
		FROM api_keys
		JOIN accounts ON accounts.id = api_keys.account_id
		WHERE LOWER(TRIM(accounts.provider::text)) = LOWER(TRIM($1))
		  AND api_keys.is_active = TRUE
		ORDER BY api_keys.updated_at DESC, api_keys.id DESC
	`, provider)
	if err != nil {
		return nil, fmt.Errorf("load active API keys for %s: %w", provider, err)
	}
	defer rows.Close()

	var credentialsList []Credentials
	for rows.Next() {
		var encrypted Credentials
		if err := rows.Scan(&encrypted.APIKey, &encrypted.APISecret, &encrypted.Passphrase); err != nil {
			return nil, fmt.Errorf("scan active API key for %s: %w", provider, err)
		}
		credentials, err := decrypt(encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt active API key for %s: %w", provider, err)
		}
		if credentials.APIKey == "" || credentials.APISecret == "" {
			continue
		}
		credentialsList = append(credentialsList, credentials)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active API keys for %s: %w", provider, err)
	}
	if len(credentialsList) > 0 {
		return credentialsList, nil
	}
	return nil, fmt.Errorf("no active API credentials configured for %s", provider)
}

func decrypt(encrypted Credentials) (Credentials, error) {
	apiKey, err := credentialcrypto.Decrypt(encrypted.APIKey)
	if err != nil {
		return Credentials{}, err
	}
	apiSecret, err := credentialcrypto.Decrypt(encrypted.APISecret)
	if err != nil {
		return Credentials{}, err
	}
	passphrase, err := credentialcrypto.Decrypt(encrypted.Passphrase)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{
		APIKey:     strings.TrimSpace(apiKey),
		APISecret:  strings.TrimSpace(apiSecret),
		Passphrase: strings.TrimSpace(passphrase),
	}, nil
}
