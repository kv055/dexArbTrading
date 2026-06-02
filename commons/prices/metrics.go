package prices

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the Prometheus instruments for the CEX price feeds. All methods
// are nil-safe so callers that don't wire metrics (tests, ad-hoc tooling) can
// leave the Manager's metrics unset without guarding every call site.
type Metrics struct {
	feedUp     *prometheus.GaugeVec
	lastUpdate *prometheus.GaugeVec
	current    *prometheus.GaugeVec
	reconnects *prometheus.CounterVec
}

// NewMetrics registers the price-feed instruments against reg and returns a
// handle the Manager updates as feeds connect, tick and reconnect. The pair is
// fixed to BTC/EUR (see defaultPair), exposed as a label for future-proofing.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		feedUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dex_price_feed_up",
			Help: "CEX price-feed websocket connection state (1 connected, 0 disconnected).",
		}, []string{"source"}),
		lastUpdate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dex_price_last_update_timestamp_seconds",
			Help: "Unix timestamp of the last price tick received from the source. Use time() - this for staleness.",
		}, []string{"source"}),
		current: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dex_price_current",
			Help: "Most recent BTC price reported by the source.",
		}, []string{"source", "pair"}),
		reconnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dex_price_feed_reconnects_total",
			Help: "Number of times a price-feed connection was re-established after dropping.",
		}, []string{"source"}),
	}
	reg.MustRegister(m.feedUp, m.lastUpdate, m.current, m.reconnects)
	return m
}

func (m *Metrics) setFeedUp(source string, up bool) {
	if m == nil {
		return
	}
	v := 0.0
	if up {
		v = 1.0
	}
	m.feedUp.WithLabelValues(source).Set(v)
}

func (m *Metrics) observePrice(source string, price float64) {
	if m == nil {
		return
	}
	m.current.WithLabelValues(source, defaultPair).Set(price)
	m.lastUpdate.WithLabelValues(source).SetToCurrentTime()
}

func (m *Metrics) incReconnect(source string) {
	if m == nil {
		return
	}
	m.reconnects.WithLabelValues(source).Inc()
}
