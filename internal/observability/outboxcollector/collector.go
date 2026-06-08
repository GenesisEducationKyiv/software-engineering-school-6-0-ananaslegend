// Package outboxcollector exposes outbox depth gauges as a prometheus.Collector.
// On each Collect (i.e. on every scrape), it issues a short-deadline COUNT(*)
// query against the release_notifications and confirmation_notifications tables.
package outboxcollector

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Querier is the minimal slice of pgxpool.Pool needed by Collector.
// (*pgxpool.Pool).QueryRow returns pgx.Row, not outboxcollector.Row, so the
// caller wires a thin adapter (see internal/app for the production adapter).
// Keeping the interface pgx-free keeps this package's tests dependency-light
// and the import graph one-directional.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

// Row mirrors pgx.Row — single method for scanning a single result row.
type Row interface {
	Scan(dest ...any) error
}

// Collector implements prometheus.Collector for outbox depth.
type Collector struct {
	q Querier

	releasePending      *prometheus.Desc
	confirmationPending *prometheus.Desc
	errors              prometheus.Counter
}

// New constructs a Collector. Register it on the Registry alongside other collectors.
func New(q Querier) *Collector {
	return &Collector{
		q: q,
		releasePending: prometheus.NewDesc(
			"release_notifications_pending",
			"Number of release_notifications rows with sent_at IS NULL.",
			nil, nil,
		),
		confirmationPending: prometheus.NewDesc(
			"confirmation_notifications_pending",
			"Number of confirmation_notifications rows with sent_at IS NULL.",
			nil, nil,
		),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_collector_errors_total",
			Help: "Total number of failures while querying outbox depth.",
		}),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.releasePending
	ch <- c.confirmationPending
	c.errors.Describe(ch)
}

// Collect implements prometheus.Collector. Runs both queries with a 1s deadline.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.collectCount(ch, c.releasePending, "release_notifications")
	c.collectCount(ch, c.confirmationPending, "confirmation_notifications")
	c.errors.Collect(ch)
}

func (c *Collector) collectCount(ch chan<- prometheus.Metric, desc *prometheus.Desc, table string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var n int64
	err := c.q.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE sent_at IS NULL").Scan(&n)
	if err != nil {
		c.errors.Inc()
		return
	}
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(n))
}
