# Integration Tests for `GET /api/subscriptions` — Design Spec

**Date:** 2026-05-14

## Summary

Extend the existing `tests/integration/subscription/` suite with coverage for `GET /api/subscriptions?email=<...>` (the `ListByEmail` handler). The suite is the same `SubscriptionSuite` already used for `POST /api/subscribe`; only new test methods and a few helper methods are added. Postgres is the real `testcontainers-go` instance; the GitHub fixture is unused (the list endpoint never calls GitHub).

## Motivation

`internal/subscription/service/service_test.go` exercises `ListByEmail` with a mocked `Repository`. That covers business plumbing but not:

- SQL correctness in `repository.ListByEmail`: the `JOIN` to `repositories`, the `WHERE s.email = $1 AND s.confirmed_at IS NOT NULL` filter, and the `ORDER BY s.created_at DESC` ordering.
- HTTP wiring: query-parameter parsing, email validation at the handler boundary, JSON DTO shape (`SubscriptionsResponse.Subscriptions`).
- That pending (un-confirmed) subscriptions are correctly excluded from the public listing.

ADR-0011 prescribes a real Postgres for repository correctness — this spec applies that to the list endpoint.

## File Layout

```
tests/integration/subscription/
├── suite_test.go        # existing — extended with new helpers
├── subscribe_test.go    # existing — unchanged
└── list_test.go         # NEW — GET /api/subscriptions tests
```

All files keep the existing `//go:build integration` tag. No changes to `tests/integration/internal/`.

## Suite Helpers (additions to `suite_test.go`)

| Helper | Purpose |
| --- | --- |
| `(s) get(path string, query url.Values) *http.Response` | Issue GET through `s.app.Client` with optional query string. Caller closes `Body`. |
| `(s) upsertRepository(owner, name string) int64` | `INSERT … ON CONFLICT (owner, name) DO UPDATE … RETURNING id`. Idempotent. |
| `(s) seedConfirmedSubscription(email, owner, name string, createdAt, confirmedAt time.Time) int64` | Direct `INSERT` into `subscriptions` with `confirm_token = NULL` (required by `subscriptions_confirm_state_check`), explicit `created_at` and `confirmed_at`. Reuses `upsertRepository`. |
| `(s) seedPendingSubscription(email, owner, name string, createdAt time.Time) int64` | Direct `INSERT` with `confirmed_at = NULL` and a non-null `confirm_token`. Reuses `upsertRepository`. |

Seeding bypasses `POST /api/subscribe` because the list endpoint test must control `created_at` (for ordering) and `confirmed_at` (for the pending-vs-confirmed filter) — the public API can't do that.

Unsubscribe and confirm tokens for seeded rows are filled with `gofakeit.UUID()` to satisfy `UNIQUE (unsubscribe_token)` and `idx_subscriptions_confirm_token`.

## `list_test.go` — Test Cases

| # | Method | Pre-conditions | Request | Expected |
| --- | --- | --- | --- | --- |
| 1 | `TestListByEmail_HappyPath_SingleConfirmedSubscription` | One confirmed subscription for `email` on `golang/go`, known `createdAt` & `confirmedAt`. | `GET /api/subscriptions?email=<email>` | `200`; body `{"subscriptions":[{repository:"golang/go", confirmed_at:~confirmedAt, created_at:~createdAt}]}`. |
| 2 | `TestListByEmail_OrdersByCreatedAtDesc` | Three confirmed subscriptions for the same `email` with `created_at` 3h/2h/1h ago. | `GET …` | `200`; three items, the most recent first; assert repository names in expected order. |
| 3 | `TestListByEmail_EmptyForUnknownEmail` | DB empty (truncate-only). | `GET …?email=<random>` | `200`; body `{"subscriptions":[]}` (handler builds `make([]…, 0)` → JSON `[]`, never `null`). |
| 4 | `TestListByEmail_ExcludesPendingSubscriptions` | One confirmed (`golang/go`) and one pending (`rust-lang/rust`) for same `email`. | `GET …?email=<email>` | `200`; one item, `repository:"golang/go"`. |
| 5 | `TestListByEmail_FiltersByEmail` | Confirmed sub for `emailA` on `golang/go`; confirmed sub for `emailB` on `rust-lang/rust`. | `GET …?email=<emailA>` | `200`; one item, `repository:"golang/go"`. |
| 6 | `TestListByEmail_InvalidEmail` | — | `GET …?email=not-an-email` | `400`; body `{"error":"invalid email"}`. |
| 7 | `TestListByEmail_MissingEmail` | — | `GET /api/subscriptions` (no query) | `400`; body `{"error":"invalid email"}` (handler validates empty as invalid). |

Time comparisons use `assert.WithinDuration(..., time.Second)` to tolerate `created_at`/`confirmed_at` round-tripping through Postgres `TIMESTAMPTZ` and JSON RFC3339.

## Error Path Coverage

| `errorStatus` branch | Test |
| --- | --- |
| `*BadRequestError` → 400 | #6, #7 |

The handler does not surface 5xx for this endpoint under any reproducible setup (the repository only returns errors on Postgres faults, which are not covered here). 500-path coverage stays in `service_test.go`.

## Out of Scope

- 500-path tests (would require killing the pool mid-request).
- Pagination / limits — not part of the current handler contract.
- `repository` ordering by other fields — only `created_at DESC` is required.

## Links

- ADR-0011 — testing strategy (real Postgres, HTTP through chi router).
- `tests/integration/subscription/subscribe_test.go` — sibling pattern.
- `docs/superpowers/specs/2026-05-13-integration-tests-subscribe-design.md` — original spec for the subscribe endpoint and the shared `tests/integration/internal/` fixtures.
