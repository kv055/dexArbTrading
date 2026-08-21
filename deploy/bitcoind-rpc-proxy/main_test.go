package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProxyRetriesTruncatedResponsesBeforeWriting(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.Header().Set("Content-Length", "20")
			_, _ = w.Write([]byte("partial"))
			return
		}
		_, _ = w.Write([]byte(`{"result":"complete"}`))
	}))
	defer upstream.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"method":"getblockcount"}`))
	proxyHandler(upstream.URL, upstream.Client()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"result":"complete"}` {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts=%d, want 3", attempts.Load())
	}
}

func TestProxyNeverForwardsPartialResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = fmt.Fprint(w, "partial")
	}))
	defer upstream.Close()
	rec := httptest.NewRecorder()
	proxyHandler(upstream.URL, upstream.Client()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "partial") {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	proxyHandler("", http.DefaultClient).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}
