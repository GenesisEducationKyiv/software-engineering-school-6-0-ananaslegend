package app

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/config"
	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/scanner"
)

func newReleaseProvider(cfg config.Config, log zerolog.Logger, reg *prometheus.Registry, rdb *redis.Client) scanner.ReleaseProvider {
	githubClient := githubclient.NewClient(cfg.GitHubToken)

	provider := githubclient.NewCachingClient(githubclient.CachingConfig{
		Provider: githubClient,
		RDB:      rdb,
		TTL:      10 * time.Minute,
		Registry: reg,
	})
	log.Info().Msg("github release cache: redis")

	return provider
}
