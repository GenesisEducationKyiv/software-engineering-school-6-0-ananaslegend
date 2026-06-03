# ADR-0017: Adopt Vector → Elasticsearch + vmagent → VictoriaMetrics + Grafana for observability

Status: accepted · 2026-06-03 · @limuzin · observability

## Context

Until now `reposeetory` produced structured zerolog JSON to stdout and exposed Prometheus metrics on `/metrics`, but had no central log aggregation, no metrics retention, no dashboards, and no documented production observability story. The application needed full-text searchable logs, durable time-series metrics for performance baselines and regression detection, and operator-facing visualisation of RED triplets across every component boundary. Constraints: local dev runs via `docker-compose`, production runs on Railway (one container per service, no Docker socket access), and the academy scope wants a working end-to-end stack rather than a managed-service contract.

## Decision

Adopt a six-component stack, deployed both locally (`docker-compose.observability.yml` overlay) and on Railway (one Railway service per component, intra-service communication via `*.railway.internal`):

- **Logs:** zerolog → `logshipper.Writer` (non-blocking HTTP, fail-open) → Vector `http_server` source → Elasticsearch single-node sink with ILM (rollover at 1d / 1GB, delete at 7d) → Kibana.
- **Metrics:** Prometheus client → `/metrics` → vmagent scrape (15s) → remote_write → VictoriaMetrics single-node (retention 30d) → Grafana.
- **Visualisation:** one Grafana dashboard ("Reposeetory RED") covering HTTP, outbound (GitHub + Email), workers (scanner/notifier/confirmer), and runtime/self-metrics — provisioned via files mounted from `infra/grafana/provisioning/`.

Rejected alternatives: Loki for logs (does not satisfy the academy "Elasticsearch + Kibana" requirement); Prometheus for metrics (heavier than VictoriaMetrics at this scale, no Alertmanager wanted); direct zerolog ES hook (couples app lifetime to ES availability — we want fail-open delivery); Docker-socket source in Vector (impossible on Railway).

## Consequences

### Positive
- Operators get a single Grafana dashboard summarising every component boundary in the system.
- Log search and metric exploration are both available locally with one `make obs-up` command.
- App-side log shipper is fail-open: Vector or ES downtime never blocks request handling.
- Configuration is identical between local and Railway — only env vars (`VECTOR_INGEST_URL`, `APP_METRICS_TARGET`, `ES_URL`) change.

### Negative
- Full stack footprint ≈ 2.7 GB RAM (ES 1 GB + Kibana 1 GB + VM 256 MB + Grafana 256 MB + Vector 100 MB + vmagent 100 MB) — Railway plan must accommodate this.
- ES single-node loses data on disk failure; acceptable for academy-scope log data with 7-day retention.

### Constraints
- Future alerting work must integrate with VictoriaMetrics (via vmalert or Grafana alerting) rather than introducing Alertmanager.
- Future dashboards belong in `infra/grafana/provisioning/dashboards/` as JSON. Consider migrating to Grafonnet (Jsonnet) once dashboards exceed a single file.

## Links

- `internal/observability/logshipper/` — non-blocking writer + Vector HTTP client.
- `internal/observability/redmetrics/` — RED helper (see [ADR-0018](0018-red-metrics-conventions.md)).
- `internal/observability/outboxcollector/` — outbox depth gauges.
- `infra/vector/vector.toml`, `infra/vmagent/vmagent.yml`, `infra/grafana/provisioning/`.
- `docker-compose.observability.yml`, `scripts/obs-init.sh`.
- `docs/operations/observability.md` — runbook.
- `docs/superpowers/specs/2026-06-03-observability-design.md` — full design spec.
- Related: [ADR-0011](0011-testing-strategy.md) (Registry-as-dependency).
