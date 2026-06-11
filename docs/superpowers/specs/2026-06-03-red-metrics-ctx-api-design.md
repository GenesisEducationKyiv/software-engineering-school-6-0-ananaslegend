# RED metrics ctx-based API — design spec

Date: 2026-06-03
Status: draft
Related: [ADR-0006](../../adr/0006-logger-via-context.md), [ADR-0004](../../adr/0004-transactor-via-context.md), [ADR-0018](../../adr/0018-red-metrics-conventions.md)

## 1. Goal

Replace the existing `redmetrics.RED.Track(*string)` (pointer-based) and the inline `defer func() { switch { ... } }()` patterns with a context-based API that:

1. Matches the project's "cross-cutting concerns live in `ctx`" idiom (logger via [ADR-0006](../../adr/0006-logger-via-context.md), transaction via [ADR-0004](../../adr/0004-transactor-via-context.md)).
2. Makes call sites read intentionally — `SetSuccess` / `SetError` / `SetResult` instead of mutating a string variable.
3. Eliminates the `result="empty"` label across all workers by making "skip recording" the default when no setter is called.

## 2. Current state

After the most recent refactor (uncommitted), all three outbox-touching workers use the pointer pattern:

```go
result := "ok"
defer worker.red.Track(&result)()
// ...
if err != nil { result = "error" }
if !processed && !processedAny { result = "empty" }
```

Issues we want to fix:

- **`"empty"` is noise.** Idle ticks dominate the `_requests_total` rate. Real productive throughput has to be queried as `{result!="empty"}`, which is unnecessarily defensive. Liveness ("is the worker alive?") is better answered by Prometheus' built-in `up` metric at scrape level.
- **`result := "ok"` + late mutation** is one indirection too many. The "intent at decision point" is hidden — you mutate a string, the defer interprets it.
- **Pattern asymmetry with the rest of the codebase.** `zerolog.Ctx(ctx)` and `transactor.ConnFromContext(ctx, pool)` follow "stash concern in ctx, retrieve where needed". RED currently does neither.

The `emailermetrics.Wrap` decorator stays as-is — it is a transparent wrapper around the `emailer.Emailer` interface, not a call-site instrumentation. Same for the github_client's `cached`-aware recording. Both remain direct `red.Observe(...)` calls.

## 3. API design

### 3.1 Functions

```go
package redmetrics

// Start binds a recorder to ctx and returns a derived ctx plus a stop closure.
//
// The recorder starts in "skip" mode: if stop() fires without any setter
// (SetSuccess / SetError / SetResult) having been called, no observation is
// recorded. This makes empty ticks naturally invisible — just return from the
// idle branch without calling anything.
//
// Idiomatic usage:
//
//   ctx, stop := redmetrics.Start(ctx, n.red)
//   defer stop()
//   ...
//   if err != nil { redmetrics.SetError(ctx); return }
//   redmetrics.SetSuccess(ctx)
//
// Nil-safe: returns the original ctx and a no-op stop when m is nil.
func Start(ctx context.Context, m *RED, extraLabels ...string) (context.Context, func())

// SetSuccess marks the operation as "ok".
func SetSuccess(ctx context.Context)

// SetError marks the operation as "error".
func SetError(ctx context.Context)

// SetResult is the escape hatch for domain-specific result labels declared in
// the subsystem's documentation (e.g. "rate_limited" for scanner,
// "cached" for github_client). Prefer SetSuccess / SetError otherwise.
func SetResult(ctx context.Context, result string)
```

No `Skip` function — the skip case is "do not call any setter".

### 3.2 Implementation

```go
type recorderKey struct{}

type recorder struct {
    red    *RED
    start  time.Time
    extras []string
    result string // "" = skip
}

func Start(ctx context.Context, m *RED, extras ...string) (context.Context, func()) {
    if m == nil {
        return ctx, func() {}
    }
    r := &recorder{red: m, start: time.Now(), extras: extras}
    return context.WithValue(ctx, recorderKey{}, r), func() {
        if r.result == "" {
            return
        }
        m.Observe(r.result, time.Since(r.start), r.extras...)
    }
}

func SetSuccess(ctx context.Context)                  { setResult(ctx, "ok") }
func SetError(ctx context.Context)                    { setResult(ctx, "error") }
func SetResult(ctx context.Context, result string)    { setResult(ctx, result) }

func setResult(ctx context.Context, result string) {
    if r, ok := ctx.Value(recorderKey{}).(*recorder); ok {
        r.result = result
    }
}
```

### 3.3 Removed surface

- `(*RED).Track(result *string, extras ...string) func()` and its two tests — replaced entirely by the ctx-based API. There are no remaining callers after migration.

## 4. Migration in workers

### 4.1 scanner.Tick

```go
func (s *Scanner) Tick(ctx context.Context) error {
    ctx, stop := redmetrics.Start(ctx, s.red)
    defer stop()

    var emptyRun bool
    err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error { ... })

    if err != nil {
        if errors.Is(err, githubclient.ErrRateLimited) {
            s.m.rateLimitedTotal.Inc()
            redmetrics.SetResult(ctx, "rate_limited")
            zerolog.Ctx(ctx).Warn().Err(err).Msg("github rate limited, skipping tick")
            return nil
        }
        redmetrics.SetError(ctx)
        return fmt.Errorf("scanner.Scanner.Tick: %w", err)
    }
    if emptyRun {
        return nil // no setter → no observation
    }
    redmetrics.SetSuccess(ctx)
    return nil
}
```

### 4.2 notifier.Flush

```go
func (n *Notifier) Flush(ctx context.Context) {
    start := time.Now()
    ctx, stop := redmetrics.Start(ctx, n.red)
    defer stop()
    defer func() { n.m.flushDuration.Observe(time.Since(start).Seconds()) }()

    processedAny := false
    for {
        var processed bool
        err := n.tx.WithinTransaction(ctx, func(ctx context.Context) error { ... })
        if err != nil {
            redmetrics.SetError(ctx)
            zerolog.Ctx(ctx).Error().Err(err).Msg("notifier: process next failed")
            return
        }
        if !processed {
            if processedAny {
                redmetrics.SetSuccess(ctx)
            }
            return
        }
        processedAny = true
    }
}
```

The legacy `flushDuration` histogram (slated for cleanup per [ADR-0018](../../adr/0018-red-metrics-conventions.md)) stays in a separate defer for now.

### 4.3 confirmer.Flush

Same shape as notifier but without `flushDuration`:

```go
func (c *Confirmer) Flush(ctx context.Context) {
    ctx, stop := redmetrics.Start(ctx, c.red)
    defer stop()

    processedAny := false
    for {
        var processed bool
        err := c.tx.WithinTransaction(ctx, func(ctx context.Context) error { ... })
        if err != nil {
            redmetrics.SetError(ctx)
            zerolog.Ctx(ctx).Error().Err(err).Msg("confirmer: process next failed")
            return
        }
        if !processed {
            if processedAny {
                redmetrics.SetSuccess(ctx)
            }
            return
        }
        processedAny = true
    }
}
```

## 5. Tests

`internal/observability/redmetrics/redmetrics_test.go` additions:

- `TestStart_SetSuccessRecordsOk` — assert counter+histogram have `result="ok"` exactly once after `Start` → `SetSuccess` → `stop()`.
- `TestStart_SetErrorRecordsError` — same for `result="error"`.
- `TestStart_NoSetterSkipsObservation` — `Start` → `stop()` without setter — assert counter is 0 and histogram empty.
- `TestStart_SetResultCustomLabel` — verify `result="rate_limited"` works for domain-specific values.
- `TestStart_NilREDIsNoOp` — `Start(ctx, nil)` returns the original ctx and a no-op closure; subsequent setter calls must not panic.
- `TestSetters_WithoutStartAreNoOp` — calling `SetSuccess`/`SetError`/`SetResult` on a ctx that has no recorder must not panic and must not affect any future observation.

Remove the two `TestRED_Track_*` tests added for the pointer API.

The existing tests at the worker level (`internal/scanner/*_test.go`, `internal/notifier/*_test.go`, `internal/confirmer/*_test.go`) must continue to pass without modification — they assert on the same metric names and labels that the new API still produces (`ok` / `error` / `rate_limited`). The only behavioral change at those tests' boundary is that previously-asserted `result="empty"` samples (if any) are no longer emitted; those assertions must be removed.

## 6. Documentation updates

### 6.1 [ADR-0018](../../adr/0018-red-metrics-conventions.md)

- Remove `empty` from the list of scanner's domain-specific result values.
- Add a paragraph in **Decision** describing the ctx-based API and the skip-by-default semantics.

### 6.2 Grafana dashboard

`infra/grafana/provisioning/dashboards/reposeetory-red.json`:
- Audit panels that filter on `{result="empty"}` or `{result!="empty"}` — simplify to plain `rate(*_requests_total[5m])` (productive rate is now the default reading).

### 6.3 Operations runbook

`docs/operations/observability.md`:
- No changes needed; the runbook does not reference the `empty` label.

## 7. Risks & mitigations

| Risk | Mitigation |
|---|---|
| **Forgot `SetError` on an error path → silent metric loss.** With skip-by-default, an un-instrumented error path produces no sample at all (no `result="error"` increment, no histogram bucket). | (1) Three call sites only — PR review covers it. (2) Test coverage at the worker level pins the expected metric assertions; deletion of an existing `SetError` would break a test. (3) If the number of call sites grows beyond ~10, revisit with a lint rule that asserts every `Start(...)` has at least one reachable setter before each `return`. |
| **Goroutine sharing the recorder** — if a worker spawns sub-goroutines that race on `SetResult`, the last write wins. | Workers do not currently spawn sub-goroutines mid-tick. If they ever do, the recorder will need internal locking — out of scope for now. |
| **`SetResult` called with arbitrary string** introduces unbounded cardinality. | The escape hatch is documented as "for values declared in the subsystem's ADR section". PR review enforces. |
| **Loss of "worker is alive" signal previously implicit in `empty` samples.** | `up{job="reposeetory"}` already covers process-level liveness. Goroutine-level liveness is out of scope; can be added later via a dedicated `<subsystem>_last_tick_timestamp_seconds` gauge if needed. |

## 8. Out of scope

- Goroutine-safe recorder (no current need).
- Heartbeat gauge per worker (separate consideration if goroutine-level liveness becomes a requirement).
- Removing the legacy `notifier_flush_duration_seconds` histogram (already noted as a separate cleanup in [ADR-0018](../../adr/0018-red-metrics-conventions.md)).
- Migrating `emailermetrics` and `github.CachingReleaseProvider` to the ctx API — they are transparent wrappers/decorators, not call-site instrumentation; the ctx pattern does not fit them and offers no benefit.
- Lint rule to enforce "every `Start` has a reachable setter" — not warranted at 3 call sites.

## 9. Acceptance criteria

- `redmetrics.Start` / `SetSuccess` / `SetError` / `SetResult` exist with the signatures above.
- `(*RED).Track(*string, ...)` is removed; its tests are removed.
- `internal/scanner/scanner.go`, `internal/notifier/notifier.go`, `internal/confirmer/confirmer.go` use the new API; `result="empty"` is no longer emitted by any of them.
- All existing tests pass; new redmetrics tests pass.
- `make lint` reports 0 issues.
- [ADR-0018](../../adr/0018-red-metrics-conventions.md) is updated.
- Grafana dashboard panels do not reference `result="empty"` after the change.
