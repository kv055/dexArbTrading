// Package credentialcrypto provides the shared application-level encryption
// format for sensitive account values.
package credentialcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

const version = "enc:v1"

func configuredKey() ([]byte, string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("APP_ENCRYPTION_KEY")))
	if err != nil || len(key) != 32 {
		return nil, "", errors.New("APP_ENCRYPTION_KEY is missing or invalid")
	}
	hash := sha256.Sum256(key)
	return key, hex.EncodeToString(hash[:4]), nil
}

// Encrypt encrypts a non-empty value. Existing enc:v1 values are preserved.
func Encrypt(value string) (string, error) {
	if value == "" || strings.HasPrefix(value, version+":") {
		return value, nil
	}
	key, keyID, err := configuredKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create credential gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	header := []byte(version + ":" + keyID)
	sealed := gcm.Seal(nil, nonce, []byte(value), header)
	return version + ":" + keyID + ":" + base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// Decrypt supports both enc:v1 values and legacy plaintext values.
func Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, version+":") {
		return value, nil
	}
	parts := strings.SplitN(value, ":", 4)
	if len(parts) != 4 || parts[2] == "" || parts[3] == "" {
		return "", errors.New("invalid encrypted credential format")
	}
	key, keyID, err := configuredKey()
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(parts[2], keyID) {
		return "", fmt.Errorf("encrypted credential uses unknown key %s", parts[2])
	}
	sealed, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return "", fmt.Errorf("decode encrypted credential: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("encrypted credential payload is invalid")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(parts[0]+":"+parts[1]+":"+parts[2]))
	if err != nil {
		return "", errors.New("decrypt encrypted credential")
	}
	return string(plain), nil
}
