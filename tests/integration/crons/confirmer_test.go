//go:build integration

package crons_test

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
)

// mailpitWait is the max time we give Mailpit to index a freshly-delivered
// message before asserting on it. DialAndSend returns once the SMTP server
// has accepted the message, but Mailpit's index update is a tick behind.
const mailpitWait = 3 * time.Second

func (s *CronsSuite) TestConfirmer_Flush_Empty_NoEmails() {
	s.confirmer.Flush(s.ctx)

	assert.Empty(s.T(), s.mailpit.Messages(s.ctx, s.T()))
	assert.Equal(s.T(), 0, s.countSentNotifications())
	s.assertConfirmerCounter("ok", 0)
	s.assertConfirmerCounter("error", 0)
}

func (s *CronsSuite) TestConfirmer_Flush_One_SendsAndMarksSent() {
	email := s.randomEmail()
	_, notifID := s.seedPendingNotification(email, "golang", "go", "tok-happy", time.Now())

	s.confirmer.Flush(s.ctx)

	msgs := s.mailpit.WaitForMessages(s.ctx, s.T(), 1, mailpitWait)
	require.Len(s.T(), msgs, 1)
	assert.Equal(s.T(), email, msgs[0].To[0].Address)
	assert.Contains(s.T(), msgs[0].Subject, "golang/go")

	sentAt := s.notificationSentAt(notifID)
	require.NotNil(s.T(), sentAt, "sent_at must be populated after a successful send")
	assert.WithinDuration(s.T(), time.Now(), *sentAt, time.Minute)
	assert.Equal(s.T(), 1, s.countSentNotifications())
	s.assertConfirmerCounter("ok", 1)
	s.assertConfirmerCounter("error", 0)
}

func (s *CronsSuite) TestConfirmer_Flush_Multiple_AllSent() {
	now := time.Now()
	_, n1 := s.seedPendingNotification(s.randomEmail(), "alpha", "one", "tok-1", now.Add(-3*time.Second))
	_, n2 := s.seedPendingNotification(s.randomEmail(), "beta", "two", "tok-2", now.Add(-2*time.Second))
	_, n3 := s.seedPendingNotification(s.randomEmail(), "gamma", "three", "tok-3", now.Add(-time.Second))

	s.confirmer.Flush(s.ctx)

	msgs := s.mailpit.WaitForMessages(s.ctx, s.T(), 3, mailpitWait)
	require.Len(s.T(), msgs, 3)
	assert.Equal(s.T(), 3, s.countSentNotifications())
	for _, id := range []int64{n1, n2, n3} {
		require.NotNil(s.T(), s.notificationSentAt(id), "notification %d should be marked sent", id)
	}
	s.assertConfirmerCounter("ok", 3)
}

func (s *CronsSuite) TestConfirmer_Flush_SkipsAlreadySent() {
	now := time.Now()
	_, firstNotif := s.seedPendingNotification(s.randomEmail(), "owner1", "repo1", "tok-skip-1", now.Add(-time.Hour))
	prevSentAt := now.Add(-30 * time.Minute)
	s.markSent(firstNotif, prevSentAt)

	_, secondNotif := s.seedPendingNotification(s.randomEmail(), "owner2", "repo2", "tok-skip-2", now)

	s.confirmer.Flush(s.ctx)

	msgs := s.mailpit.WaitForMessages(s.ctx, s.T(), 1, mailpitWait)
	require.Len(s.T(), msgs, 1, "only the still-pending notification should be sent")

	firstSentAt := s.notificationSentAt(firstNotif)
	require.NotNil(s.T(), firstSentAt)
	assert.WithinDuration(s.T(), prevSentAt, *firstSentAt, time.Second,
		"pre-existing sent_at must not be overwritten")

	require.NotNil(s.T(), s.notificationSentAt(secondNotif))
	s.assertConfirmerCounter("ok", 1)
}

func (s *CronsSuite) TestConfirmer_Flush_SkipsWhenTokenCleared() {
	subID, notifID := s.seedPendingNotification(s.randomEmail(), "golang", "go", "tok-cleared", time.Now())
	// Simulate the user clicking the confirm link before the cron picks up
	// the outbox row: confirm_token becomes NULL, so the JOIN predicate
	// `s.confirm_token IS NOT NULL` must filter this row out.
	s.markSubscriptionConfirmed(subID)

	s.confirmer.Flush(s.ctx)

	assert.Empty(s.T(), s.mailpit.Messages(s.ctx, s.T()),
		"row whose confirm_token is NULL must not produce an email")
	assert.Nil(s.T(), s.notificationSentAt(notifID), "sent_at must remain NULL")
	s.assertConfirmerCounter("ok", 0)
}

func (s *CronsSuite) TestConfirmer_Flush_ProcessingOrder_ByCreatedAt() {
	// Insert in shuffled order; created_at is intentionally NOT monotonic with id.
	now := time.Now()
	_, _ = s.seedPendingNotification("c@example.test", "gh", "c-repo", "tok-c", now.Add(-1*time.Second))
	_, _ = s.seedPendingNotification("a@example.test", "gh", "a-repo", "tok-a", now.Add(-3*time.Second))
	_, _ = s.seedPendingNotification("b@example.test", "gh", "b-repo", "tok-b", now.Add(-2*time.Second))

	s.confirmer.Flush(s.ctx)

	msgs := s.mailpit.WaitForMessages(s.ctx, s.T(), 3, mailpitWait)
	require.Len(s.T(), msgs, 3)

	// Mailpit returns newest-first; the oldest created_at must have been sent
	// first, so it lands at the END of the list.
	received := []string{
		msgs[2].To[0].Address,
		msgs[1].To[0].Address,
		msgs[0].To[0].Address,
	}
	assert.Equal(s.T(), []string{"a@example.test", "b@example.test", "c@example.test"}, received,
		"oldest created_at must be drained first")
}

func (s *CronsSuite) TestConfirmer_Flush_ConfirmURL_Composition() {
	token := "abc123-deterministic"
	_, _ = s.seedPendingNotification(s.randomEmail(), "golang", "go", token, time.Now())

	s.confirmer.Flush(s.ctx)

	msgs := s.mailpit.WaitForMessages(s.ctx, s.T(), 1, mailpitWait)
	require.Len(s.T(), msgs, 1)

	source := s.mailpit.MessageSource(s.ctx, s.T(), msgs[0].ID)
	wantURL := cronsTestBaseURL + "/api/confirm/" + token
	assert.Contains(s.T(), source, wantURL, "email body must embed the composed confirm URL")
	assert.Contains(s.T(), source, "golang/go", "email body must mention repository full name")
}

func (s *CronsSuite) TestConfirmer_Flush_MailerError_NoMarkSent() {
	// Swap the SMTP mailer for one pointing at a port that accepts TCP
	// connections but immediately closes them, so the SMTP handshake fails
	// deterministically. (A bind-close trick on the same port would race
	// with the kernel reassigning the port to another listener.)
	deadPort := unreachableSMTPPort(s.T())
	failing, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
		Host:      "127.0.0.1",
		Port:      deadPort,
		From:      cronsTestFromAddr,
		TLSPolicy: "none",
	})
	require.NoError(s.T(), err)
	s.useMailer(failing)

	_, notifID := s.seedPendingNotification(s.randomEmail(), "golang", "go", "tok-err", time.Now())

	s.confirmer.Flush(s.ctx)

	assert.Empty(s.T(), s.mailpit.Messages(s.ctx, s.T()), "no message must reach mailpit")
	assert.Nil(s.T(), s.notificationSentAt(notifID),
		"sent_at must remain NULL after a mailer error so the row stays eligible")
	s.assertConfirmerCounter("ok", 0)
	s.assertConfirmerCounter("error", 1)
}

func (s *CronsSuite) TestConfirmer_Flush_SkipLocked_ConcurrentDrain() {
	_, notifID := s.seedPendingNotification(s.randomEmail(), "golang", "go", "tok-concurrent", time.Now())

	// Two goroutines share the same Confirmer (and therefore the same Repository,
	// which uses the suite's pool). Each goroutine's Flush opens its own pgx
	// transaction; FOR UPDATE OF cn SKIP LOCKED in GetConfirmationsWithLock
	// guarantees only one of them sees the row, the other observes an empty
	// result and exits the inner loop without sending.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.confirmer.Flush(s.ctx) }()
	go func() { defer wg.Done(); s.confirmer.Flush(s.ctx) }()
	wg.Wait()

	msgs := s.mailpit.WaitForMessages(s.ctx, s.T(), 1, mailpitWait)
	require.Len(s.T(), msgs, 1, "FOR UPDATE SKIP LOCKED must deliver exactly one email")
	require.NotNil(s.T(), s.notificationSentAt(notifID))
	assert.Equal(s.T(), 1, s.countSentNotifications())
	s.assertConfirmerCounter("ok", 1)
	s.assertConfirmerCounter("error", 0)
}

// --- helpers private to this file ---

func (s *CronsSuite) randomEmail() string {
	s.T().Helper()
	return strings.ToLower(gofakeit.Email())
}

// unreachableSMTPPort binds a loopback TCP listener and starts an accept
// loop that closes every connection immediately. The returned port number
// always accepts TCP but never speaks SMTP, so go-mail's client gets an
// EOF during the protocol handshake and returns an error. This avoids the
// race the previous bind-then-close helper had, where the kernel could
// reassign the port to another listener between Close and dial.
func unreachableSMTPPort(t testing.TB) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}
