# ADR-0018: Adopt RED metric conventions via a shared `redmetrics` helper

Status: accepted · 2026-06-03 · @limuzin · observability

## Context

Each feature package historically defined its own metrics, leading to inconsistent naming (`scanner_ticks_total{result}` vs `notifier_emails_sent_total{result}` vs `notifier_flush_duration_seconds`), missing duration histograms on some workers, and no shared discipline around label cardinality. The observability initiative (see [ADR-0017](0017-observability-stack.md)) adds outbound and worker instrumentation; without a convention this asymmetry would worsen and dashboards would have to encode per-component metric naming quirks.

## Decision

Introduce a single helper at `internal/observability/redmetrics` that produces one *counter + histogram* pair per subsystem.

- **Counter name:** `<subsystem>_requests_total`.
- **Histogram name:** `<subsystem>_request_duration_seconds`.
- **Mandatory label:** `result`, with bounded values `ok | error`. Subsystem may add domain-specific values (e.g. `cached` for github_client, `empty` and `rate_limited` for scanner).
- **Extra labels** must be a fixed, bounded enum (e.g. `driver={resend,smtp,stub}` for email). High-cardinality labels (user_id, email, repo full-name, error message) are forbidden.
- **HTTP exception:** the existing `http_requests_total{method,path,status}` and `http_request_duration_seconds{method,path}` are kept as-is. Renaming them would invalidate load-test baselines (ADR-0016). The HTTP layer uses `status` as the discriminator instead of `result`.
- **Bucket defaults:** sub-second pairs (HTTP, GitHub, email) use `DefaultBuckets` (`[0.005 … 30]`); long-running ticks (scanner/notifier/confirmer) use `longBuckets` (`[0.05 … 120]`).

The helper is consumed via `Config.RED *redmetrics.RED` fields on each feature's `Config` struct, following the Registry-as-dependency pattern from [ADR-0011](0011-testing-strategy.md).

## Consequences

### Positive
- All RED-shaped metrics live behind one helper and one convention. Reading the registry is uniform.
- New subsystems instrumented via `redmetrics.New` automatically get rate, error, and duration with no boilerplate.
- Dashboards reference predictable metric names per subsystem.

### Negative
- Some pre-existing feature metrics overlap with the new RED series (notifier_emails_sent_total, confirmer_emails_sent_total, notifier_flush_duration_seconds). They remain as legacy until a follow-up cleanup PR — temporary noise in the registry.

### Constraints
- New subsystems MUST use `redmetrics.New` rather than rolling their own counter+histogram pair. PR review enforces this.
- Adding a new extra-label MUST keep cardinality bounded; high-cardinality values are forbidden by convention.

## Links

- `internal/observability/redmetrics/` — helper.
- `internal/observability/emailermetrics/` — RED decorator over the emailer interface.
- `internal/observability/outboxcollector/` — outbox depth gauges (not RED, but registered alongside).
- Related: [ADR-0011](0011-testing-strategy.md), [ADR-0017](0017-observability-stack.md).
