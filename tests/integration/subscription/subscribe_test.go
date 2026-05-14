//go:build integration

package subscription_test

import (
	"net/http"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func (s *SubscriptionSuite) TestSubscribe_HappyPath() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})

	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	var body subhttp.StatusResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "pending_confirmation", body.Status)

	// Repository row
	repoID, ok := s.selectRepository("golang", "go")
	require.True(s.T(), ok, "repositories row missing")

	// Subscription row
	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	sub := subs[0]
	assert.Equal(s.T(), repoID, sub.RepositoryID)
	assert.Nil(s.T(), sub.ConfirmedAt)
	require.NotNil(s.T(), sub.ConfirmToken)
	assert.NotEmpty(s.T(), *sub.ConfirmToken)
	assert.NotEmpty(s.T(), sub.UnsubscribeToken)
	require.NotNil(s.T(), sub.ConfirmTokenExpiresAt)
	assert.True(s.T(), sub.ConfirmTokenExpiresAt.After(time.Now()))

	// Outbox row
	assert.Equal(s.T(), 1, s.countConfirmationNotifications(sub.ID))

	// GitHub was called once
	assert.Equal(s.T(), 1, s.githubFx.RequestCount())

	// Metric incremented
	s.assertCreatedCounter(1)
}

func (s *SubscriptionSuite) TestSubscribe_InvalidJSON() {
	resp := s.post("/api/subscribe", "not-json")
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "invalid JSON body", body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(""), "no subscription row should be created")
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 0, s.githubFx.RequestCount())
	s.assertCreatedCounter(0)
}

func (s *SubscriptionSuite) TestSubscribe_InvalidEmail() {
	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      "not-an-email",
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "invalid email", body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail("not-an-email"))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 0, s.githubFx.RequestCount(), "validation must short-circuit before reaching GitHub")
	s.assertCreatedCounter(0)
}

func (s *SubscriptionSuite) TestSubscribe_InvalidRepoFormat() {
	email := s.randomEmail()
	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "justonepart",
	})
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), domain.ErrInvalidRepoFormat.Error(), body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 0, s.githubFx.RequestCount())
	s.assertCreatedCounter(0)
}

func (s *SubscriptionSuite) TestSubscribe_RepoURL_Normalized() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "https://github.com/golang/go.git",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	_, ok := s.selectRepository("golang", "go")
	require.True(s.T(), ok, "repository should be stored as owner/name without prefix or .git")

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
}

func (s *SubscriptionSuite) TestSubscribe_RepoNotFoundOnGitHub() {
	s.githubFx.SetRepoExists("foo/bar", false)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "foo/bar",
	})
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), domain.ErrRepoNotFound.Error(), body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.Equal(s.T(), 1, s.githubFx.RequestCount())
	s.assertCreatedCounter(0)
}

func (s *SubscriptionSuite) TestSubscribe_DuplicateSubscription() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()
	req := subhttp.SubscribeRequest{Email: email, Repository: "golang/go"}

	// First subscribe: 202.
	resp1 := s.post("/api/subscribe", req)
	require.Equal(s.T(), http.StatusAccepted, resp1.StatusCode)
	resp1.Body.Close()

	// Second subscribe: 409.
	resp2 := s.post("/api/subscribe", req)
	require.Equal(s.T(), http.StatusConflict, resp2.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp2, &body)
	assert.Contains(s.T(), body.Error, domain.ErrAlreadyExists.Error())

	// Exactly one subscription row and one outbox row remain.
	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	assert.Equal(s.T(), 1, s.countConfirmationNotifications(subs[0].ID))
	assert.Equal(s.T(), 1, s.countAllConfirmationNotifications())

	// Counter only ticks on the successful subscribe.
	s.assertCreatedCounter(1)
}

func (s *SubscriptionSuite) TestSubscribe_GitHubError() {
	s.githubFx.ForceError()
	email := s.randomEmail()

	resp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusInternalServerError, resp.StatusCode)

	var body subhttp.ErrorResponse
	s.decodeJSON(resp, &body)
	assert.Equal(s.T(), "internal server error", body.Error)

	assert.Empty(s.T(), s.selectSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countAllConfirmationNotifications())
	assert.GreaterOrEqual(s.T(), s.githubFx.RequestCount(), 1)
	s.assertCreatedCounter(0)
}
