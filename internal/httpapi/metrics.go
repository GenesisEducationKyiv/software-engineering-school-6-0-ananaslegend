package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

type httpMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func newHTTPMetrics(reg *prometheus.Registry) httpMetrics {
	m := httpMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		}, []string{"method", "path", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 10},
		}, []string{"method", "path"}),
	}
	if reg != nil {
		reg.MustRegister(m.requestsTotal, m.requestDuration)
	}
	return m
}

// PrometheusMiddleware returns a chi middleware that records http_requests_total
// and http_request_duration_seconds. Requests to /metrics are not tracked.
func PrometheusMiddleware(reg *prometheus.Registry) func(http.Handler) http.Handler {
	m := newHTTPMetrics(reg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)

			pattern := chi.RouteContext(r.Context()).RoutePattern()
			if pattern == "" {
				pattern = r.URL.Path
			}
			status := strconv.Itoa(ww.Status())
			m.requestsTotal.WithLabelValues(r.Method, pattern, status).Inc()
			m.requestDuration.WithLabelValues(r.Method, pattern).Observe(time.Since(start).Seconds())
		})
	}
}
