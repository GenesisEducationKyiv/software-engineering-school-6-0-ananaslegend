package confirmer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/mock/gomock"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	"github.com/ananaslegend/reposeetory/internal/confirmer/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	txmocks "github.com/ananaslegend/reposeetory/internal/transactor/mocks"
	"github.com/stretchr/testify/require"
)

func newConfirmer(t *testing.T) (*confirmer.Confirmer, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockMailSender) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	c := confirmer.New(confirmer.Config{Tx: tx, Repo: repo, Mailer: m, BaseURL: "http://localhost:8080"})
	return c, tx, repo, m
}

// invokeWithinTransaction makes the mock call fn with the given context.
func invokeWithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

var testPending = confirmer.PendingConfirmation{
	ID:           1,
	Email:        "user@example.com",
	ConfirmToken: "tok-abc123",
	RepoOwner:    "golang",
	RepoName:     "go",
}

func TestConfirmer_FlushEmpty_NoMailer(t *testing.T) {
	c, tx, repo, _ := newConfirmer(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushOne_MailerCalled(t *testing.T) {
	c, tx, repo, m := newConfirmer(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]confirmer.PendingConfirmation{testPending}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), domain.SendConfirmationParams{
		To:           "user@example.com",
		ConfirmURL:   "http://localhost:8080/api/confirm/tok-abc123",
		RepoFullName: "golang/go",
	}).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), int64(1)).Return(nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushMailerError_NoMarkSentAndStops(t *testing.T) {
	c, tx, repo, m := newConfirmer(t)

	smtpErr := errors.New("smtp timeout")
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]confirmer.PendingConfirmation{testPending}, nil)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(smtpErr)
	// MarkSent must NOT be called on mailer error

	c.Flush(context.Background())
}

func TestConfirmer_FlushMultiple_ProcessedInOrder(t *testing.T) {
	c, tx, repo, m := newConfirmer(t)

	second := confirmer.PendingConfirmation{ID: 2, Email: "b@example.com", ConfirmToken: "tok-xyz", RepoOwner: "foo", RepoName: "bar"}

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]confirmer.PendingConfirmation{testPending}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]confirmer.PendingConfirmation{second}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	c.Flush(context.Background())
}

func newConfirmerWithRegistry(t *testing.T) (*confirmer.Confirmer, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockMailSender, *prometheus.Registry) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	reg := prometheus.NewRegistry()
	c := confirmer.New(confirmer.Config{Tx: tx, Repo: repo, Mailer: m, BaseURL: "http://localhost:8080", Registry: reg})
	return c, tx, repo, m, reg
}

func TestConfirmer_Flush_IncrementsEmailSentMetric(t *testing.T) {
	c, tx, repo, m, reg := newConfirmerWithRegistry(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]confirmer.PendingConfirmation{testPending}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil)

	c.Flush(context.Background())

	expected := strings.NewReader(`
		# HELP confirmer_emails_sent_total Total number of confirmation emails attempted.
		# TYPE confirmer_emails_sent_total counter
		confirmer_emails_sent_total{result="ok"} 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "confirmer_emails_sent_total"))
}
