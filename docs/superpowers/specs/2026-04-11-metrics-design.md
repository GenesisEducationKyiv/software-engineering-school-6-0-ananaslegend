# Prometheus Metrics — Design Spec

**Date:** 2026-04-11  
**Status:** Approved

## Overview

Add a `/metrics` endpoint exposing Prometheus metrics for operational alerting, product insights, and performance tuning. Uses a single `*prometheus.Registry` created in `main.go` and passed via each component's `Config` struct (approach B — explicit dependencies, no global state).

## Dependencies

```
github.com/prometheus/client_golang/prometheus
github.com/prometheus/client_golang/prometheus/promhttp
github.com/prometheus/client_golang/prometheus/collectors
```

## Metric Definitions

### Go Runtime + Process (registered in `main.go`)
- `collectors.NewGoCollector()` — goroutines, GC, heap
- `collectors.NewProcessCollector()` — open FDs, CPU, memory

### DB Connection Pool (`main.go` — custom `pgxpoolCollector`)
| Metric | Type | Description |
|---|---|---|
| `db_pool_acquired_conns` | Gauge | Connections currently in use |
| `db_pool_idle_conns` | Gauge | Connections idle in the pool |
| `db_pool_total_conns` | Gauge | Total open connections |

Implemented as a `prometheus.Collector` reading `pgxpool.Pool.Stat()` on each scrape.

### HTTP (`internal/httpapi/metrics.go`)
| Metric | Type | Labels | Description |
|---|---|---|---|
| `http_requests_total` | Counter | `method`, `path`, `status` | Request count |
| `http_request_duration_seconds` | Histogram | `method`, `path` | Request latency |

- Buckets: `5ms, 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 10s`
- `path` uses chi route pattern (e.g. `/api/confirm/{token}`) to avoid high cardinality
- Implemented as chi middleware; `/metrics` path is excluded from tracking

### Scanner (`internal/scanner/metrics.go`)
| Metric | Type | Labels | Description |
|---|---|---|---|
| `scanner_ticks_total` | Counter | `result: ok\|error` | Scan tick outcomes |
| `scanner_repos_scanned_total` | Counter | — | Repos processed across all ticks |

Incremented in `Tick()` after completion.

### Notifier (`internal/notifier/metrics.go`)
| Metric | Type | Labels | Description |
|---|---|---|---|
| `notifier_emails_sent_total` | Counter | `result: ok\|error` | Release emails attempted |
| `notifier_flush_duration_seconds` | Histogram | — | Time for one `Flush()` call |

Buckets for flush duration: `10ms, 50ms, 100ms, 500ms, 1s, 5s, 10s, 30s`

### Confirmer (`internal/confirmer/metrics.go`)
| Metric | Type | Labels | Description |
|---|---|---|---|
| `confirmer_emails_sent_total` | Counter | `result: ok\|error` | Confirmation emails attempted |

### GitHub Cache (`internal/github/metrics.go`)
| Metric | Type | Description |
|---|---|---|
| `github_cache_hits_total` | Counter | Redis MGET hits |
| `github_cache_misses_total` | Counter | Redis MGET misses |
| `github_cache_errors_total` | Counter | Redis errors (silent fallback) |

Incremented in `CachingReleaseProvider.GetLatestReleases()`.

### Subscription Service (`internal/subscription/service/metrics.go`)
| Metric | Type | Description |
|---|---|---|
| `subscriptions_created_total` | Counter | Successful `CreateSubscription` calls |
| `subscriptions_confirmed_total` | Counter | Successful `Confirm` calls |
| `subscriptions_deleted_total` | Counter | Successful `Unsubscribe` calls |

Incremented after successful DB operations in the service layer.

## Architecture

### Registry Flow

```
main.go
  └── prometheus.NewRegistry()
        ├── GoCollector
        ├── ProcessCollector
        ├── pgxpoolCollector(pool)
        ├── → scanner.Config{Registry: reg}
        ├── → notifier.Config{Registry: reg}
        ├── → confirmer.Config{Registry: reg}
        ├── → github.CachingConfig{Registry: reg}
        ├── → service.Config{Registry: reg}
        └── → httpapi.NewRouter(..., reg)
              ├── middleware: prometheusMiddleware(reg)
              └── GET /metrics → promhttp.HandlerFor(reg, ...)
```

### Nil-safe Registration

Each component's `New()` checks `cfg.Registry != nil` before registering metrics. When `nil`, a no-op stub is used. This means **all existing tests require zero changes**.

```go
// pattern used in every component
type metrics struct {
    ticksTotal *prometheus.CounterVec
}

func newMetrics(reg *prometheus.Registry) metrics {
    if reg == nil {
        return metrics{ticksTotal: prometheus.NewCounterVec(...)} // unregistered, safe
    }
    m := metrics{ticksTotal: prometheus.NewCounterVec(...)}
    reg.MustRegister(m.ticksTotal)
    return m
}
```

### Router Change

```go
func NewRouter(log zerolog.Logger, subHandler *subhttp.Handler, reg *prometheus.Registry) http.Handler {
    r.Use(prometheusMiddleware(reg))
    r.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
    // existing routes unchanged
}
```

## File Layout

```
internal/
  httpapi/
    metrics.go          # HTTP counters + histogram + middleware constructor
  scanner/
    metrics.go
  notifier/
    metrics.go
  confirmer/
    metrics.go
  github/
    metrics.go
  subscription/service/
    metrics.go
cmd/api/
  main.go               # registry init, pgxpoolCollector, wiring
```

## Testing

- Existing tests: no changes required (`Registry: nil` → no-op)
- New metric tests (optional): create `prometheus.NewRegistry()`, pass to component, assert with `testutil.GatherAndCompare`
