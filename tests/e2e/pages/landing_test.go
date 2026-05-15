//go:build e2e

package pages_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// TestLandingForm_InvalidRepoFormatShowsBlurHint covers the inline client-side
// validator on the repository input. The handler fires on blur — no backend
// call. Without this test the JS regex could silently rot.
func (s *PagesSuite) TestLandingForm_InvalidRepoFormatShowsBlurHint() {
	t := s.T()
	page := s.browser.NewPage(t)
	_, err := page.Goto(s.app.Server.URL)
	require.NoError(t, err)

	require.NoError(t, page.GetByTestId("repo-input").Fill("axios"))
	// Tab moves focus to the next form element and fires `blur` on repo-input.
	require.NoError(t, page.GetByTestId("repo-input").Press("Tab"))

	requireErrorText(t, page, "Format: owner/repository")
}

// TestLandingForm_RepoNotFoundShowsServerError covers the 404 branch of the
// JS handler in landing.html: when /api/subscribe responds 404, the inline
// script must write the localized server-error message.
func (s *PagesSuite) TestLandingForm_RepoNotFoundShowsServerError() {
	t := s.T()
	const (
		repo  = "nope/missing"
		email = "u@test.local"
	)
	s.ghREST.SetRepoExists(repo, false) // explicit 404

	page := s.browser.NewPage(t)
	_, err := page.Goto(s.app.Server.URL)
	require.NoError(t, err)

	require.NoError(t, page.GetByTestId("repo-input").Fill(repo))
	require.NoError(t, page.GetByTestId("email-input").Fill(email))
	require.NoError(t, page.GetByTestId("submit-button").Click())

	requireErrorText(t, page, "Repository not found on GitHub.")
}

// TestLandingForm_DuplicateSubscriptionShowsServerError covers the 409 branch
// of the JS handler. We pre-create a subscription via the API and then submit
// the form for the same email/repo from the browser; the second call must
// produce 409 and the JS must surface the "already subscribed" message.
func (s *PagesSuite) TestLandingForm_DuplicateSubscriptionShowsServerError() {
	t := s.T()
	const (
		repo  = "dup/test"
		email = "dup@test.local"
	)
	s.ghREST.SetRepoExists(repo, true)

	body, err := json.Marshal(map[string]string{"repository": repo, "email": email})
	require.NoError(t, err)
	resp, err := s.app.Client.Post(s.app.Server.URL+"/api/subscribe",
		"application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "seed subscription must succeed")

	page := s.browser.NewPage(t)
	_, err = page.Goto(s.app.Server.URL)
	require.NoError(t, err)

	require.NoError(t, page.GetByTestId("repo-input").Fill(repo))
	require.NoError(t, page.GetByTestId("email-input").Fill(email))
	require.NoError(t, page.GetByTestId("submit-button").Click())

	requireErrorText(t, page, "This email is already subscribed to that repository.")
}

// requireErrorText polls #error-message until it contains the expected
// string. The landing-page JS sets textContent asynchronously after fetch,
// so a synchronous read would race with the network round-trip.
func requireErrorText(t *testing.T, page playwright.Page, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, err := page.GetByTestId("error-message").TextContent()
		return err == nil && got == want
	}, 5*time.Second, 50*time.Millisecond, "want error-message=%q", want)
}
