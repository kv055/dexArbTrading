package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/proxy"
)

const (
	maxResponseBytes = 32 << 20
	maxRequestBytes  = 4 << 20
	maxAttempts      = 4
)

func proxyHandler(upstream string, client *http.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok\n")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil || int64(len(body)) > maxRequestBytes {
			http.Error(w, "invalid request body", http.StatusRequestEntityTooLarge)
			return
		}
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			req, err := http.NewRequestWithContext(r.Context(), r.Method, upstream, bytes.NewReader(body))
			if err == nil {
				req.Header = r.Header.Clone()
				resp, requestErr := client.Do(req)
				if requestErr == nil {
					data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
					closeErr := resp.Body.Close()
					if readErr == nil && closeErr == nil && len(data) <= maxResponseBytes {
						for k, values := range resp.Header {
							for _, v := range values {
								w.Header().Add(k, v)
							}
						}
						w.WriteHeader(resp.StatusCode)
						_, _ = w.Write(data)
						return
					}
					err = fmt.Errorf("read upstream response: %w", firstErr(readErr, closeErr))
				} else {
					err = requestErr
				}
			}
			if attempt < maxAttempts {
				log.Printf("upstream attempt %d/%d failed: %v", attempt, maxAttempts, err)
				select {
				case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
				case <-r.Context().Done():
					http.Error(w, r.Context().Err().Error(), http.StatusGatewayTimeout)
					return
				}
			}
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	})
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func main() {
	upstream := os.Getenv("UPSTREAM_URL")
	socksAddr := os.Getenv("TOR_SOCKS_ADDR")
	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.Dial(network, address)
	}}}
	log.Printf("proxying Bitcoin RPC via %s to %s", socksAddr, upstream)
	log.Fatal(http.ListenAndServe(":8332", proxyHandler(upstream, client)))
}
