package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/notifications/transport"
)

func TestNewRouter_HealthAndMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := transport.NewHandler(transport.Config{Sender: nil, Registry: reg})
	srv := httptest.NewServer(newRouter(h, reg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
}
