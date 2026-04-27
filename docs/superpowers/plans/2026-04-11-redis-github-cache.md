# Redis GitHub Release Cache — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wrap `*github.Client` with a Redis-backed `CachingReleaseProvider` decorator that caches release tags per repo for 10 minutes; the scanner is unaware of caching.

**Architecture:** `CachingReleaseProvider` in `internal/github/caching_client.go` implements the same `GetLatestReleases` method as `*Client`. It reads via `MGET` (one round-trip), calls the wrapped client for misses only, and writes results via a Redis pipeline. Repos with no release are never cached. Redis errors fall back silently to the wrapped client.

**Tech Stack:** `github.com/redis/go-redis/v9`, `github.com/alicebob/miniredis/v2` (tests only), `go mod vendor`

---

## File Map

| Action | Path | Responsibility |
|---|---|---|
| Create | `internal/storage/redis/client.go` | `NewClient(cfg) (*redis.Client, error)` — parse URL, Ping |
| Create | `internal/github/caching_client.go` | `CachingReleaseProvider` — MGET, pipeline SET, fallback |
| Create | `internal/github/caching_client_test.go` | unit tests with miniredis |
| Modify | `internal/config/config.go` | add `RedisURL string` field |
| Modify | `cmd/api/main.go` | wire Redis + wrap githubClient |
| Modify | `docker-compose.yml` | add `redis:7-alpine` service |
| Modify | `.env.example` | add `REDIS_URL` |

---

### Task 1: Add dependencies

**Files:**
- Modify: `go.mod`, `go.sum`, `vendor/`

- [ ] **Step 1: Add runtime and test dependencies**

```bash
cd /path/to/repo
go get github.com/redis/go-redis/v9
go get github.com/alicebob/miniredis/v2
go mod tidy
go mod vendor
```

Expected: no errors, `vendor/` updated with `go-redis` and `miniredis` packages.

- [ ] **Step 2: Verify build still passes**

```bash
go build ./...
```

Expected: exits 0, no output.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum vendor/
git commit -m "chore: add go-redis and miniredis dependencies"
```

---

### Task 2: Redis storage client

**Files:**
- Create: `internal/storage/redis/client.go`

- [ ] **Step 1: Create `internal/storage/redis/client.go`**

```go
package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	URL string
}

func NewClient(cfg Config) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return rdb, nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/storage/redis/...
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/redis/client.go
git commit -m "feat: add Redis client constructor"
```

---

### Task 3: CachingReleaseProvider — scaffold + cache miss

**Files:**
- Create: `internal/github/caching_client.go`
- Create: `internal/github/caching_client_test.go`

- [ ] **Step 1: Create test file with helpers and cache miss test**

`internal/github/caching_client_test.go`:

```go
package github

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type stubProvider struct {
	calls  int
	result map[int64]string
}

func (s *stubProvider) GetLatestReleases(_ context.Context, p GetLatestReleasesParams) (map[int64]string, error) {
	s.calls++
	filtered := make(map[int64]string)
	for _, repo := range p.Repos {
		if tag, ok := s.result[repo.ID]; ok {
			filtered[repo.ID] = tag
		}
	}
	return filtered, nil
}

func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return rdb, mr
}

func TestCachingReleaseProvider_CacheMiss(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(stub, rdb, time.Minute)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "golang", Name: "go"}}
	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})

	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", tags[1])
	assert.Equal(t, 1, stub.calls)
}
```

- [ ] **Step 2: Run test — expect compile failure (type not defined yet)**

```bash
go test ./internal/github/... -run TestCachingReleaseProvider_CacheMiss -v
```

Expected: compile error `undefined: NewCachingClient`.

- [ ] **Step 3: Create minimal scaffold `internal/github/caching_client.go`**

```go
package github

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type releaseProvider interface {
	GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error)
}

// CachingReleaseProvider wraps a ReleaseProvider with Redis caching.
type CachingReleaseProvider struct {
	wrapped releaseProvider
	rdb     *redis.Client
	ttl     time.Duration
}

func NewCachingClient(wrapped releaseProvider, rdb *redis.Client, ttl time.Duration) *CachingReleaseProvider {
	return &CachingReleaseProvider{wrapped: wrapped, rdb: rdb, ttl: ttl}
}

func (c *CachingReleaseProvider) GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error) {
	return c.wrapped.GetLatestReleases(ctx, p)
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
go test ./internal/github/... -run TestCachingReleaseProvider_CacheMiss -v
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/github/caching_client.go internal/github/caching_client_test.go
git commit -m "feat: scaffold CachingReleaseProvider with cache miss test"
```

---

### Task 4: CachingReleaseProvider — cache hit + partial hit/miss

**Files:**
- Modify: `internal/github/caching_client_test.go`
- Modify: `internal/github/caching_client.go`

- [ ] **Step 1: Add cache hit and partial hit/miss tests**

Append to `internal/github/caching_client_test.go`:

```go
func TestCachingReleaseProvider_CacheHit(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(stub, rdb, time.Minute)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "golang", Name: "go"}}

	// First call — populates cache
	_, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)

	// Second call — must hit cache, not call wrapped again
	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", tags[1])
	assert.Equal(t, 1, stub.calls)
}

func TestCachingReleaseProvider_PartialHitMiss(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0", 2: "v2.0.0"}}
	c := NewCachingClient(stub, rdb, time.Minute)

	repo1 := domain.GitHubRepo{ID: 1, Owner: "golang", Name: "go"}
	repo2 := domain.GitHubRepo{ID: 2, Owner: "torvalds", Name: "linux"}

	// Populate cache for repo1 only
	_, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: []domain.GitHubRepo{repo1}})
	require.NoError(t, err)
	assert.Equal(t, 1, stub.calls)

	// repo1 from cache, repo2 from GitHub — wrapped called once more (for repo2 only)
	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: []domain.GitHubRepo{repo1, repo2}})
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", tags[1])
	assert.Equal(t, "v2.0.0", tags[2])
	assert.Equal(t, 2, stub.calls)
}
```

- [ ] **Step 2: Run new tests — expect FAIL**

```bash
go test ./internal/github/... -run "TestCachingReleaseProvider_CacheHit|TestCachingReleaseProvider_PartialHitMiss" -v
```

Expected: FAIL — `stub.calls` is 2 (no caching implemented yet).

- [ ] **Step 3: Replace `GetLatestReleases` in `caching_client.go` with full implementation**

Replace the entire file content with:

```go
package github

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type releaseProvider interface {
	GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error)
}

// CachingReleaseProvider wraps a ReleaseProvider with Redis caching.
// Cache key: "github:release:{repoID}", TTL: configured.
// Repos with no release are never cached — GitHub is queried on every tick.
// Redis errors fall back to the wrapped provider without returning an error.
type CachingReleaseProvider struct {
	wrapped releaseProvider
	rdb     *redis.Client
	ttl     time.Duration
}

func NewCachingClient(wrapped releaseProvider, rdb *redis.Client, ttl time.Duration) *CachingReleaseProvider {
	return &CachingReleaseProvider{wrapped: wrapped, rdb: rdb, ttl: ttl}
}

func (c *CachingReleaseProvider) GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error) {
	if len(p.Repos) == 0 {
		return nil, nil
	}

	log := zerolog.Ctx(ctx)

	keys := make([]string, len(p.Repos))
	for i, repo := range p.Repos {
		keys[i] = cacheKey(repo.ID)
	}

	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		log.Warn().Err(err).Msg("redis mget failed, falling back to github")
		return c.wrapped.GetLatestReleases(ctx, p)
	}

	result := make(map[int64]string)
	var misses []domain.GitHubRepo

	for i, repo := range p.Repos {
		if vals[i] == nil {
			misses = append(misses, repo)
		} else {
			result[repo.ID] = vals[i].(string)
		}
	}

	if len(misses) == 0 {
		return result, nil
	}

	fresh, err := c.wrapped.GetLatestReleases(ctx, GetLatestReleasesParams{Repos: misses})
	if err != nil {
		return nil, err
	}

	pipe := c.rdb.Pipeline()
	for _, repo := range misses {
		if tag, ok := fresh[repo.ID]; ok {
			pipe.Set(ctx, cacheKey(repo.ID), tag, c.ttl)
			result[repo.ID] = tag
		}
		// no release → not cached, will be queried on next tick
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Warn().Err(err).Msg("redis pipeline exec failed")
	}

	return result, nil
}

func cacheKey(repoID int64) string {
	return "github:release:" + strconv.FormatInt(repoID, 10)
}
```

- [ ] **Step 4: Run all caching tests — expect PASS**

```bash
go test ./internal/github/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/github/caching_client.go internal/github/caching_client_test.go
git commit -m "feat: implement CachingReleaseProvider with MGET + pipeline SET"
```

---

### Task 5: CachingReleaseProvider — no-release not cached

**Files:**
- Modify: `internal/github/caching_client_test.go`

- [ ] **Step 1: Add no-release test**

Append to `internal/github/caching_client_test.go`:

```go
func TestCachingReleaseProvider_NoRelease_NotCached(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{}} // repo has no release
	c := NewCachingClient(stub, rdb, time.Minute)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar"}}

	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)
	assert.Empty(t, tags)
	assert.Equal(t, 1, stub.calls)

	// Second call — no-release was not cached, wrapped must be called again
	tags, err = c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)
	assert.Empty(t, tags)
	assert.Equal(t, 2, stub.calls)
}
```

- [ ] **Step 2: Run test — expect PASS (implementation already handles this)**

```bash
go test ./internal/github/... -run TestCachingReleaseProvider_NoRelease_NotCached -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/github/caching_client_test.go
git commit -m "test: verify no-release repos are not cached"
```

---

### Task 6: CachingReleaseProvider — Redis error fallback

**Files:**
- Modify: `internal/github/caching_client_test.go`

- [ ] **Step 1: Add Redis error fallback test**

Append to `internal/github/caching_client_test.go`:

```go
func TestCachingReleaseProvider_RedisError_Fallback(t *testing.T) {
	rdb, mr := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(stub, rdb, time.Minute)

	mr.Close() // simulate Redis unavailable

	repos := []domain.GitHubRepo{{ID: 1, Owner: "golang", Name: "go"}}
	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})

	require.NoError(t, err) // no error — silently fell back
	assert.Equal(t, "v1.0.0", tags[1])
	assert.Equal(t, 1, stub.calls)
}
```

- [ ] **Step 2: Run test — expect PASS**

```bash
go test ./internal/github/... -run TestCachingReleaseProvider_RedisError_Fallback -v
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

```bash
go test ./...
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/github/caching_client_test.go
git commit -m "test: verify Redis error falls back to GitHub client silently"
```

---

### Task 7: Config, wiring, docker-compose, .env.example

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/api/main.go`
- Modify: `docker-compose.yml`
- Modify: `.env.example`

- [ ] **Step 1: Add `RedisURL` to config**

In `internal/config/config.go`, add after `DBMaxConns`:

```go
RedisURL string `envconfig:"REDIS_URL"`
```

Full `Config` struct becomes:

```go
type Config struct {
	HTTPAddr            string        `envconfig:"HTTP_ADDR" default:":8080"`
	HTTPReadTimeout     time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"10s"`
	HTTPWriteTimeout    time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"10s"`
	HTTPShutdownTimeout time.Duration `envconfig:"HTTP_SHUTDOWN_TIMEOUT" default:"15s"`

	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`
	DBMaxConns  int32  `envconfig:"DB_MAX_CONNS" default:"10"`

	RedisURL string `envconfig:"REDIS_URL"`

	AppBaseURL      string        `envconfig:"APP_BASE_URL" default:"http://localhost:8080"`
	ConfirmTokenTTL time.Duration `envconfig:"CONFIRM_TOKEN_TTL" default:"24h"`

	LogLevel  string `envconfig:"LOG_LEVEL" default:"info"`
	LogPretty bool   `envconfig:"LOG_PRETTY" default:"true"`

	SMTPHost      string `envconfig:"SMTP_HOST"`
	SMTPPort      int    `envconfig:"SMTP_PORT" default:"587"`
	SMTPUser      string `envconfig:"SMTP_USER"`
	SMTPPass      string `envconfig:"SMTP_PASS"`
	SMTPFrom      string `envconfig:"SMTP_FROM"`
	SMTPTLSPolicy string `envconfig:"SMTP_TLS_POLICY" default:"starttls"`

	ResendAPIKey string `envconfig:"RESEND_API_KEY"`
	ResendFrom   string `envconfig:"RESEND_FROM"`

	GitHubToken string `envconfig:"GITHUB_TOKEN"`

	ScannerInterval   time.Duration `envconfig:"SCANNER_INTERVAL" default:"5m"`
	NotifierInterval  time.Duration `envconfig:"NOTIFIER_INTERVAL" default:"30s"`
	ConfirmerInterval time.Duration `envconfig:"CONFIRMER_INTERVAL" default:"30s"`
}
```

- [ ] **Step 2: Wire Redis + CachingReleaseProvider in `cmd/api/main.go`**

Add two imports (into the existing import block):

```go
"time"

redisstorage "github.com/ananaslegend/reposeetory/internal/storage/redis"
```

Replace the existing block:

```go
githubClient := githubclient.NewClient(cfg.GitHubToken)

scan := scanner.New(scanner.Config{
    Repo:     scannerrepo.New(pool),
    GitHub:   githubClient,
    Interval: cfg.ScannerInterval,
})
```

With:

```go
githubClient := githubclient.NewClient(cfg.GitHubToken)

var releaseProvider scanner.ReleaseProvider = githubClient
if cfg.RedisURL != "" {
    rdb, err := redisstorage.NewClient(redisstorage.Config{URL: cfg.RedisURL})
    if err != nil {
        log.Warn().Err(err).Msg("redis unavailable, github caching disabled")
    } else {
        releaseProvider = githubclient.NewCachingClient(githubClient, rdb, 10*time.Minute)
        log.Info().Msg("github release cache: redis")
    }
}

scan := scanner.New(scanner.Config{
    Repo:     scannerrepo.New(pool),
    GitHub:   releaseProvider,
    Interval: cfg.ScannerInterval,
})
```

- [ ] **Step 3: Add Redis service to `docker-compose.yml`**

Add the `redis` service and its dependency in `api`:

```yaml
services:
  postgres:
    ports:
      - "5432:5432"
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: pass
      POSTGRES_DB: postgres
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 10

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 10

  mailpit:
    image: axllent/mailpit:latest
    ports:
      - "8025:8025"

  migrate:
    image: migrate/migrate:latest
    volumes:
      - ./migrations:/migrations:ro
    command:
      - "-path=/migrations"
      - "-database=postgres://postgres:pass@postgres:5432/postgres?sslmode=disable"
      - "up"
    depends_on:
      postgres:
        condition: service_healthy
    restart: on-failure

  api:
    build: .
    ports:
      - "8080:8080"
    environment:
      DATABASE_URL: postgres://postgres:pass@postgres:5432/postgres?sslmode=disable
      APP_BASE_URL: http://localhost:8080
      SMTP_HOST: mailpit
      SMTP_PORT: 1025
      SMTP_TLS_POLICY: none
      SMTP_FROM: noreply@reposeetory.local
      REDIS_URL: redis://redis:6379
      LOG_PRETTY: "true"
      LOG_LEVEL: info
    env_file:
      - path: .env
        required: false
    depends_on:
      migrate:
        condition: service_completed_successfully
      mailpit:
        condition: service_started
      redis:
        condition: service_healthy

volumes:
  pgdata:
```

- [ ] **Step 4: Add `REDIS_URL` to `.env.example`**

Add after the `DB_MAX_CONNS` line:

```
# Redis (optional — leave empty to disable GitHub release caching)
# REDIS_URL=redis://localhost:6379
```

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

Expected: exits 0.

- [ ] **Step 6: Run full test suite**

```bash
go test ./...
```

Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go cmd/api/main.go docker-compose.yml .env.example
git commit -m "feat: wire Redis caching for GitHub release provider"
```
