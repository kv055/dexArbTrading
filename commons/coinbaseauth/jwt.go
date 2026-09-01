// Package coinbaseauth builds short-lived CDP API JWTs for Coinbase App and
// Advanced Trade endpoints. Coinbase currently issues both ES256 and Ed25519
// keys, so callers must not assume that every private key is an EC PEM.
package coinbaseauth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// BuildJWT signs a Coinbase CDP JWT for method, host and path. Query parameters
// must be omitted from path because Coinbase signs only the request path.
func BuildJWT(keyName, privateKey, method, host, path string) (string, error) {
	key, algorithm, err := parsePrivateKey(privateKey)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("coinbase JWT nonce: %w", err)
	}
	header := map[string]any{"alg": algorithm, "typ": "JWT", "kid": keyName, "nonce": hex.EncodeToString(nonce)}
	payload := map[string]any{
		"iss": "cdp", "sub": keyName, "nbf": now, "exp": now + 120,
		"uri": strings.ToUpper(method) + " " + host + path,
	}
	unsigned, err := encodeParts(header, payload)
	if err != nil {
		return "", err
	}

	var signature []byte
	switch typed := key.(type) {
	case *ecdsa.PrivateKey:
		digest := sha256.Sum256([]byte(unsigned))
		r, s, err := ecdsa.Sign(rand.Reader, typed, digest[:])
		if err != nil {
			return "", fmt.Errorf("coinbase JWT sign: %w", err)
		}
		signature = append(padInt(r, 32), padInt(s, 32)...)
	case ed25519.PrivateKey:
		signature = ed25519.Sign(typed, []byte(unsigned))
	default:
		return "", fmt.Errorf("coinbase JWT: unsupported private key type")
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parsePrivateKey(raw string) (any, string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	if block, _ := pem.Decode([]byte(normalized)); block != nil {
		if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
			return key, "ES256", nil
		}
		if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			switch key := parsed.(type) {
			case *ecdsa.PrivateKey:
				return key, "ES256", nil
			case ed25519.PrivateKey:
				return key, "EdDSA", nil
			}
		}
	}

	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err == nil {
		switch len(decoded) {
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(decoded), "EdDSA", nil
		case ed25519.PrivateKeySize:
			return ed25519.PrivateKey(decoded), "EdDSA", nil
		}
	}
	return nil, "", fmt.Errorf("coinbase JWT: private key must be an EC/Ed25519 PEM or base64 Ed25519 key")
}

func encodeParts(header, payload any) (string, error) {
	head, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(claims), nil
}

func padInt(value *big.Int, width int) []byte {
	out := make([]byte, width)
	encoded := value.Bytes()
	copy(out[width-len(encoded):], encoded)
	return out
}
