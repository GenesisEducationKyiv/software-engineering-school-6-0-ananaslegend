package notifier_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/mock/gomock"

	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/ananaslegend/reposeetory/internal/notifier/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	txmocks "github.com/ananaslegend/reposeetory/internal/transactor/mocks"
	"github.com/stretchr/testify/require"
)

func newNotifier(t *testing.T) (*notifier.Notifier, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockMailSender) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	n := notifier.New(notifier.Config{Tx: tx, Repo: repo, Mailer: m})
	return n, tx, repo, m
}

// invokeWithinTransaction makes the mock call fn with the given context.
func invokeWithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

var testPending = notifier.PendingNotification{
	ID:               42,
	Email:            "user@example.com",
	RepoOwner:        "golang",
	RepoName:         "go",
	ReleaseTag:       "go1.22.0",
	UnsubscribeToken: "tok-abc123",
}

func TestNotifier_FlushEmpty_NoMailer(t *testing.T) {
	n, tx, repo, _ := newNotifier(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil)

	n.Flush(context.Background())
}

func TestNotifier_FlushOneNotification_MailerCalled(t *testing.T) {
	n, tx, repo, m := newNotifier(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return([]notifier.PendingNotification{testPending}, nil),
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendRelease(gomock.Any(), domain.SendReleaseParams{
		To:             "user@example.com",
		RepoFullName:   "golang/go",
		ReleaseTag:     "go1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/go1.22.0",
		UnsubscribeURL: "/api/unsubscribe/tok-abc123",
	}).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), int64(42)).Return(nil)

	n.Flush(context.Background())
}

func TestNotifier_FlushMailerError_NoMarkSentAndStops(t *testing.T) {
	n, tx, repo, m := newNotifier(t)

	smtpErr := errors.New("smtp timeout")
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return([]notifier.PendingNotification{testPending}, nil)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(smtpErr)
	// MarkSent must NOT be called on mailer error

	n.Flush(context.Background())
}

func TestNotifier_FlushMultipleNotifications_ProcessedInOrder(t *testing.T) {
	n, tx, repo, m := newNotifier(t)

	second := notifier.PendingNotification{ID: 43, Email: "b@example.com", RepoOwner: "foo", RepoName: "bar", ReleaseTag: "v2.0"}

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return([]notifier.PendingNotification{testPending}, nil),
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return([]notifier.PendingNotification{second}, nil),
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	n.Flush(context.Background())
}

func newNotifierWithRegistry(t *testing.T) (*notifier.Notifier, *txmocks.MockTransactor, *mocks.MockRepository, *mocks.MockMailSender, *prometheus.Registry) {
	t.Helper()
	ctrl := gomock.NewController(t)
	tx := txmocks.NewMockTransactor(ctrl)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	reg := prometheus.NewRegistry()
	n := notifier.New(notifier.Config{Tx: tx, Repo: repo, Mailer: m, Registry: reg})
	return n, tx, repo, m, reg
}

func TestNotifier_Flush_IncrementsEmailSentMetric(t *testing.T) {
	n, tx, repo, m, reg := newNotifierWithRegistry(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return([]notifier.PendingNotification{testPending}, nil),
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil)

	n.Flush(context.Background())

	expected := strings.NewReader(`
		# HELP notifier_emails_sent_total Total number of release emails attempted.
		# TYPE notifier_emails_sent_total counter
		notifier_emails_sent_total{result="ok"} 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "notifier_emails_sent_total"))
}
