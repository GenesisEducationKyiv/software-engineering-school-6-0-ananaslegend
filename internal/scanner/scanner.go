package scanner

//go:generate mockgen -source=scanner.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

// Repository is the storage contract for the scanner.
type Repository interface {
	GetRepositoriesWithLock(ctx context.Context, limit int) ([]domain.GitHubRepo, error)
	InsertNotifications(ctx context.Context, repoID int64, tag string) error
	UpsertLastSeen(ctx context.Context, repoID int64, tag string) error
}

// ReleaseProvider fetches the latest release tag for a batch of repos.
type ReleaseProvider interface {
	GetLatestReleases(ctx context.Context, p githubclient.GetLatestReleasesParams) (map[int64]string, error)
}

// Config holds Scanner dependencies.
type Config struct {
	Tx       transactor.Transactor
	Repo     Repository
	GitHub   ReleaseProvider
	Interval time.Duration
	Registry *prometheus.Registry
}

// Scanner periodically checks GitHub for new releases and writes outbox rows.
type Scanner struct {
	tx       transactor.Transactor
	repo     Repository
	github   ReleaseProvider
	interval time.Duration
	m        scannerMetrics
}

const scanLimit = 100

// New creates a Scanner from cfg.
func New(cfg Config) *Scanner {
	return &Scanner{
		tx:       cfg.Tx,
		repo:     cfg.Repo,
		github:   cfg.GitHub,
		interval: cfg.Interval,
		m:        newScannerMetrics(cfg.Registry),
	}
}

// Run blocks until ctx is cancelled, firing a scan tick on each interval.
func (s *Scanner) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			zerolog.Ctx(ctx).Debug().Msg("scanner tick")
			if err := s.Tick(ctx); err != nil {
				zerolog.Ctx(ctx).Error().Err(err).Msg("scanner tick failed")
			}
		}
	}
}

// Tick executes one scan cycle. Exported for testing.
func (s *Scanner) Tick(ctx context.Context) error {
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		repos, err := s.repo.GetRepositoriesWithLock(ctx, scanLimit)
		if err != nil {
			return fmt.Errorf("get repos with lock: %w", err)
		}

		if len(repos) == 0 {
			return nil
		}
		s.m.reposScanned.Add(float64(len(repos)))

		tags, err := s.github.GetLatestReleases(ctx, githubclient.GetLatestReleasesParams{Repos: repos})
		if err != nil {
			return fmt.Errorf("get latest releases: %w", err)
		}

		for _, repo := range repos {
			latestTag := tags[repo.ID]

			if err = s.repo.UpsertLastSeen(ctx, repo.ID, latestTag); err != nil {
				return err
			}

			if shouldNotify(repo, latestTag) {
				if err = s.repo.InsertNotifications(ctx, repo.ID, latestTag); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, githubclient.ErrRateLimited) {
			s.m.rateLimitedTotal.Inc()
			s.m.ticksTotal.WithLabelValues("rate_limited").Inc()
			zerolog.Ctx(ctx).Warn().Err(err).Msg("github rate limited, skipping tick")
			return nil
		}
		s.m.ticksTotal.WithLabelValues("error").Inc()
		return err
	}
	s.m.ticksTotal.WithLabelValues("ok").Inc()
	return nil
}

// First time seeing a release — no notification.
func shouldNotify(repo domain.GitHubRepo, latestTag string) bool {
	return repo.LastSeenTag != nil && latestTag != *repo.LastSeenTag
}
