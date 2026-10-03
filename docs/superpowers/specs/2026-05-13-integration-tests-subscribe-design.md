# Integration Tests for `/api/subscribe` — Design Spec

**Date:** 2026-05-13

## Summary

Introduce a feature-scoped integration test suite for the `/api/subscribe` endpoint. The suite drives the real HTTP router with a real `pgxpool`-backed repository against a Postgres container spun up by `testcontainers-go`, and replaces only the outbound GitHub REST API with an in-process `httptest.Server` fixture. Tests are organised under `tests/integration/` behind a `//go:build integration` build tag so that the default `go test ./...` workflow remains fast and Docker-free. The chosen test runner is `testify/suite`.

This spec covers `/api/subscribe` only. The same scaffolding (`tests/integration/internal/`) will be re-used by future suites for `/api/confirm`, `/api/unsubscribe`, `/api/subscriptions`, and the cron workers (`scanner`, `notifier`, `confirmer`).

## Motivation

`internal/subscription/service/service_test.go` exercises the subscribe path with mocks of `Repository` and `RemoteRepositoryProvider`. That covers business rules but is silent on:

- SQL correctness in `subscription/repository/` (constraints, FK to `repositories`, the atomic two-INSERT transaction that queues the outbox row).
- HTTP wiring in `internal/httpapi/router.go` (middleware ordering, error→status mapping, JSON marshalling).
- Whether `service` + `repository` + `httpapi` actually compose as `cmd/api/main.go` does.

ADR-0011 already prescribes the answer: real Postgres for the repository layer, `httptest.Server` for the GitHub HTTP cycle, mocks only at consumer-defined seams. This spec is the first concrete application of that strategy at the HTTP boundary.

## File Layout

```
tests/
└── integration/
    ├── README.md                       # how to run, prerequisites (Docker)
    ├── internal/                       # shared fixtures, package `internal`
    │   ├── pg.go                       # Postgres container + migrations
    │   ├── github.go                   # REST fixture (httptest.Server)
    │   └── app.go                      # wire-up: pool → repo → service → handler → router
    └── subscription/                   # package `subscription_test`
        ├── suite_test.go               # SubscriptionSuite struct + hooks + helpers
        └── subscribe_test.go           # `/api/subscribe` test methods
```

All files inside `tests/integration/` (including the `internal` package) carry `//go:build integration` on the first line. The `internal/` directory is a Go visibility marker: only packages under `tests/integration/...` can import it. The Go package name inside is `internal`, so calls look like `internal.NewPostgres(...)` and `internal.NewApp(...)`.

## Build Tag

Every file in `tests/integration/...` starts with:

```go
//go:build integration

package <name>
```

Behaviour:

- `go test ./...` (and the current `make test`) — compiles without the tag, so integration files are invisible. Runs fast and needs no Docker.
- `go test -tags=integration ./tests/integration/...` — compiles the tagged files. Requires a running Docker daemon (for `testcontainers-go`).

A new `Makefile` target wires the second form:

```
.PHONY: integration-test
integration-test:
	go test -tags=integration -count=1 ./tests/integration/...
```

CI changes are out of scope for this spec.

## Dependencies Added to `go.mod`

| Module | Purpose |
| --- | --- |
| `github.com/stretchr/testify/suite` | Suite-based test grouping (`testify/assert` and `testify/require` already vendored). |
| `github.com/testcontainers/testcontainers-go` | Docker-driven container lifecycle. |
| `github.com/testcontainers/testcontainers-go/modules/postgres` | High-level `postgres.Run(...)` helper with sensible defaults. |
| `github.com/brianvoe/gofakeit/v7` | Random email generation (`gofakeit.Email()`). |

`make tidy` is run once to download + vendor these. No other production code changes follow from new deps.

## Production Code Change: `internal/github/client.go`

`RepoExists` calls `https://api.github.com/repos/{owner}/{name}` (REST `HEAD`). The current `NewClient(token string)` hardcodes `https://api.github.com` and `https://api.github.com/graphql` in private fields, so a test cannot redirect requests to an `httptest.Server` without touching the package.

**Refactor** — introduce a `Config` struct, idiomatic with the rest of the codebase (`scanner.Config`, `notifier.Config`, `service.Config`):

```go
type Config struct {
    Token      string
    GraphQLURL string       // optional, default "https://api.github.com/graphql"
    RESTURL    string       // optional, default "https://api.github.com"
    HTTPClient *http.Client // optional, default &http.Client{}
}

func New(cfg Config) *Client {
    if cfg.GraphQLURL == "" {
        cfg.GraphQLURL = "https://api.github.com/graphql"
    }
    if cfg.RESTURL == "" {
        cfg.RESTURL = "https://api.github.com"
    }
    if cfg.HTTPClient == nil {
        cfg.HTTPClient = &http.Client{}
    }
    return &Client{
        token:      cfg.Token,
        httpClient: cfg.HTTPClient,
        graphqlURL: cfg.GraphQLURL,
        restURL:    cfg.RESTURL,
    }
}

// Preserved for callers that pass only a token.
func NewClient(token string) *Client { return New(Config{Token: token}) }
```

`internal/app/github.go` continues to call `githubclient.NewClient(cfg.GitHubToken)` and is not changed. Existing unit tests in `internal/github/client_test.go` keep working because they still poke private fields directly.

This is the only production code change required by the spec.

## `tests/integration/internal/pg.go` — Postgres Helper

```go
type Postgres struct {
    Pool      *pgxpool.Pool
    container testcontainers.Container
}

func NewPostgres(ctx context.Context, t testing.TB) *Postgres { /* ... */ }
func (p *Postgres) Truncate(ctx context.Context, t testing.TB) { /* ... */ }
```

`NewPostgres`:

1. `postgres.Run(ctx, "postgres:17-alpine", postgres.WithDatabase("test"), postgres.WithUsername("test"), postgres.WithPassword("test"), testcontainers.WithWaitStrategy(wait.ForLog(...).WithOccurrence(2).WithStartupTimeout(60*time.Second)))`. The image matches `docker-compose.yml` so local dev and tests share the same Postgres major.
2. `pgC.ConnectionString(ctx, "sslmode=disable")` → DSN.
3. `migrateUp(dsn)` — uses `golang-migrate/migrate/v4` with `iofs` source over the existing `migrations.FS` (`embed.FS`). The `migrate` package is already vendored (used by `internal/app/migrate.go`), so no new dependency is needed.
4. `pgxpool.New(ctx, dsn)` → pool.
5. `t.Cleanup` closes the pool and terminates the container.

`Truncate` executes:

```sql
TRUNCATE
    subscriptions,
    repositories,
    confirmation_notifications,
    release_notifications
RESTART IDENTITY CASCADE
```

`RESTART IDENTITY` keeps IDs deterministic across tests; `CASCADE` is required because `confirmation_notifications.subscription_id` has a foreign key.

## `tests/integration/internal/github.go` — GitHub REST Fixture

```go
type GitHubFixture struct {
    server   *httptest.Server
    mu       sync.Mutex
    repos    map[string]bool   // "owner/name" → exists
    forceErr bool              // if true, returns 500 on every call
    reqCount int               // number of handled requests since last Reset
}

func NewGitHubFixture(t testing.TB) *GitHubFixture { /* ... */ }
func (f *GitHubFixture) URL() string                  // base URL to pass as RESTURL
func (f *GitHubFixture) SetRepoExists(repo string, exists bool)
func (f *GitHubFixture) ForceError()
func (f *GitHubFixture) Reset()
func (f *GitHubFixture) RequestCount() int
```

The handler answers `HEAD /repos/{owner}/{name}`:

| State | Response |
| --- | --- |
| `forceErr == true` | `500 Internal Server Error` |
| `repos["owner/name"] == true` | `200 OK` |
| `repos["owner/name"] == false` (or absent) | `404 Not Found` |

`reqCount` is incremented under the mutex on every request. Each test that wants to assert "GitHub was called exactly once" can call `RequestCount()` after the request.

`Reset` clears `repos`, `forceErr`, and `reqCount`. It is invoked by `SetupTest`.

## `tests/integration/internal/app.go` — Composition Root

```go
type AppConfig struct {
    Pool            *pgxpool.Pool
    GitHubBaseURL   string
    GitHubToken     string
    AppBaseURL      string
    ConfirmTokenTTL time.Duration
}

type App struct {
    Pool     *pgxpool.Pool
    Registry *prometheus.Registry
    Server   *httptest.Server
    Client   *http.Client
}

func NewApp(t testing.TB, cfg AppConfig) *App { /* ... */ }
```

Wiring inside `NewApp` (mirrors `cmd/api/main.go` for the subscription path):

1. `registry := prometheus.NewRegistry()`.
2. `gh := githubclient.New(githubclient.Config{Token: cfg.GitHubToken, RESTURL: cfg.GitHubBaseURL})`. The GraphQL URL is left at default — `/api/subscribe` only uses REST.
3. `repo := subrepo.New(cfg.Pool)`.
4. `svc := service.New(service.Config{Repo: repo, GitHub: gh, AppBaseURL: cfg.AppBaseURL, ConfirmTokenTTL: cfg.ConfirmTokenTTL, Registry: registry})`.
5. `handler := subhttp.NewHandler(svc)`.
6. `router := httpapi.NewRouter(httpapi.RouterConfig{Log: zerolog.Nop(), SubHandler: handler, Registry: registry})`.
7. `server := httptest.NewServer(router); t.Cleanup(server.Close)`.

Cron workers (`scanner`, `notifier`, `confirmer`) are intentionally not wired here. `confirmation_notifications` rows therefore stay in the table for direct verification, with no race against a drainer.

## `tests/integration/subscription/suite_test.go` — Suite

```go
//go:build integration

package subscription_test

type SubscriptionSuite struct {
    suite.Suite

    ctx      context.Context
    cancel   context.CancelFunc

    pg       *internal.Postgres
    githubFx *internal.GitHubFixture
    app      *internal.App
}

func TestSubscriptionSuite(t *testing.T) {
    suite.Run(t, new(SubscriptionSuite))
}
```

| Hook | Body |
| --- | --- |
| `SetupSuite` | `s.ctx, s.cancel = context.WithCancel(context.Background())`; `s.pg = internal.NewPostgres(s.ctx, s.T())`; `s.githubFx = internal.NewGitHubFixture(s.T())`; `gofakeit.Seed(0)` (non-deterministic seed each run). |
| `SetupTest` | `s.pg.Truncate(s.ctx, s.T())`; `s.githubFx.Reset()`; rebuild `s.app = internal.NewApp(s.T(), internal.AppConfig{Pool: s.pg.Pool, GitHubBaseURL: s.githubFx.URL(), GitHubToken: "test-token", AppBaseURL: "http://test.local", ConfirmTokenTTL: 24*time.Hour})`. |
| `TearDownTest` | (nop — `t.Cleanup` registered inside `NewApp` closes the previous `httptest.Server`). |
| `TearDownSuite` | `s.cancel()`. Postgres container and GitHub fixture are torn down via the `t.Cleanup` hooks registered in `NewPostgres`/`NewGitHubFixture`. |

Rebuilding `App` per test gives every test a fresh `*prometheus.Registry`, so counter assertions can use `testutil.GatherAndCompare` against absolute values starting at `0`. The cost is microseconds (no container restart, no migrations).

### Suite Helpers

| Helper | Purpose |
| --- | --- |
| `(s) post(path string, body any) *http.Response` | Marshal `body` to JSON, POST through `s.app.Client`, return response. Caller closes `Body`. |
| `(s) decodeJSON(resp *http.Response, v any)` | Asserts `Content-Type: application/json`, decodes into `v`. |
| `(s) randomEmail() string` | `gofakeit.Email()`. |
| `(s) selectSubscriptions(email string) []domain.Subscription` | Direct SELECT, ordered by `id`. |
| `(s) countConfirmationNotifications(subID int64) int` | `SELECT count(*) FROM confirmation_notifications WHERE subscription_id = $1`. |
| `(s) selectRepository(owner, name string) (id int64, found bool)` | Direct SELECT. |
| `(s) assertCounter(name string, want float64)` | Wraps `testutil.GatherAndCompare(s.app.Registry, ..., name)`. |

## `tests/integration/subscription/subscribe_test.go` — Test Cases

| # | Method | Pre-conditions | Request | Expected |
| --- | --- | --- | --- | --- |
| 1 | `TestSubscribe_HappyPath` | `githubFx.SetRepoExists("golang/go", true)` | `POST /api/subscribe {email: gofakeit.Email(), repository: "golang/go"}` | `202`; body `{"status":"pending_confirmation"}`; one row in `repositories` (`owner=golang, name=go`); one row in `subscriptions` (matching email, `confirmed_at IS NULL`, both tokens non-empty, `confirm_token_expires_at > now`); one row in `confirmation_notifications` for that subscription with `sent_at IS NULL`; `subscriptions_created_total == 1`; `githubFx.RequestCount() == 1`. |
| 2 | `TestSubscribe_RepoURL_Normalized` | `githubFx.SetRepoExists("golang/go", true)` | `POST` with `repository: "https://github.com/golang/go.git"` | `202`; `repositories` row has `owner=golang, name=go` (no `.git`, no host). |
| 3 | `TestSubscribe_InvalidJSON` | — | `POST` with raw body `"not-json"` | `400`; body `{"error":"invalid JSON body"}`; zero rows in `subscriptions`, `confirmation_notifications`; `subscriptions_created_total == 0`; `githubFx.RequestCount() == 0`. |
| 4 | `TestSubscribe_InvalidEmail` | — | `POST {email: "not-an-email", repository: "golang/go"}` | `400`; body `{"error":"invalid email"}`; zero rows in DB; `githubFx.RequestCount() == 0` (validation is at the HTTP layer, before service). |
| 5 | `TestSubscribe_InvalidRepoFormat` | — | `POST {email: gofakeit.Email(), repository: "justonepart"}` | `400`; body contains the text of `domain.ErrInvalidRepoFormat`; zero rows; `githubFx.RequestCount() == 0`. |
| 6 | `TestSubscribe_RepoNotFoundOnGitHub` | `githubFx.SetRepoExists("foo/bar", false)` | `POST {email: gofakeit.Email(), repository: "foo/bar"}` | `404`; body contains the text of `domain.ErrRepoNotFound`; zero rows in `subscriptions`/`confirmation_notifications`; `githubFx.RequestCount() == 1`. |
| 7 | `TestSubscribe_DuplicateSubscription` | `githubFx.SetRepoExists("golang/go", true)`; perform one successful subscribe with `email := s.randomEmail()`. | Repeat `POST` with the same `email` and `repository`. | Second response: `409`; body contains the text of `domain.ErrAlreadyExists`; exactly **one** row in `subscriptions` for that `(email, repo_id)`; exactly **one** row in `confirmation_notifications`; `subscriptions_created_total == 1`. |
| 8 | `TestSubscribe_GitHubError` | `githubFx.ForceError()` | `POST {email: gofakeit.Email(), repository: "golang/go"}` | `500`; body `{"error":"internal server error"}` (handler masks the internal cause); zero rows in `subscriptions`/`confirmation_notifications`. |

For test #4 the precise wording of the error message is fixed by the handler (`"invalid email"`); for tests #5/#6/#7 the wording is fixed by the sentinel error in `internal/subscription/domain/errors.go` — tests assert the actual `.Error()` string so they double as documentation of the public error contract.

## Error Path Coverage

The status mapping in `errorStatus` (`internal/subscription/http/handler.go`) is exercised by tests #3-#8:

| `errorStatus` branch | Test |
| --- | --- |
| `ErrInvalidRepoFormat` → 400 | #5 |
| `*BadRequestError` → 400 | #3, #4 |
| `ErrRepoNotFound` → 404 | #6 |
| `ErrAlreadyExists` → 409 | #7 |
| `default` → 500 | #8 |
| `ErrTokenExpired` → 410 | not relevant for `/api/subscribe`; covered later by the `/api/confirm` suite. |
| `ErrTokenNotFound` → 404 | same as above. |

## Makefile

```
.PHONY: integration-test
integration-test:
	go test -tags=integration -count=1 ./tests/integration/...
```

`-count=1` disables the test cache, which is the standard idiom for tests that depend on external state (Docker, network).

## README in `tests/integration/`

Short markdown (~30 lines) with:

- Prerequisite: Docker daemon running.
- How to run: `make integration-test` (or `go test -tags=integration ./tests/integration/...`).
- How to add a new endpoint suite: copy `subscription/suite_test.go`, point at the right entry in `internal/app.go` if more deps are needed.
- A note that the cron worker suites will live in `tests/integration/{scanner,notifier,confirmer}/` and share `internal/`.

## Out of Scope

- CI changes (a follow-up adds a `integration-test` job).
- `/api/confirm`, `/api/unsubscribe`, `/api/subscriptions` test files.
- Cron worker integration suites (`scanner`, `notifier`, `confirmer`).
- Redis caching path (`CachingReleaseProvider`) — not on the `/api/subscribe` code path.
- Race / concurrency tests for duplicate inserts at the SQL level — already enforced by the unique constraint; one duplicate test is sufficient end-to-end.
- Faker-driven property tests — not justified by the test count.

## Links

- ADR-0001 — screaming architecture (`internal/<feature>/`).
- ADR-0002 — consumer-side interfaces (mocks live with consumers).
- ADR-0003 — async email outbox (`confirmation_notifications` row is the contract verified by test #1).
- ADR-0011 — testing strategy (real Postgres for repos, `httptest.Server` for HTTP cycles, metric assertions via `testutil`).
- `internal/github/client_test.go` — reference pattern for `httptest.Server` testing of an HTTP client.
- `internal/subscription/service/service_test.go` — reference pattern for `testutil.GatherAndCompare` metric assertions.
- `migrations/migrations.go` — `embed.FS` re-used by `pg.go` for migration application.