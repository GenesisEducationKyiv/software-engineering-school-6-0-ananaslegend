//go:build integration

package crons_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ananaslegend/reposeetory/tests/internal"
	"github.com/brianvoe/gofakeit/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/internal/notifier"
	notifrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

// NotifierSuite drives the release-notification outbox drainer
// (internal/notifier.Notifier) against real Postgres plus an in-memory spy
// mailer. The spy is deliberate: it lets these tests inject mailer errors
// deterministically and assert on the constructed URLs / params without
// parsing rendered email bodies — properties Mailpit (used by CronsSuite for
// the confirmer) cannot give us cheaply.
type NotifierSuite struct {
	suite.Suite

	ctx    context.Context
	cancel context.CancelFunc

	pg *internal.Postgres

	registry *prometheus.Registry
	mailer   *spyMailer
	tx       *transactor.PgxTransactor
	repo     *notifrepo.Repository
	notifier *notifier.Notifier
}

func TestNotifierSuite(t *testing.T) {
	suite.Run(t, new(NotifierSuite))
}

func (s *NotifierSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.pg = internal.NewPostgres(s.ctx, s.T())
	gofakeit.Seed(0)
}

func (s *NotifierSuite) TearDownSuite() {
	s.cancel()
}

func (s *NotifierSuite) SetupTest() {
	s.pg.Truncate(s.ctx, s.T())

	s.registry = prometheus.NewRegistry()
	s.mailer = newSpyMailer()
	s.tx = transactor.New(s.pg.Pool)
	s.repo = notifrepo.New(s.pg.Pool)
	s.notifier = notifier.New(notifier.Config{
		Tx:       s.tx,
		Repo:     s.repo,
		Mailer:   s.mailer,
		Interval: time.Hour, // unused — tests drive Flush() directly
		BaseURL:  cronsTestBaseURL,
		Registry: s.registry,
	})
}

// --- Spy mailer ---

// spyMailer captures every SendRelease call. The mutex serialises capture so
// concurrent drainers race only on the product code, not on the recorder.
type spyMailer struct {
	mu     sync.Mutex
	sent   []domain.SendReleaseParams
	err    error
	onCall func()
}

func newSpyMailer() *spyMailer { return &spyMailer{} }

func (m *spyMailer) SendRelease(_ context.Context, p domain.SendReleaseParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, p)
	if m.onCall != nil {
		m.onCall()
	}
	return m.err
}

func (m *spyMailer) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *spyMailer) calls() []domain.SendReleaseParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.SendReleaseParams, len(m.sent))
	copy(out, m.sent)
	return out
}

func (m *spyMailer) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// --- DB seed helpers ---

// seedConfirmedSubscription inserts a confirmed subscription plus its repository
// (upsert). Returns the subscription id, repository id, and the unsubscribe
// token used for URL assertions.
func (s *NotifierSuite) seedConfirmedSubscription(email, owner, name string) (subID, repoID int64, unsubToken string) {
	s.T().Helper()

	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO repositories (owner, name)
		VALUES ($1, $2)
		ON CONFLICT (owner, name) DO UPDATE SET owner = EXCLUDED.owner
		RETURNING id
	`, owner, name).Scan(&repoID))

	unsubToken = gofakeit.UUID()
	now := time.Now().UTC()
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO subscriptions
			(email, repository_id, confirm_token, confirm_token_expires_at,
			 unsubscribe_token, confirmed_at, created_at)
		VALUES ($1, $2, NULL, NULL, $3, $4, $4)
		RETURNING id
	`, email, repoID, unsubToken, now).Scan(&subID))

	return subID, repoID, unsubToken
}

// enqueueReleaseNotification inserts a pending row with a custom created_at,
// returning its id. createdAt drives FIFO ordering tests.
func (s *NotifierSuite) enqueueReleaseNotification(subID, repoID int64, tag string, createdAt time.Time) int64 {
	s.T().Helper()
	var id int64
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO release_notifications (subscription_id, repository_id, release_tag, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, subID, repoID, tag, createdAt).Scan(&id))
	return id
}

// enqueueSentReleaseNotification inserts an already-sent row to verify the
// drainer skips it.
func (s *NotifierSuite) enqueueSentReleaseNotification(subID, repoID int64, tag string, sentAt time.Time) int64 {
	s.T().Helper()
	var id int64
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx, `
		INSERT INTO release_notifications (subscription_id, repository_id, release_tag, sent_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, subID, repoID, tag, sentAt).Scan(&id))
	return id
}

func (s *NotifierSuite) getReleaseNotificationSentAt(id int64) *time.Time {
	s.T().Helper()
	var sentAt *time.Time
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT sent_at FROM release_notifications WHERE id = $1`, id,
	).Scan(&sentAt))
	return sentAt
}

func (s *NotifierSuite) countPendingReleaseNotifications() int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM release_notifications WHERE sent_at IS NULL`,
	).Scan(&n))
	return n
}

// --- Metrics ---

// assertCounter delegates to the shared requireCounter helper.
func (s *NotifierSuite) assertCounter(name string, want map[string]string, expected float64) {
	s.T().Helper()
	requireCounter(s.T(), s.registry, name, want, expected)
}
