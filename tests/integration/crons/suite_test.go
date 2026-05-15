//go:build integration

package crons_test

import (
	"context"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	confirmerrepo "github.com/ananaslegend/reposeetory/internal/confirmer/repository"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
	"github.com/ananaslegend/reposeetory/pkg/transactor"

	"github.com/ananaslegend/reposeetory/tests/integration/internal"
)

const (
	cronsTestBaseURL  = "http://test.local"
	cronsTestFromAddr = "no-reply@reposeetory.test"
)

// CronsSuite drives the background cron workers against real infrastructure:
// a Postgres container plus a Mailpit container in place of an SMTP server.
//
// The HTTP router is intentionally NOT mounted — these tests exercise the
// outbox-drainer code paths directly via *Confirmer.Flush.
type CronsSuite struct {
	suite.Suite

	ctx    context.Context
	cancel context.CancelFunc

	pg      *internal.Postgres
	mailpit *internal.Mailpit

	registry  *prometheus.Registry
	txr       *transactor.PgxTransactor
	mailer    *emailer.SMTPMailer
	confirmer *confirmer.Confirmer
}

func TestCronsSuite(t *testing.T) {
	suite.Run(t, new(CronsSuite))
}

func (s *CronsSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.pg = internal.NewPostgres(s.ctx, s.T())
	s.mailpit = internal.NewMailpit(s.ctx, s.T())
	gofakeit.Seed(0)
}

func (s *CronsSuite) SetupTest() {
	s.pg.Truncate(s.ctx, s.T())
	s.mailpit.Reset(s.ctx, s.T())
	s.registry = prometheus.NewRegistry()
	s.txr = transactor.New(s.pg.Pool)
	s.mailer = s.newSMTPMailer(s.mailpit.SMTPHost, s.mailpit.SMTPPort)
	s.confirmer = confirmer.New(confirmer.Config{
		Tx:       s.txr,
		Repo:     confirmerrepo.New(s.pg.Pool),
		Mailer:   s.mailer,
		BaseURL:  cronsTestBaseURL,
		Registry: s.registry,
	})
}

func (s *CronsSuite) TearDownSuite() {
	s.cancel()
}

func (s *CronsSuite) newSMTPMailer(host string, port int) *emailer.SMTPMailer {
	s.T().Helper()
	m, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
		Host:      host,
		Port:      port,
		From:      cronsTestFromAddr,
		TLSPolicy: "none",
	})
	require.NoError(s.T(), err, "build smtp mailer for mailpit")
	return m
}

// --- DB helpers ---

// seedPendingNotification inserts a repository, a pending (unconfirmed)
// subscription, and a confirmation_notifications row pointing at it. Returns
// the IDs so the test can assert on row state after a Flush.
//
// The optional createdAt is used for the notification row only — the
// subscription row is timestamped "now" because the confirmer doesn't sort on
// the subscription's created_at.
func (s *CronsSuite) seedPendingNotification(email, owner, name, token string, createdAt time.Time) (subID, notifID int64) {
	s.T().Helper()

	var repoID int64
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO repositories (owner, name)
		VALUES ($1, $2)
		ON CONFLICT (owner, name) DO UPDATE SET owner = EXCLUDED.owner
		RETURNING id
	`, owner, name).Scan(&repoID))

	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO subscriptions
			(email, repository_id, confirm_token, confirm_token_expires_at,
			 unsubscribe_token, confirmed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, NULL, NOW())
		RETURNING id
	`, email, repoID, token, time.Now().Add(24*time.Hour), gofakeit.UUID()).Scan(&subID))

	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO confirmation_notifications (subscription_id, created_at)
		VALUES ($1, $2)
		RETURNING id
	`, subID, createdAt).Scan(&notifID))
	return subID, notifID
}

func (s *CronsSuite) markSent(notifID int64, when time.Time) {
	s.T().Helper()
	_, err := s.pg.Pool.Exec(s.ctx,
		`UPDATE confirmation_notifications SET sent_at = $1 WHERE id = $2`, when, notifID)
	require.NoError(s.T(), err)
}

// markSubscriptionConfirmed simulates a subscriber clicking the confirm link
// AFTER the outbox row was already enqueued: confirm_token becomes NULL and
// confirmed_at is set, satisfying subscriptions_confirm_state_check.
func (s *CronsSuite) markSubscriptionConfirmed(subID int64) {
	s.T().Helper()
	_, err := s.pg.Pool.Exec(s.ctx, `
		UPDATE subscriptions
		SET confirm_token = NULL,
		    confirm_token_expires_at = NULL,
		    confirmed_at = NOW()
		WHERE id = $1
	`, subID)
	require.NoError(s.T(), err)
}

// useMailer swaps the suite's mailer and rebuilds the confirmer on a fresh
// Prometheus registry so subsequent assertions see only the new confirmer's
// metrics. Used by tests that need a non-default mailer (e.g. a failing one).
func (s *CronsSuite) useMailer(m confirmer.MailSender) {
	s.T().Helper()
	s.registry = prometheus.NewRegistry()
	s.confirmer = confirmer.New(confirmer.Config{
		Tx:       s.txr,
		Repo:     confirmerrepo.New(s.pg.Pool),
		Mailer:   m,
		BaseURL:  cronsTestBaseURL,
		Registry: s.registry,
	})
}

func (s *CronsSuite) notificationSentAt(notifID int64) *time.Time {
	s.T().Helper()
	var sentAt *time.Time
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT sent_at FROM confirmation_notifications WHERE id = $1`, notifID,
	).Scan(&sentAt))
	return sentAt
}

func (s *CronsSuite) countSentNotifications() int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM confirmation_notifications WHERE sent_at IS NOT NULL`,
	).Scan(&n))
	return n
}

// --- Metrics ---

// assertConfirmerCounter sums every observed sample of
// confirmer_emails_sent_total whose `result` label equals label.
func (s *CronsSuite) assertConfirmerCounter(label string, want float64) {
	s.T().Helper()
	mf, err := s.registry.Gather()
	require.NoError(s.T(), err)
	var got float64
	for _, m := range mf {
		if m.GetName() != "confirmer_emails_sent_total" {
			continue
		}
		for _, metric := range m.GetMetric() {
			if !labelEquals(metric.GetLabel(), "result", label) {
				continue
			}
			got += metric.GetCounter().GetValue()
		}
	}
	require.Equal(s.T(), want, got, "confirmer_emails_sent_total{result=%q}", label)
}

// labelEquals reports whether pairs contain a {name=value} entry.
func labelEquals(pairs []*dto.LabelPair, name, value string) bool {
	for _, p := range pairs {
		if p.GetName() == name {
			return p.GetValue() == value
		}
	}
	return false
}
