//go:build integration

package subscription_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/tests/integration/internal"
)

type SubscriptionSuite struct {
	suite.Suite

	ctx    context.Context
	cancel context.CancelFunc

	pg       *internal.Postgres
	githubFx *internal.GitHubFixture
	app      *internal.App
}

func TestSubscriptionSuite(t *testing.T) {
	suite.Run(t, new(SubscriptionSuite))
}

func (s *SubscriptionSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.pg = internal.NewPostgres(s.ctx, s.T())
	s.githubFx = internal.NewGitHubFixture(s.T())
	gofakeit.Seed(0) // 0 = non-deterministic seed each run
}

func (s *SubscriptionSuite) SetupTest() {
	s.pg.Truncate(s.ctx, s.T())
	s.githubFx.Reset()
	s.app = internal.NewApp(s.T(), internal.AppConfig{
		Pool:            s.pg.Pool,
		GitHubBaseURL:   s.githubFx.URL(),
		GitHubToken:     "test-token",
		AppBaseURL:      "http://test.local",
		ConfirmTokenTTL: 24 * time.Hour,
	})
}

func (s *SubscriptionSuite) TearDownSuite() {
	s.cancel()
}

// --- HTTP helpers ---

func (s *SubscriptionSuite) post(path string, body any) *http.Response {
	s.T().Helper()
	var bodyReader io.Reader
	switch b := body.(type) {
	case string:
		bodyReader = strings.NewReader(b)
	case nil:
		bodyReader = nil
	default:
		buf, err := json.Marshal(b)
		require.NoError(s.T(), err)
		bodyReader = strings.NewReader(string(buf))
	}

	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.app.Server.URL+path, bodyReader)
	require.NoError(s.T(), err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.app.Client.Do(req)
	require.NoError(s.T(), err)
	return resp
}

func (s *SubscriptionSuite) decodeJSON(resp *http.Response, v any) {
	s.T().Helper()
	defer resp.Body.Close()
	require.Contains(s.T(), resp.Header.Get("Content-Type"), "application/json")
	require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(v))
}

func (s *SubscriptionSuite) randomEmail() string {
	return gofakeit.Email()
}

// --- DB helpers ---

type dbSubscription struct {
	ID                    int64
	Email                 string
	RepositoryID          int64
	ConfirmedAt           *time.Time
	ConfirmToken          *string
	ConfirmTokenExpiresAt *time.Time
	UnsubscribeToken      string
}

func (s *SubscriptionSuite) selectSubscriptionsByEmail(email string) []dbSubscription {
	s.T().Helper()
	rows, err := s.pg.Pool.Query(s.ctx, `
		SELECT id, email, repository_id, confirmed_at, confirm_token, confirm_token_expires_at, unsubscribe_token
		FROM subscriptions
		WHERE email = $1
		ORDER BY id
	`, email)
	require.NoError(s.T(), err)
	defer rows.Close()

	var out []dbSubscription
	for rows.Next() {
		var sub dbSubscription
		require.NoError(s.T(), rows.Scan(
			&sub.ID, &sub.Email, &sub.RepositoryID,
			&sub.ConfirmedAt, &sub.ConfirmToken, &sub.ConfirmTokenExpiresAt,
			&sub.UnsubscribeToken,
		))
		out = append(out, sub)
	}
	require.NoError(s.T(), rows.Err())
	return out
}

func (s *SubscriptionSuite) countConfirmationNotifications(subID int64) int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM confirmation_notifications WHERE subscription_id = $1`,
		subID,
	).Scan(&n))
	return n
}

func (s *SubscriptionSuite) countAllConfirmationNotifications() int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM confirmation_notifications`,
	).Scan(&n))
	return n
}

func (s *SubscriptionSuite) selectRepository(owner, name string) (int64, bool) {
	s.T().Helper()
	var id int64
	err := s.pg.Pool.QueryRow(s.ctx,
		`SELECT id FROM repositories WHERE owner = $1 AND name = $2`, owner, name,
	).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}

// --- Metrics ---

// assertCreatedCounter sums every observed subscriptions_created_total sample
// in the suite's registry. Works whether or not the counter has been touched
// (testutil.GatherAndCompare requires the metric to be present, which is not
// guaranteed in error-path tests where service.Subscribe never runs).
func (s *SubscriptionSuite) assertCreatedCounter(want float64) {
	s.T().Helper()
	mf, err := s.app.Registry.Gather()
	require.NoError(s.T(), err)
	var got float64
	for _, m := range mf {
		if m.GetName() != "subscriptions_created_total" {
			continue
		}
		for _, metric := range m.GetMetric() {
			got += metric.GetCounter().GetValue()
		}
	}
	require.Equal(s.T(), want, got, "subscriptions_created_total")
}
