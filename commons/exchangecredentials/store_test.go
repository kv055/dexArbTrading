package exchangecredentials

import (
	"encoding/base64"
	"strings"
	"testing"

	"commons/credentialcrypto"
)

func TestDecryptStoredCredentials(t *testing.T) {
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))

	apiKey, err := credentialcrypto.Encrypt("api-key")
	if err != nil {
		t.Fatalf("encrypt API key: %v", err)
	}
	apiSecret, err := credentialcrypto.Encrypt("api-secret")
	if err != nil {
		t.Fatalf("encrypt API secret: %v", err)
	}
	passphrase, err := credentialcrypto.Encrypt("passphrase")
	if err != nil {
		t.Fatalf("encrypt passphrase: %v", err)
	}

	got, err := decrypt(Credentials{
		APIKey:     apiKey,
		APISecret:  apiSecret,
		Passphrase: passphrase,
	})
	if err != nil {
		t.Fatalf("decrypt credentials: %v", err)
	}
	if got.APIKey != "api-key" || got.APISecret != "api-secret" || got.Passphrase != "passphrase" {
		t.Fatalf("credentials = %#v", got)
	}
}

func TestResolveRejectsMissingDatabase(t *testing.T) {
	if _, err := NewStore(nil).Resolve("Kraken"); err == nil {
		t.Fatal("Resolve error = nil, want missing-database error")
	}
}
