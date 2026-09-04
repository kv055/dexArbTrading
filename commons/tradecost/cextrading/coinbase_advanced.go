package cextrading

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"commons/coinbaseauth"
	"commons/exchangecredentials"
)

const coinbaseAdvancedFeePath = "/api/v3/brokerage/transaction_summary?product_type=SPOT"

func fetchCoinbaseAdvancedSpotTakerRate(credentials exchangecredentials.Credentials) (float64, error) {
	token, err := buildCoinbaseAdvancedJWT(credentials.APIKey, credentials.APISecret, http.MethodGet, "/api/v3/brokerage/transaction_summary")
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.coinbase.com"+coinbaseAdvancedFeePath, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("coinbase advanced fee summary: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("coinbase advanced fee summary: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("coinbase advanced fee summary: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		FeeTier struct {
			TakerFeeRate string `json:"taker_fee_rate"`
		} `json:"fee_tier"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("coinbase advanced fee summary: decode: %w", err)
	}
	rate, err := strconv.ParseFloat(result.FeeTier.TakerFeeRate, 64)
	if err != nil || rate < 0 {
		return 0, fmt.Errorf("coinbase advanced fee summary: invalid taker rate %q", result.FeeTier.TakerFeeRate)
	}
	return rate, nil
}

func buildCoinbaseAdvancedJWT(keyName, privateKey, method, path string) (string, error) {
	return coinbaseauth.BuildJWT(keyName, privateKey, method, "api.coinbase.com", path)
}
