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
