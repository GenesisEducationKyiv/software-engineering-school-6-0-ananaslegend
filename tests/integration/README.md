# Integration Tests

Feature-scoped integration tests that drive the real HTTP router against a Postgres container.

## Prerequisites

- Docker daemon running (used by `testcontainers-go`).

## Running

```sh
make test-integration
# equivalent to:
go test -tags=integration -count=1 ./tests/integration/...
```

The default `make test` skips this tree — every file under `tests/integration/` is gated by `//go:build integration`.

## Layout

- `internal/` — shared fixtures (package `internal`). Imports are restricted by Go's `internal/` rule to packages under `tests/integration/`.
  - `pg.go` — Postgres container helper (`NewPostgres`, `Truncate`).
  - `github.go` — REST fixture (`GitHubFixture`) for `HEAD /repos/{owner}/{name}`.
  - `app.go` — composition root (`NewApp`).
- `subscription/` — `/api/subscribe`, and future suites for `/api/confirm`, `/api/unsubscribe`, `/api/subscriptions`.
- Future: `scanner/`, `notifier/`, `confirmer/` will share `internal/`.

## Adding a new suite

1. Create `tests/integration/<feature>/suite_test.go`. Start with `//go:build integration`, package `<feature>_test`.
2. Embed `suite.Suite`. In `SetupSuite`, call `internal.NewPostgres` and any needed fixtures.
3. In `SetupTest`, truncate tables, reset fixtures, and rebuild `internal.NewApp(...)` so each test gets a fresh `*prometheus.Registry`.
