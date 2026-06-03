package scanner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/mock/gomock"

	txmocks "github.com/ananaslegend/reposeetory/pkg/transactor/mocks"

	"github.com/stretchr/testify/require"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
	"github.com/ananaslegend/reposeetory/internal/scanner"
	"github.com/ananaslegend/reposeetory/internal/scanner/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func newScanner(t *testing.T) (*scanner.Scanner, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockReleaseProvider) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	gh := mocks.NewMockReleaseProvider(ctrl)
	s := scanner.New(scanner.Config{
		Tx:     tx,
		Repo:   repo,
		GitHub: gh,
		RED:    redmetrics.New(redmetrics.Config{Subsystem: "scanner"}),
	})
	return s, tx, repo, gh
}

// invokeWithinTransaction makes the mock call fn with the given context.
func invokeWithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func TestScanner_NoRepos_NoGitHubCall(t *testing.T) {
	s, tx, repo, _ := newScanner(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(nil, nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)
}

func TestScanner_FirstScan_UpsertOnlyNoNotification(t *testing.T) {
	s, tx, repo, gh := newScanner(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: nil}}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), githubclient.GetLatestReleasesParams{Repos: repos}).
		Return(map[int64]string{1: "v1.0.0"}, nil)
	repo.EXPECT().UpsertLastSeen(gomock.Any(), int64(1), "v1.0.0").Return(nil)
	// InsertNotifications must NOT be called on first scan

	err := s.Tick(context.Background())
	require.NoError(t, err)
}

func TestScanner_NewRelease_InsertsNotificationsAndUpserts(t *testing.T) {
	s, tx, repo, gh := newScanner(t)

	oldTag := "v1.0.0"
	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: &oldTag}}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{1: "v2.0.0"}, nil)
	repo.EXPECT().InsertNotifications(gomock.Any(), int64(1), "v2.0.0").Return(nil)
	repo.EXPECT().UpsertLastSeen(gomock.Any(), int64(1), "v2.0.0").Return(nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)
}

func TestScanner_NoChange_UpsertOnly(t *testing.T) {
	s, tx, repo, gh := newScanner(t)

	tag := "v1.0.0"
	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: &tag}}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{1: "v1.0.0"}, nil)
	repo.EXPECT().UpsertLastSeen(gomock.Any(), int64(1), "v1.0.0").Return(nil)
	// InsertNotifications must NOT be called — tag unchanged

	err := s.Tick(context.Background())
	require.NoError(t, err)
}

func TestScanner_NoRelease_UpsertWithEmptyTag(t *testing.T) {
	s, tx, repo, gh := newScanner(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: nil}}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{}, nil)
	repo.EXPECT().UpsertLastSeen(gomock.Any(), int64(1), "").Return(nil)
	// InsertNotifications must NOT be called — LastSeenTag is nil (first scan)

	err := s.Tick(context.Background())
	require.NoError(t, err)
}

func TestScanner_GitHubError_PropagatesError(t *testing.T) {
	s, tx, repo, gh := newScanner(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar"}}
	ghErr := errors.New("github unavailable")

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(nil, ghErr)

	err := s.Tick(context.Background())
	require.ErrorContains(t, err, "get latest releases")
}

func newScannerWithRegistry(t *testing.T) (*scanner.Scanner, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockReleaseProvider, *prometheus.Registry) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	gh := mocks.NewMockReleaseProvider(ctrl)
	reg := prometheus.NewRegistry()
	s := scanner.New(scanner.Config{
		Tx:       tx,
		Repo:     repo,
		GitHub:   gh,
		Registry: reg,
		RED:      redmetrics.New(redmetrics.Config{Subsystem: "scanner", Registry: reg}),
	})
	return s, tx, repo, gh, reg
}

func TestScanner_Tick_IncrementsMetrics(t *testing.T) {
	s, tx, repo, gh, reg := newScannerWithRegistry(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: nil}}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{1: "v1.0.0"}, nil)
	repo.EXPECT().UpsertLastSeen(gomock.Any(), int64(1), "v1.0.0").Return(nil)

	err := s.Tick(context.Background())
	require.NoError(t, err)

	expected := strings.NewReader(`
		# HELP scanner_repos_scanned_total Total number of repositories processed by the scanner.
		# TYPE scanner_repos_scanned_total counter
		scanner_repos_scanned_total 1
		# HELP scanner_requests_total Total number of scanner operations.
		# TYPE scanner_requests_total counter
		scanner_requests_total{result="ok"} 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected,
		"scanner_repos_scanned_total",
		"scanner_requests_total",
	))
}

func TestScanner_TickRecordsRED(t *testing.T) {
	s, tx, repo, gh, reg := newScannerWithRegistry(t)

	repos := []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar", LastSeenTag: nil}}

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetRepositoriesWithLock(gomock.Any(), 100).Return(repos, nil)
	gh.EXPECT().GetLatestReleases(gomock.Any(), gomock.Any()).Return(map[int64]string{1: "v1.0.0"}, nil)
	repo.EXPECT().UpsertLastSeen(gomock.Any(), int64(1), "v1.0.0").Return(nil)

	err := s.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if !hasMetric(families, "scanner_requests_total") {
		t.Fatalf("expected scanner_requests_total in registry")
	}
	if !hasMetric(families, "scanner_request_duration_seconds") {
		t.Fatalf("expected scanner_request_duration_seconds in registry")
	}
}

func hasMetric(families []*dto.MetricFamily, name string) bool {
	for _, f := range families {
		if f.GetName() == name {
			return true
		}
	}
	return false
}
