package credentialcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

var testKey = []byte("01234567890123456789012345678901")

func configureTestKey(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(testKey))
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	configureTestKey(t)

	encrypted, err := Encrypt("a@b.example")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if !strings.HasPrefix(encrypted, version+":") {
		t.Fatalf("Encrypt() = %q, want %q prefix", encrypted, version+":")
	}

	got, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if got != "a@b.example" {
		t.Fatalf("Decrypt() = %q, want %q", got, "a@b.example")
	}
}

func TestDecryptV1CompatibilityVector(t *testing.T) {
	configureTestKey(t)
	hash := sha256.Sum256(testKey)
	keyID := hex.EncodeToString(hash[:4])
	block, err := aes.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	ciphertext := gcm.Seal(nil, nonce, []byte("a@b.example"), []byte(version+":"+keyID))
	encoded := version + ":" + keyID + ":" + base64.RawStdEncoding.EncodeToString(append(nonce, ciphertext...))

	got, err := Decrypt(encoded)
	if err != nil || got != "a@b.example" {
		t.Fatalf("Decrypt(compatibility vector) = %q, %v", got, err)
	}
}

func TestPlaintextAndEmptyCompatibility(t *testing.T) {
	for _, value := range []string{"", "legacy@example.com"} {
		t.Run(value, func(t *testing.T) {
			got, err := Decrypt(value)
			if err != nil || got != value {
				t.Fatalf("Decrypt(%q) = %q, %v", value, got, err)
			}
		})
	}

	got, err := Encrypt("")
	if err != nil || got != "" {
		t.Fatalf("Encrypt(empty) = %q, %v", got, err)
	}
}

func TestEncryptPreservesExistingV1ValueWithoutConfiguredKey(t *testing.T) {
	t.Setenv("APP_ENCRYPTION_KEY", "")
	const value = "enc:v1:existing:payload"
	got, err := Encrypt(value)
	if err != nil || got != value {
		t.Fatalf("Encrypt(existing) = %q, %v", got, err)
	}
}

func TestDecryptRejectsMalformedValue(t *testing.T) {
	configureTestKey(t)
	for _, value := range []string{"enc:v1:", "enc:v1::payload", "enc:v1:key:"} {
		if _, err := Decrypt(value); err == nil {
			t.Fatalf("Decrypt(%q) succeeded, want error", value)
		}
	}
}

func TestDecryptRejectsUnknownKey(t *testing.T) {
	configureTestKey(t)
	if _, err := Decrypt("enc:v1:deadbeef:payload"); err == nil || !strings.Contains(err.Error(), "unknown key deadbeef") {
		t.Fatalf("Decrypt() error = %v, want unknown-key error", err)
	}
}

func TestDecryptRejectsInvalidPayload(t *testing.T) {
	configureTestKey(t)
	encrypted, err := Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(encrypted, ":", 4)

	invalidBase64 := strings.Join([]string{parts[0], parts[1], parts[2], "not-base64!"}, ":")
	if _, err := Decrypt(invalidBase64); err == nil || !strings.Contains(err.Error(), "decode encrypted credential") {
		t.Fatalf("Decrypt(invalid base64) error = %v", err)
	}

	payload, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] ^= 1
	tampered := strings.Join([]string{parts[0], parts[1], parts[2], base64.RawStdEncoding.EncodeToString(payload)}, ":")
	if _, err := Decrypt(tampered); err == nil || err.Error() != "decrypt encrypted credential" {
		t.Fatalf("Decrypt(tampered) error = %v", err)
	}
}
