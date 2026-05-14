//go:build integration

package subscription_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func (s *SubscriptionSuite) TestConfirm_HappyPath() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	resp := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	sub := subs[0]
	require.NotNil(s.T(), sub.ConfirmedAt)
	assert.True(s.T(), sub.ConfirmedAt.After(time.Now().Add(-time.Minute)),
		"confirmed_at should be recent: %v", sub.ConfirmedAt)
	assert.Nil(s.T(), sub.ConfirmToken, "confirm_token must be cleared after confirmation")
	assert.Nil(s.T(), sub.ConfirmTokenExpiresAt, "confirm_token_expires_at must be cleared after confirmation")

	s.assertConfirmedCounter(1)
}

func (s *SubscriptionSuite) TestConfirm_TokenNotFound() {
	resp := s.get("/api/confirm/non-existent-token-abc123")
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	s.assertConfirmedCounter(0)
}

func (s *SubscriptionSuite) TestConfirm_TokenExpired() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	require.NotNil(s.T(), subs[0].ConfirmToken)
	token := *subs[0].ConfirmToken
	subID := subs[0].ID

	s.setConfirmTokenExpired(subID, time.Now().Add(-time.Hour))

	resp := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusGone, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	after := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), after, 1)
	assert.Nil(s.T(), after[0].ConfirmedAt, "confirmed_at must remain NULL when token is expired")
	require.NotNil(s.T(), after[0].ConfirmToken, "confirm_token must NOT be cleared when token is expired")
	assert.Equal(s.T(), token, *after[0].ConfirmToken)

	s.assertConfirmedCounter(0)
}

func (s *SubscriptionSuite) TestConfirm_TokenIsConsumed() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	resp1 := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusOK, resp1.StatusCode)
	resp1.Body.Close()

	// MarkConfirmed sets confirm_token = NULL, so the same URL must now return 404.
	resp2 := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusNotFound, resp2.StatusCode)
	assert.Contains(s.T(), resp2.Header.Get("Content-Type"), "text/html")
	resp2.Body.Close()

	s.assertConfirmedCounter(1)
}

func (s *SubscriptionSuite) TestConfirm_StateIdempotentOnRepeat() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	resp1 := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusOK, resp1.StatusCode)
	resp1.Body.Close()

	// Snapshot the terminal DB state right after the only successful confirm.
	beforeRepeat := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), beforeRepeat, 1)

	// Two more identical requests must not mutate the row, even though they 404.
	for i := 0; i < 2; i++ {
		resp := s.get("/api/confirm/" + token)
		require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	}

	afterRepeat := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), afterRepeat, 1)
	assert.Equal(s.T(), beforeRepeat[0], afterRepeat[0],
		"DB row must be byte-identical after repeated already-consumed confirm calls")

	s.assertConfirmedCounter(1)
}

func (s *SubscriptionSuite) TestConfirm_Concurrent() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	const concurrency = 5
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		statuses []int
	)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			resp := s.get("/api/confirm/" + token)
			resp.Body.Close()
			mu.Lock()
			statuses = append(statuses, resp.StatusCode)
			mu.Unlock()
		}()
	}
	wg.Wait()

	require.Len(s.T(), statuses, concurrency)
	successes := 0
	for _, st := range statuses {
		switch st {
		case http.StatusOK:
			successes++
		case http.StatusNotFound:
			// expected once the token has been consumed by a sibling goroutine
		default:
			s.T().Fatalf("unexpected status code: %d (statuses=%v)", st, statuses)
		}
	}
	assert.GreaterOrEqual(s.T(), successes, 1, "at least one concurrent request must succeed")

	after := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), after, 1)
	assert.NotNil(s.T(), after[0].ConfirmedAt, "confirmed_at must be set after concurrent confirm")
	assert.Nil(s.T(), after[0].ConfirmToken, "confirm_token must be cleared after concurrent confirm")

	// Counter is incremented once per successful service.Confirm — by construction
	// it should match the number of HTTP 200 responses we observed.
	s.assertCounter("subscriptions_confirmed_total", float64(successes))
}

func (s *SubscriptionSuite) TestConfirm_LongRandomToken() {
	// 1024 hex characters — far above legitimate token length (64 hex),
	// safely under any URL-length limit. Asserts the route does not panic
	// or 500 on pathological path parameters.
	token := strings.Repeat("abcdef0123456789", 64)
	require.Len(s.T(), token, 1024)

	resp := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	s.assertConfirmedCounter(0)
}
