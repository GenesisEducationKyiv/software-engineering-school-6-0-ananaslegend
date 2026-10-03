# Integration Tests for /api/subscribe — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a feature-scoped integration test suite for `POST /api/subscribe` that drives the real chi router against a Postgres container (`testcontainers-go`) with a `httptest.Server` GitHub REST fixture, hidden behind a `//go:build integration` tag.

**Architecture:** New `tests/integration/` tree. `tests/integration/internal/` (package `internal`, build-tag gated) hosts the Postgres helper, the GitHub REST fixture, and the composition root used by every suite. `tests/integration/subscription/` (package `subscription_test`) hosts the `SubscriptionSuite` (`testify/suite`) and the eight `/api/subscribe` test methods. The Postgres container starts once per suite in `SetupSuite`; `SetupTest` truncates tables, resets the fixture, and rebuilds the wired app with a fresh `*prometheus.Registry` so counter assertions start at zero.

**Tech Stack:** Go 1.x, `testify/suite` + `testify/{assert,require}`, `testcontainers-go` + `modules/postgres`, `golang-migrate/migrate/v4` + `iofs` (already vendored), `brianvoe/gofakeit/v7`, existing `prometheus/client_golang/testutil`, existing `internal/github`, `internal/subscription/*`, `internal/httpapi`.

**Reference spec:** [`docs/superpowers/specs/2026-05-13-integration-tests-subscribe-design.md`](../specs/2026-05-13-integration-tests-subscribe-design.md)

---

## File Map

| Path | Action | Purpose |
| --- | --- | --- |
| `internal/github/client.go` | Modify | Add `Config` struct + `New(cfg Config) *Client`; keep `NewClient(token)` as backward-compat wrapper. |
| `internal/github/client_test.go` | Modify | Add two unit tests for the new constructor (defaults + custom URLs). |
| `tests/integration/README.md` | Create | How to run, prerequisites, how to add new suites. |
| `tests/integration/internal/pg.go` | Create | `Postgres` helper (container + migrations + truncate). |
| `tests/integration/internal/github.go` | Create | `GitHubFixture` (REST `HEAD /repos/{owner}/{name}` fake). |
| `tests/integration/internal/app.go` | Create | `NewApp` composition root (registry + github client + repo + service + handler + router + httptest.Server). |
| `tests/integration/subscription/suite_test.go` | Create | `SubscriptionSuite` struct, hooks, helpers, `TestSubscriptionSuite` entry. |
| `tests/integration/subscription/subscribe_test.go` | Create | Eight test methods for `/api/subscribe`. |
| `Makefile` | Modify | Add `integration-test` target. |
| `go.mod`, `go.sum`, `vendor/` | Modify (via `make tidy`) | Add `testify/suite`, `testcontainers-go`, `testcontainers-go/modules/postgres`, `gofakeit/v7`. |

---

## Task 1: Add `Config` + `New` to `internal/github/client.go`

**Files:**
- Modify: `internal/github/client.go`
- Modify: `internal/github/client_test.go`

- [ ] **Step 1.1: Add the failing test for defaults**

Append at the bottom of `internal/github/client_test.go`:

```go
func TestNew_AppliesDefaults(t *testing.T) {
	c := New(Config{Token: "tok"})

	assert.Equal(t, "tok", c.token)
	assert.Equal(t, "https://api.github.com/graphql", c.graphqlURL)
	assert.Equal(t, "https://api.github.com", c.restURL)
	assert.NotNil(t, c.httpClient)
}

func TestNew_CustomURLsUsed(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := New(Config{Token: "tok", RESTURL: srv.URL, HTTPClient: srv.Client()})
	_, err := c.RepoExists(context.Background(), domain.RepoExistsParams{Owner: "foo", Name: "bar"})
	require.NoError(t, err)
	assert.Equal(t, "/repos/foo/bar", hitPath)
}

func TestNewClient_BackwardCompat(t *testing.T) {
	c := NewClient("legacy-tok")
	assert.Equal(t, "legacy-tok", c.token)
	assert.Equal(t, "https://api.github.com/graphql", c.graphqlURL)
	assert.Equal(t, "https://api.github.com", c.restURL)
}
```

- [ ] **Step 1.2: Run the tests to verify they fail**

```bash
go test ./internal/github/ -run 'TestNew_AppliesDefaults|TestNew_CustomURLsUsed|TestNewClient_BackwardCompat' -v
```

Expected: compile error `undefined: New` and `undefined: Config`.

- [ ] **Step 1.3: Implement `Config` and `New` in `internal/github/client.go`**

Replace the existing `NewClient` definition (lines 35-42 of `internal/github/client.go`) with:

```go
// Config holds optional dependencies for Client. Zero-value fields fall back to
// the public GitHub API defaults.
type Config struct {
	Token      string
	GraphQLURL string       // optional, default "https://api.github.com/graphql"
	RESTURL    string       // optional, default "https://api.github.com"
	HTTPClient *http.Client // optional, default &http.Client{}
}

// New returns a Client configured by cfg. Empty Config fields use GitHub-public
// defaults.
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

// NewClient returns a Client targeting the real GitHub API.
// token is optional; without it the rate limit is 60 req/h.
// Kept for backward compatibility — internally delegates to New.
func NewClient(token string) *Client {
	return New(Config{Token: token})
}
```

- [ ] **Step 1.4: Run the tests to verify they pass**

```bash
go test ./internal/github/ -v
```

Expected: all tests in the package pass, including the new three plus the existing `TestRepoExists_*` and `TestGetLatestReleases_*`.

- [ ] **Step 1.5: Run `go vet` and the linter**

```bash
make vet
make lint
```

Expected: clean.

- [ ] **Step 1.6: Commit**

```bash
git add internal/github/client.go internal/github/client_test.go
git commit -m "feat(github): add Config struct and New constructor for client URL overrides

NewClient(token) preserved as backward-compatible wrapper. Enables
integration tests to point the client at an httptest.Server fixture
without monkey-patching."
```

---

## Task 2: Create `tests/integration/README.md`

**Files:**
- Create: `tests/integration/README.md`

- [ ] **Step 2.1: Write the README**

```markdown
# Integration Tests

Feature-scoped end-to-end tests that drive the real HTTP router against a Postgres container.

## Prerequisites

- Docker daemon running (used by `testcontainers-go`).

## Running

```sh
make integration-test
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
```

- [ ] **Step 2.2: Commit**

```bash
git add tests/integration/README.md
git commit -m "docs(tests): add tests/integration/ README"
```

---

## Task 3: Add `tests/integration/internal/pg.go` — Postgres helper

**Files:**
- Create: `tests/integration/internal/pg.go`
- Modify: `go.mod`, `go.sum`, `vendor/` (via `make tidy`)

- [ ] **Step 3.1: Create `tests/integration/internal/pg.go`**

```go
//go:build integration

package internal

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ananaslegend/reposeetory/migrations"
)

// Postgres wraps a testcontainer-backed Postgres instance plus a connection pool.
// Lifetime is bound to the test that called NewPostgres via t.Cleanup.
type Postgres struct {
	Pool      *pgxpool.Pool
	container testcontainers.Container
}

// NewPostgres starts a Postgres 17 container, applies all migrations, and
// returns a ready-to-use pool. The container is terminated automatically when
// the calling test finishes.
func NewPostgres(ctx context.Context, t testing.TB) *Postgres {
	t.Helper()

	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err, "start postgres container")

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "container connection string")

	if err := migrateUp(dsn); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "new pgx pool")

	pg := &Postgres{Pool: pool, container: pgC}
	t.Cleanup(func() {
		pool.Close()
		_ = pgC.Terminate(context.Background())
	})
	return pg
}

// Truncate empties every domain table and restarts identity sequences. Use
// from SetupTest of a suite to get a clean slate per test.
func (p *Postgres) Truncate(ctx context.Context, t testing.TB) {
	t.Helper()
	_, err := p.Pool.Exec(ctx, `
		TRUNCATE
			subscriptions,
			repositories,
			confirmation_notifications,
			release_notifications
		RESTART IDENTITY CASCADE
	`)
	require.NoError(t, err, "truncate tables")
}

func migrateUp(dsn string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}
	pgxDSN := "pgx5://" + trimPrefix(dsn, "postgres://")

	m, err := migrate.NewWithSourceInstance("iofs", src, pgxDSN)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func trimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
```

Note on the DSN: `golang-migrate/migrate/v4`'s pgx v5 driver is registered under scheme `pgx5://`. The testcontainers-go Postgres module produces a `postgres://...` DSN; we rewrite the scheme so `migrate.NewWithSourceInstance` picks up the right driver. Verify the actual driver registration in `internal/app/migrate.go` (it may use `pgx5` or `pgx`) and reuse the same scheme name here.

- [ ] **Step 3.2: Verify migrate driver name used by the project**

```bash
grep -rn "golang-migrate/migrate/v4/database" internal/app/ go.mod vendor/github.com/golang-migrate/migrate/v4/database/ 2>/dev/null | head -20
```

Expected: a path like `vendor/github.com/golang-migrate/migrate/v4/database/pgx/v5/`. If the driver registers itself under scheme `pgx5`, the code above is correct. If it registers under `pgx`, change `"pgx5://"` to `"pgx://"` and the blank import to match. If the project uses a different driver entirely (e.g., `pgx` v4), use whatever scheme `internal/app/migrate.go` already wires.

- [ ] **Step 3.3: Run `make tidy` to fetch testcontainers-go**

```bash
make tidy
```

Expected: `go mod tidy` adds `github.com/testcontainers/testcontainers-go` and `github.com/testcontainers/testcontainers-go/modules/postgres` to `go.mod`; `go mod vendor` writes them under `vendor/`. The blank `_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"` import is already vendored if `internal/app/migrate.go` uses it; if not, `make tidy` adds it.

- [ ] **Step 3.4: Verify the file compiles with the build tag**

```bash
go build -tags=integration ./tests/integration/internal/...
```

Expected: clean build, no output.

- [ ] **Step 3.5: Commit**

```bash
git add tests/integration/internal/pg.go go.mod go.sum vendor/
git commit -m "feat(tests): add Postgres testcontainer helper for integration suites"
```

---

## Task 4: Add `tests/integration/internal/github.go` — REST fixture

**Files:**
- Create: `tests/integration/internal/github.go`

- [ ] **Step 4.1: Create the file**

```go
//go:build integration

package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// GitHubFixture is an in-process double for the GitHub REST API. It only
// implements HEAD /repos/{owner}/{name} — enough for service.Subscribe.
type GitHubFixture struct {
	server *httptest.Server

	mu       sync.Mutex
	repos    map[string]bool
	forceErr bool
	reqCount int
}

// NewGitHubFixture starts the fixture server. It is closed automatically when
// the calling test finishes.
func NewGitHubFixture(t testing.TB) *GitHubFixture {
	f := &GitHubFixture{repos: make(map[string]bool)}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// URL returns the base URL to pass as RESTURL into githubclient.Config.
func (f *GitHubFixture) URL() string { return f.server.URL }

// SetRepoExists programs the fixture's response for a single repository.
// repo is formatted as "owner/name".
func (f *GitHubFixture) SetRepoExists(repo string, exists bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[repo] = exists
}

// ForceError makes the fixture return 500 on every subsequent call.
func (f *GitHubFixture) ForceError() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forceErr = true
}

// Reset clears the programmed map, the forced-error flag, and the request
// counter. Call from SetupTest of a suite.
func (f *GitHubFixture) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos = make(map[string]bool)
	f.forceErr = false
	f.reqCount = 0
}

// RequestCount returns the number of requests handled since the last Reset.
func (f *GitHubFixture) RequestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqCount
}

func (f *GitHubFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqCount++
	forceErr := f.forceErr
	repos := f.repos
	f.mu.Unlock()

	if forceErr {
		http.Error(w, "forced error", http.StatusInternalServerError)
		return
	}

	// Expected path: /repos/{owner}/{name}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "repos" {
		http.Error(w, fmt.Sprintf("unexpected path: %s", r.URL.Path), http.StatusBadRequest)
		return
	}
	key := parts[1] + "/" + parts[2]
	if exists, ok := repos[key]; ok && exists {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}
```

- [ ] **Step 4.2: Verify it compiles**

```bash
go build -tags=integration ./tests/integration/internal/...
```

Expected: clean.

- [ ] **Step 4.3: Commit**

```bash
git add tests/integration/internal/github.go
git commit -m "feat(tests): add GitHub REST fixture for integration suites"
```

---

## Task 5: Add `tests/integration/internal/app.go` — composition root

**Files:**
- Create: `tests/integration/internal/app.go`

- [ ] **Step 5.1: Create the file**

```go
//go:build integration

package internal

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	"github.com/ananaslegend/reposeetory/internal/subscription/http"
	subrepo "github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
)

// AppConfig holds the knobs callers normally control. Sensible defaults are
// applied when fields are zero.
type AppConfig struct {
	Pool            *pgxpool.Pool
	GitHubBaseURL   string
	GitHubToken     string
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
}

// App is the wired test instance: an httptest.Server in front of the real
// chi router, sharing a single Prometheus Registry across handler, service,
// and github client.
type App struct {
	Pool     *pgxpool.Pool
	Registry *prometheus.Registry
	Server   *httptest.Server
	Client   *http.Client
}

// NewApp builds the subscription HTTP stack and wraps it in an httptest.Server.
// The server is closed automatically when the calling test finishes.
//
// Cron workers (scanner, notifier, confirmer) are intentionally NOT started —
// callers verify outbox-table state directly.
func NewApp(t testing.TB, cfg AppConfig) *App {
	t.Helper()

	if cfg.AppBaseURL == "" {
		cfg.AppBaseURL = "http://test.local"
	}
	if cfg.ConfirmTokenTTL == 0 {
		cfg.ConfirmTokenTTL = 24 * time.Hour
	}
	if cfg.GitHubToken == "" {
		cfg.GitHubToken = "test-token"
	}

	registry := prometheus.NewRegistry()

	gh := githubclient.New(githubclient.Config{
		Token:   cfg.GitHubToken,
		RESTURL: cfg.GitHubBaseURL,
	})

	repo := subrepo.New(cfg.Pool)

	svc := service.New(service.Config{
		Repo:            repo,
		GitHub:          gh,
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
		Registry:        registry,
	})

	handler := subhttp.NewHandler(svc)

	router := httpapi.NewRouter(httpapi.RouterConfig{
		Log:        zerolog.Nop(),
		SubHandler: handler,
		Registry:   registry,
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &App{
		Pool:     cfg.Pool,
		Registry: registry,
		Server:   server,
		Client:   server.Client(),
	}
}
```

Note: the import alias `subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"` is mandated by CLAUDE.md (the package is named `http`, shadowing `net/http`). The plan above declares the import but writes `subhttp.NewHandler(...)` — fix the import block to:

```go
subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
```

and remove the unaliased `"github.com/ananaslegend/reposeetory/internal/subscription/http"`. Final import block:

```go
import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/httpapi"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
	subrepo "github.com/ananaslegend/reposeetory/internal/subscription/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
)
```

- [ ] **Step 5.2: Verify it compiles**

```bash
go build -tags=integration ./tests/integration/internal/...
```

Expected: clean.

- [ ] **Step 5.3: Commit**

```bash
git add tests/integration/internal/app.go
git commit -m "feat(tests): add integration composition root"
```

---

## Task 6: Add the empty suite + Makefile target + new deps

**Files:**
- Create: `tests/integration/subscription/suite_test.go`
- Modify: `Makefile`
- Modify: `go.mod`, `go.sum`, `vendor/` (via `make tidy`)

- [ ] **Step 6.1: Create the suite skeleton**

```go
//go:build integration

package subscription_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/tests/integration/internal"
)

type SubscriptionSuite struct {
	suite.Suite

	ctx    context.Context
	cancel context.CancelFunc

	pg       *internal.Postgres
	githubFx *internal.GitHubFixture
	app      *internal.App
}

func TestSubscriptionSuite(t *testing.T) {
	suite.Run(t, new(SubscriptionSuite))
}

func (s *SubscriptionSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.pg = internal.NewPostgres(s.ctx, s.T())
	s.githubFx = internal.NewGitHubFixture(s.T())
	gofakeit.Seed(0) // 0 = non-deterministic seed each run
}

func (s *SubscriptionSuite) SetupTest() {
	s.pg.Truncate(s.ctx, s.T())
	s.githubFx.Reset()
	s.app = internal.NewApp(s.T(), internal.AppConfig{
		Pool:            s.pg.Pool,
		GitHubBaseURL:   s.githubFx.URL(),
		GitHubToken:     "test-token",
		AppBaseURL:      "http://test.local",
		ConfirmTokenTTL: 24 * time.Hour,
	})
}

func (s *SubscriptionSuite) TearDownSuite() {
	s.cancel()
}

// --- HTTP helpers ---

func (s *SubscriptionSuite) post(path string, body any) *http.Response {
	s.T().Helper()
	var bodyReader io.Reader
	switch b := body.(type) {
	case string:
		bodyReader = strings.NewReader(b)
	case nil:
		bodyReader = nil
	default:
		buf, err := json.Marshal(b)
		require.NoError(s.T(), err)
		bodyReader = strings.NewReader(string(buf))
	}

	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.app.Server.URL+path, bodyReader)
	require.NoError(s.T(), err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.app.Client.Do(req)
	require.NoError(s.T(), err)
	return resp
}

func (s *SubscriptionSuite) decodeJSON(resp *http.Response, v any) {
	s.T().Helper()
	defer resp.Body.Close()
	require.Contains(s.T(), resp.Header.Get("Content-Type"), "application/json")
	require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(v))
}

func (s *SubscriptionSuite) randomEmail() string {
	return gofakeit.Email()
}

// --- DB helpers ---

type dbSubscription struct {
	ID                    int64
	Email                 string
	RepositoryID          int64
	ConfirmedAt           *time.Time
	ConfirmToken          *string
	ConfirmTokenExpiresAt *time.Time
	UnsubscribeToken      string
}

func (s *SubscriptionSuite) selectSubscriptionsByEmail(email string) []dbSubscription {
	s.T().Helper()
	rows, err := s.pg.Pool.Query(s.ctx, `
		SELECT id, email, repository_id, confirmed_at, confirm_token, confirm_token_expires_at, unsubscribe_token
		FROM subscriptions
		WHERE email = $1
		ORDER BY id
	`, email)
	require.NoError(s.T(), err)
	defer rows.Close()

	var out []dbSubscription
	for rows.Next() {
		var sub dbSubscription
		require.NoError(s.T(), rows.Scan(
			&sub.ID, &sub.Email, &sub.RepositoryID,
			&sub.ConfirmedAt, &sub.ConfirmToken, &sub.ConfirmTokenExpiresAt,
			&sub.UnsubscribeToken,
		))
		out = append(out, sub)
	}
	require.NoError(s.T(), rows.Err())
	return out
}

func (s *SubscriptionSuite) countConfirmationNotifications(subID int64) int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM confirmation_notifications WHERE subscription_id = $1`,
		subID,
	).Scan(&n))
	return n
}

func (s *SubscriptionSuite) countAllConfirmationNotifications() int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM confirmation_notifications`,
	).Scan(&n))
	return n
}

func (s *SubscriptionSuite) selectRepository(owner, name string) (int64, bool) {
	s.T().Helper()
	var id int64
	err := s.pg.Pool.QueryRow(s.ctx,
		`SELECT id FROM repositories WHERE owner = $1 AND name = $2`, owner, name,
	).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}

// --- Metrics ---

// assertCreatedCounter sums every observed subscriptions_created_total sample
// in the suite's registry. Works whether or not the counter has been touched
// (testutil.GatherAndCompare requires the metric to be present, which is not
// guaranteed in error-path tests where service.Subscribe never runs).
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

Notes:
- `assertCreatedCounter` uses `Registry.Gather()` + manual summation instead of `testutil.GatherAndCompare`, because the latter fails with a confusing "metric not found" when the counter was never observed (no `Inc()` call happened). All eight tests below — successful and error-path — share this helper.

- [ ] **Step 6.2: Add the Makefile target**

Append to `Makefile`:

```makefile
.PHONY: integration-test
integration-test:
	go test -tags=integration -count=1 ./tests/integration/...
```

- [ ] **Step 6.3: Run `make tidy` to fetch testify/suite + gofakeit**

```bash
make tidy
```

Expected: `go.mod` gains `github.com/stretchr/testify` (already present, but `suite` subpackage now actively imported) and `github.com/brianvoe/gofakeit/v7`. `vendor/` is updated.

- [ ] **Step 6.4: Run the empty suite**

```bash
make integration-test
```

Expected: `TestSubscriptionSuite` runs, prints `--- PASS: TestSubscriptionSuite (X.Xs)` with no sub-tests (suite hooks fire, no test methods yet). Docker pulls `postgres:17-alpine` on first run.

If you see `connection refused` or `dial tcp`: Docker daemon is not running. If you see `executable file not found in $PATH`: the binary `docker` is missing.

- [ ] **Step 6.5: Commit**

```bash
git add tests/integration/subscription/suite_test.go Makefile go.mod go.sum vendor/
git commit -m "feat(tests): scaffold subscription integration suite and make target"
```

---

## Task 7: Test `TestSubscribe_HappyPath`

**Files:**
- Create: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 7.1: Create the file with the first test**

```go
//go:build integration

package subscription_test

import (
	"net/http"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func (s *SubscriptionSuite) TestSubscribe_HappyPath() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})

	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	var body subhttp.StatusResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "pending_confirmation", body.Status)

	// Repository row
	repoID, ok := s.selectRepository("golang", "go")
	require.True(s.T(), ok, "repositories row missing")

	// Subscription row
	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	sub := subs[0]
	assert.Equal(s.T(), repoID, sub.RepositoryID)
	assert.Nil(s.T(), sub.ConfirmedAt)
	require.NotNil(s.T(), sub.ConfirmToken)
	assert.NotEmpty(s.T(), *sub.ConfirmToken)
	assert.NotEmpty(s.T(), sub.UnsubscribeToken)
	require.NotNil(s.T(), sub.ConfirmTokenExpiresAt)
	assert.True(s.T(), sub.ConfirmTokenExpiresAt.After(time.Now()))

	// Outbox row
	assert.Equal(s.T(), 1, s.countConfirmationNotifications(sub.ID))

	// GitHub was called once
	assert.Equal(s.T(), 1, s.githubFx.RequestCount())

	// Metric incremented
	s.assertCreatedCounter(1)

	// Silence unused-import warning until later tests pull in domain.
	_ = domain.ErrInvalidRepoFormat
}
```

Note: the `domain` import is included now because Tasks 10/12/13 below depend on it. The `_ = domain.ErrInvalidRepoFormat` line keeps the import live and will be removed once Task 10 lands.

- [ ] **Step 7.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_HappyPath' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 7.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe happy path"
```

---

## Task 8: `TestSubscribe_InvalidJSON`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 8.1: Append the test**

Append:

```go
func (s *SubscriptionSuite) TestSubscribe_InvalidJSON() {
	resp := s.post("/api/subscribe", "not-json")
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "invalid JSON body", body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(""), "no subscription row should be created")
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 0, s.githubFx.RequestCount())
	s.assertCreatedCounter(0)
}
```

- [ ] **Step 8.2: Run the suite**

```bash
make integration-test
```

Expected: both `TestSubscribe_HappyPath` and `TestSubscribe_InvalidJSON` PASS.

- [ ] **Step 8.3: Commit**

```bash
git add tests/integration/subscription/
git commit -m "test(integration): /api/subscribe invalid JSON body"
```

---

## Task 9: `TestSubscribe_InvalidEmail`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 9.1: Append the test**

```go
func (s *SubscriptionSuite) TestSubscribe_InvalidEmail() {
	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      "not-an-email",
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "invalid email", body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail("not-an-email"))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 0, s.githubFx.RequestCount(), "validation must short-circuit before reaching GitHub")
	s.assertCreatedCounter(0)
}
```

- [ ] **Step 9.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_InvalidEmail' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 9.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe invalid email"
```

---

## Task 10: `TestSubscribe_InvalidRepoFormat`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 10.1: Append the test**

```go
func (s *SubscriptionSuite) TestSubscribe_InvalidRepoFormat() {
	email := s.randomEmail()
	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "justonepart",
	})
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), domain.ErrInvalidRepoFormat.Error(), body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 0, s.githubFx.RequestCount())
	s.assertCreatedCounter(0)
}
```

- [ ] **Step 10.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_InvalidRepoFormat' -v ./tests/integration/subscription/...
```

Expected: PASS. If FAIL because `body.Error` differs, inspect `domain.ErrInvalidRepoFormat` in `internal/subscription/domain/errors.go` and reconcile.

- [ ] **Step 10.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe invalid repository format"
```

---

## Task 11: `TestSubscribe_RepoURL_Normalized`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 11.1: Append the test**

```go
func (s *SubscriptionSuite) TestSubscribe_RepoURL_Normalized() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "https://github.com/golang/go.git",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	_, ok := s.selectRepository("golang", "go")
	require.True(s.T(), ok, "repository should be stored as owner/name without prefix or .git")

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
}
```

- [ ] **Step 11.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_RepoURL_Normalized' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 11.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe normalizes full repo URL"
```

---

## Task 12: `TestSubscribe_RepoNotFoundOnGitHub`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 12.1: Append the test**

```go
func (s *SubscriptionSuite) TestSubscribe_RepoNotFoundOnGitHub() {
	s.githubFx.SetRepoExists("foo/bar", false)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "foo/bar",
	})
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), domain.ErrRepoNotFound.Error(), body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 1, s.githubFx.RequestCount())
	s.assertCreatedCounter(0)
}
```

- [ ] **Step 12.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_RepoNotFoundOnGitHub' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 12.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe repo not found on GitHub"
```

---

## Task 13: `TestSubscribe_DuplicateSubscription`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 13.1: Append the test**

```go
func (s *SubscriptionSuite) TestSubscribe_DuplicateSubscription() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()
	req := subhttp.SubscribeRequest{Email: email, Repository: "golang/go"}

	// First subscribe: 202.
	resp1 := s.post("/api/subscribe", req)
	require.Equal(s.T(), http.StatusAccepted, resp1.StatusCode)
	resp1.Body.Close()

	// Second subscribe: 409.
	resp2 := s.post("/api/subscribe", req)
	require.Equal(s.T(), http.StatusConflict, resp2.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp2, &body)
	assert.Equal(s.T(), domain.ErrAlreadyExists.Error(), body.Error)

	// Exactly one subscription row and one outbox row remain.
	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	assert.Equal(s.T(), 1, s.countConfirmationNotifications(subs[0].ID))
	assert.Equal(s.T(), 1, s.countAllConfirmationNotifications())

	// Counter only ticks on the successful subscribe.
	s.assertCreatedCounter(1)
}
```

- [ ] **Step 13.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_DuplicateSubscription' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 13.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe duplicate returns 409"
```

---

## Task 14: `TestSubscribe_GitHubError`

**Files:**
- Modify: `tests/integration/subscription/subscribe_test.go`

- [ ] **Step 14.1: Append the test**

```go
func (s *SubscriptionSuite) TestSubscribe_GitHubError() {
	s.githubFx.ForceError()
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusInternalServerError, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "internal server error", body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.GreaterOrEqual(s.T(), s.githubFx.RequestCount(), 1)
	s.assertCreatedCounter(0)
}
```

- [ ] **Step 14.2: Run the test**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_GitHubError' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 14.3: Commit**

```bash
git add tests/integration/subscription/subscribe_test.go
git commit -m "test(integration): /api/subscribe maps GitHub error to 500"
```

---

## Task 15: Final pass — vet, lint, full suite

**Files:**
- None (verification only).

- [ ] **Step 15.1: Run vet on integration tree**

```bash
go vet -tags=integration ./tests/integration/...
```

Expected: clean.

- [ ] **Step 15.2: Run lint on integration tree**

```bash
golangci-lint run --build-tags=integration ./tests/integration/...
```

Expected: clean. If `wrapcheck` flags the helpers, add `tests/integration/internal/` to the per-package linter exclusion list in `.golangci.yml`, or accept that integration helpers may wrap differently from production code. Prefer the latter — only add exclusions if a specific lint is wrong.

- [ ] **Step 15.3: Run the full integration suite**

```bash
make integration-test
```

Expected:

```
=== RUN   TestSubscriptionSuite
=== RUN   TestSubscriptionSuite/TestSubscribe_DuplicateSubscription
=== RUN   TestSubscriptionSuite/TestSubscribe_GitHubError
=== RUN   TestSubscriptionSuite/TestSubscribe_HappyPath
=== RUN   TestSubscriptionSuite/TestSubscribe_InvalidEmail
=== RUN   TestSubscriptionSuite/TestSubscribe_InvalidJSON
=== RUN   TestSubscriptionSuite/TestSubscribe_InvalidRepoFormat
=== RUN   TestSubscriptionSuite/TestSubscribe_RepoNotFoundOnGitHub
=== RUN   TestSubscriptionSuite/TestSubscribe_RepoURL_Normalized
--- PASS: TestSubscriptionSuite
PASS
```

Method names in the output above are alphabetical (testify-suite default), so the order is fixed.

- [ ] **Step 15.4: Run the existing unit tests to ensure no regression**

```bash
make test
```

Expected: all existing tests still pass (no `-tags=integration` here).

- [ ] **Step 15.5: Final commit (only if any lint/vet adjustment was needed)**

If Step 15.2 required changes to `.golangci.yml`:

```bash
git add .golangci.yml
git commit -m "chore: golangci-lint adjustments for integration tests"
```

Otherwise, nothing to commit at this step.

---

## Out of Scope (handled by future plans)

- Suites for `/api/confirm`, `/api/unsubscribe`, `/api/subscriptions`.
- Cron worker suites (`scanner`, `notifier`, `confirmer`).
- CI job that runs `make integration-test`.
- Caching-decorator coverage (`internal/github/caching_client.go`) — irrelevant to `/api/subscribe`.

---

## Spec → Plan coverage map

| Spec section | Plan task(s) |
| --- | --- |
| File Layout | Task 2 (README), Task 3-5 (helpers), Task 6 (subscription package) |
| Build Tag | All Tasks 3-14 (every file carries `//go:build integration`) |
| Dependencies Added to `go.mod` | Task 3 (testcontainers), Task 6 (testify/suite, gofakeit) |
| Production Code Change: `internal/github/client.go` | Task 1 |
| `tests/integration/internal/pg.go` | Task 3 |
| `tests/integration/internal/github.go` | Task 4 |
| `tests/integration/internal/app.go` | Task 5 |
| `tests/integration/subscription/suite_test.go` | Task 6, Task 8 (helper expansion) |
| `tests/integration/subscription/subscribe_test.go` (8 cases) | Tasks 7-14 |
| Makefile | Task 6 |
| README | Task 2 |