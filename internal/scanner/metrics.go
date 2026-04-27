package scanner

import "github.com/prometheus/client_golang/prometheus"

type scannerMetrics struct {
	ticksTotal       *prometheus.CounterVec
	reposScanned     prometheus.Counter
	rateLimitedTotal prometheus.Counter
}

func newScannerMetrics(reg *prometheus.Registry) scannerMetrics {
	m := scannerMetrics{
		ticksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scanner_ticks_total",
			Help: "Total number of scanner ticks.",
		}, []string{"result"}),
		reposScanned: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "scanner_repos_scanned_total",
			Help: "Total number of repositories processed by the scanner.",
		}),
		rateLimitedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "scanner_github_rate_limited_total",
			Help: "Total number of ticks skipped due to GitHub 429 rate limiting.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.ticksTotal, m.reposScanned, m.rateLimitedTotal)
	}
	return m
}
