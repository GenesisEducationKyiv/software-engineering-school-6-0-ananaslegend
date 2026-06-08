package app

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/ananaslegend/reposeetory/internal/observability/outboxcollector"
)

func newMetricsRegistry(pool *pgxpool.Pool) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		NewPoolCollector(pool),
		outboxcollector.New(&outboxQuerier{pool: pool}),
	)
	return reg
}

// outboxQuerier adapts *pgxpool.Pool to outboxcollector.Querier.
type outboxQuerier struct{ pool *pgxpool.Pool }

func (q *outboxQuerier) QueryRow(ctx context.Context, sql string, args ...any) outboxcollector.Row {
	return pgxRow{Row: q.pool.QueryRow(ctx, sql, args...)}
}

// pgxRow lifts pgx.Row to outboxcollector.Row (signatures are compatible).
type pgxRow struct{ pgx.Row }
