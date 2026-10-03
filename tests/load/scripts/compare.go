package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	p95RegressionFactor  = 1.20 // FAIL if current p95 > baseline × this
	p99RegressionFactor  = 1.30 // FAIL if current p99 > baseline × this
	rpsDropFactor        = 0.90 // FAIL if current RPS < baseline × this
	errRateMultiplier    = 1.5  // error-rate cap = max(baseline × this, errRateAbsoluteFloor)
	errRateAbsoluteFloor = 0.01
)

// k6Summary mirrors the JSON shape that k6 emits via --summary-export.
type k6Summary struct {
	Metrics map[string]struct {
		Type   string                 `json:"type"`
		Values map[string]json.Number `json:"values"`
	} `json:"metrics"`
}

type runMetrics struct {
	P50Ms     float64
	P95Ms     float64
	P99Ms     float64
	RPS       float64
	ErrorRate float64
}

type baselineFile struct {
	Scenario    string         `json:"scenario"`
	CapturedAt  string         `json:"captured_at"`
	GitSHA      string         `json:"git_sha"`
	Environment string         `json:"environment"`
	Config      map[string]any `json:"config"`
	Metrics     map[string]any `json:"metrics"`
}

type currentRunConfig struct {
	RateRPS      int
	RedisEnabled bool
}

type diffReport struct {
	Failed bool
	Lines  []string
}

func parseSummary(path string) (runMetrics, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from filepath.Glob, not user input
	if err != nil {
		return runMetrics{}, fmt.Errorf("compare.parseSummary: read %s: %w", path, err)
	}
	var s k6Summary
	if err := json.Unmarshal(raw, &s); err != nil {
		return runMetrics{}, fmt.Errorf("compare.parseSummary: unmarshal %s: %w", path, err)
	}
	dur := s.Metrics["http_req_duration"].Values
	reqs := s.Metrics["http_reqs"].Values
	failed := s.Metrics["http_req_failed"].Values

	p50, _ := dur["p(50)"].Float64()
	p95, _ := dur["p(95)"].Float64()
	p99, _ := dur["p(99)"].Float64()
	rps, _ := reqs["rate"].Float64()
	er, _ := failed["rate"].Float64()

	return runMetrics{P50Ms: p50, P95Ms: p95, P99Ms: p99, RPS: rps, ErrorRate: er}, nil
}

func medianRuns(runs []runMetrics) runMetrics {
	pick := func(get func(runMetrics) float64) float64 {
		vals := make([]float64, len(runs))
		for i, r := range runs {
			vals[i] = get(r)
		}
		sort.Float64s(vals)
		mid := len(vals) / 2
		if len(vals)%2 == 1 {
			return vals[mid]
		}
		return (vals[mid-1] + vals[mid]) / 2
	}
	return runMetrics{
		P50Ms:     pick(func(r runMetrics) float64 { return r.P50Ms }),
		P95Ms:     pick(func(r runMetrics) float64 { return r.P95Ms }),
		P99Ms:     pick(func(r runMetrics) float64 { return r.P99Ms }),
		RPS:       pick(func(r runMetrics) float64 { return r.RPS }),
		ErrorRate: pick(func(r runMetrics) float64 { return r.ErrorRate }),
	}
}

func diff(base, cur runMetrics) diffReport {
	var report diffReport
	if cur.P95Ms > base.P95Ms*p95RegressionFactor {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL p95: %.1fms vs baseline %.1fms (+%.1f%%, threshold +%.0f%%)",
				cur.P95Ms, base.P95Ms, pct(base.P95Ms, cur.P95Ms), (p95RegressionFactor-1)*100))
	}
	if cur.P99Ms > base.P99Ms*p99RegressionFactor {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL p99: %.1fms vs baseline %.1fms (+%.1f%%, threshold +%.0f%%)",
				cur.P99Ms, base.P99Ms, pct(base.P99Ms, cur.P99Ms), (p99RegressionFactor-1)*100))
	}
	errCap := base.ErrorRate * errRateMultiplier
	if errCap < errRateAbsoluteFloor {
		errCap = errRateAbsoluteFloor
	}
	if cur.ErrorRate > errCap {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL error rate: %.4f vs baseline %.4f (cap %.4f)",
				cur.ErrorRate, base.ErrorRate, errCap))
	}
	if cur.RPS < base.RPS*rpsDropFactor {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL RPS: %.1f vs baseline %.1f (-%.1f%%, threshold -%.0f%%)",
				cur.RPS, base.RPS, pct(cur.RPS, base.RPS), (1-rpsDropFactor)*100))
	}
	if !report.Failed {
		report.Lines = append(report.Lines, "PASS")
	}
	return report
}

func pct(from, to float64) float64 {
	if from == 0 {
		return 0
	}
	return (to - from) / from * 100
}

func checkConfig(base baselineFile, cur currentRunConfig) error {
	baseRate, _ := base.Config["rate_rps"].(float64)
	if int(baseRate) != cur.RateRPS {
		return fmt.Errorf("compare.checkConfig: config mismatch: baseline rate_rps=%v, current rate_rps=%d", baseRate, cur.RateRPS)
	}
	baseRedis, _ := base.Config["redis_enabled"].(bool)
	if baseRedis != cur.RedisEnabled {
		return fmt.Errorf("compare.checkConfig: config mismatch: baseline redis_enabled=%v, current=%v", baseRedis, cur.RedisEnabled)
	}
	return nil
}

func main() {
	var (
		baselineDir = flag.String("baseline-dir", "tests/load/baseline", "directory holding baseline JSON files")
		resultsDir  = flag.String("results-dir", "tests/load/results", "directory holding per-run summary JSON files")
		scenario    = flag.String("scenario", "subscribe_storm", "scenario name to compare (subscribe_storm | token_flow)")
		environment = flag.String("environment", "local-testcontainers", "environment tag, must match baseline")
		updateMode  = flag.Bool("update-baseline", false, "overwrite baseline with current run's medians (no comparison)")
	)
	flag.Parse()

	pattern := summaryGlobFor(*scenario, *resultsDir)
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "compare: no summary files matching %s\n", pattern)
		os.Exit(2)
	}

	runs := make([]runMetrics, 0, len(files))
	for _, f := range files {
		m, err := parseSummary(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "compare: parse %s: %v\n", f, err)
			os.Exit(2)
		}
		runs = append(runs, m)
	}
	cur := medianRuns(runs)

	basePath := baselinePathFor(*baselineDir, *scenario, *environment)

	if *updateMode {
		if err := writeBaseline(basePath, *scenario, *environment, cur); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("baseline updated: %s (p50=%.1fms p95=%.1fms p99=%.1fms rps=%.1f err=%.4f)\n",
			basePath, cur.P50Ms, cur.P95Ms, cur.P99Ms, cur.RPS, cur.ErrorRate)
		return
	}

	baseRaw, err := os.ReadFile(basePath) //nolint:gosec // basePath is constructed from flag + scenario, not user input
	if err != nil {
		fmt.Fprintf(os.Stderr, "compare: no baseline at %s; run with --update-baseline first\n", basePath)
		os.Exit(2)
	}
	var base baselineFile
	if err := json.Unmarshal(baseRaw, &base); err != nil {
		fmt.Fprintf(os.Stderr, "compare: parse baseline: %v\n", err)
		os.Exit(2)
	}

	if err := checkConfig(base, currentRunConfig{RateRPS: rateFromEnv(*scenario), RedisEnabled: true}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	baseMetrics := runMetrics{
		P50Ms:     numberField(base.Metrics, "http_req_duration_p50_ms"),
		P95Ms:     numberField(base.Metrics, "http_req_duration_p95_ms"),
		P99Ms:     numberField(base.Metrics, "http_req_duration_p99_ms"),
		RPS:       numberField(base.Metrics, "http_reqs_per_sec"),
		ErrorRate: numberField(base.Metrics, "http_req_failed_rate"),
	}

	report := diff(baseMetrics, cur)
	// Markdown output is cosmetic; a write failure must not change the pass/fail verdict.
	if err := writeMarkdown(*resultsDir, *scenario, baseMetrics, cur, report); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	for _, line := range report.Lines {
		fmt.Println(line)
	}
	if report.Failed {
		os.Exit(1)
	}
}

func summaryGlobFor(scenario, resultsDir string) string {
	// [0-9]* requires the char after the underscore to be a digit, so stray files like
	// subscribe_smoke.json or subscribe_tmp.json won't match and corrupt the median.
	switch scenario {
	case "token_flow":
		return filepath.Join(resultsDir, "token_[0-9]*.json")
	case "subscribe_storm":
		fallthrough
	default:
		return filepath.Join(resultsDir, "subscribe_[0-9]*.json")
	}
}

func baselinePathFor(baselineDir, scenario, environment string) string {
	if environment == "local-testcontainers" {
		return filepath.Join(baselineDir, scenario+".json")
	}
	return filepath.Join(baselineDir, scenario+"."+environment+".json")
}

func numberField(m map[string]any, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

func rateFromEnv(scenario string) int {
	key := "SUBSCRIBE_RATE"
	dflt := 100
	if scenario == "token_flow" {
		key = "TOKEN_RATE"
		dflt = 30
	}
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return dflt
}

func writeBaseline(path, scenario, environment string, m runMetrics) error {
	sha := "unknown"
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		sha = strings.TrimSpace(string(out))
	}
	b := baselineFile{
		Scenario:    scenario,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339),
		GitSHA:      sha,
		Environment: environment,
		Config: map[string]any{
			"rate_rps":        float64(rateFromEnv(scenario)),
			"duration":        "3m",
			"db_max_conns":    20.0,
			"redis_enabled":   true,
			"redis_cache_ttl": "10m",
		},
		Metrics: map[string]any{
			"http_req_duration_p50_ms": m.P50Ms,
			"http_req_duration_p95_ms": m.P95Ms,
			"http_req_duration_p99_ms": m.P99Ms,
			"http_reqs_per_sec":        m.RPS,
			"http_req_failed_rate":     m.ErrorRate,
		},
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("compare.writeBaseline: marshal: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("compare.writeBaseline: write %s: %w", path, err)
	}
	return nil
}

func writeMarkdown(dir, scenario string, base, cur runMetrics, r diffReport) error {
	body := fmt.Sprintf(`# %s — Last Run

| Metric | Baseline | Current | Delta |
|---|---:|---:|---:|
| p50 (ms) | %.1f | %.1f | %+.1f%% |
| p95 (ms) | %.1f | %.1f | %+.1f%% |
| p99 (ms) | %.1f | %.1f | %+.1f%% |
| RPS | %.1f | %.1f | %+.1f%% |
| Error rate | %.4f | %.4f | — |

%s
`, scenario,
		base.P50Ms, cur.P50Ms, pct(base.P50Ms, cur.P50Ms),
		base.P95Ms, cur.P95Ms, pct(base.P95Ms, cur.P95Ms),
		base.P99Ms, cur.P99Ms, pct(base.P99Ms, cur.P99Ms),
		base.RPS, cur.RPS, pct(base.RPS, cur.RPS),
		base.ErrorRate, cur.ErrorRate,
		strings.Join(r.Lines, "\n"))
	path := filepath.Join(dir, "last-run-"+scenario+".md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("compare.writeMarkdown: write %s: %w", path, err)
	}
	return nil
}
