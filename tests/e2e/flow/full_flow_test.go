//go:build e2e

package flow_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/tests/internal"
)

func TestMain(m *testing.M) {
	if err := playwright.Install(); err != nil {
		log.Fatalf("playwright install: %v", err)
	}
	os.Exit(m.Run())
}

func TestFullSubscriberLifecycle(t *testing.T) {
	suite.Run(t, new(FullFlowSuite))
}

type FullFlowSuite struct {
	suite.Suite

	pg        *internal.Postgres
	mailpit   *internal.Mailpit
	ghREST    *internal.GitHubFixture
	ghGraphQL *internal.GitHubGraphQLFixture
	browser   *internal.Browser
	app       *internal.E2EApp
}

func (s *FullFlowSuite) SetupSuite() {
	ctx := context.Background()
	s.pg = internal.NewPostgres(ctx, s.T())
	s.mailpit = internal.NewMailpit(ctx, s.T())
	s.ghREST = internal.NewGitHubFixture(s.T())
	s.ghGraphQL = internal.NewGitHubGraphQLFixture(s.T())
	s.browser = internal.NewBrowser(s.T())
}

func (s *FullFlowSuite) SetupTest() {
	ctx := context.Background()
	s.pg.Truncate(ctx, s.T())
	s.mailpit.Reset(ctx, s.T())
	s.ghREST.Reset()
	s.ghGraphQL.Reset()

	s.app = internal.NewE2EApp(s.T(), internal.E2EAppConfig{
		Pool:           s.pg.Pool,
		Mailpit:        s.mailpit,
		GitHubRESTURL:  s.ghREST.URL(),
		GitHubGraphURL: s.ghGraphQL.URL(),
		ScannerTick:    50 * time.Millisecond,
		DrainerTick:    50 * time.Millisecond,
	})
}

func (s *FullFlowSuite) TestLifecycle() {
	t := s.T()
	ctx := context.Background()

	const (
		repo  = "e2e-org/e2e-repo"
		email = "e2e@test.local"
	)

	// Phase 0 — seed fixtures.
	s.ghREST.SetRepoExists(repo, true)
	s.ghGraphQL.SetLatestTag(repo, "v1.0.0")

	// Phase 1 — subscribe via UI.
	page := s.browser.NewPage(t)
	_, err := page.Goto(s.app.Server.URL)
	require.NoError(t, err)
	require.NoError(t, page.GetByTestId("repo-input").Fill(repo))
	require.NoError(t, page.GetByTestId("email-input").Fill(email))
	require.NoError(t, page.GetByTestId("submit-button").Click())
	require.NoError(t, page.WaitForURL("**/subscribed",
		playwright.PageWaitForURLOptions{Timeout: playwright.Float(5000)}))
	heading, err := page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Check your inbox", heading)

	// Phase 2 — receive confirmation email.
	msgs := s.mailpit.WaitForMessages(ctx, t, 1, 5*time.Second)
	require.Equal(t, email, msgs[0].To[0].Address)
	require.Contains(t, msgs[0].Subject, "Confirm")

	emailPage := s.browser.LoadEmailHTML(t, s.mailpit.MessageHTML(ctx, t, msgs[0].ID))
	emailHeading, err := emailPage.GetByTestId("email-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Confirm Your Subscription", emailHeading)

	confirmURL, err := emailPage.GetByTestId("confirm-button").GetAttribute("href")
	require.NoError(t, err)
	require.Contains(t, confirmURL, "/api/confirm/")
	s.mailpit.Reset(ctx, t)

	// Phase 3 — confirm via link.
	_, err = page.Goto(confirmURL)
	require.NoError(t, err)
	heading, err = page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Subscription Confirmed", heading)

	// Phase 4 — emulate new release.
	// Scanner needs one tick after confirm to record `v1.0.0` baseline,
	// otherwise the next tick treats v2.0.0 as the only-and-baseline tag.
	time.Sleep(150 * time.Millisecond)
	s.ghGraphQL.SetLatestTag(repo, "v2.0.0")

	msgs = s.mailpit.WaitForMessages(ctx, t, 1, 5*time.Second)
	require.Contains(t, msgs[0].Subject, "v2.0.0")

	emailPage = s.browser.LoadEmailHTML(t, s.mailpit.MessageHTML(ctx, t, msgs[0].ID))
	emailHeading, err = emailPage.GetByTestId("email-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "New Release Published", emailHeading)

	releaseURL, err := emailPage.GetByTestId("release-button").GetAttribute("href")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/e2e-org/e2e-repo/releases/tag/v2.0.0", releaseURL)

	unsubURL, err := emailPage.GetByTestId("unsubscribe-link").GetAttribute("href")
	require.NoError(t, err)
	require.Contains(t, unsubURL, "/api/unsubscribe/")
	s.mailpit.Reset(ctx, t)

	// Phase 5 — unsubscribe via link.
	_, err = page.Goto(unsubURL)
	require.NoError(t, err)
	heading, err = page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "You've Been Unsubscribed", heading)

	// Phase 6 — emulate yet another release; mailbox must stay empty.
	s.ghGraphQL.SetLatestTag(repo, "v3.0.0")
	time.Sleep(500 * time.Millisecond) // 5× scanner + 5× drainer ticks
	require.Empty(t, s.mailpit.Messages(ctx, t),
		"unsubscribed user must not receive release email")

	// Phase 7 — re-subscribe the same email/repo via UI.
	_, err = page.Goto(s.app.Server.URL)
	require.NoError(t, err)
	require.NoError(t, page.GetByTestId("repo-input").Fill(repo))
	require.NoError(t, page.GetByTestId("email-input").Fill(email))
	require.NoError(t, page.GetByTestId("submit-button").Click())
	require.NoError(t, page.WaitForURL("**/subscribed",
		playwright.PageWaitForURLOptions{Timeout: playwright.Float(5000)}))
	heading, err = page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Check your inbox", heading)

	// Phase 8 — receive new confirmation email.
	msgs = s.mailpit.WaitForMessages(ctx, t, 1, 5*time.Second)
	require.Equal(t, email, msgs[0].To[0].Address)
	require.Contains(t, msgs[0].Subject, "Confirm")

	emailPage = s.browser.LoadEmailHTML(t, s.mailpit.MessageHTML(ctx, t, msgs[0].ID))
	emailHeading, err = emailPage.GetByTestId("email-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Confirm Your Subscription", emailHeading)

	confirmURL, err = emailPage.GetByTestId("confirm-button").GetAttribute("href")
	require.NoError(t, err)
	require.Contains(t, confirmURL, "/api/confirm/")
	s.mailpit.Reset(ctx, t)

	// Phase 9 — confirm the re-subscription.
	_, err = page.Goto(confirmURL)
	require.NoError(t, err)
	heading, err = page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Subscription Confirmed", heading)

	// Phase 10 — emulate yet another release; the re-subscribed user must
	// receive it. last_seen_tag is already v3.0.0 from Phase 6, so no
	// baseline sleep is needed: v3.0.0 → v4.0.0 is a fresh transition.
	s.ghGraphQL.SetLatestTag(repo, "v4.0.0")
	msgs = s.mailpit.WaitForMessages(ctx, t, 1, 5*time.Second)
	require.Contains(t, msgs[0].Subject, "v4.0.0")

	emailPage = s.browser.LoadEmailHTML(t, s.mailpit.MessageHTML(ctx, t, msgs[0].ID))
	emailHeading, err = emailPage.GetByTestId("email-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "New Release Published", emailHeading)

	releaseURL, err = emailPage.GetByTestId("release-button").GetAttribute("href")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/e2e-org/e2e-repo/releases/tag/v4.0.0", releaseURL)
}
