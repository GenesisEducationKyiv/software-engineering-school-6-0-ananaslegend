package github

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

// ReleaseProvider is the interface for fetching the latest release tags.
type ReleaseProvider interface {
	GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error)
}

// CachingConfig holds dependencies for CachingReleaseProvider.
type CachingConfig struct {
	Provider ReleaseProvider
	RDB      *redis.Client
	TTL      time.Duration
	Registry *prometheus.Registry
	RED      *redmetrics.RED
}

// CachingReleaseProvider wraps a ReleaseProvider with Redis caching.
// Cache key: "github:release:{repoID}", TTL: configured.
// Repos with no release are never cached — GitHub is queried on every tick.
// Redis errors fall back to the wrapped provider without returning an error.
type CachingReleaseProvider struct {
	wrapped ReleaseProvider
	rdb     *redis.Client
	ttl     time.Duration
	m       cacheMetrics
	red     *redmetrics.RED
}

func NewCachingClient(cfg CachingConfig) *CachingReleaseProvider {
	return &CachingReleaseProvider{
		wrapped: cfg.Provider,
		rdb:     cfg.RDB,
		ttl:     cfg.TTL,
		m:       newCacheMetrics(cfg.Registry),
		red:     cfg.RED,
	}
}

// GetLatestReleases is the instrumented boundary for the cached GitHub release
// provider. It records RED metrics with one of three result classifications:
//
//   - "cached" — every requested repo was found in Redis; the wrapped provider
//     was not called.
//   - "ok"     — at least one cache miss caused a fallthrough to the wrapped
//     provider, which returned successfully. Also covers the empty-input case
//     and silent Redis-MGET-failure fallbacks that succeed against GitHub.
//   - "error"  — the wrapped provider returned an error.
func (c *CachingReleaseProvider) GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error) {
	start := time.Now()
	result := "ok"
	defer func() {
		c.red.Observe(result, time.Since(start))
	}()

	if len(p.Repos) == 0 {
		return nil, nil
	}

	res, hadMisses, err := c.fetch(ctx, p)
	switch {
	case err != nil:
		result = "error"
	case !hadMisses:
		result = "cached"
	default:
		result = "ok"
	}
	if err != nil {
		return res, fmt.Errorf("github.CachingReleaseProvider.GetLatestReleases: %w", err)
	}
	return res, nil
}

// fetch performs the cache-lookup-then-fallback flow. It returns the merged
// result map, whether the wrapped provider was invoked (i.e. at least one
// cache miss occurred — also true on silent Redis MGET failures), and any
// error from the wrapped provider.
func (c *CachingReleaseProvider) fetch(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, bool, error) {
	log := zerolog.Ctx(ctx)

	keys := make([]string, len(p.Repos))
	for i, repo := range p.Repos {
		keys[i] = cacheKey(repo.ID)
	}

	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		log.Warn().Err(err).Msg("redis mget failed, falling back to github")
		c.m.errors.Inc()
		fresh, err := c.wrapped.GetLatestReleases(ctx, p)
		if err != nil {
			return nil, true, fmt.Errorf("github.CachingReleaseProvider.fetch: ReleaseProvider.GetLatestReleases (redis fallback): %w", err)
		}
		return fresh, true, nil
	}

	result := make(map[int64]string)
	var misses []domain.GitHubRepo

	for i, repo := range p.Repos {
		if vals[i] == nil {
			misses = append(misses, repo)
			c.m.misses.Inc()
		} else if s, ok := vals[i].(string); ok {
			result[repo.ID] = s
			c.m.hits.Inc()
		} else {
			misses = append(misses, repo)
			c.m.misses.Inc()
		}
	}

	hadMisses := len(misses) > 0
	if !hadMisses {
		return result, false, nil
	}

	fresh, err := c.wrapped.GetLatestReleases(ctx, GetLatestReleasesParams{Repos: misses})
	if err != nil {
		return nil, true, fmt.Errorf("github.CachingReleaseProvider.fetch: ReleaseProvider.GetLatestReleases: %w", err)
	}

	var toCache []domain.GitHubRepo
	for _, repo := range misses {
		if tag, ok := fresh[repo.ID]; ok {
			result[repo.ID] = tag
			toCache = append(toCache, repo)
		}
		// no release → not cached, will be queried on next tick
	}
	if len(toCache) > 0 {
		pipe := c.rdb.Pipeline()
		for _, repo := range toCache {
			pipe.Set(ctx, cacheKey(repo.ID), fresh[repo.ID], c.ttl)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Warn().Err(err).Int("count", len(toCache)).Msg("redis pipeline exec failed")
		}
	}

	return result, true, nil
}

func cacheKey(repoID int64) string {
	return "github:release:" + strconv.FormatInt(repoID, 10)
}
