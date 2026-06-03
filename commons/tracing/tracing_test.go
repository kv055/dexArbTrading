package tracing

import (
	"context"
	"testing"
)

func TestInitNoopWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	shutdown, err := Init(context.Background(), "test")
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

func TestSamplerRatio(t *testing.T) {
	cases := map[string]float64{
		"":     1.0,
		"1.0":  1.0,
		"0.25": 0.25,
		"0":    0.0,
		"-1":   1.0, // invalid -> default
		"2":    1.0, // clamped
		"abc":  1.0, // unparseable -> default
	}
	for in, want := range cases {
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", in)
		if got := samplerRatio(); got != want {
			t.Errorf("samplerRatio(%q) = %v, want %v", in, got, want)
		}
	}
}
