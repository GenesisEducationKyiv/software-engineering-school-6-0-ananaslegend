//go:build integration

package crons_test

import (
	"errors"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/ananaslegend/reposeetory/internal/notifier"
	notifrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

const metricNotifierEmailsSent = "notifier_emails_sent_total"

func (s *NotifierSuite) TestFlush_HappyPath_SingleRow_SendsEmailAndMarksSent() {
	email := "user@example.com"
	subID, repoID, unsubToken := s.seedConfirmedSubscription(email, "golang", "go")
	rowID := s.enqueueReleaseNotification(subID, repoID, "v1.2.3", time.Now().UTC())

	s.notifier.Flush(s.ctx)

	calls := s.mailer.calls()
	require.Len(s.T(), calls, 1)
	got := calls[0]
	assert.Equal(s.T(), email, got.To)
	assert.Equal(s.T(), "golang/go", got.RepoFullName)
	assert.Equal(s.T(), "v1.2.3", got.ReleaseTag)
	assert.Equal(s.T(), "https://github.com/golang/go/releases/tag/v1.2.3", got.ReleaseURL)
	assert.Equal(s.T(), cronsTestBaseURL+"/api/unsubscribe/"+unsubToken, got.UnsubscribeURL)

	assert.NotNil(s.T(), s.getReleaseNotificationSentAt(rowID), "row must be marked sent")
	assert.Equal(s.T(), 0, s.countPendingReleaseNotifications())

	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "ok"}, 1)
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "error"}, 0)
}

func (s *NotifierSuite) TestFlush_Empty_NoMailerCalls_NoMetricChange() {
	s.notifier.Flush(s.ctx)

	assert.Equal(s.T(), 0, s.mailer.callCount())
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "ok"}, 0)
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "error"}, 0)
}

func (s *NotifierSuite) TestFlush_MultipleRows_AllProcessedInFIFOOrder() {
	subID, repoID, _ := s.seedConfirmedSubscription("fifo@example.com", "golang", "go")

	now := time.Now().UTC()
	idOld := s.enqueueReleaseNotification(subID, repoID, "t-old", now.Add(-3*time.Minute))
	idMid := s.enqueueReleaseNotification(subID, repoID, "t-mid", now.Add(-2*time.Minute))
	idNew := s.enqueueReleaseNotification(subID, repoID, "t-new", now.Add(-1*time.Minute))

	s.notifier.Flush(s.ctx)

	calls := s.mailer.calls()
	require.Len(s.T(), calls, 3)
	tags := []string{calls[0].ReleaseTag, calls[1].ReleaseTag, calls[2].ReleaseTag}
	assert.Equal(s.T(), []string{"t-old", "t-mid", "t-new"}, tags, "rows drained in FIFO order")

	for _, id := range []int64{idOld, idMid, idNew} {
		assert.NotNil(s.T(), s.getReleaseNotificationSentAt(id), "row %d must be marked sent", id)
	}
	assert.Equal(s.T(), 0, s.countPendingReleaseNotifications())
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "ok"}, 3)
}

func (s *NotifierSuite) TestFlush_MailerError_RowRemainsPending_LoopStops() {
	subID, repoID, _ := s.seedConfirmedSubscription("err@example.com", "rust-lang", "rust")
	rowID := s.enqueueReleaseNotification(subID, repoID, "v1.0", time.Now().UTC())

	s.mailer.setError(errors.New("smtp fail"))
	s.notifier.Flush(s.ctx)

	assert.Equal(s.T(), 1, s.mailer.callCount())
	assert.Nil(s.T(), s.getReleaseNotificationSentAt(rowID), "row must remain pending after mailer error")
	assert.Equal(s.T(), 1, s.countPendingReleaseNotifications())
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "error"}, 1)
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "ok"}, 0)

	// Row must not be lost: clearing the error and retrying must drain it.
	s.mailer.setError(nil)
	s.notifier.Flush(s.ctx)

	assert.Equal(s.T(), 2, s.mailer.callCount())
	assert.NotNil(s.T(), s.getReleaseNotificationSentAt(rowID))
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "ok"}, 1)
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "error"}, 1)
}

func (s *NotifierSuite) TestFlush_AlreadySent_RowSkipped() {
	subID, repoID, _ := s.seedConfirmedSubscription("skip@example.com", "kubernetes", "kubernetes")

	pastSentAt := time.Now().UTC().Add(-time.Hour)
	sentRowID := s.enqueueSentReleaseNotification(subID, repoID, "v0.9", pastSentAt)
	pendingRowID := s.enqueueReleaseNotification(subID, repoID, "v1.0", time.Now().UTC())

	s.notifier.Flush(s.ctx)

	calls := s.mailer.calls()
	require.Len(s.T(), calls, 1)
	assert.Equal(s.T(), "v1.0", calls[0].ReleaseTag, "only the pending row must be sent")

	// Already-sent row's sent_at must not be touched (still ~pastSentAt).
	got := s.getReleaseNotificationSentAt(sentRowID)
	require.NotNil(s.T(), got)
	assert.WithinDuration(s.T(), pastSentAt, *got, time.Second)

	assert.NotNil(s.T(), s.getReleaseNotificationSentAt(pendingRowID))
}

func (s *NotifierSuite) TestFlush_Idempotent_SecondRunIsNoOp() {
	subID, repoID, _ := s.seedConfirmedSubscription("idem@example.com", "torvalds", "linux")
	s.enqueueReleaseNotification(subID, repoID, "v6.10", time.Now().UTC())

	s.notifier.Flush(s.ctx)
	s.notifier.Flush(s.ctx)

	assert.Equal(s.T(), 1, s.mailer.callCount(), "second Flush must be a no-op")
	s.assertCounter(metricNotifierEmailsSent, map[string]string{"result": "ok"}, 1)
}

func (s *NotifierSuite) TestFlush_ConcurrentDrainers_NoDuplicateSendsViaSkipLocked() {
	subID, repoID, _ := s.seedConfirmedSubscription("race@example.com", "facebook", "react")

	now := time.Now().UTC()
	s.enqueueReleaseNotification(subID, repoID, "r-1", now.Add(-2*time.Minute))
	s.enqueueReleaseNotification(subID, repoID, "r-2", now.Add(-1*time.Minute))

	// Second drainer shares pool and mailer but owns a fresh transactor —
	// enough to race two distinct DB transactions on the same outbox.
	// Registry is nil because notifierMetrics.MustRegister rejects the
	// duplicate collector; the property under test (no duplicate sends) is
	// observed via the mailer + DB state, not metrics.
	tx2 := transactor.New(s.pg.Pool)
	repo2 := notifrepo.New(s.pg.Pool)
	n2 := notifier.New(notifier.Config{
		Tx:       tx2,
		Repo:     repo2,
		Mailer:   s.mailer,
		Interval: time.Hour,
		BaseURL:  cronsTestBaseURL,
	})

	g, gctx := errgroup.WithContext(s.ctx)
	g.Go(func() error { s.notifier.Flush(gctx); return nil })
	g.Go(func() error { n2.Flush(gctx); return nil })
	require.NoError(s.T(), g.Wait())

	calls := s.mailer.calls()
	require.Len(s.T(), calls, 2, "each row must be delivered exactly once")

	gotTags := map[string]bool{calls[0].ReleaseTag: true, calls[1].ReleaseTag: true}
	assert.True(s.T(), gotTags["r-1"] && gotTags["r-2"], "both rows must be drained, no duplicates: %v", gotTags)

	assert.Equal(s.T(), 0, s.countPendingReleaseNotifications())
	// Metric assertion is deliberately omitted: n2 uses a nil registry to
	// avoid duplicate-collector panic, so only s.notifier's increments land
	// in s.registry. The "no duplicate sends" contract is fully covered by
	// the mailer + DB state assertions above.
}
