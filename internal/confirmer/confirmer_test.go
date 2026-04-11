package confirmer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	"github.com/ananaslegend/reposeetory/internal/confirmer/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newConfirmer(t *testing.T) (*confirmer.Confirmer, *mocks.MockRepository, *mocks.MockMailSender) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	c := confirmer.New(confirmer.Config{Repo: repo, Mailer: m, BaseURL: "http://localhost:8080"})
	return c, repo, m
}

var testPending = confirmer.PendingConfirmation{
	ID:           1,
	Email:        "user@example.com",
	ConfirmToken: "tok-abc123",
	RepoOwner:    "golang",
	RepoName:     "go",
}

// invokeProcessNext makes the mock call fn with the given confirmation.
func invokeProcessNext(p confirmer.PendingConfirmation, returnProcessed bool) func(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
	return func(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
		if err := fn(ctx, p); err != nil {
			return false, err
		}
		return returnProcessed, nil
	}
}

func TestConfirmer_FlushEmpty_NoMailer(t *testing.T) {
	c, repo, _ := newConfirmer(t)

	repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushOne_MailerCalled(t *testing.T) {
	c, repo, m := newConfirmer(t)

	gomock.InOrder(
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(testPending, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), domain.SendConfirmationParams{
		To:           "user@example.com",
		ConfirmURL:   "http://localhost:8080/api/confirm/tok-abc123",
		RepoFullName: "golang/go",
	}).Return(nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushMailerError_RollsBackAndStops(t *testing.T) {
	c, repo, m := newConfirmer(t)

	smtpErr := errors.New("smtp timeout")
	repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
			err := fn(ctx, testPending)
			require.Error(t, err)
			return false, err
		},
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(smtpErr)

	c.Flush(context.Background())
}

func TestConfirmer_FlushMultiple_ProcessedInOrder(t *testing.T) {
	c, repo, m := newConfirmer(t)

	second := confirmer.PendingConfirmation{ID: 2, Email: "b@example.com", ConfirmToken: "tok-xyz", RepoOwner: "foo", RepoName: "bar"}

	gomock.InOrder(
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(testPending, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(second, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	c.Flush(context.Background())
}

// suppress unused import warning
var _ = assert.New
