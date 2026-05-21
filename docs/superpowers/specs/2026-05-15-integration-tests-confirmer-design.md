# Integration Tests for `confirmer` Cron — Design Spec

**Date:** 2026-05-15

## Summary

Add an integration test suite for the `internal/confirmer` outbox drainer (a cron-style background worker run from `internal/app/workers.go`). The new suite lives under `tests/integration/crons/` because both `confirmer` and `scanner` are cron workers and will share the same package; this spec only covers the `confirmer` half. The suite drives `*Confirmer.Flush` against a real Postgres container, a real `pgx` repository, the real `PgxTransactor`, and a real Mailpit container in place of an SMTP server. There are no production code changes.

Nine test cases cover the database-level invariants and side effects that the existing mock-based unit tests at `internal/confirmer/confirmer_test.go` cannot prove: real SQL of `GetConfirmationsWithLock` (the `FOR UPDATE OF cn SKIP LOCKED` + `sent_at IS NULL` + `confirm_token IS NOT NULL` predicate), `MarkSent` actually setting `sent_at = NOW()`, processing order by `created_at`, atomic single-success on concurrent flushes against the same row (skip-locked semantics), correct `ConfirmURL` shape, and the Prometheus counter labels.

## Motivation

`confirmer.Confirmer` is already covered by mock-based unit tests at the package layer (`internal/confirmer/confirmer_test.go`). What those tests cannot prove:

- That the SQL in `Repository.GetConfirmationsWithLock` actually filters by `sent_at IS NULL` and `s.confirm_token IS NOT NULL` against the real schema — a wrong column name passes the mock and breaks production.
- That `ORDER BY cn.created_at` returns rows in insertion order, which the confirmer relies on for fair drain.
- That `MarkSent` writes `sent_at` on the correct row and that subsequent `GetConfirmationsWithLock` calls correctly skip already-sent rows.
- That `FOR UPDATE OF cn SKIP LOCKED` actually causes two concurrent `Flush` invocations on the same row to observe one success and one no-op, not two duplicate emails.
- That the composed `ConfirmURL` (`baseURL + "/api/confirm/" + token`) arrives at the SMTP receiver verbatim, with `RepoFullName` rendered correctly into both HTML and plaintext alternatives.
- That `MarkSent` is **not** called when the mailer returns an error, leaving the row eligible for the next tick.
- That the Prometheus counter `confirmer_emails_sent_total` increments with the correct `result=ok` / `result=error` label.

These are all SQL- and SMTP-shaped concerns, exactly where ADR-0011 prescribes integration tests.

## File Layout

```
tests/integration/
├── internal/
│   ├── pg.go            # unchanged
│   ├── github.go        # unchanged
│   ├── app.go           # unchanged
│   └── mailpit.go       # NEW — Mailpit testcontainer + REST API client
└── crons/
    ├── suite_test.go    # NEW — CronsSuite wires Postgres, Mailpit, real Confirmer
    └── confirmer_test.go # NEW — TestConfirmer_* methods
```

The `//go:build integration` tag applies to every new file.

`tests/integration/internal/pg.go` is reused as-is — the truncation list already includes `confirmation_notifications`. A small follow-up will be needed if/when scanner tests are added (a `seedRelease`-style helper), but that is out of scope here.

## Dependencies

No new module dependencies. `testcontainers-go`, `testify`, `gofakeit/v7`, and `prometheus/client_golang` are already vendored. Mailpit is reached via two of its ports on the container: SMTP on `1025/tcp` (used by the real `SMTPMailer`) and HTTP on `8025/tcp` (used by the suite to read what was actually delivered).

## Production Code Changes

**None.** `Confirmer.Flush`, `Repository`, `SMTPMailer`, and `PgxTransactor` are all already shaped to be driven from a test — `confirmer.Config` accepts the dependencies directly, and `Flush` is exported precisely so tests can drive a single drain pass without spinning up the time.Ticker.

## Test Infrastructure

### `tests/integration/internal/mailpit.go`

A new helper exposing:

```go
type Mailpit struct {
    Host    string  // host reachable from the test process
    SMTPPort int    // mapped 1025/tcp
    APIBase string  // e.g. "http://127.0.0.1:32801"
}

func NewMailpit(ctx context.Context, t testing.TB) *Mailpit
func (m *Mailpit) Reset(ctx context.Context, t testing.TB)
func (m *Mailpit) Messages(ctx context.Context, t testing.TB) []MailpitMessage
```

Uses `axllent/mailpit:latest`, waits for the `/api/v1/info` endpoint to respond 200, and is bound to `t.Cleanup` so the container is terminated when the suite exits. `Reset` calls `DELETE /api/v1/messages` so each test starts with an empty inbox. `Messages` fetches `GET /api/v1/messages` and decodes the relevant fields (`To`, `Subject`, `Snippet`); to access the full HTML/text body the helper exposes `MessageSource(id)`.

### `CronsSuite` (suite_test.go)

```go
type CronsSuite struct {
    suite.Suite

    ctx    context.Context
    cancel context.CancelFunc

    pg      *internal.Postgres
    mailpit *internal.Mailpit

    registry  *prometheus.Registry
    txr       *transactor.PgxTransactor
    mailer    *emailer.SMTPMailer
    confirmer *confirmer.Confirmer

    baseURL string
}
```

- `SetupSuite` brings up Postgres + Mailpit once and gofakeit-seeds.
- `SetupTest` truncates the DB, calls `mailpit.Reset`, recreates the Prometheus registry, and re-wires `transactor`, `SMTPMailer`, and `Confirmer` so every test starts from a clean composition root.
- `TearDownSuite` cancels the context.

The SMTP mailer is built with `TLSPolicy: "none"`, `User: ""` (mailpit accepts unauthenticated), `From: "test@reposeetory.test"`, `Host: mailpit.Host`, `Port: mailpit.SMTPPort`.

### Helpers on `CronsSuite`

```go
func (s *CronsSuite) seedPendingNotification(email, owner, name, token string) (subID, notifID int64)
func (s *CronsSuite) markSent(notifID int64)            // pretend an earlier tick already delivered
func (s *CronsSuite) clearConfirmToken(subID int64)     // simulate a sub that was confirmed after enqueue
func (s *CronsSuite) selectNotification(id int64) (sentAt *time.Time)
func (s *CronsSuite) countSentNotifications() int
func (s *CronsSuite) assertConfirmerCounter(label string, want float64)
```

The seed helper uses `gofakeit.UUID()` for the unsubscribe token, copies the pending-row schema invariants from `subscription`'s suite, and inserts the `confirmation_notifications` row in the same transaction.

## Test Cases

All run from `(s *CronsSuite) TestConfirmer_*` so they share the suite's `SetupTest`. Names mirror the unit-test style.

| # | Name | Setup | Action | Assertions |
|---|------|-------|--------|------------|
| 1 | `TestConfirmer_Flush_Empty_NoEmails` | None. | `confirmer.Flush(ctx)`. | Mailpit inbox empty; counter zero. |
| 2 | `TestConfirmer_Flush_One_SendsAndMarksSent` | Seed one pending notification. | `Flush`. | 1 message in mailpit, `To` and `Subject` shaped correctly; `sent_at` is non-NULL and within the last minute; counter `result=ok` = 1. |
| 3 | `TestConfirmer_Flush_Multiple_AllSent` | Seed three pending notifications (distinct emails). | `Flush`. | 3 messages in mailpit; all three `sent_at` non-NULL; counter `result=ok` = 3. |
| 4 | `TestConfirmer_Flush_SkipsAlreadySent` | Seed two pending, mark the first as sent. | `Flush`. | 1 message; only the second row had its `sent_at` updated (the first row's pre-existing `sent_at` is unchanged). |
| 5 | `TestConfirmer_Flush_SkipsWhenTokenCleared` | Seed one pending, then `clearConfirmToken(subID)`. | `Flush`. | 0 messages; notification row's `sent_at` is still NULL (predicate `confirm_token IS NOT NULL` blocks the join). |
| 6 | `TestConfirmer_Flush_ProcessingOrder_ByCreatedAt` | Seed three notifications with `created_at` going *backwards* in time. | `Flush`. | All three delivered; mailpit message order matches the `created_at` order (oldest first). |
| 7 | `TestConfirmer_Flush_ConfirmURL_Composition` | Seed one pending with a fixed token like `abc123`. | `Flush`. | Mailpit message body contains `baseURL + "/api/confirm/abc123"`. |
| 8 | `TestConfirmer_Flush_MailerError_NoMarkSent` | Replace the mailer with one whose `Host` points at a closed TCP port. Seed one pending. | `Flush`. | 0 messages reach mailpit; notification's `sent_at` still NULL; counter `result=error` = 1; row is still eligible for the next tick. |
| 9 | `TestConfirmer_Flush_SkipLocked_ConcurrentDrain` | Seed one pending notification. Launch two `Flush` goroutines on independent `Confirmer` instances sharing the same pool. | `wg.Wait`. | Exactly one message lands in mailpit; one of the two flushes performed the send; combined counter `result=ok` = 1. |

The concurrent test is the only one that uses `sync.WaitGroup`; it follows the same pattern as `TestConfirm_Concurrent` in `subscription_test.go` but at the SQL layer.

## Stability Knobs

- `mailpit.Reset` is the per-test cleanup; the inbox is never assumed to be empty without it.
- Mailpit's REST API is queried with a short polling loop (max 2s, 50ms cadence) because `DialAndSend` returns before the message lands in mailpit's index. The loop exits as soon as `len(Messages) >= expected`. Race-detector clean.
- `assertConfirmerCounter` iterates over `MetricFamily.GetMetric()` and matches on the `result` label.

## Out of Scope

- Scanner integration tests (will arrive in a separate spec / commit, sharing the `crons` package).
- The notifier outbox drainer (a separate package; same shape, separate spec).
- Email body templating regressions beyond the URL assertion (covered by `emailer` unit tests).
- TLS / authenticated SMTP — Mailpit is configured plaintext, mirroring the local dev `docker-compose` setup.
