package github

import "github.com/prometheus/client_golang/prometheus"

type cacheMetrics struct {
	hits   prometheus.Counter
	misses prometheus.Counter
	errors prometheus.Counter
}

func newCacheMetrics(reg *prometheus.Registry) cacheMetrics {
	m := cacheMetrics{
		hits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "github_cache_hits_total",
			Help: "Total number of GitHub release cache hits.",
		}),
		misses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "github_cache_misses_total",
			Help: "Total number of GitHub release cache misses.",
		}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "github_cache_errors_total",
			Help: "Total number of GitHub release cache Redis errors.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.hits, m.misses, m.errors)
	}
	return m
}
