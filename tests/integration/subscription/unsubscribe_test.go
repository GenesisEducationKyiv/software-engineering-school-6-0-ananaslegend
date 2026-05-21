//go:build integration

package subscription_test

import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *SubscriptionSuite) TestUnsubscribe_TokenNotFound() {
	resp := s.get("/api/unsubscribe/non-existent-token-abc123")
	defer resp.Body.Close()

	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(""))
	s.assertDeletedCounter(0)
}

func (s *SubscriptionSuite) TestUnsubscribe_LongRandomToken() {
	// 1024 chars — far above legitimate token length (~43 base64url chars),
	// well under any reasonable URL-length limit.
	token := strings.Repeat("a1b2c3d4", 128)
	require.Equal(s.T(), 1024, len(token))

	resp := s.get("/api/unsubscribe/" + token)
	defer resp.Body.Close()

	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	s.assertDeletedCounter(0)
}

func (s *SubscriptionSuite) TestUnsubscribe_HappyPath() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	subID := subs[0].ID

	repoID, ok := s.selectRepository("golang", "go")
	require.True(s.T(), ok)

	s.insertReleaseNotification(subID, repoID, "v1.0.0")
	require.Equal(s.T(), 1, s.countReleaseNotifications(subID))
	require.Equal(s.T(), 1, s.countConfirmationNotifications(subID))

	token := s.getUnsubscribeTokenForEmail(email)
	unsubResp := s.get("/api/unsubscribe/" + token)
	defer unsubResp.Body.Close()

	require.Equal(s.T(), http.StatusOK, unsubResp.StatusCode)
	assert.Contains(s.T(), unsubResp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(unsubResp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countConfirmationNotifications(subID), "CASCADE on confirmation_notifications.subscription_id")
	assert.Equal(s.T(), 0, s.countReleaseNotifications(subID), "CASCADE on release_notifications.subscription_id")

	s.assertDeletedCounter(1)
}

func (s *SubscriptionSuite) TestUnsubscribe_HappyPath_AfterConfirm() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	var confirmToken *string
	var unsubscribeToken string
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT confirm_token, unsubscribe_token FROM subscriptions WHERE email = $1`, email,
	).Scan(&confirmToken, &unsubscribeToken))
	require.NotNil(s.T(), confirmToken)
	require.NotEmpty(s.T(), *confirmToken)
	require.NotEmpty(s.T(), unsubscribeToken)

	confResp := s.get("/api/confirm/" + *confirmToken)
	require.Equal(s.T(), http.StatusOK, confResp.StatusCode)
	confResp.Body.Close()

	postConfirm := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), postConfirm, 1)
	require.NotNil(s.T(), postConfirm[0].ConfirmedAt)
	require.Nil(s.T(), postConfirm[0].ConfirmToken)
	require.Equal(s.T(), unsubscribeToken, postConfirm[0].UnsubscribeToken)

	unsubResp := s.get("/api/unsubscribe/" + unsubscribeToken)
	defer unsubResp.Body.Close()

	require.Equal(s.T(), http.StatusOK, unsubResp.StatusCode)
	assert.Contains(s.T(), unsubResp.Header.Get("Content-Type"), "text/html")

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	s.assertDeletedCounter(1)
}

func (s *SubscriptionSuite) TestUnsubscribe_Idempotency() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	token := s.getUnsubscribeTokenForEmail(email)

	r1 := s.get("/api/unsubscribe/" + token)
	require.Equal(s.T(), http.StatusOK, r1.StatusCode)
	r1.Body.Close()

	r2 := s.get("/api/unsubscribe/" + token)
	defer r2.Body.Close()
	require.Equal(s.T(), http.StatusNotFound, r2.StatusCode)
	assert.Contains(s.T(), r2.Header.Get("Content-Type"), "text/html")

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	s.assertDeletedCounter(1)
}

func (s *SubscriptionSuite) TestUnsubscribe_Concurrent() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	token := s.getUnsubscribeTokenForEmail(email)

	const N = 5
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes = make([]int, 0, N)
		ready = make(chan struct{})
	)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			r := s.get("/api/unsubscribe/" + token)
			mu.Lock()
			codes = append(codes, r.StatusCode)
			mu.Unlock()
			r.Body.Close()
		}()
	}
	close(ready)
	wg.Wait()

	var ok, notFound int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusNotFound:
			notFound++
		default:
			s.T().Fatalf("unexpected status code %d in concurrent batch: %v", c, codes)
		}
	}
	assert.Equal(s.T(), 1, ok, "exactly one DELETE should observe RowsAffected>0")
	assert.Equal(s.T(), N-1, notFound, "the remaining requests should see the row already gone")

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	s.assertDeletedCounter(1)
}
