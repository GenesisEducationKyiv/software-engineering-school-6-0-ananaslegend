//go:build e2e

package pages_test

import (
	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// TestUnavailable_BrokenConfirmTokenRendersPage hits /api/confirm/{token}
// with an unknown token, expecting the handler to render the Unavailable
// page (404) and the home-link to navigate back to landing.
func (s *PagesSuite) TestUnavailable_BrokenConfirmTokenRendersPage() {
	t := s.T()
	page := s.browser.NewPage(t)

	_, err := page.Goto(s.app.Server.URL + "/api/confirm/garbage")
	require.NoError(t, err)

	heading, err := page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Link Unavailable", heading)

	require.NoError(t, page.GetByTestId("home-link").Click())
	require.NoError(t, page.GetByTestId("subscribe-form").WaitFor(
		playwright.LocatorWaitForOptions{Timeout: playwright.Float(5000)}))
}
