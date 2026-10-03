package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	"github.com/ananaslegend/reposeetory/internal/subscription/service/mocks"
	txmocks "github.com/ananaslegend/reposeetory/pkg/transactor/mocks"
)

// invokeWithinTransaction makes the mock transactor call fn synchronously with the same context.
func invokeWithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func newSvc(t *testing.T) (*service.Service, *mocks.MockRepository, *mocks.MockConfirmator, *mocks.MockRemoteRepositoryProvider, *txmocks.MockTransactor) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	confirms := mocks.NewMockConfirmator(ctrl)
	gh := mocks.NewMockRemoteRepositoryProvider(ctrl)
	tx := txmocks.NewMockTransactor(ctrl)
	svc := service.New(service.Config{
		Tx:              tx,
		Repo:            repo,
		Confirms:        confirms,
		GitHub:          gh,
		AppBaseURL:      "http://localhost:8080",
		ConfirmTokenTTL: 24 * time.Hour,
	})
	return svc, repo, confirms, gh, tx
}

var validSubscribeParams = domain.SubscribeParams{
	Email:      "vasya@example.com",
	Repository: "golang/go",
}

// --- Subscribe ---

func TestSubscribe_HappyPath(t *testing.T) {
	svc, repo, confirms, gh, tx := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), domain.RepoExistsParams{Owner: "golang", Name: "go"}).Return(true, nil)
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().SaveRepo(gomock.Any(), domain.UpsertRepoParams{Owner: "golang", Name: "go"}).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)
	confirms.EXPECT().CreateConfirmation(gomock.Any(), int64(1)).Return(nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	require.NoError(t, err)
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)

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
			svc, repo, confirms, gh, tx := newSvc(t)
			gh.EXPECT().RepoExists(gomock.Any(), domain.RepoExistsParams{Owner: "golang", Name: "go"}).Return(true, nil)
			tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
			repo.EXPECT().SaveRepo(gomock.Any(), domain.UpsertRepoParams{Owner: "golang", Name: "go"}).Return(int64(1), nil)
			repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)
			confirms.EXPECT().CreateConfirmation(gomock.Any(), int64(1)).Return(nil)

			err := svc.Subscribe(context.Background(), domain.SubscribeParams{Email: "vasya@example.com", Repository: url})
			require.NoError(t, err)
		})
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	svc, _, _, gh, _ := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(false, nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	assert.ErrorIs(t, err, domain.ErrRepoNotFound)
}

func TestSubscribe_AlreadyExists(t *testing.T) {
	svc, repo, _, gh, tx := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(true, nil)
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().SaveRepo(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(nil, domain.ErrAlreadyExists)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

// --- Confirm ---

func TestConfirm_HappyPath(t *testing.T) {
	svc, repo, _, _, _ := newSvc(t)

	exp := time.Now().Add(time.Hour)
	token := "validtoken"
	repo.EXPECT().GetByConfirmToken(gomock.Any(), token).
		Return(&domain.Subscription{ID: 1, ConfirmToken: &token, ConfirmTokenExpiresAt: &exp}, nil)
	repo.EXPECT().MarkConfirmed(gomock.Any(), gomock.Any()).Return(nil)

	err := svc.Confirm(context.Background(), token)
	require.NoError(t, err)
}

func TestConfirm_TokenNotFound(t *testing.T) {
	svc, repo, _, _, _ := newSvc(t)

	repo.EXPECT().GetByConfirmToken(gomock.Any(), "nosuchtoken").Return(nil, domain.ErrTokenNotFound)

	err := svc.Confirm(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

func TestConfirm_TokenExpired(t *testing.T) {
	svc, repo, _, _, _ := newSvc(t)

	past := time.Now().Add(-time.Hour)
	token := "expiredtoken"
	repo.EXPECT().GetByConfirmToken(gomock.Any(), token).
		Return(&domain.Subscription{ID: 1, ConfirmToken: &token, ConfirmTokenExpiresAt: &past}, nil)

	err := svc.Confirm(context.Background(), token)
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
}

// --- Unsubscribe ---

func TestUnsubscribe_HappyPath(t *testing.T) {
	svc, repo, _, _, _ := newSvc(t)

	repo.EXPECT().DeleteByUnsubscribeToken(gomock.Any(), "sometoken").Return(true, nil)

	err := svc.Unsubscribe(context.Background(), "sometoken")
	require.NoError(t, err)
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	svc, repo, _, _, _ := newSvc(t)

	repo.EXPECT().DeleteByUnsubscribeToken(gomock.Any(), "nosuchtoken").Return(false, nil)

	err := svc.Unsubscribe(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

// --- Metrics ---

func newSvcWithRegistry(t *testing.T) (*service.Service, *mocks.MockRepository, *mocks.MockConfirmator, *mocks.MockRemoteRepositoryProvider, *txmocks.MockTransactor, *prometheus.Registry) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	confirms := mocks.NewMockConfirmator(ctrl)
	gh := mocks.NewMockRemoteRepositoryProvider(ctrl)
	tx := txmocks.NewMockTransactor(ctrl)
	reg := prometheus.NewRegistry()
	svc := service.New(service.Config{
		Tx:              tx,
		Repo:            repo,
		Confirms:        confirms,
		GitHub:          gh,
		AppBaseURL:      "http://localhost:8080",
		ConfirmTokenTTL: 24 * time.Hour,
		Registry:        reg,
	})
	return svc, repo, confirms, gh, tx, reg
}

func TestService_Subscribe_IncrementsCreatedCounter(t *testing.T) {
	svc, repo, confirms, gh, tx, reg := newSvcWithRegistry(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(true, nil)
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().SaveRepo(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)
	confirms.EXPECT().CreateConfirmation(gomock.Any(), int64(1)).Return(nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	require.NoError(t, err)

	expected := strings.NewReader(`
		# HELP subscriptions_created_total Total number of subscriptions created.
		# TYPE subscriptions_created_total counter
		subscriptions_created_total 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "subscriptions_created_total"))
}
