package cextrading

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"commons/coinbaseauth"
	"commons/exchangecredentials"
)

// CoinbaseFeeObservation is an actual network fee from a completed Coinbase
// App BTC send. AmountBTC is always a decimal BTC amount.
type CoinbaseFeeObservation struct {
	TransactionID string
	AmountBTC     float64
	ObservedAt    time.Time
	TxHash        string
}

// FetchCoinbaseSendFeeObservations inspects up to 100 recent transactions and
// returns BTC send fees observed within lookback. Coinbase has no documented
// reliable preflight fee quote, so these are historical inputs, not quotations.
func FetchCoinbaseSendFeeObservations(credentials exchangecredentials.Credentials, lookback time.Duration) ([]CoinbaseFeeObservation, error) {
	if strings.TrimSpace(credentials.Passphrase) != "" {
		return nil, fmt.Errorf("coinbase App fee history requires a CDP key without an INTX passphrase")
	}
	if lookback <= 0 {
		lookback = 30 * 24 * time.Hour
	}
	accountID, err := coinbaseBTCAccountID(credentials)
	if err != nil {
		return nil, err
	}
	path := "/v2/accounts/" + url.PathEscape(accountID) + "/transactions"
	var response struct {
		Data []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
			Network   struct {
				Hash           string `json:"hash"`
				TransactionFee struct {
					Amount   string `json:"amount"`
					Currency string `json:"currency"`
				} `json:"transaction_fee"`
			} `json:"network"`
		} `json:"data"`
	}
	if err := coinbaseCDPJSON(credentials, http.MethodGet, path+"?limit=100", &response); err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-lookback)
	observations := make([]CoinbaseFeeObservation, 0)
	for _, row := range response.Data {
		if !strings.EqualFold(row.Type, "send") || !strings.EqualFold(row.Network.TransactionFee.Currency, "BTC") {
			continue
		}
		observedAt, err := time.Parse(time.RFC3339, row.CreatedAt)
		if err != nil || observedAt.Before(cutoff) {
			continue
		}
		amount, err := strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(row.Network.TransactionFee.Amount), "-"), 64)
		if err != nil || amount < 0 || strings.TrimSpace(row.ID) == "" {
			continue
		}
		observations = append(observations, CoinbaseFeeObservation{TransactionID: row.ID, AmountBTC: amount, ObservedAt: observedAt, TxHash: row.Network.Hash})
	}
	return observations, nil
}

func coinbaseBTCAccountID(credentials exchangecredentials.Credentials) (string, error) {
	var response struct {
		Data struct {
			ID       string `json:"id"`
			Currency struct {
				Code string `json:"code"`
			} `json:"currency"`
		} `json:"data"`
	}
	primaryErr := coinbaseCDPJSON(credentials, http.MethodGet, "/v2/accounts/BTC", &response)
	if primaryErr == nil && strings.TrimSpace(response.Data.ID) != "" && strings.EqualFold(response.Data.Currency.Code, "BTC") {
		return response.Data.ID, nil
	}
	var advanced struct {
		Accounts []struct {
			UUID     string `json:"uuid"`
			Currency string `json:"currency"`
		} `json:"accounts"`
	}
	if err := coinbaseCDPJSON(credentials, http.MethodGet, "/api/v3/brokerage/accounts?limit=250", &advanced); err == nil {
		for _, account := range advanced.Accounts {
			if strings.EqualFold(account.Currency, "BTC") && strings.TrimSpace(account.UUID) != "" {
				return account.UUID, nil
			}
		}
	} else if primaryErr == nil {
		primaryErr = err
	}
	if primaryErr != nil {
		return "", fmt.Errorf("coinbase App BTC account lookup: %w", primaryErr)
	}
	return "", fmt.Errorf("coinbase App BTC account was not found")
}

func coinbaseCDPJSON(credentials exchangecredentials.Credentials, method, path string, destination any) error {
	signPath := path
	if query := strings.IndexByte(signPath, '?'); query >= 0 {
		signPath = signPath[:query]
	}
	token, err := coinbaseauth.BuildJWT(credentials.APIKey, credentials.APISecret, method, "api.coinbase.com", signPath)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, "https://api.coinbase.com"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("coinbase App %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coinbase App %s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("coinbase App decode %s: %w", path, err)
	}
	return nil
}
