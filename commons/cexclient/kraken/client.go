// Package kraken adapts the official Kraken SDK without exporting SDK types.
package kraken

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"commons/cexclient"
	spot "github.com/krakenfx/api-go/v2/pkg/spot"
)

const defaultTimeout = 20 * time.Second

// Options allows deterministic mock-server and timeout tests.
type Options struct {
	BaseURL string
	Timeout time.Duration
	Client  *http.Client
}

// Client executes authenticated Kraken Spot requests. Authentication, nonce
// generation, and request signing are entirely delegated to Kraken's SDK.
type Client struct {
	rest  *spot.REST
	nonce atomic.Int64
}

func New(apiKey, apiSecret string, opts Options) *Client {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.kraken.com"
	}
	httpClient := opts.Client
	if httpClient == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	rest := spot.NewREST()
	rest.BaseURL = baseURL
	rest.PublicKey = apiKey
	rest.PrivateKey = apiSecret
	rest.Executor = httpClient.Do
	client := &Client{rest: rest}
	client.nonce.Store(time.Now().UnixMicro())
	rest.Nonce = func() string {
		now := time.Now().UnixMicro()
		for {
			previous := client.nonce.Load()
			next := previous + 1
			if now > next {
				next = now
			}
			if client.nonce.CompareAndSwap(previous, next) {
				return fmt.Sprint(next)
			}
		}
	}
	return client
}

// PrivatePOST uses the SDK's generic authenticated request path. It is used for
// endpoints without correct typed coverage, notably DepositAddresses in v2.0.0.
func (c *Client) PrivatePOST(ctx context.Context, operation, path string, body map[string]any) ([]byte, error) {
	raw, err := c.privatePOSTOnce(ctx, operation, path, body)
	if err == nil && !isMutation(path) && strings.Contains(string(raw), "EAPI:Invalid nonce") {
		c.nonce.Add(1000)
		return c.privatePOSTOnce(ctx, operation, path, body)
	}
	return raw, err
}

func (c *Client) privatePOSTOnce(ctx context.Context, operation, path string, body map[string]any) ([]byte, error) {
	req, err := c.rest.NewRequest(spot.RequestOptions{
		Auth: true, Method: http.MethodPost, Path: path, Body: body,
		ContentType: "application/x-www-form-urlencoded",
	})
	if err != nil {
		return nil, wrap(operation, 0, false, err)
	}
	req.Request = req.Request.WithContext(ctx)
	resp, err := req.Do()
	if err != nil {
		ambiguous := ctx.Err() != nil || isTimeout(err)
		return nil, wrap(operation, 0, ambiguous, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &cexclient.Error{
			Provider: "kraken", Operation: operation, HTTPStatus: resp.StatusCode,
			Message: http.StatusText(resp.StatusCode), Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500,
		}
	}
	return resp.Body, nil
}

func isMutation(path string) bool {
	return strings.HasSuffix(path, "/AddOrder") || strings.HasSuffix(path, "/Withdraw")
}

func wrap(operation string, status int, ambiguous bool, err error) error {
	return &cexclient.Error{Provider: "kraken", Operation: operation, HTTPStatus: status,
		Message: "request failed", Retryable: isTimeout(err), AmbiguousMutation: ambiguous, Cause: err}
}

func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	t, ok := err.(timeout)
	return ok && t.Timeout()
}
