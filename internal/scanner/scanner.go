package scanner

//go:generate mockgen -source=scanner.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
)

// ScanResult describes one repo whose tag changed during a scan tick.
type ScanResult struct {
	RepoID      int64
	NewTag      string
	IsFirstScan bool // if true: record tag but do NOT create notifications
	BumpOnly    bool // if true: only bump last_checked_at, tag unchanged
}

// Repository is the storage contract for the scanner.
// RunInTx starts a transaction, SELECTs up to limit repos FOR UPDATE SKIP LOCKED,
// passes them to fn (with a tx-enriched context), then processes the returned
// ScanResults (inserts release_notifications + updates last_seen_tag), and commits.
// If fn returns an error the transaction is rolled back.
type Repository interface {
	RunInTx(ctx context.Context, limit int, fn func(ctx context.Context, repos []domain.GitHubRepo) ([]ScanResult, error)) error
}

// ReleaseProvider fetches the latest release tag for a batch of repos.
type ReleaseProvider interface {
	GetLatestReleases(ctx context.Context, p githubclient.GetLatestReleasesParams) (map[int64]string, error)
}

// Config holds Scanner dependencies.
type Config struct {
	Repo     Repository
	GitHub   ReleaseProvider
	Interval time.Duration
}

// Scanner periodically checks GitHub for new releases and writes outbox rows.
type Scanner struct {
	repo     Repository
	github   ReleaseProvider
	interval time.Duration
}

const scanLimit = 100

// New creates a Scanner from cfg.
func New(cfg Config) *Scanner {
	return &Scanner{repo: cfg.Repo, github: cfg.GitHub, interval: cfg.Interval}
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
			if err := s.Tick(ctx); err != nil {
				zerolog.Ctx(ctx).Error().Err(err).Msg("scanner tick failed")
			}
		}
	}
}

// Tick executes one scan cycle. Exported for testing.
func (s *Scanner) Tick(ctx context.Context) error {
	return s.repo.RunInTx(ctx, scanLimit, func(ctx context.Context, repos []domain.GitHubRepo) ([]ScanResult, error) {
		if len(repos) == 0 {
			return nil, nil
		}

		tags, err := s.github.GetLatestReleases(ctx, githubclient.GetLatestReleasesParams{Repos: repos})
		if err != nil {
			return nil, fmt.Errorf("get latest releases: %w", err)
		}

		var results []ScanResult
		for _, repo := range repos {
			latestTag, hasRelease := tags[repo.ID]
			if !hasRelease {
				// Repo has no releases yet — still bump last_checked_at.
				results = append(results, ScanResult{RepoID: repo.ID, BumpOnly: true})
				continue
			}
			if repo.LastSeenTag == nil {
				// First time we see a release for this repo — record it, no notification.
				results = append(results, ScanResult{RepoID: repo.ID, NewTag: latestTag, IsFirstScan: true})
				continue
			}
			if latestTag != *repo.LastSeenTag {
				results = append(results, ScanResult{RepoID: repo.ID, NewTag: latestTag, IsFirstScan: false})
				continue
			}
			// Tag unchanged — bump last_checked_at only.
			results = append(results, ScanResult{RepoID: repo.ID, BumpOnly: true})
		}
		return results, nil
	})
}
