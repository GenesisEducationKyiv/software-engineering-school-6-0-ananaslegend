# Load Testing — Design Spec

**Date:** 2026-05-26
**Status:** Draft, pending implementation plan
**Owner:** @ananaslegend

## 1. Goal and Scope

### 1.1 Primary goal

Establish a **performance regression baseline** for the service that can be
re-run on demand and on a nightly schedule, and fail when latency, RPS, or
error rate drifts from the committed baseline beyond an acceptable threshold.

### 1.2 Secondary goals (not first-wave, but the design accommodates them)

- **Capacity discovery** — find the breaking point of the local setup.
  Delivered as a one-off `discovery` k6 scenario that informs the baseline
  rate, but does not run on a schedule.
- **Endurance / soak** — run constant load for hours to catch leaks, growing
  outbox lag, connection exhaustion. Out of MVP scope; the toolchain
  (k6 + docker-compose) supports it without redesign.

### 1.3 Explicit non-goals

- Running load tests on every PR. Too slow, too noisy on shared CI runners.
- Multi-node distributed k6. Not needed for local or single-instance staging.
- Live Grafana dashboards. JSON diff against `baseline/*.json` is enough at
  this stage; trend visualization is future work.
- Testing `GET /api/subscriptions?email=...` as a load scenario. Cheap read,
  low business value, deferred.

## 2. Architecture and File Layout

```
tests/load/
├── k6/
│   ├── discovery.js                # ramping-arrival-rate, one-off
│   ├── subscribe_storm.js          # constant-arrival-rate, baseline
│   ├── token_flow.js               # confirm + unsubscribe, baseline
│   └── lib/
│       ├── env.js                  # BASE_URL via ENV, defaults
│       └── thresholds.js           # shared SLO threshold definitions
├── seeder/
│   ├── main.go                     # CLI: --subscriptions=N --pending-tokens=M
│   └── README.md
├── pipeline/
│   ├── drain_test.go               # Go test, build tag `loadtest`
│   └── stub.go                     # GitHub GraphQL stub, scales to thousands of repos
├── githubstub/
│   ├── main.go                     # standalone HTTP server, used by docker-compose
│   └── Dockerfile
├── baseline/
│   ├── subscribe_storm.json        # committed baseline (per environment)
│   ├── token_flow.json
│   └── pipeline.json
├── results/                        # gitignored; per-run artifacts
├── scripts/
│   ├── compare.go                  # parses k6 summary + diffs against baseline
│   ├── wait-healthy.sh             # blocks until docker-compose stack is up
│   └── update-baseline.sh          # used by `make load-baseline`
├── docker-compose.loadtest.yml
└── README.md
```

### 2.1 Reuse of existing test infrastructure

- `tests/internal/pg.go` — testcontainer Postgres helper (used by `pipeline/drain_test.go` and `seeder`)
- `tests/internal/github_graphql.go` — extended for stub-at-scale (see §2.3)
- `tests/internal/app.go` `NewE2EApp` — used by `pipeline/drain_test.go` with `NoopMailer`
- `tests/internal/mailpit.go` — **not** reused for load; Mailpit becomes the bottleneck

### 2.2 Single production-code change

The existing `internal/notifier/emailer/StubMailer` (today's implicit fallback
for missing SMTP/Resend config) already satisfies the noop-mailer requirement —
it implements `emailer.Emailer` and returns nil immediately. Load mode just
needs an **explicit** way to select it.

Change: add `MAILER_DRIVER` env (`smtp` / `resend` / `noop`) to `internal/config`
and rewrite the switch in `internal/app/mailer.go` so it dispatches on
`cfg.MailerDriver` when set, falling back to the existing implicit logic when
empty (preserves dev/prod behavior unchanged).

This is the **only** production code modified for load testing. Everything
else lives in `tests/load/` and the new docker-compose file.

### 2.3 GitHub stub at scale

Today's `tests/internal/github_graphql.go` is fixture-style — registers a
small static set of repos. For load (pipeline scenario in particular) we
need a standalone HTTP service with two extra capabilities:

1. Pre-configured with N repos at startup (e.g. `STUB_REPOS=1000`).
2. Admin endpoint `POST /admin/release-all` that bumps the "latest tag"
   for every known repo at once, simulating a synchronized burst of new
   releases (used by `pipeline/drain_test.go`).

The standalone version (`tests/load/githubstub/`) shares response shape with
the in-process stub from `tests/internal/github_graphql.go` but runs as a
docker-compose service so it is reachable from the dockerized `app` and
adds realistic network latency (configurable via `STUB_LATENCY_MS`).

## 3. Scenarios

### 3.1 Stage 0 — Discovery (one-off)

`tests/load/k6/discovery.js` — `ramping-arrival-rate` executor:

| Stage | Duration | Target RPS |
|------:|---------:|-----------:|
|     1 |      30s |          5 |
|     2 |       1m |         25 |
|     3 |       1m |         50 |
|     4 |       1m |        100 |
|     5 |       1m |        200 |
|     6 |       1m |        400 |

**Purpose:** find the "knee" — the RPS at which p95 crosses 1s, error rate
crosses 1%, or connections start failing. Run via `make load-discover`,
results land in `tests/load/results/discovery.json`, **not committed**.
Used to choose the constant rate for baseline (target: 60-70% of knee).

### 3.2 Stage 1 — Baseline (committed)

#### 3.2.1 `subscribe_storm.js`

- Executor: `constant-arrival-rate`
- Rate: determined by discovery (placeholder: 100 RPS, replaced on first run)
- Duration: 5 minutes per run, 3 runs total
- Payload:
  - `email`: `loadtest+<vu>-<iter>-<ts>@example.com` (guaranteed unique)
  - `repo`: random pick from a fixed list of 100 owner/repo strings the
    `github-stub` is pre-configured with
- SLO thresholds (placeholders; finalized after first discovery):
  - `http_req_duration{scenario:subscribe} p(95) < 300ms`
  - `http_req_duration{scenario:subscribe} p(99) < 800ms`
  - `http_req_failed{scenario:subscribe} rate < 0.005`

#### 3.2.2 `token_flow.js`

- Pre-seed (via `seeder` CLI before k6 runs):
  - 10 000 `pending` subscriptions with confirmation tokens
  - 10 000 `confirmed` subscriptions with unsubscribe tokens
  - Tokens dumped to `tokens.json`, loaded once via `SharedArray`
- Executor: two `constant-arrival-rate` scenarios in parallel
  - `confirm` — 30 iter/s, 4 minutes
  - `unsubscribe` — 30 iter/s, 4 minutes
- Token use is one-shot: each VU consumes the next token index (`__VU` + counter)
- SLO thresholds:
  - `http_req_duration{scenario:confirm} p(95) < 150ms`
  - `http_req_duration{scenario:unsubscribe} p(95) < 150ms`
  - `http_req_failed rate < 0.005`
  - `checks rate > 0.99` (HTML response asserts page-specific markers)

#### 3.2.3 `pipeline/drain_test.go` (Go, not k6)

Setup:

1. Boot Postgres (testcontainer) + Redis (testcontainer) + standalone
   github-stub via `tests/load/githubstub` binary.
2. Seed: 10 000 `confirmed` subscriptions across 1 000 repos (via
   `seeder`).
3. Build `NewE2EApp` with `NoopMailer` and a high Tick rate (`50ms` for
   scanner/notifier/confirmer to exercise drain loops, not Tick latency).
4. Call `POST /admin/release-all` on the github-stub — every repo gets a
   "new tag" in the next scanner Tick.
5. Record `start = time.Now()`; sample `SELECT COUNT(*) FROM release_notifications WHERE sent_at IS NULL` every 100ms.
6. Stop the clock when count returns to 0. Duration is the throughput
   metric.
7. Scrape `/metrics` once at the end; capture `outbox_lag_max` (custom
   gauge sampled during the run), `notifier_send_duration_seconds` p95,
   `scanner_tick_duration_seconds` p95, `notifier_batch_size` p50/p95.

SLO thresholds (placeholders):

- Drain time < 60s for 10k rows
- `scanner_tick_duration_seconds` p95 < 2s
- Zero notifier errors (NoopMailer cannot fail)

Output: stdout table + `baseline/pipeline.json`.

### 3.3 Stage 2 — Regression (committed, scheduled)

Replays Stage 1 scenarios with **identical** rate/duration/config and
diffs against committed baselines. See §6.

## 4. Metrics and Baseline Format

### 4.1 Captured metrics

**HTTP scenarios (k6 native):**

| Metric | Type | Use |
|---|---|---|
| `http_req_duration` | trend (p50/p95/p99) | Primary regression signal |
| `http_reqs` | rate | Verify target RPS was actually reached |
| `http_req_failed` | rate | 4xx/5xx + transport errors |
| `iteration_duration` | trend | k6 overhead sanity check |
| `checks` | rate | Response-body assertions (status, HTML markers) |
| `vus` | gauge | Saturation indicator (hitting `maxVUs`) |

Exported via `--summary-export=summary.json` (rolled-up) and
`--out json=raw.json` (event stream, for forensics).

**Pipeline test (Go + Prometheus):**

| Metric | Source | Use |
|---|---|---|
| `drain_duration_seconds` | measured in test (start→count=0) | Throughput |
| `notifier_send_duration_seconds` p95 | existing in `internal/notifier/metrics.go` | Per-send cost |
| `scanner_tick_duration_seconds` p95 | existing in `internal/scanner/metrics.go` | Scanner health |
| `outbox_lag_max` | new gauge, sampled @100ms in the test | Peak queue depth |
| `notifier_batch_size` p50/p95 | existing | SKIP LOCKED efficiency |

Scraped via the in-process Prometheus registry at end of run, parsed with
`prometheus/expfmt`, dumped to JSON.

### 4.2 Fixed service config for load mode

Hard-coded in `docker-compose.loadtest.yml`. Any change here invalidates
existing baselines and requires `make load-baseline`.

```
SCANNER_INTERVAL=500ms
NOTIFIER_INTERVAL=200ms
CONFIRMER_INTERVAL=200ms
DB_MAX_CONNS=20
DB_MIN_CONNS=5
REDIS_URL=redis://redis:6379
REDIS_CACHE_TTL=10m
GITHUB_TOKEN=loadtest
MAILER_DRIVER=noop
LOG_LEVEL=warn
```

### 4.3 Baseline file format

`tests/load/baseline/<scenario>.json`:

```json
{
  "scenario": "subscribe_storm",
  "captured_at": "2026-05-26T15:00:00Z",
  "git_sha": "abc1234",
  "environment": "local-testcontainers",
  "config": {
    "rate_rps": 100,
    "duration": "5m",
    "db_max_conns": 20,
    "redis_enabled": true,
    "redis_cache_ttl": "10m"
  },
  "metrics": {
    "http_req_duration_p50_ms": 42.0,
    "http_req_duration_p95_ms": 180.0,
    "http_req_duration_p99_ms": 410.0,
    "http_reqs_per_sec": 99.7,
    "http_req_failed_rate": 0.0021
  }
}
```

Rationale per field:

- `git_sha` + `captured_at` — provenance, so a year later we know what
  the numbers mean.
- `environment` — `local-testcontainers` vs future `railway-staging`.
  Regression compares only within the same environment.
- `config` — `compare.go` refuses to diff if current run's config differs.

### 4.4 Anti-flakiness strategy

- **3 sequential runs**, `compare.go` reads all `<scenario>_*.json`
  summaries from `tests/load/results/` and takes the **median** of p50,
  p95, p99, RPS, and error-rate across runs. Median eliminates one
  outlier without needing fancy stats.
- **Per run: 3 minutes total**, split into a 30s ramp stage (5→target RPS)
  and a 2:30 steady-state stage. Only the steady-state segment is used
  for regression metrics — the ramp is tagged `phase:warmup` in the k6
  scenario and excluded by `compare.go`. Total time for a 3-run
  regression: ~12 minutes including DB resets.
- Postgres on `tmpfs` with `fsync=off` — eliminates disk-IO noise from
  laptop SSDs.
- k6 shares network namespace with the app container
  (`network_mode: service:app`) — eliminates Docker-bridge overhead (~5-10ms
  per request).

## 5. Docker Compose Stack and Portability

### 5.1 `docker-compose.loadtest.yml`

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: loadtest
      POSTGRES_PASSWORD: loadtest
      POSTGRES_DB: loadtest
    tmpfs: /var/lib/postgresql/data
    command: >
      postgres
        -c shared_buffers=256MB
        -c max_connections=100
        -c fsync=off
        -c synchronous_commit=off
    ports: ["5433:5432"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U loadtest"]
      interval: 2s
      retries: 10

  redis:
    image: redis:7-alpine
    command: redis-server --save "" --appendonly no
    tmpfs: /data
    ports: ["6380:6379"]

  github-stub:
    build: ./tests/load/githubstub
    environment:
      STUB_REPOS: "1000"
      STUB_LATENCY_MS: "20"
    ports: ["8090:8090"]

  app:
    build: .
    environment:
      DATABASE_URL: postgres://loadtest:loadtest@postgres:5432/loadtest
      REDIS_URL: redis://redis:6379
      GITHUB_API_URL: http://github-stub:8090
      GITHUB_TOKEN: loadtest
      RESEND_API_KEY: ""
      MAILER_DRIVER: noop
      LOG_LEVEL: warn
      SCANNER_INTERVAL: 500ms
      NOTIFIER_INTERVAL: 200ms
      CONFIRMER_INTERVAL: 200ms
      DB_MAX_CONNS: "20"
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_started }
      github-stub: { condition: service_started }
    ports: ["8080:8080"]

  k6:
    image: grafana/k6:0.50.0
    volumes:
      - ./tests/load/k6:/scripts:ro
      - ./tests/load/results:/results
    environment:
      BASE_URL: http://app:8080
    network_mode: "service:app"
    profiles: ["k6"]
```

Key choices and trade-offs:

| Choice | Reason | Trade-off |
|---|---|---|
| `tmpfs` Postgres data dir | Remove disk-IO noise | Data lost on container stop (irrelevant for load) |
| `fsync=off`, `synchronous_commit=off` | Same | Not durability-safe — fine for load, never for prod |
| Ports `5433`/`6380`/`8090`/`8080` | Avoid conflict with dev compose | Devs must remember the offset |
| `network_mode: "service:app"` for k6 | Remove ~5-10ms bridge overhead | k6 cannot reach other services by name (we only need `app`) |
| `MAILER_DRIVER=noop` | Mailpit would saturate at ~100 RPS | New env var to maintain |
| Standalone `github-stub` | Realistic network, scales to 1000 repos | New service to build/maintain |

### 5.2 Portability to staging

The k6 scenarios and `compare.go` are **environment-agnostic**. Switching
to staging requires only ENV overrides:

```sh
BASE_URL=https://staging.app \
DATABASE_URL=postgres://...railway... \
SKIP_COMPOSE=1 \
make load-baseline-staging
```

`SKIP_COMPOSE=1` skips `load-up`/`load-reset` (staging is managed externally).
`compare.go` writes to `baseline/subscribe_storm.staging.json`; regression
compares only within the same environment.

## 6. Makefile Targets and Regression Detection

### 6.1 Targets

```makefile
LOAD_COMPOSE := docker compose -f docker-compose.loadtest.yml

.PHONY: load-up load-down load-reset load-discover load-baseline load-regression load-pipeline

load-up:          ## bring stack up (postgres, redis, github-stub, app)
	$(LOAD_COMPOSE) up -d --build
	@./tests/load/scripts/wait-healthy.sh

load-down:        ## tear down stack and volumes
	$(LOAD_COMPOSE) down -v

load-reset:       ## clear DB + Redis between runs (no teardown)
	$(LOAD_COMPOSE) exec -T postgres psql -U loadtest -c "TRUNCATE subscriptions, confirmation_notifications, release_notifications CASCADE"
	$(LOAD_COMPOSE) exec -T redis redis-cli FLUSHALL

load-discover:    ## stage 0 — ramp to failure, does NOT update baseline
	@$(MAKE) load-up
	@$(MAKE) load-reset
	$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/discovery.js --summary-export=/results/discovery.json
	@echo ">> Read tests/load/results/discovery.json and pick rate for baseline"

load-baseline:    ## stage 1 — capture new baseline (3 runs, median)
	@$(MAKE) load-up
	@for i in 1 2 3; do \
		$(MAKE) load-reset; \
		echo "=== Run $$i/3 ==="; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/subscribe_storm.js --summary-export=/results/subscribe_$$i.json; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/token_flow.js --summary-export=/results/token_$$i.json; \
	done
	go run ./tests/load/scripts/compare.go --update-baseline

load-regression:  ## stage 2 — replay and diff, exit 1 on regression
	@$(MAKE) load-up
	@for i in 1 2 3; do \
		$(MAKE) load-reset; \
		echo "=== Run $$i/3 ==="; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/subscribe_storm.js --summary-export=/results/subscribe_$$i.json; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/token_flow.js --summary-export=/results/token_$$i.json; \
	done
	go run ./tests/load/scripts/compare.go

load-pipeline:    ## background drain throughput test
	@$(MAKE) load-up
	@$(MAKE) load-reset
	go test -tags=loadtest -timeout=15m -v ./tests/load/pipeline/...
```

### 6.2 `compare.go` behavior

For each scenario, `compare.go`:

1. Globs `tests/load/results/<scenario>_*.json` and parses k6 summary JSON.
2. Takes the **median** of p50_ms, p95_ms, p99_ms, RPS, and error rate
   across the discovered runs (typically 3).
3. Filters out samples tagged `phase:warmup` before computing percentiles
   (warmup is the 30s ramp stage at the start of each run).
4. Loads `tests/load/baseline/<scenario>.json` matching the current
   `environment` field.
5. If current `config` differs from baseline `config` (rate, duration,
   `db_max_conns`, `redis_enabled`, etc.), exits with explanatory error
   — **not** treated as regression.
6. Otherwise applies thresholds (below) and writes a markdown diff to
   `tests/load/results/last-run.md`.
7. `--update-baseline` flag: skip threshold check, overwrite the
   baseline file with current run's medians, embed current `git_sha`
   and `captured_at`.

#### Thresholds

| Metric | Regression triggered when |
|---|---|
| `http_req_duration_p95_ms` | current > baseline × **1.20** |
| `http_req_duration_p99_ms` | current > baseline × **1.30** (heavier tail tolerance) |
| `http_req_failed_rate` | current > max(baseline × 1.5, 0.01) |
| `http_reqs_per_sec` | current < baseline × **0.90** |
| pipeline `drain_duration_seconds` | current > baseline × **1.20** |

Exit code 1 on any failed threshold; 0 on pass; 2 on config mismatch.

## 7. CI Integration

### 7.1 `.github/workflows/load-test.yml`

```yaml
name: load-test
on:
  workflow_dispatch:
    inputs:
      mode:
        type: choice
        options: [regression, baseline]
        default: regression
  schedule:
    - cron: "0 3 * * *"  # nightly 03:00 UTC

jobs:
  load:
    runs-on: ubuntu-latest-4-core  # default 2-core gives 30% p95 variance
    timeout-minutes: 25
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v6
      - name: Run load test
        run: make load-${{ inputs.mode || 'regression' }}
      - name: Upload results
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: load-results-${{ github.run_number }}
          path: tests/load/results/
          retention-days: 90
      - name: Open issue on regression
        if: failure() && github.event_name == 'schedule'
        uses: actions/github-script@v7
        with:
          script: |
            github.rest.issues.create({
              ...context.repo,
              title: `Load regression: nightly ${new Date().toISOString().slice(0,10)}`,
              body: `Run: ${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}`,
              labels: ['regression', 'performance']
            })
```

### 7.2 Why not on every PR

- 15 min per run × every push = unaffordable CI budget.
- Shared-runner noise produces false-positive regressions; that erodes
  trust in the signal.
- For this project, a regression detected the next morning is acceptable.

**Exception:** PRs touching hot paths (`internal/subscription/service`,
`/repository`, `internal/notifier`, `internal/confirmer`) — developer runs
`make load-regression` locally and pastes results in the PR description.
Enforced via PR template, not via CI gate.

## 8. Baseline Lifecycle

Updating a baseline is a **deliberate commit of new truth**, not an
automatic slide. Acceptable reasons to run `make load-baseline` and commit:

1. Intentional performance optimization → new numbers are better → fix as
   new floor.
2. Accepted regression for a different goal (more validation, new feature,
   security fix) → new numbers are worse → fix as new ceiling, with a
   `tests/load/CHANGELOG.md` entry explaining why.
3. Test infrastructure change (new runner, new Postgres version, changed
   ENV defaults) → old numbers no longer comparable → new baseline.

**Not acceptable:** updating baseline to "make nightly green again".
A red regression is work — investigate and fix, or explicitly accept and
record.

## 9. Documentation Deliverables

| File | Status | Content |
|---|---|---|
| `docs/adr/0016-load-testing.md` | **new** | Full ADR: decision, context, consequences, alternatives. Cross-references ADR-0015 (testing-trophy) noting load testing is an orthogonal axis (performance, not correctness). |
| `docs/testing.md` | **edited** | New "Load tests" section after E2E; new row in build-tag table for `loadtest`; new "Load tier" prerequisites entry; new "Load" subsection under Philosophy. |
| `tests/load/README.md` | **new** | Quickstart, per-scenario explanation, ENV reference, baseline-update checklist. |
| `CLAUDE.md` | **edited** | Add `make load-*` entries to the Commands block; one-line reference to ADR-0016 in the ADR list. |
| `tests/load/CHANGELOG.md` | **new** | Log of baseline updates with reasons. Empty at creation. |

Existing ADR files (`docs/adr/0001`...`0015`) are **not modified**.

## 10. Out of Scope (deferred)

- Soak / endurance (hours-long constant load): toolchain ready, separate
  spec when needed.
- Capacity ramp-to-failure as a recurring test: discovery covers this
  one-off; recurring capacity probing is a separate concern.
- Live trend dashboards (Grafana Cloud, GitHub Pages): wait for actual
  baseline drift to motivate it.
- Distributed/multi-node k6: not needed at current scale.
- `GET /api/subscriptions?email=...` load scenario: cheap read, deferred.
- Mixed-scenario runs (all three in parallel): isolate first, mix once
  each scenario has a stable baseline.
