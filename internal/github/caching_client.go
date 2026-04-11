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
