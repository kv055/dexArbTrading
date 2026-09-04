package coinbaseauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildJWTSupportsEd25519(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token, err := BuildJWT("organizations/test/apiKeys/key", base64.StdEncoding.EncodeToString(privateKey), "GET", "api.coinbase.com", "/v2/accounts/BTC")
	if err != nil {
		t.Fatalf("BuildJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts, want 3", len(parts))
	}
	headerRaw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var header map[string]any
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		t.Fatal(err)
	}
	if header["alg"] != "EdDSA" {
		t.Fatalf("alg = %v, want EdDSA", header["alg"])
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("JWT Ed25519 signature did not verify")
	}
}
