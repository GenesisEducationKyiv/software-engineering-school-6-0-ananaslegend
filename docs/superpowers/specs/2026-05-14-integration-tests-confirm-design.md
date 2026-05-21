# Integration Tests for `/api/confirm/{token}` — Design Spec

**Date:** 2026-05-14

## Summary

Add an integration test suite for the `GET /api/confirm/{token}` endpoint, mounted on the existing `SubscriptionSuite` (`tests/integration/subscription/`). The suite reuses the Postgres container, the GitHub REST fixture, and the wired-up `App` from the `/api/subscribe` work; the only test-infrastructure changes are a handful of new helper methods on the suite and a small refactor of `assertCreatedCounter` into a parameterised `assertCounter`. No production code changes.

Six test cases cover the full status-code matrix that `internal/subscription/http/handler.go::Confirm` can produce — 200 (HTML), 404 (token not found), 410 (token expired) — plus the database-level invariants that the unit tests of `service.Confirm` and `Repository.MarkConfirmed` cannot observe in isolation (token consumption, atomic single-success under concurrent requests).

## Motivation

`service.Confirm` is already covered by mock-based unit tests at the service layer. What those tests cannot prove:

- That the `chi` route `/api/confirm/{token}` binds the path parameter the handler reads via `chi.URLParam(r, "token")` — a typo here is invisible to the service.
- That `Repository.MarkConfirmed` actually writes `confirmed_at`, `confirm_token = NULL`, `confirm_token_expires_at = NULL` against the real schema. A wrong column name passes the mock and breaks production.
- That the handler maps `ErrTokenNotFound`→404 and `ErrTokenExpired`→410 to the HTML renderer with the correct `Content-Type`.
- That confirming a token twice does not increment `subscriptions_confirmed_total` twice (the second attempt must find no row, because `MarkConfirmed` clears `confirm_token`).
- That the route doesn't panic or return 500 when handed an arbitrary 1024-character path parameter — a defensive check against future regressions in routing or token comparison.

These are all SQL- and HTTP-shaped concerns, exactly where ADR-0011 prescribes integration tests.

## File Layout

```
tests/integration/subscription/
├── suite_test.go        # MODIFIED — new helpers (get, getConfirmTokenForEmail,
│                        #   setConfirmTokenExpired, assertCounter, assertConfirmedCounter)
├── subscribe_test.go    # unchanged
└── confirm_test.go      # NEW — six `(s *SubscriptionSuite) TestConfirm_*` methods
```

`tests/integration/internal/` is untouched. The Postgres helper, GitHub REST fixture, and `App` composition root from the subscribe spec remain authoritative; this spec only consumes them.

The `//go:build integration` tag applies to the new file and the modified file (already present).

## Dependencies

None. `testify`, `testcontainers-go`, `gofakeit/v7`, and `prometheus/client_golang` are already vendored from the previous spec.

## Production Code Changes

**None.** All behaviour required by the test cases is already present in `internal/subscription/{service,repository,http}` and `internal/subscription/http/pages`. The 6 tests exercise the existing code paths; no refactors are needed to make the code testable.

## Test-Infrastructure Refactor: `assertCounter`

Current `suite_test.go`:

```go
func (s *SubscriptionSuite) assertCreatedCounter(want float64) {
    s.T().Helper()
    mf, err := s.app.Registry.Gather()
    require.NoError(s.T(), err)
    var got float64
    for _, m := range mf {
        if m.GetName() != "subscriptions_created_total" {
            continue
        }
        for _, metric := range m.GetMetric() {
            got += metric.GetCounter().GetValue()
        }
    }
    require.Equal(s.T(), want, got, "subscriptions_created_total")
}
```

New shape — extract the loop into a parameterised helper, keep both metric-specific wrappers:

```go
// assertCounter sums every observed sample for the named counter and asserts
// it equals want. Works whether or not the counter has been touched (the
// metric may not appear in Gather() output if it was never incremented).
func (s *SubscriptionSuite) assertCounter(name string, want float64) {
    s.T().Helper()
    mf, err := s.app.Registry.Gather()
    require.NoError(s.T(), err)
    var got float64
    for _, m := range mf {
        if m.GetName() != name {
            continue
        }
        for _, metric := range m.GetMetric() {
            got += metric.GetCounter().GetValue()
        }
    }
    require.Equal(s.T(), want, got, name)
}

func (s *SubscriptionSuite) assertCreatedCounter(want float64) {
    s.T().Helper()
    s.assertCounter("subscriptions_created_total", want)
}

func (s *SubscriptionSuite) assertConfirmedCounter(want float64) {
    s.T().Helper()
    s.assertCounter("subscriptions_confirmed_total", want)
}
```

The 8 existing subscribe tests keep their `s.assertCreatedCounter(...)` calls — behaviour is identical.

## New Suite Helpers

Added to `suite_test.go`:

| Helper | Signature | Body |
| --- | --- | --- |
| `get` | `(s) get(path string) *http.Response` | Build `http.NewRequestWithContext(s.ctx, GET, s.app.Server.URL+path, nil)`, send via `s.app.Client.Do`, `require.NoError`, return. Caller closes `Body`. |
| `getConfirmTokenForEmail` | `(s) getConfirmTokenForEmail(email string) string` | `SELECT confirm_token FROM subscriptions WHERE email = $1` — scans into `*string`, `require.NotNil` on the result, returns the dereferenced value. Used by tests that need a valid token after `POST /api/subscribe`. |
| `setConfirmTokenExpired` | `(s) setConfirmTokenExpired(subID int64, expiresAt time.Time)` | `UPDATE subscriptions SET confirm_token_expires_at = $1 WHERE id = $2`. Used by `TestConfirm_TokenExpired` to age a freshly-created subscription. |

These helpers live in the same `// --- DB helpers ---` and `// --- HTTP helpers ---` sections that already exist.

## Test Cases — `confirm_test.go`

Six methods on `SubscriptionSuite`:

| # | Method | Pre-conditions | Request | Expected |
| --- | --- | --- | --- | --- |
| 1 | `TestConfirm_HappyPath` | `githubFx.SetRepoExists("golang/go", true)`; `POST /api/subscribe` with random email and `golang/go`; capture `token := s.getConfirmTokenForEmail(email)`. | `GET /api/confirm/{token}`. | `200`; `Content-Type` contains `text/html`; body non-empty. DB after: exactly one `subscriptions` row for that email with `confirmed_at` set within the last minute, `confirm_token IS NULL`, `confirm_token_expires_at IS NULL`. Metric `subscriptions_confirmed_total == 1`. |
| 2 | `TestConfirm_TokenNotFound` | — | `GET /api/confirm/non-existent-token-abc123`. | `404`; `Content-Type` contains `text/html`. No DB rows present (truncated by `SetupTest`). Metric `subscriptions_confirmed_total == 0`. |
| 3 | `TestConfirm_TokenExpired` | Subscribe via API; read the subscription row; `setConfirmTokenExpired(sub.ID, time.Now().Add(-time.Hour))`. | `GET /api/confirm/{token}`. | `410`; `Content-Type` contains `text/html`. DB after: `confirmed_at IS NULL` (no mutation), `confirm_token` and `confirm_token_expires_at` unchanged (the handler returns before `MarkConfirmed`). Metric `subscriptions_confirmed_total == 0`. |
| 4 | `TestConfirm_TokenIsConsumed` | Subscribe via API; capture `token`. | `GET /api/confirm/{token}` twice in sequence. | First: `200`. Second: `404` — because the successful first call set `confirm_token = NULL`, so the same token URL no longer matches any row. Metric `subscriptions_confirmed_total == 1` (single increment). |
| 5 | `TestConfirm_Concurrent` | Subscribe via API; capture `token`. | Launch 5 goroutines via `sync.WaitGroup`, each issuing `GET /api/confirm/{token}`; collect status codes under a mutex. | Every response is either `200` or `404` (no `500`, no panics). At least one `200` is observed. DB after: one row, `confirmed_at` not nil, `confirm_token IS NULL`. Metric `subscriptions_confirmed_total >= 1`. |
| 6 | `TestConfirm_LongRandomToken` | — | `GET /api/confirm/{1024-char hex string}`. | `404`; `Content-Type` contains `text/html`; no panic / no 500. Metric `subscriptions_confirmed_total == 0`. |

### Notes on Specific Tests

**HTML body assertion.** All six tests assert only `Content-Type: text/html` + body non-empty (`assert.NotEmpty(body)`). They do **not** scrape the HTML for specific phrases like "Confirmed" or "Link expired"; that is brittle (template wording can change without changing behaviour) and is already covered by `internal/subscription/http/pages/pages_test.go`.

**`TestConfirm_TokenExpired` — why UPDATE rather than configure short TTL.** Reducing `ConfirmTokenTTL` in `AppConfig` to `1ms` and inserting `time.Sleep(5*ms)` is flaky on a busy CI host and visibly slow. The `UPDATE ... SET confirm_token_expires_at = $1` path is deterministic, finishes in microseconds, and exercises exactly the same handler branch (`now.After(*sub.ConfirmTokenExpiresAt)` in `service.Confirm`).

**`TestConfirm_Concurrent` — why the assertion is a floor, not an equality.** `service.Confirm` runs `GetByConfirmToken` (a plain `SELECT WHERE confirm_token=$1`, no `FOR UPDATE`) followed by `MarkConfirmed` (`UPDATE ... WHERE id=$2`, no `AND confirmed_at IS NULL`, no `RETURNING` check). Two goroutines that both finish their `SELECT` before either has committed its `UPDATE` will each see the row, each issue an idempotent `UPDATE`, and each increment `subscriptions_confirmed_total`. The race window is small but not zero with 5 concurrent requests. A stricter assertion ("exactly one 200") would therefore flake without a production fix — adding `AND confirmed_at IS NULL` + `RETURNING id` to the `MarkConfirmed` SQL, and propagating `rows_affected == 0` as `ErrAlreadyConfirmed` in `service.Confirm`. That fix is out of scope for this spec. As written, the test still catches: panics under concurrent access, total loss of success (zero `200`s), inconsistent terminal state, and `500` responses. The race-tightening can be revisited in a follow-up spec, at which point this test can be tightened to `== 1` simultaneously.

**`TestConfirm_LongRandomToken` — why 1024 chars.** Far above any plausible legitimate token length (`domain.GenerateToken()` returns 64-char hex), but well under typical URL-length limits. Catches any path-handling code that truncates, allocates per-byte, or does pathological work on the parameter. The expected outcome is the same as any other unknown-token case — `404`.

## Error Path Coverage (Handler)

Combined with the subscribe suite, these tests close out the remaining branches of `errorStatus` in `internal/subscription/http/handler.go`:

| `errorStatus` branch | Test |
| --- | --- |
| `ErrTokenNotFound` → 404 | #2, #4 (second call), #5 (four of five calls), #6 |
| `ErrTokenExpired` → 410 | #3 |
| Happy-path renderer (`pages.Confirmed`) | #1, #4 (first call), #5 (one of five calls) |
| `default` → 500 (renders `pages.Oops`) | not exercised here — would require a DB outage; out of scope. |

## Test Isolation

`SetupTest` already runs `TRUNCATE ... RESTART IDENTITY CASCADE` on all four tables and rebuilds `App` with a fresh `*prometheus.Registry`. The new tests need no additional reset hooks.

`gofakeit.Email()` provides per-test uniqueness for `email` values; collisions are effectively impossible inside a single run.

## Makefile / CI / Docs

No changes. `make test-integration` and `make test-all` already cover `tests/integration/...` and pick up the new file via the build tag.

## Out of Scope

- `/api/unsubscribe/{token}` integration tests (a sibling spec).
- `/api/subscriptions?email=` integration tests.
- Cron worker integration suites (`scanner`, `notifier`, `confirmer`).
- Internal-server-error path on `/api/confirm` (would require simulating a DB outage; covered by handler unit tests on `errorStatus`).
- HTML-content assertions (covered by `pages/pages_test.go`).
- Property/fuzzing tests on token format.

## Links

- ADR-0011 — testing strategy (the principle this spec implements at the HTTP layer for `confirm`).
- `docs/superpowers/specs/2026-05-13-integration-tests-subscribe-design.md` — the sibling spec that established the infrastructure reused here.
- `internal/subscription/http/handler.go::Confirm` — the handler under test.
- `internal/subscription/service/service.go::Confirm` — the service logic exercised end-to-end.
- `internal/subscription/repository/subscriptions.go::{GetByConfirmToken, MarkConfirmed}` — the SQL whose correctness this suite verifies.
- `internal/subscription/http/pages/pages.go` — the renderer whose `Confirmed` / `Unavailable` responses are status-asserted (not content-asserted) here.