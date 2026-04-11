package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolCollector is a prometheus.Collector that exposes pgxpool.Stat metrics.
type PoolCollector struct {
	pool          *pgxpool.Pool
	acquiredConns *prometheus.Desc
	idleConns     *prometheus.Desc
	totalConns    *prometheus.Desc
}

// NewPoolCollector creates a PoolCollector for the given pool.
func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	return &PoolCollector{
		pool: pool,
		acquiredConns: prometheus.NewDesc(
			"db_pool_acquired_conns",
			"Number of currently acquired (in-use) connections in the pool.",
			nil, nil,
		),
		idleConns: prometheus.NewDesc(
			"db_pool_idle_conns",
			"Number of currently idle connections in the pool.",
			nil, nil,
		),
		totalConns: prometheus.NewDesc(
			"db_pool_total_conns",
			"Total number of connections in the pool (idle + acquired + constructing).",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquiredConns
	ch <- c.idleConns
	ch <- c.totalConns
}

// Collect implements prometheus.Collector.
func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(c.acquiredConns, prometheus.GaugeValue, float64(stat.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(c.idleConns, prometheus.GaugeValue, float64(stat.IdleConns()))
	ch <- prometheus.MustNewConstMetric(c.totalConns, prometheus.GaugeValue, float64(stat.TotalConns()))
}
