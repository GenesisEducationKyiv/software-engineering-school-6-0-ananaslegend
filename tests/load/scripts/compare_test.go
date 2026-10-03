package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const sampleK6Summary = `{
  "metrics": {
    "http_req_duration": {
      "type": "trend",
      "values": {
        "p(50)": 40.1,
        "p(95)": 180.5,
        "p(99)": 410.0,
        "avg": 55.0,
        "med": 40.1
      }
    },
    "http_reqs": {
      "type": "counter",
      "values": { "count": 30000, "rate": 99.8 }
    },
    "http_req_failed": {
      "type": "rate",
      "values": { "rate": 0.002, "passes": 29940, "fails": 60 }
    }
  }
}`

func writeSummary(t *testing.T, dir, name, contents string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
	return p
}

func TestParseSummary(t *testing.T) {
	dir := t.TempDir()
	p := writeSummary(t, dir, "s1.json", sampleK6Summary)
	got, err := parseSummary(p)
	require.NoError(t, err)
	require.InDelta(t, 180.5, got.P95Ms, 0.001)
	require.InDelta(t, 410.0, got.P99Ms, 0.001)
	require.InDelta(t, 99.8, got.RPS, 0.001)
	require.InDelta(t, 0.002, got.ErrorRate, 0.001)
}

func TestMedianRuns_OddCount(t *testing.T) {
	runs := []runMetrics{
		{P95Ms: 100, P99Ms: 200, RPS: 90, ErrorRate: 0.01},
		{P95Ms: 110, P99Ms: 210, RPS: 95, ErrorRate: 0.02},
		{P95Ms: 105, P99Ms: 205, RPS: 92, ErrorRate: 0.015},
	}
	m := medianRuns(runs)
	require.InDelta(t, 105.0, m.P95Ms, 0.001)
	require.InDelta(t, 205.0, m.P99Ms, 0.001)
	require.InDelta(t, 92.0, m.RPS, 0.001)
	require.InDelta(t, 0.015, m.ErrorRate, 0.001)
}

func TestMedianRuns_EvenCount(t *testing.T) {
	runs := []runMetrics{
		{P95Ms: 100, P99Ms: 200, RPS: 90, ErrorRate: 0.010},
		{P95Ms: 110, P99Ms: 210, RPS: 100, ErrorRate: 0.020},
	}
	m := medianRuns(runs)
	require.InDelta(t, 105.0, m.P95Ms, 0.001) // (100+110)/2
	require.InDelta(t, 205.0, m.P99Ms, 0.001)
	require.InDelta(t, 95.0, m.RPS, 0.001)
	require.InDelta(t, 0.015, m.ErrorRate, 0.001)
}

func TestDiff_DetectsP95Regression(t *testing.T) {
	base := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.001}
	cur := runMetrics{P95Ms: 121, P99Ms: 200, RPS: 100, ErrorRate: 0.001} // +21%
	report := diff(base, cur)
	require.True(t, report.Failed, "21%% p95 increase must trip the 20%% threshold")
	require.Contains(t, report.Lines[0], "p95")
}

func TestDiff_PassesWithinTolerance(t *testing.T) {
	base := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.001}
	cur := runMetrics{P95Ms: 119, P99Ms: 200, RPS: 100, ErrorRate: 0.001} // +19%
	report := diff(base, cur)
	require.False(t, report.Failed)
}

func TestDiff_ErrorRateFloor(t *testing.T) {
	// baseline.ErrorRate × 1.5 = 0.0015; floor at 0.01 prevents flaky test runs from
	// failing just because one request blipped.
	base := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.001}
	cur := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.009} // would be 9× baseline
	report := diff(base, cur)
	require.False(t, report.Failed, "0.009 error rate is below 0.01 floor; must NOT regress")
}

func TestDiff_RPSDropFails(t *testing.T) {
	base := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.001}
	cur := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 89, ErrorRate: 0.001} // -11%
	report := diff(base, cur)
	require.True(t, report.Failed, "RPS dropping 11%% must trip -10%% threshold")
}

func TestCheckConfig_Mismatch(t *testing.T) {
	base := baselineFile{
		Config: map[string]any{"rate_rps": 100.0, "redis_enabled": true},
	}
	cur := currentRunConfig{RateRPS: 50, RedisEnabled: true}
	err := checkConfig(base, cur)
	require.ErrorContains(t, err, "config mismatch")
}

func TestCheckConfig_Match(t *testing.T) {
	base := baselineFile{
		Config: map[string]any{"rate_rps": 100.0, "redis_enabled": true},
	}
	cur := currentRunConfig{RateRPS: 100, RedisEnabled: true}
	err := checkConfig(base, cur)
	require.NoError(t, err)
}
