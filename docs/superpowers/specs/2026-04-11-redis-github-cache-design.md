# Redis GitHub Cache — Design Spec

**Date:** 2026-04-11  
**Status:** Approved

## Problem

The scanner calls GitHub GraphQL API every tick (default: 5 min) for up to 100 repos.
With many subscribers, the same repo tags are fetched repeatedly within a short window.
Redis caching reduces redundant API calls and helps stay within GitHub rate limits.

## Solution

A `CachingReleaseProvider` decorator in `internal/github/` wraps the real `*Client`.
The scanner uses `ReleaseProvider` interface and is unaware of caching.

## Architecture

```
scanner.ReleaseProvider (interface)
         ↑
CachingReleaseProvider   ← wraps →   *github.Client
         ↑
    *redis.Client
```

## Components

### 1. `internal/github/caching_client.go`

New type `CachingReleaseProvider` with fields:
- `wrapped releaseProvider` — unexported interface (`GetLatestReleases` only), satisfied by `*Client`
- `rdb *redis.Client`
- `ttl time.Duration`
- `log zerolog.Logger`

Constructor: `NewCachingClient(wrapped releaseProvider, rdb *redis.Client, ttl time.Duration) *CachingReleaseProvider`

**`GetLatestReleases` algorithm:**

1. Build keys `github:release:{repoID}` for all repos
2. `MGET` all keys in one Redis round-trip
3. If Redis error → `log.Warn`, delegate entire call to `wrapped`, return result directly
4. Split repos into **hits** (key present in Redis) and **misses** (key absent)
5. If no misses → return cached result
6. Call `wrapped.GetLatestReleases` for misses only
7. For each miss: if tag found in GitHub response → `SET github:release:{id} {tag} EX 600`; if no release → skip (do not cache)
8. SET error → `log.Warn`, continue
9. Merge cached hits + fresh results, return

**Cache key:** `github:release:{repoID}`  
**TTL:** 10 minutes (600 seconds)  
**No-release repos:** never cached — GitHub is queried every tick for them

### 2. `internal/storage/redis/client.go`

Mirrors `internal/storage/postgres/pool.go` pattern:

```go
type Config struct {
    URL string
}

func NewClient(cfg Config) (*redis.Client, error)
```

Performs `Ping` on creation for early-fail. Returns error if unreachable.

**Library:** `github.com/redis/go-redis/v9`

### 3. Config (`internal/config/config.go`)

Add one field:

```go
RedisURL string `envconfig:"REDIS_URL"`
```

Optional — no default, no `required:"true"`.

### 4. Wiring (`cmd/api/main.go`)

```go
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

### 5. Infrastructure

**`docker-compose.yml`:** add `redis:7-alpine` service, port `6379`.

**`.env.example`:** add `REDIS_URL=redis://localhost:6379`

## Error Handling

| Scenario | Behavior |
|---|---|
| `REDIS_URL` not set | No Redis client created; scanner uses `*Client` directly |
| Redis unreachable at startup | `log.Warn`, caching disabled, app starts normally |
| `MGET` error at runtime | `log.Warn`, full fallback to `wrapped`, no error returned |
| `SET` error at runtime | `log.Warn`, cached data already used, continue |
| GitHub error | Propagated normally (Redis not involved) |

## Testing

File: `internal/github/caching_client_test.go`

Library: `github.com/alicebob/miniredis/v2` (in-process Redis, no external dependency in tests)

Test cases:
- Cache miss → wrapped is called, result cached
- Cache hit → wrapped is NOT called
- Partial hit/miss → wrapped called only for misses
- No-release repo (absent from GitHub response) → not cached, wrapped called again next time
- Redis MGET error → full fallback to wrapped, no error returned
- Redis SET error → result still returned correctly

## Out of Scope

- Cache invalidation on demand
- Metrics (cache hit rate)
- Per-repo TTL configuration
