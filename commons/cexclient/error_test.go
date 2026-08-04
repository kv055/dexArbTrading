package cexclient

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorIsStructuredAndDoesNotExposeCause(t *testing.T) {
	secret := "super-secret-signature"
	err := &Error{Provider: "binance", Operation: "withdraw", HTTPStatus: 429, Code: "-1003", Message: "rate limited", Retryable: true, Cause: errors.New(secret)}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("Error() exposed its underlying cause")
	}
	got, ok := AsError(err)
	if !ok || got.Code != "-1003" || !got.Retryable {
		t.Fatalf("AsError() = %#v, %v", got, ok)
	}
}
