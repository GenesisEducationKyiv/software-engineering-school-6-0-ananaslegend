# ADR-0011: Testing strategy — mock interfaces, run real for stateful systems

Status: accepted · 2026-05-16 · @ananaslegend · testing

## Context

A test suite has to balance two failure modes:

- **Too much mocking** — tests pass while production breaks, because
  the mock didn't model the real system (broken SQL, missing
  migration, wrong Redis serialisation).
- **Too little mocking** — tests are slow, flaky, and need
  infrastructure that is hard to spin up.

The common Go shortcut "just mock the database" falls into the first
trap. For this project we have a Postgres schema with non-trivial SQL
(`FOR UPDATE SKIP LOCKED`, partial indexes, constraints), a Redis
caching decorator, a GitHub GraphQL client, and outbox drainers that
span service + repository + transactor. Each layer has a different
"what's cheap to run for real" answer.

## Decision

Layered test taxonomy. Rule of thumb: **mock interfaces defined by
the consumer; run the real thing for stateful or SQL-heavy systems.**

- **Service layer** — unit tests with `uber-go/mock` mocks of the
  consumer-side interfaces from
  [ADR-0002](0002-consumer-side-interfaces.md) (`Repository`,
  `MailSender`, `RemoteRepositoryProvider`). Generated via
  `//go:generate mockgen` into `internal/<feature>/mocks/`.
- **Repository layer** — integration tests against a **real
  Postgres** with migrations applied. No SQL mocking. Catches
  migration, constraint, and query-plan bugs that a fake driver
  cannot.
- **Caching decorator** — `miniredis` (in-process, no network) plus
  a hand-written `StubClient` for the wrapped provider. Covers hit,
  miss, partial, and Redis-error fallback paths.
- **GitHub client** — `httptest.Server` returning shaped GraphQL
  JSON. Tests the full HTTP cycle, not a method-call shape.
- **Cross-component workflow** (scanner end-to-end) — real
  implementations where cheap; `StubClient` for the SaaS call;
  assertions via Prometheus counters
  (`testutil.GatherAndCompare`).

- **HTTP layer integration tests** — drive the real `chi` router
  against a real Postgres (testcontainers-go) with the GitHub REST
  API replaced by an in-process `httptest.Server` fixture. Tests live
  in `tests/integration/<feature>/` (e.g.
  `tests/integration/subscription/`); shared fixtures (Postgres
  helper, GitHub fixture, composition root) live in
  `tests/integration/internal/`. Every file under
  `tests/integration/` is gated by `//go:build integration`, so the
  default `go test ./...` workflow stays Docker-free. The runner is
  `testify/suite`: `SetupSuite` starts the container, `SetupTest`
  truncates tables and rebuilds the wired app with a fresh
  `*prometheus.Registry` so each test sees counters starting at zero.

## Consequences

### Positive
- Real-Postgres tests catch SQL, migration, and constraint bugs that
  mock-DB approaches silently let through.
- `miniredis` keeps Redis-aware tests in the unit-test budget —
  milliseconds per case, no Docker.

### Negative
- Onboarding cost: contributors must learn *which* double to use at
  *which* layer. The decision tree in this ADR replaces scattered
  tribal knowledge.
- No end-to-end "spin up the binary" tests — covered by smoke checks
  in higher environments, out of scope for this ADR.

### Constraints
- Generated mocks (`mocks/mock_interfaces.go`) must be regenerated on
  every consumer-interface change; CI catches stale mocks via a
  compilation failure. `make generate` is the single entry point.

## Links

- `internal/subscription/service/service_test.go` — service unit test
  with mockgen mocks.
- `internal/scanner/scanner_test.go`,
  `internal/notifier/notifier_test.go`,
  `internal/confirmer/confirmer_test.go` — workflow tests using
  metric assertions.
- `internal/github/client_test.go` — `httptest.Server` pattern.
- `internal/github/caching_client_test.go` — `miniredis` + `StubClient`
  pattern.
- `tests/integration/subscription/` — HTTP layer integration suite
  for `/api/subscribe` (testify/suite + testcontainers-go +
  `httptest.Server` GitHub REST fixture).
- `tests/integration/internal/` — shared fixtures (Postgres helper,
  GitHub REST fixture, composition root).
- `docs/testing.md` — how to run unit and integration tests.
- [ADR-0002](0002-consumer-side-interfaces.md) — explains why mocks
  live next to the consumer.
