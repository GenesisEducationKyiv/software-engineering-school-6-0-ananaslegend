// Package redmetrics provides a small helper for the RED methodology
// (rate / errors / duration) — a uniform counter + histogram pair per subsystem.
//
// Usage:
//
//	m := redmetrics.New(redmetrics.Config{Subsystem: "github_client", Registry: reg})
//	start := time.Now()
//	...
//	m.Observe("ok", time.Since(start))
package redmetrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Config controls how a RED metric pair is built.
type Config struct {
	// Subsystem becomes the metric prefix (e.g. "github_client" → "github_client_requests_total").
	Subsystem string
	// ExtraLabels are appended after the mandatory "result" label.
	// Example: []string{"driver"} for the email subsystem.
	ExtraLabels []string
	// Buckets overrides histogram buckets (nil → DefaultBuckets).
	Buckets []float64
	// Registry receives the metrics. nil is allowed (useful in tests).
	Registry *prometheus.Registry
}

// RED is a counter + histogram pair following the RED methodology.
type RED struct {
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	extraLabels []string
}

// DefaultBuckets cover sub-second latencies typical of HTTP / outbound calls.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// New constructs a RED pair and registers it on cfg.Registry (if non-nil).
func New(cfg Config) *RED {
	buckets := cfg.Buckets
	if buckets == nil {
		buckets = DefaultBuckets
	}
	labels := append([]string{"result"}, cfg.ExtraLabels...)
	r := &RED{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: cfg.Subsystem + "_requests_total",
			Help: "Total number of " + cfg.Subsystem + " operations.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    cfg.Subsystem + "_request_duration_seconds",
			Help:    "Duration of " + cfg.Subsystem + " operations in seconds.",
			Buckets: buckets,
		}, labels),
		extraLabels: cfg.ExtraLabels,
	}
	if cfg.Registry != nil {
		cfg.Registry.MustRegister(r.requests, r.duration)
	}
	return r
}

// Observe records one operation. extraLabels must be passed in the same order
// as Config.ExtraLabels; if no extra labels were declared, pass nothing.
func (m *RED) Observe(result string, dur time.Duration, extraLabels ...string) {
	if m == nil {
		return
	}
	values := append([]string{result}, extraLabels...)
	m.requests.WithLabelValues(values...).Inc()
	m.duration.WithLabelValues(values...).Observe(dur.Seconds())
}
