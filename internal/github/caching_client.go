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
