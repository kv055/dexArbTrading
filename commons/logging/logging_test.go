package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceHandlerAddsTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)})

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0xc, 0xd, 0xe, 0xf, 0x10},
		SpanID:     trace.SpanID{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	logger.InfoContext(ctx, "hello")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if got := m["trace_id"]; got != sc.TraceID().String() {
		t.Fatalf("trace_id = %v, want %s", got, sc.TraceID().String())
	}
	if got := m["span_id"]; got != sc.SpanID().String() {
		t.Fatalf("span_id = %v, want %s", got, sc.SpanID().String())
	}
}

func TestTraceHandlerNoSpanNoTraceID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)})

	logger.InfoContext(context.Background(), "hello")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if _, ok := m["trace_id"]; ok {
		t.Fatalf("trace_id should be absent without a span, got %v", m["trace_id"])
	}
}
