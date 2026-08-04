package binance

import (
	"context"
	"strings"
	"testing"

	"commons/cexclient"
	"github.com/binance/binance-connector-go/common/v2/common"
)

func TestWrapErrorRedactsResponseBody(t *testing.T) {
	secret := `"address":"bc1q-secret-deposit"`
	sdkErr := common.NewBadRequestError(`HTTP error 400: Bad Request, {"code":-1021,"msg":"Timestamp for this request was 1000ms ahead of the server's time.",` + secret + `}`)
	err := WrapError("account info", false, sdkErr)
	got, ok := cexclient.AsError(err)
	if !ok {
		t.Fatalf("AsError failed: %v", err)
	}
	if got.HTTPStatus != 400 || got.Code != "-1021" {
		t.Fatalf("got %#v", got)
	}
	if got.Message != "Timestamp for this request was 1000ms ahead of the server's time." {
		t.Fatalf("Message = %q", got.Message)
	}
	if strings.Contains(got.Error(), "bc1q-secret-deposit") || strings.Contains(got.Message, secret) {
		t.Fatalf("wrapped error retained response body: %v", got)
	}
}

func TestWrapErrorMarksAmbiguousMutationOnNetworkFailure(t *testing.T) {
	err := WrapError("withdraw", true, common.NewNetworkError("Network error: i/o timeout"))
	got, ok := cexclient.AsError(err)
	if !ok || !got.AmbiguousMutation || !got.Retryable {
		t.Fatalf("got %#v", got)
	}
}

func TestWrapErrorMarksAmbiguousMutationOnCancel(t *testing.T) {
	err := WrapError("order", true, context.Canceled)
	got, ok := cexclient.AsError(err)
	if !ok || !got.AmbiguousMutation {
		t.Fatalf("got %#v", got)
	}
}
