package cextrading

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	key, err := parseCoinbaseAdvancedKey(privateKey)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("coinbase advanced JWT nonce: %w", err)
	}
	header := map[string]any{"alg": "ES256", "typ": "JWT", "kid": keyName, "nonce": fmt.Sprintf("%x", nonce)}
	payload := map[string]any{
		"iss": "cdp", "sub": keyName, "nbf": now, "exp": now + 120,
		"uri": strings.ToUpper(method) + " api.coinbase.com" + path,
	}
	encode := func(value any) (string, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}
	head, err := encode(header)
	if err != nil {
		return "", err
	}
	claims, err := encode(payload)
	if err != nil {
		return "", err
	}
	unsigned := head + "." + claims
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", fmt.Errorf("coinbase advanced JWT sign: %w", err)
	}
	signature := append(padCoinbaseJWTInt(r, 32), padCoinbaseJWTInt(s, 32)...)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseCoinbaseAdvancedKey(raw string) (*ecdsa.PrivateKey, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(normalized))
	if block == nil {
		return nil, fmt.Errorf("coinbase advanced JWT: decode EC private key PEM")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("coinbase advanced JWT: parse private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("coinbase advanced JWT: private key is not ECDSA")
	}
	return key, nil
}

func padCoinbaseJWTInt(value *big.Int, width int) []byte {
	out := make([]byte, width)
	encoded := value.Bytes()
	copy(out[width-len(encoded):], encoded)
	return out
}
