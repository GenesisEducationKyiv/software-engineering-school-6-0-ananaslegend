# Integration Tests for `/api/unsubscribe/{token}` — Design Spec

**Date:** 2026-05-14

## Summary

Add an integration test suite for the `GET /api/unsubscribe/{token}` endpoint, mounted on the existing `SubscriptionSuite` (`tests/integration/subscription/`). The suite reuses the Postgres container, the GitHub REST fixture, and the wired-up `App` from the `/api/subscribe` and `/api/confirm` work; the only test-infrastructure changes are a handful of new helper methods on the suite and a one-line `assertDeletedCounter` wrapper around the already-extracted `assertCounter`.

Six test cases cover the full status-code matrix that `internal/subscription/http/handler.go::Unsubscribe` can produce — 200 (HTML) and 404 (token not found) — plus the database-level invariants that the unit tests cannot observe: the GDPR hard-delete contract (subscription row plus `confirmation_notifications` plus `release_notifications` purged via `ON DELETE CASCADE`), idempotency under a repeated request, and exactly-once success under concurrent requests against the same token.

No production code changes.

## Motivation

`service.Unsubscribe` is already covered by mock-based unit tests at the service layer. What those tests cannot prove:

- That the `chi` route `/api/unsubscribe/{token}` binds the path parameter the handler reads via `chi.URLParam(r, "token")` — a typo here is invisible to the service.
- That `Repository.DeleteByUnsubscribeToken` actually issues a `DELETE` against the real schema and that `RowsAffected()` correctly distinguishes hit-vs-miss.
- That the FK `ON DELETE CASCADE` declared in `migrations/000002_release_notifications.up.sql` and `migrations/000003_confirmation_notifications.up.sql` is wired correctly — i.e. deleting a subscription really does purge every outbox row that references it. This is the GDPR contract the endpoint exists to enforce; it lives entirely in SQL DDL and cannot be exercised by mocks.
- That the handler maps `ErrTokenNotFound` → 404 to the HTML renderer with the correct `Content-Type`.
- That two requests on the same token cannot both succeed (counter does not double-increment).
- That the route doesn't panic or return 500 when handed an arbitrary 1024-character path parameter — a defensive check against future regressions in routing or token comparison.

These are all SQL- and HTTP-shaped concerns, exactly where ADR-0011 prescribes integration tests.

## File Layout

```
tests/integration/subscription/
├── suite_test.go         # MODIFIED — new helpers (getUnsubscribeTokenForEmail,
│                         #   countSubscriptionsByEmail, countReleaseNotifications,
│                         #   insertReleaseNotification, assertDeletedCounter)
├── subscribe_test.go     # unchanged
├── confirm_test.go       # unchanged (assumed merged before this work)
└── unsubscribe_test.go   # NEW — six `(s *SubscriptionSuite) TestUnsubscribe_*` methods
```

`tests/integration/internal/` is untouched. The Postgres helper, GitHub REST fixture, and `App` composition root from the subscribe spec remain authoritative; this spec only consumes them.

The `//go:build integration` tag applies to the new file and the modified file (already present).

## Dependencies

None. `testify`, `testcontainers-go`, `gofakeit/v7`, and `prometheus/client_golang` are already vendored from the previous specs.

## Production Code Changes

**None.** All behaviour required by the test cases is already present in `internal/subscription/{service,repository,http}`, `internal/subscription/http/pages`, and the migration-level FK declarations. The 6 tests exercise the existing code paths; no refactors are needed to make the code testable.

## New Suite Helpers

Added to `suite_test.go`. The HTTP `get` helper and the parameterised `assertCounter` (with `assertCreatedCounter` / `assertConfirmedCounter` wrappers) are assumed to be in place from the `/api/confirm` spec.

| Helper | Signature | Body |
| --- | --- | --- |
| `getUnsubscribeTokenForEmail` | `(s) getUnsubscribeTokenForEmail(email string) string` | `SELECT unsubscribe_token FROM subscriptions WHERE email = $1` — scans into `string`, `require.NotEmpty` on the result, returns it. Used by tests that need a valid token after `POST /api/subscribe`. |
| `countSubscriptionsByEmail` | `(s) countSubscriptionsByEmail(email string) int` | `SELECT count(*) FROM subscriptions WHERE email = $1`. Sharper assertion than `len(selectSubscriptionsByEmail(...))` when the test only cares about presence/absence. |
| `countReleaseNotifications` | `(s) countReleaseNotifications(subID int64) int` | `SELECT count(*) FROM release_notifications WHERE subscription_id = $1`. Used to prove the FK CASCADE removed the seeded outbox row. |
| `insertReleaseNotification` | `(s) insertReleaseNotification(subID, repoID int64, tag string)` | `INSERT INTO release_notifications (subscription_id, repository_id, release_tag) VALUES ($1,$2,$3)`. Directly seeded — there is no production code path that creates these rows at subscribe time (the scanner/notifier writes them on real releases), so the test must INSERT them by hand to exercise CASCADE. |
| `assertDeletedCounter` | `(s) assertDeletedCounter(want float64)` | One-liner: `s.assertCounter("subscriptions_deleted_total", want)`. Mirrors `assertCreatedCounter` / `assertConfirmedCounter`. |

These helpers live in the same `// --- DB helpers ---` and `// --- Metrics ---` sections that already exist.

## Test Cases — `unsubscribe_test.go`

Six methods on `SubscriptionSuite`:

| # | Method | Pre-conditions | Request | Expected |
| --- | --- | --- | --- | --- |
| 1 | `TestUnsubscribe_HappyPath` | `githubFx.SetRepoExists("golang/go", true)`; `POST /api/subscribe` with random email and `golang/go`; capture `subID, repoID` from `selectSubscriptionsByEmail` and `selectRepository`; `insertReleaseNotification(subID, repoID, "v1.0.0")` to seed an outbox row; `token := s.getUnsubscribeTokenForEmail(email)`. | `GET /api/unsubscribe/{token}`. | `200`; `Content-Type` contains `text/html`; body non-empty. DB after: `countSubscriptionsByEmail(email) == 0`, `countConfirmationNotifications(subID) == 0` (CASCADE), `countReleaseNotifications(subID) == 0` (CASCADE). Metric `subscriptions_deleted_total == 1`. |
| 2 | `TestUnsubscribe_HappyPath_AfterConfirm` | Subscribe via API; `GET /api/confirm/{confirmToken}` to flip the row to confirmed; capture `unsubscribeToken`. | `GET /api/unsubscribe/{unsubscribeToken}`. | `200`; `Content-Type` contains `text/html`. DB after: `countSubscriptionsByEmail(email) == 0`. Metric `subscriptions_deleted_total == 1`. Proves the endpoint works regardless of whether the subscription is pending or confirmed — the handler does not inspect `confirmed_at`. |
| 3 | `TestUnsubscribe_TokenNotFound` | — | `GET /api/unsubscribe/non-existent-token-abc123`. | `404`; `Content-Type` contains `text/html`. No DB rows present (truncated by `SetupTest`). Metric `subscriptions_deleted_total == 0`. |
| 4 | `TestUnsubscribe_Idempotency` | Subscribe via API; capture `token`. | `GET /api/unsubscribe/{token}` twice in sequence. | First: `200`. Second: `404` — because the first call removed the row, so the same token URL no longer matches anything. Metric `subscriptions_deleted_total == 1` (single increment; the second call short-circuits in the service at `if !deleted`). |
| 5 | `TestUnsubscribe_Concurrent` | Subscribe via API; capture `token`. | Launch 5 goroutines via `sync.WaitGroup`, each issuing `GET /api/unsubscribe/{token}`; collect status codes under a mutex. | **Exactly one** `200` and **exactly four** `404`. No `500`s, no panics. DB after: `countSubscriptionsByEmail(email) == 0`. Metric `subscriptions_deleted_total == 1`. |
| 6 | `TestUnsubscribe_LongRandomToken` | — | `GET /api/unsubscribe/{1024-char hex string}`. | `404`; `Content-Type` contains `text/html`; no panic / no 500. Metric `subscriptions_deleted_total == 0`. |

### Notes on Specific Tests

**HTML body assertion.** All six tests assert only `Content-Type: text/html` + body non-empty (`assert.NotEmpty(body)`). They do **not** scrape the HTML for specific phrases like "Unsubscribed" or "Link not found"; that is brittle (template wording can change without changing behaviour) and is already covered by `internal/subscription/http/pages/pages_test.go`.

**`TestUnsubscribe_HappyPath` — why also seed `release_notifications`.** The endpoint exists to satisfy GDPR hard-delete: unsubscribing must purge every artifact tied to the email/subscription pair. `confirmation_notifications` is created transactionally by `Repository.CreateSubscription` (so it shows up for free after `POST /api/subscribe`), but `release_notifications` rows are written only by the scanner on real GitHub releases — they never exist at subscribe time in a test run. To prove the CASCADE on `release_notifications.subscription_id` is in place, the test must INSERT one by hand. Without this seed, the CASCADE on the release outbox would be untested in any integration suite; a future migration that drops `ON DELETE CASCADE` from that FK would silently break GDPR compliance and pass all tests.

**`TestUnsubscribe_HappyPath_AfterConfirm` — why a second happy path.** It would be tempting to fold this into #1 by always confirming before unsubscribing. We don't, because the handler/service contract is "delete by token, regardless of state" — and that statelessness is itself worth pinning down. If a future change adds a `WHERE confirmed_at IS NOT NULL` clause to `DeleteByUnsubscribeToken` (a plausible-looking "defensive" edit), test #1 still passes (it doesn't confirm) and test #2 catches the regression. Splitting the two costs ~15 lines of test code and gains explicit coverage of both states.

**`TestUnsubscribe_Idempotency` — why metric `== 1`, not `>= 1`.** The service flow is `DeleteByUnsubscribeToken` → if `!deleted` return `ErrTokenNotFound` (no Inc) → otherwise `subscriptionsDeleted.Inc()`. The second call deterministically falls into the `!deleted` branch (the row is already gone), so equality is safe. This contrasts with the confirm-flow idempotency check, where the counter could in principle double-tick under concurrency; here, sequential calls are strictly single-increment.

**`TestUnsubscribe_Concurrent` — why strict equality is safe.** `Repository.DeleteByUnsubscribeToken` issues a single statement: `DELETE FROM subscriptions WHERE unsubscribe_token = $1`. Under Postgres' default READ COMMITTED isolation, concurrent `DELETE`s against the same row serialize on the row lock: the first transaction acquires it, deletes the row, commits and releases. Subsequent transactions, on resuming, re-evaluate the `WHERE` clause against the post-commit snapshot, find no matching row, and report `RowsAffected == 0`. There is no SELECT-then-UPDATE window here (unlike `service.Confirm`), so exactly one of the five goroutines sees `deleted == true` and increments the counter; the rest cleanly return `ErrTokenNotFound` → 404. Asserting `== 1` (rather than the confirm spec's `>= 1`) reflects this stronger guarantee. If the assertion ever flakes, that is a real signal that someone introduced a non-atomic delete (e.g. SELECT-then-DELETE).

**`TestUnsubscribe_LongRandomToken` — why 1024 chars.** Far above any plausible legitimate token length (`domain.GenerateToken()` returns ~43-char base64url), but well under typical URL-length limits. Catches any path-handling code that truncates, allocates per-byte, or does pathological work on the parameter. The expected outcome is the same as any other unknown-token case — `404`.

## Error Path Coverage (Handler)

Combined with the subscribe and confirm suites, these tests close out the remaining branches of `internal/subscription/http/handler.go::Unsubscribe`:

| Handler branch | Test |
| --- | --- |
| `ErrTokenNotFound` → `pages.Unavailable(404)` | #3, #4 (second call), #5 (four of five calls), #6 |
| Success → `pages.Unsubscribed()` (200) | #1, #2, #4 (first call), #5 (one of five calls) |
| `default` → `pages.Oops()` (500) | not exercised here — would require a DB outage; out of scope. |

## Test Isolation

`SetupTest` already runs `TRUNCATE ... RESTART IDENTITY CASCADE` on all four tables and rebuilds `App` with a fresh `*prometheus.Registry`. The new tests need no additional reset hooks.

`gofakeit.Email()` provides per-test uniqueness for `email` values; collisions are effectively impossible inside a single run.

## Makefile / CI / Docs

No changes. `make test-integration` and `make test-all` already cover `tests/integration/...` and pick up the new file via the build tag.

## Out of Scope

- `/api/subscriptions?email=` integration tests.
- Cron worker integration suites (`scanner`, `notifier`, `confirmer`).
- Internal-server-error path on `/api/unsubscribe` (would require simulating a DB outage; covered by handler unit tests on the default branch).
- HTML-content assertions (covered by `pages/pages_test.go`).
- Verification that `repositories` rows are **not** removed when their last subscription is unsubscribed (the FK direction is `subscriptions.repository_id → repositories ON DELETE CASCADE`, which cascades the other way; orphaned `repositories` cleanup is a separate concern and not part of GDPR delete-on-unsubscribe).
- Property/fuzzing tests on token format.

## Links

- ADR-0011 — testing strategy (the principle this spec implements at the HTTP layer for `unsubscribe`).
- `docs/superpowers/specs/2026-05-13-integration-tests-subscribe-design.md` — the sibling spec that established the infrastructure reused here.
- `docs/superpowers/specs/2026-05-14-integration-tests-confirm-design.md` — the sibling spec that introduced the parameterised `assertCounter` and the `get` helper this spec reuses.
- `internal/subscription/http/handler.go::Unsubscribe` — the handler under test.
- `internal/subscription/service/service.go::Unsubscribe` — the service logic exercised end-to-end.
- `internal/subscription/repository/subscriptions.go::DeleteByUnsubscribeToken` — the SQL whose correctness this suite verifies.
- `migrations/000002_release_notifications.up.sql`, `migrations/000003_confirmation_notifications.up.sql` — the `ON DELETE CASCADE` declarations that test #1 pins down.
- `internal/subscription/http/pages/pages.go::Unsubscribed` — the renderer whose 200 response is status-asserted (not content-asserted) here.
