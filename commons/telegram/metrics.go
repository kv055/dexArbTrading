package telegram

import (
	"github.com/prometheus/client_golang/prometheus"
)

// apiCalls counts every Telegram Bot API request made through Client.post,
// so all senders (SendMessage/Notify/AnswerCallback/SetWebhook/relay polling)
// are covered at one chokepoint. result is "ok" (HTTP 200), "http_error"
// (non-200 response) or "transport_error" (request never completed).
//
// Package-level so the shared client can record without a registry handle;
// each service that wants the series exported calls RegisterMetrics on its
// own service registry.
var apiCalls = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "dex_telegram_api_calls_total",
		Help: "Telegram Bot API calls by method and result (ok/http_error/transport_error).",
	},
	[]string{"method", "result"},
)

// RegisterMetrics attaches the telegram client instruments to reg.
func RegisterMetrics(reg prometheus.Registerer) {
	reg.MustRegister(apiCalls)
}

func recordAPICall(method string, status int, err error) {
	result := "ok"
	switch {
	case err != nil:
		result = "transport_error"
	case status != 200:
		result = "http_error"
	}
	apiCalls.WithLabelValues(method, result).Inc()
}
