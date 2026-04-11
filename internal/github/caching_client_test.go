package github

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

func TestCachingReleaseProvider_CacheMiss(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(CachingConfig{Provider: stub, RDB: rdb, TTL: time.Minute})

	repos := []domain.GitHubRepo{{ID: 1, Owner: "golang", Name: "go"}}
	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})

	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", tags[1])
	assert.Equal(t, 1, stub.calls)
}

func TestCachingReleaseProvider_CacheHit(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(CachingConfig{Provider: stub, RDB: rdb, TTL: time.Minute})

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
	c := NewCachingClient(CachingConfig{Provider: stub, RDB: rdb, TTL: time.Minute})

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

func TestCachingReleaseProvider_NoRelease_NotCached(t *testing.T) {
	rdb, _ := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{}} // repo has no release
	c := NewCachingClient(CachingConfig{Provider: stub, RDB: rdb, TTL: time.Minute})

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

func TestCachingClient_MetricsCacheHit(t *testing.T) {
	rdb, _ := newTestRedis(t)
	reg := prometheus.NewRegistry()
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(CachingConfig{Provider: stub, RDB: rdb, TTL: time.Minute, Registry: reg})

	repos := []domain.GitHubRepo{{ID: 1}}
	ctx := context.Background()

	// first call — miss, populates cache
	_, err := c.GetLatestReleases(ctx, GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)

	// second call — hit
	_, err = c.GetLatestReleases(ctx, GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP github_cache_hits_total Total number of GitHub release cache hits.
# TYPE github_cache_hits_total counter
github_cache_hits_total 1
# HELP github_cache_misses_total Total number of GitHub release cache misses.
# TYPE github_cache_misses_total counter
github_cache_misses_total 1
`), "github_cache_hits_total", "github_cache_misses_total"))
}

func TestCachingReleaseProvider_RedisError_Fallback(t *testing.T) {
	rdb, mr := newTestRedis(t)
	stub := &stubProvider{result: map[int64]string{1: "v1.0.0"}}
	c := NewCachingClient(CachingConfig{Provider: stub, RDB: rdb, TTL: time.Minute})

	mr.Close() // simulate Redis unavailable

	repos := []domain.GitHubRepo{{ID: 1, Owner: "golang", Name: "go"}}
	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})

	require.NoError(t, err) // no error — silently fell back
	assert.Equal(t, "v1.0.0", tags[1])
	assert.Equal(t, 1, stub.calls)
}
