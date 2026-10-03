package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/httpapi"
)

func TestPrometheusMiddleware_RecordsRequestMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := chi.NewRouter()
	r.Use(httpapi.PrometheusMiddleware(reg))
	r.Get("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP http_requests_total Total number of HTTP requests.
# TYPE http_requests_total counter
http_requests_total{method="GET",path="/api/ping",status="200"} 1
`), "http_requests_total"))
}

func TestPrometheusMiddleware_MetricsRouteNotTracked(t *testing.T) {
	reg := prometheus.NewRegistry()

	r := chi.NewRouter()
	r.Use(httpapi.PrometheusMiddleware(reg))
	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	mfs, err := reg.Gather()
	require.NoError(t, err)
	require.Empty(t, mfs)
}
