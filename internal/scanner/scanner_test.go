package scanner_test

import (
	"context"
	"errors"
	"testing"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	"github.com/ananaslegend/reposeetory/internal/scanner/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newScanner(t *testing.T) (*scanner.Scanner, *mocks.MockRepository, *mocks.MockReleaseProvider) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	gh := mocks.NewMockReleaseProvider(ctrl)
	s := scanner.New(scanner.Config{
		Repo:   repo,
		GitHub: gh,
	})
	return s, repo, gh
}

// invokeRunInTx is a helper that makes the mock RunInTx call fn with the given repos.
func invokeRunInTx(repos []domain.GitHubRepo) func(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
	return func(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
		_, err := fn(ctx, repos)
		return err
	}
}

func TestScanner_NoRepos_NoGitHubCall(t *testing.T) {
	s, repo, _ := newScanner(t)

	repo.EXPECT().RunInTx(gomock.Any(), 100, gomock.Any()).DoAndReturn(invokeRunInTx(nil))

	err := s.Tick(context.Background())
	require.NoError(t, err)
}

func TestScanner_FirstScan_IsFirstScanTrue(t *testing.T) {
	s, repo, gh := newScanner(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: nil}}

	var capturedResults []scanner.ScanResult
	repo.EXPECT().RunInTx(gomock.Any(), 100, gomock.Any()).DoAndReturn(
		func(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
			results, err := fn(ctx, repos)
			capturedResults = results
			return err
		},
	)
	gh.EXPECT().GetLatestReleases(gomock.Any(), githubclient.GetLatestReleasesParams{Repos: repos}).
		Return(map[int64]string{1: "v1.0.0"}, nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Len(t, capturedResults, 1)
	assert.Equal(t, int64(1), capturedResults[0].RepoID)
	assert.Equal(t, "v1.0.0", capturedResults[0].NewTag)
	assert.True(t, capturedResults[0].IsFirstScan)
}

func TestScanner_NewRelease_IsFirstScanFalse(t *testing.T) {
	s, repo, gh := newScanner(t)

	oldTag := "v1.0.0"
	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: &oldTag}}

	var capturedResults []scanner.ScanResult
	repo.EXPECT().RunInTx(gomock.Any(), 100, gomock.Any()).DoAndReturn(
		func(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
			results, err := fn(ctx, repos)
			capturedResults = results
			return err
		},
	)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{1: "v2.0.0"}, nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Len(t, capturedResults, 1)
	assert.Equal(t, "v2.0.0", capturedResults[0].NewTag)
	assert.False(t, capturedResults[0].IsFirstScan)
}

func TestScanner_NoChange_BumpsCheckedAt(t *testing.T) {
	s, repo, gh := newScanner(t)

	tag := "v1.0.0"
	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: &tag}}

	var capturedResults []scanner.ScanResult
	repo.EXPECT().RunInTx(gomock.Any(), 100, gomock.Any()).DoAndReturn(
		func(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
			results, err := fn(ctx, repos)
			capturedResults = results
			return err
		},
	)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{1: "v1.0.0"}, nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Len(t, capturedResults, 1)
	assert.Equal(t, int64(1), capturedResults[0].RepoID)
	assert.True(t, capturedResults[0].BumpOnly)
}

func TestScanner_GitHubError_PropagatesError(t *testing.T) {
	s, repo, gh := newScanner(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar"}}
	ghErr := errors.New("github unavailable")

	repo.EXPECT().RunInTx(gomock.Any(), 100, gomock.Any()).DoAndReturn(invokeRunInTx(repos))
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(nil, ghErr)

	err := s.Tick(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "get latest releases")
}

func TestScanner_NoRelease_BumpsCheckedAt(t *testing.T) {
	s, repo, gh := newScanner(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: nil}}

	var capturedResults []scanner.ScanResult
	repo.EXPECT().RunInTx(gomock.Any(), 100, gomock.Any()).DoAndReturn(
		func(ctx context.Context, limit int, fn func(context.Context, []domain.GitHubRepo) ([]scanner.ScanResult, error)) error {
			results, err := fn(ctx, repos)
			capturedResults = results
			return err
		},
	)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{}, nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)
	require.Len(t, capturedResults, 1)
	assert.Equal(t, int64(1), capturedResults[0].RepoID)
	assert.True(t, capturedResults[0].BumpOnly)
}
