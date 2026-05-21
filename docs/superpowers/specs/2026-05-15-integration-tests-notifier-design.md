# Integration Tests for `notifier.Notifier` (release outbox drainer) — Design Spec

**Date:** 2026-05-15

## Summary

Add a new integration suite `tests/integration/notifier/` that exercises the
release-notification outbox drainer (`internal/notifier`) end-to-end: real
Postgres (testcontainers), real `notifier/repository` over real
`pkg/transactor`, real `notifier.Notifier`. The mailer is a tiny in-process
spy — `internal/notifier/emailer.StubMailer` only logs and would hide
behaviour; the unit-test gomock lives in a different package and would tightly
couple the integration tests to mock setup.

The HTTP stack is irrelevant here — `Flush(ctx)` is exported precisely so
tests can drive the loop synchronously, mirroring the existing pattern in
`tests/integration/subscription/` (which already inserts release rows
directly via `insertReleaseNotification` to seed downstream behaviour).

## Motivation

`internal/notifier/notifier_test.go` covers `Flush()` with mocked repository,
mailer, and transactor. That validates control flow but skips:

- The real SQL path through `notifier/repository.GetNotificationsWithLock`
  including the JOIN to `subscriptions` + `repositories`, the
  `WHERE rn.sent_at IS NULL` filter, FIFO `ORDER BY rn.created_at`, and
  `LIMIT 1 FOR UPDATE OF rn SKIP LOCKED`.
- The real transaction boundary — that `MarkSent` runs in the same tx as
  `GetNotificationsWithLock`, so a row crashed mid-flush is *not* lost.
- The interaction between `SKIP LOCKED` and parallel drainers (the property
  that protects us against duplicate emails when more than one notifier
  instance runs).
- That `MarkSent` actually flips `sent_at` so the next `Flush()` is a no-op
  (idempotency on the row level).

ADR-0011 prescribes a real Postgres for repository correctness; this spec
applies that to the notifier outbox drainer.

## File Layout

```
tests/integration/notifier/
├── suite_test.go     # NEW — NotifierSuite, spy mailer, DB helpers, metric helpers
└── notifier_test.go  # NEW — Flush() scenarios
```

Both files carry the existing `//go:build integration` tag. No changes to
`tests/integration/internal/` (the shared `App` builder there wires only the
HTTP stack; the notifier owns its own `Registry` here).

## Suite Layout

```go
type NotifierSuite struct {
    suite.Suite
    ctx      context.Context
    cancel   context.CancelFunc

    pg *internal.Postgres   // shared container, lifetime = suite
    // built per-test in SetupTest:
    registry *prometheus.Registry
    mailer   *spyMailer
    tx       *transactor.PgxTransactor
    repo     *notifrepo.Repository
    notifier *notifier.Notifier
}
```

`SetupSuite` starts the Postgres container once; `SetupTest` truncates and
rebuilds `mailer/registry/notifier` so each test owns clean state. The
suite uses `BaseURL = "http://test.local"` to make the unsubscribe-URL
assertion stable.

## Spy Mailer

Lives in `suite_test.go`, kept private to the package:

```go
type spyMailer struct {
    mu       sync.Mutex
    sent     []domain.SendReleaseParams
    err      error          // returned from every SendRelease call
    onCall   func()         // optional hook fired before returning (for race scenarios)
}
```

- `SendRelease` records params and returns `err` (default `nil`).
- `setError(err error)` swaps the return value (used by failure test).
- Confirmation-mail method is omitted — the notifier never calls it.

A spy beats `gomock` here because tests assert on captured payloads (URLs,
ordering) rather than rigid call expectations, and the order of internal
`Flush()` iterations is part of what we're verifying.

## Suite Helpers

| Helper | Purpose |
| --- | --- |
| `seedConfirmedSubscription(email, owner, name string) (subID, repoID int64, unsubToken string)` | Direct insert: confirmed subscription + repository (upsert). Returns IDs needed to enqueue release rows and the unsubscribe token used for URL assertion. |
| `enqueueReleaseNotification(subID, repoID int64, tag string) int64` | Direct `INSERT INTO release_notifications (...) RETURNING id`. `sent_at` left NULL. |
| `enqueueSentReleaseNotification(subID, repoID int64, tag string, sentAt time.Time) int64` | Same, but pre-marked sent — used to assert the drainer skips it. |
| `getReleaseNotification(id int64) (sentAt *time.Time)` | Fetches a single row's `sent_at`. |
| `countPendingReleaseNotifications() int` | `count(*) WHERE sent_at IS NULL`. |
| `assertCounter(name string, want float64)` | Sums every observed sample of the named counter in `s.registry`. Same semantics as the helper in `subscription/suite_test.go` (kept local; not worth moving to `internal/` for two suites). |

## `notifier_test.go` — Test Cases

| # | Method | Pre-conditions | Action | Expected |
| --- | --- | --- | --- | --- |
| 1 | `TestFlush_HappyPath_SingleRow_SendsEmailAndMarksSent` | One pending `release_notifications` row for confirmed sub on `golang/go`, tag `v1.2.3`. | `s.notifier.Flush(ctx)` | spy received exactly one `SendRelease` with `To = email`, `RepoFullName = "golang/go"`, `ReleaseTag = "v1.2.3"`, `ReleaseURL = "https://github.com/golang/go/releases/tag/v1.2.3"`, `UnsubscribeURL = "http://test.local/api/unsubscribe/<token>"`. Row's `sent_at` is now set (`require.NotNil`). `notifier_emails_sent_total{result="ok"}` = 1; `result="error"` = 0. |
| 2 | `TestFlush_Empty_NoMailerCalls_NoMetricChange` | DB truncated. | `Flush(ctx)` | spy received zero calls. Both counter labels remain 0. |
| 3 | `TestFlush_MultipleRows_AllProcessedInFIFOOrder` | Three pending rows, inserted in known `created_at` order (`now-3m`, `now-2m`, `now-1m`) for tags `t-old`, `t-mid`, `t-new`. | `Flush(ctx)` | spy received three calls; `params[i].ReleaseTag` matches `[t-old, t-mid, t-new]`. All three rows have `sent_at` set. `notifier_emails_sent_total{result="ok"}` = 3. |
| 4 | `TestFlush_MailerError_RowRemainsPending_LoopStops` | One pending row. `mailer.setError(errors.New("smtp fail"))`. | `Flush(ctx)` | spy got exactly 1 call. Row's `sent_at` is still NULL. `notifier_emails_sent_total{result="error"}` = 1; `result="ok"` = 0. A subsequent `Flush()` (after `mailer.setError(nil)`) drains it and increments `result="ok"` to 1 — proves the row was not lost. |
| 5 | `TestFlush_AlreadySent_RowSkipped` | One row with `sent_at = now-1h` (pre-sent), one pending row. | `Flush(ctx)` | spy got 1 call (for the pending row only). |
| 6 | `TestFlush_Idempotent_SecondRunIsNoOp` | One pending row. | `Flush(ctx)` then `Flush(ctx)` again. | spy got exactly 1 call total. `notifier_emails_sent_total{result="ok"}` = 1. |
| 7 | `TestFlush_ConcurrentDrainers_NoDuplicateSendsViaSkipLocked` | Two pending rows. Two `Notifier` instances built against the same pool/registry/mailer. | `errgroup` runs `n1.Flush(ctx)` and `n2.Flush(ctx)` in parallel. | spy got exactly 2 calls (no duplicates), one row each. `count(* WHERE sent_at IS NULL) == 0`. `notifier_emails_sent_total{result="ok"}` = 2. Run with `go test -race`. |

Test 7 builds a second `notifier.Notifier` sharing the spy, registry, and
pool but with its own transactor instance — that's enough to exercise two
distinct DB transactions racing for `FOR UPDATE SKIP LOCKED`. The mailer
spy's mutex serialises capture, so race-detector findings would point at the
real product code.

## Out of Scope

- HTTP-level coverage (already in `tests/integration/subscription/`).
- `notifier_flush_duration_seconds` histogram values — observed by
  test 1 implicitly (assertion would just verify "non-zero", which is
  brittle); kept out so we don't pin timing.
- The `Run(ctx)` ticker loop — exercised only via `Flush()` since `Run` is
  a thin `time.Ticker` wrapper around `Flush` and ticker tests are flaky
  by nature. Same choice as the upstream `notifier_test.go` unit tests.
- Confirmer outbox — separate suite, separate spec.

## Links

- ADR-0003 — async email via outbox tables.
- ADR-0004 — transactor through context.
- ADR-0011 — testing strategy (real Postgres for repository, mock at
  consumer-side interfaces).
- `internal/notifier/notifier.go` — drainer under test.
- `internal/notifier/repository/db.go` — SQL surface under test.
- `tests/integration/subscription/suite_test.go` — sibling suite pattern.
