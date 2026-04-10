package notifier_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ananaslegend/reposeetory/internal/notifier"
	"github.com/ananaslegend/reposeetory/internal/notifier/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newNotifier(t *testing.T) (*notifier.Notifier, *mocks.MockRepository, *mocks.MockMailSender) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	n := notifier.New(notifier.Config{Repo: repo, Mailer: m})
	return n, repo, m
}

var testPending = notifier.PendingNotification{
	ID:               42,
	Email:            "user@example.com",
	RepoOwner:        "golang",
	RepoName:         "go",
	ReleaseTag:       "go1.22.0",
	UnsubscribeToken: "tok-abc123",
}

// invokeProcessNext makes the mock call fn with the given notification.
func invokeProcessNext(n notifier.PendingNotification, returnProcessed bool) func(ctx context.Context, fn func(context.Context, notifier.PendingNotification) error) (bool, error) {
	return func(ctx context.Context, fn func(context.Context, notifier.PendingNotification) error) (bool, error) {
		if err := fn(ctx, n); err != nil {
			return false, err
		}
		return returnProcessed, nil
	}
}

func TestNotifier_FlushEmpty_NoMailer(t *testing.T) {
	n, repo, _ := newNotifier(t)

	repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil)

	n.Flush(context.Background())
}

func TestNotifier_FlushOneNotification_MailerCalled(t *testing.T) {
	n, repo, m := newNotifier(t)

	gomock.InOrder(
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(testPending, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil),
	)
	m.EXPECT().SendRelease(gomock.Any(), domain.SendReleaseParams{
		To:             "user@example.com",
		RepoFullName:   "golang/go",
		ReleaseTag:     "go1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/go1.22.0",
		UnsubscribeURL: "/api/unsubscribe/tok-abc123",
	}).Return(nil)

	n.Flush(context.Background())
}

func TestNotifier_FlushMailerError_RollsBackAndStops(t *testing.T) {
	n, repo, m := newNotifier(t)

	smtpErr := errors.New("smtp timeout")
	repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context, notifier.PendingNotification) error) (bool, error) {
			err := fn(ctx, testPending)
			require.Error(t, err)
			return false, err
		},
	)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(smtpErr)

	n.Flush(context.Background())
}

func TestNotifier_FlushMultipleNotifications_ProcessedInOrder(t *testing.T) {
	n, repo, m := newNotifier(t)

	second := notifier.PendingNotification{ID: 43, Email: "b@example.com", RepoOwner: "foo", RepoName: "bar", ReleaseTag: "v2.0"}

	gomock.InOrder(
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(testPending, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(second, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil),
	)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	n.Flush(context.Background())
}

// suppress unused import warning
var _ = assert.New
