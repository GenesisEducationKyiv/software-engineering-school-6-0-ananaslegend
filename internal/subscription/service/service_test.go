package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/mock/gomock"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	"github.com/ananaslegend/reposeetory/internal/subscription/service/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSvc(t *testing.T) (*service.Service, *mocks.MockRepository, *mocks.MockRemoteRepositoryProvider) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	gh := mocks.NewMockRemoteRepositoryProvider(ctrl)
	svc := service.New(service.Config{
		Repo:            repo,
		GitHub:          gh,
		AppBaseURL:      "http://localhost:8080",
		ConfirmTokenTTL: 24 * time.Hour,
	})
	return svc, repo, gh
}

var validSubscribeParams = domain.SubscribeParams{
	Email:      "vasya@example.com",
	Repository: "golang/go",
}

// --- Subscribe ---

func TestSubscribe_HappyPath(t *testing.T) {
	svc, repo, gh := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), domain.RepoExistsParams{Owner: "golang", Name: "go"}).Return(true, nil)
	repo.EXPECT().UpsertRepo(gomock.Any(), domain.UpsertRepoParams{Owner: "golang", Name: "go"}).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	require.NoError(t, err)
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	svc, _, _ := newSvc(t)

	err := svc.Subscribe(context.Background(), domain.SubscribeParams{Email: "vasya@example.com", Repository: "not-a-repo"})
	assert.ErrorIs(t, err, domain.ErrInvalidRepoFormat)
}

func TestSubscribe_FullRepoURL(t *testing.T) {
	urls := []string{
		"https://github.com/golang/go",
		"https://github.com/golang/go.git",
		"http://github.com/golang/go",
		"https://gitlab.com/golang/go",
		"https://gitlab.com/golang/go.git",
	}
	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			svc, repo, gh := newSvc(t)
			gh.EXPECT().RepoExists(gomock.Any(), domain.RepoExistsParams{Owner: "golang", Name: "go"}).Return(true, nil)
			repo.EXPECT().UpsertRepo(gomock.Any(), domain.UpsertRepoParams{Owner: "golang", Name: "go"}).Return(int64(1), nil)
			repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)

			err := svc.Subscribe(context.Background(), domain.SubscribeParams{Email: "vasya@example.com", Repository: url})
			require.NoError(t, err)
		})
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	svc, _, gh := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(false, nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	assert.ErrorIs(t, err, domain.ErrRepoNotFound)
}

func TestSubscribe_AlreadyExists(t *testing.T) {
	svc, repo, gh := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().UpsertRepo(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(nil, domain.ErrAlreadyExists)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

// --- Confirm ---

func TestConfirm_HappyPath(t *testing.T) {
	svc, repo, _ := newSvc(t)

	exp := time.Now().Add(time.Hour)
	token := "validtoken"
	repo.EXPECT().GetByConfirmToken(gomock.Any(), token).
		Return(&domain.Subscription{ID: 1, ConfirmToken: &token, ConfirmTokenExpiresAt: &exp}, nil)
	repo.EXPECT().MarkConfirmed(gomock.Any(), gomock.Any()).Return(nil)

	err := svc.Confirm(context.Background(), token)
	require.NoError(t, err)
}

func TestConfirm_TokenNotFound(t *testing.T) {
	svc, repo, _ := newSvc(t)

	repo.EXPECT().GetByConfirmToken(gomock.Any(), "nosuchtoken").Return(nil, domain.ErrTokenNotFound)

	err := svc.Confirm(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

func TestConfirm_TokenExpired(t *testing.T) {
	svc, repo, _ := newSvc(t)

	past := time.Now().Add(-time.Hour)
	token := "expiredtoken"
	repo.EXPECT().GetByConfirmToken(gomock.Any(), token).
		Return(&domain.Subscription{ID: 1, ConfirmToken: &token, ConfirmTokenExpiresAt: &past}, nil)

	err := svc.Confirm(context.Background(), token)
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
}

// --- Unsubscribe ---

func TestUnsubscribe_HappyPath(t *testing.T) {
	svc, repo, _ := newSvc(t)

	repo.EXPECT().DeleteByUnsubscribeToken(gomock.Any(), "sometoken").Return(true, nil)

	err := svc.Unsubscribe(context.Background(), "sometoken")
	require.NoError(t, err)
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	svc, repo, _ := newSvc(t)

	repo.EXPECT().DeleteByUnsubscribeToken(gomock.Any(), "nosuchtoken").Return(false, nil)

	err := svc.Unsubscribe(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

// --- Metrics ---

func newSvcWithRegistry(t *testing.T) (*service.Service, *mocks.MockRepository, *mocks.MockRemoteRepositoryProvider, *prometheus.Registry) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	gh := mocks.NewMockRemoteRepositoryProvider(ctrl)
	reg := prometheus.NewRegistry()
	svc := service.New(service.Config{
		Repo:            repo,
		GitHub:          gh,
		AppBaseURL:      "http://localhost:8080",
		ConfirmTokenTTL: 24 * time.Hour,
		Registry:        reg,
	})
	return svc, repo, gh, reg
}

func TestService_Subscribe_IncrementsCreatedCounter(t *testing.T) {
	svc, repo, gh, reg := newSvcWithRegistry(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().UpsertRepo(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	require.NoError(t, err)

	expected := strings.NewReader(`
		# HELP subscriptions_created_total Total number of subscriptions created.
		# TYPE subscriptions_created_total counter
		subscriptions_created_total 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "subscriptions_created_total"))
}
