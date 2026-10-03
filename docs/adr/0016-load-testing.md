# ADR-0016: Load testing for performance-regression detection

Status: accepted · 2026-05-27 · @ananaslegend · testing

## Context

The project had no performance baseline. Regressions in the hot paths —
POST /api/subscribe (the synchronous HTTP path) and the outbox drain pipeline
(scanner → `confirmation_notifications` / `release_notifications` → notifier)
— could silently land without any signal until they reached production.

The Testing Trophy ([ADR-0015](0015-testing-trophy.md)) covers **functional
correctness**; performance is an orthogonal axis it does not address. A
passing Integration suite gives no information about whether p95 latency
doubled or throughput halved. We needed a repeatable way to:

1. **Discover** capacity — find the breaking point on the current code so we
   know where the system lives today.
2. **Fix a baseline** — commit that snapshot so future runs have something to
   diff against.
3. **Detect drift over time** — fail CI when a change degrades the numbers
   past acceptable thresholds.

The GitHub GraphQL batch fetcher has a Redis caching decorator
([ADR-0008](0008-github-graphql-batch-fetch.md)) whose effectiveness is only
visible under load; functional tests never exercise the cache path at the
concurrency levels that make it matter.

## Decision

### Tooling

- **k6** (`grafana/k6:0.50.0`) for HTTP scenarios. It speaks VU/RPS natively,
  has built-in p95/p99 aggregation, and outputs machine-readable JSON summaries
  that `compare.go` can diff.
- **Go test** (with `testcontainers-go`, build tag `loadtest`) for the
  background pipeline drain — `pipeline_test.go` seeds 10 000 outbox rows and
  measures how long the scanner+notifier pair takes to drain them to zero.
  This test reuses the existing testcontainer fixtures from the integration
  tier rather than adding a new container image.

### Isolated stack

A dedicated `docker-compose.loadtest.yml` runs a throwaway environment:

- **Postgres** on a `tmpfs` volume with `fsync=off` — removes disk I/O from
  the numbers so they reflect application behaviour, not storage latency.
- **Redis** — exercises the `CachingReleaseProvider` decorator under realistic
  concurrency.
- **GitHub stub** — a standalone HTTP service (`tests/load/githubstub/`) that
  serves canned GraphQL and REST responses, so the test never touches the real
  GitHub API and results are deterministic.
- **App** — the production binary with `MAILER_DRIVER=noop`, eliminating
  network round-trips to an email provider.
- **k6** — runs as a compose service so `docker compose up` is the only
  prerequisite.

Ports are intentionally non-standard (5433/6380/8090/8080) so the load stack
does not collide with the dev compose stack.

### Three-stage ritual

| Stage | Make target | Purpose |
|-------|-------------|---------|
| 0 | `make load-discover` | Ramp 5→400 RPS (`discovery` scenario); find the breaking point. Run once; do not commit results. |
| 1 | `make load-baseline` | Run `subscribe_storm` + `token_flow` at a fixed rate, 3 times; commit the median as the new SLO floor. |
| 2 | `make load-regression` | Same scenarios at the same rate; fail if any metric drifts past threshold. |

The pipeline drain has its own target (`make load-pipeline`) that runs the Go
test directly.

### Thresholds in `compare.go`

`tests/load/scripts/compare.go` reads the committed baseline JSON, runs the
scenarios three times, takes the **median** of each metric, and fails (`exit 1`)
when:

- p95 worsens by more than **+20 %**
- p99 worsens by more than **+30 %**
- RPS drops by more than **−10 %**
- error rate exceeds **1.5 × baseline** (floor: 0.01)

`--update-baseline` overwrites the committed JSON; use this deliberately, not
as a reflex to silence a red run.

### CI placement

Load tests run **nightly** (`.github/workflows/load-test.yml`) and on
`workflow_dispatch`. They are **not** part of the per-PR pipeline because:

- A single regression run takes ~15 minutes on a 2-core shared runner.
- Shared-runner resource contention introduces timing noise that inflates
  p99, producing false positives.

### Portability

All scenarios read `BASE_URL` and `DATABASE_URL` from the environment. The
same k6 scripts can be pointed at a future Railway staging environment by
changing those two variables; no code changes are required.

## Consequences

### Positive

- Early detection of performance regressions before they reach production.
- The `discovery` scenario gives an objective answer to "how much headroom do
  we have?" at any point in the project's life.
- Reuses existing testcontainer fixtures for the pipeline drain — no new
  infrastructure skills required.
- The isolated stack (noop mailer, GitHub stub, tmpfs Postgres) makes results
  reproducible across machines and CI environments.
- Portable: the same scenario files run against a Railway staging environment
  without modification.

### Negative

- Nightly CI adds ~15 minutes to the build graph; a red run is work, not a
  number to bump.
- Docker is required on the developer machine to run load tests locally.
- Baselines must be maintained deliberately. Every intentional change in
  performance (optimisation or accepted regression) requires a `git commit` of
  new baseline JSON and a `CHANGELOG.md` entry explaining why.
- On a 2-core shared GitHub Actions runner the +20 % p95 threshold may need
  widening — the first few nightly runs will calibrate this.

## Alternatives considered

1. **vegeta** — a single-scenario constant-rate HTTP loader. Simpler, but
   offers weaker multi-scenario support and no built-in VU model for the
   `token_flow` scenario that must pre-seed tokens.
2. **Go `testing.B` benchmarks** — fast, zero-dependency, but run in-process
   against the real stack only when wired up manually. Not portable to a remote
   target without a custom harness.
3. **Production-only monitoring** (Prometheus + alerting) — catches regressions
   after they have been deployed. The feedback loop is too slow to prevent a
   bad merge; it complements but does not replace pre-merge load testing.

## Links

- [`docs/testing.md`](../testing.md) — layer layout, run instructions, prerequisites.
- [`tests/load/README.md`](../../tests/load/README.md) — scenario reference, baseline update policy.
- [`docs/superpowers/specs/2026-05-26-load-testing-design.md`](../superpowers/specs/2026-05-26-load-testing-design.md) — original design spec.
- [ADR-0015](0015-testing-trophy.md) — the Testing Trophy; load testing is the performance axis orthogonal to the correctness layers described there.
- [ADR-0008](0008-github-graphql-batch-fetch.md) — the Redis caching decorator exercised under load by these scenarios.
