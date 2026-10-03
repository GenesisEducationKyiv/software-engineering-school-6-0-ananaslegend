//go:build integration

package subscription_test

import (
	"net/http"
	"net/url"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func (s *SubscriptionSuite) TestListByEmail_HappyPath_SingleConfirmedSubscription() {
	email := s.randomEmail()
	confirmedAt := time.Now().UTC().Truncate(time.Microsecond)
	createdAt := confirmedAt.Add(-time.Hour)
	s.seedConfirmedSubscription(email, "golang", "go", createdAt, confirmedAt)

	resp := s.get("/api/subscriptions?email=" + url.QueryEscape(email))
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	var body subhttp.SubscriptionsResponse
	s.decodeJSON(resp, &body)

	require.Len(s.T(), body.Subscriptions, 1)
	item := body.Subscriptions[0]
	assert.Equal(s.T(), "golang/go", item.Repository)
	require.NotNil(s.T(), item.ConfirmedAt)
	assert.WithinDuration(s.T(), confirmedAt, *item.ConfirmedAt, time.Second)
	assert.WithinDuration(s.T(), createdAt, item.CreatedAt, time.Second)
}

func (s *SubscriptionSuite) TestListByEmail_OrdersByCreatedAtDesc() {
	email := s.randomEmail()
	now := time.Now().UTC().Truncate(time.Microsecond)

	// Insert out of order so we know the ordering comes from SQL, not insertion sequence.
	s.seedConfirmedSubscription(email, "alpha", "first", now.Add(-3*time.Hour), now)
	s.seedConfirmedSubscription(email, "beta", "second", now.Add(-1*time.Hour), now)
	s.seedConfirmedSubscription(email, "gamma", "third", now.Add(-2*time.Hour), now)

	resp := s.get("/api/subscriptions?email=" + url.QueryEscape(email))
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	var body subhttp.SubscriptionsResponse
	s.decodeJSON(resp, &body)

	require.Len(s.T(), body.Subscriptions, 3)
	assert.Equal(s.T(), "beta/second", body.Subscriptions[0].Repository, "most recent first")
	assert.Equal(s.T(), "gamma/third", body.Subscriptions[1].Repository)
	assert.Equal(s.T(), "alpha/first", body.Subscriptions[2].Repository, "oldest last")
}

func (s *SubscriptionSuite) TestListByEmail_EmptyForUnknownEmail() {
	email := s.randomEmail()

	resp := s.get("/api/subscriptions?email=" + url.QueryEscape(email))
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	var body subhttp.SubscriptionsResponse
	s.decodeJSON(resp, &body)
	assert.Empty(s.T(), body.Subscriptions)
}

func (s *SubscriptionSuite) TestListByEmail_ExcludesPendingSubscriptions() {
	email := s.randomEmail()
	now := time.Now().UTC().Truncate(time.Microsecond)

	s.seedConfirmedSubscription(email, "golang", "go", now.Add(-time.Hour), now)
	s.seedPendingSubscription(email, "rust-lang", "rust", now)

	resp := s.get("/api/subscriptions?email=" + url.QueryEscape(email))
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	var body subhttp.SubscriptionsResponse
	s.decodeJSON(resp, &body)
	require.Len(s.T(), body.Subscriptions, 1)
	assert.Equal(s.T(), "golang/go", body.Subscriptions[0].Repository)
}

func (s *SubscriptionSuite) TestListByEmail_FiltersByEmail() {
	emailA := s.randomEmail()
	emailB := s.randomEmail()
	now := time.Now().UTC().Truncate(time.Microsecond)

	s.seedConfirmedSubscription(emailA, "golang", "go", now, now)
	s.seedConfirmedSubscription(emailB, "rust-lang", "rust", now, now)

	resp := s.get("/api/subscriptions?email=" + url.QueryEscape(emailA))
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	var body subhttp.SubscriptionsResponse
	s.decodeJSON(resp, &body)
	require.Len(s.T(), body.Subscriptions, 1)
	assert.Equal(s.T(), "golang/go", body.Subscriptions[0].Repository)
}

func (s *SubscriptionSuite) TestListByEmail_InvalidEmail() {
	resp := s.get("/api/subscriptions?email=not-an-email")
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "invalid email", body.Error)
}

func (s *SubscriptionSuite) TestListByEmail_MissingEmail() {
	resp := s.get("/api/subscriptions")
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "invalid email", body.Error)
}
