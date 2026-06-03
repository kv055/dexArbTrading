// Package logging configures slog for the dexArbTrading Go services.
//
// Output is structured JSON on stdout, which Grafana Alloy scrapes into Loki.
// Each record is decorated with the active trace_id/span_id (when the call site
// passes a context carrying a span), so a log line links back to its trace in
// Grafana and vice-versa.
//
// Use the context-aware slog helpers at call sites — slog.InfoContext(ctx, …),
// slog.ErrorContext(ctx, …) — so the trace correlation actually fires.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Init builds the JSON logger for service, installs it as slog's default and
// returns it. Level comes from LOG_LEVEL (debug|info|warn|error; default info).
func Init(service string) *slog.Logger {
	handler := &traceHandler{
		Handler: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: levelFromEnv(),
		}),
	}
	logger := slog.New(handler).With(slog.String("service", service))
	slog.SetDefault(logger)
	return logger
}

// traceHandler decorates each record with the trace_id/span_id from the span in
// the record's context. Alloy lifts trace_id into Loki structured metadata, so
// Grafana can pivot from a log line straight to its trace.
type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}

func levelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
