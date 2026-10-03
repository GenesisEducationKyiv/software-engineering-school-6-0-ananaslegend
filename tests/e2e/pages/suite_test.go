//go:build e2e

package pages_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/tests/internal"
)

func TestMain(m *testing.M) {
	if err := playwright.Install(); err != nil {
		log.Fatalf("playwright install: %v", err)
	}
	os.Exit(m.Run())
}

func TestPages(t *testing.T) {
	suite.Run(t, new(PagesSuite))
}

// PagesSuite drives every page-rendering scenario that is not part of the
// full subscriber lifecycle. Containers are reused across methods;
// SetupTest truncates DB and rebuilds the app for each test.
type PagesSuite struct {
	suite.Suite

	pg        *internal.Postgres
	mailpit   *internal.Mailpit
	ghREST    *internal.GitHubFixture
	ghGraphQL *internal.GitHubGraphQLFixture
	browser   *internal.Browser
	app       *internal.E2EApp
}

func (s *PagesSuite) SetupSuite() {
	ctx := context.Background()
	s.pg = internal.NewPostgres(ctx, s.T())
	s.mailpit = internal.NewMailpit(ctx, s.T())
	s.ghREST = internal.NewGitHubFixture(s.T())
	s.ghGraphQL = internal.NewGitHubGraphQLFixture(s.T())
	s.browser = internal.NewBrowser(s.T())
}

func (s *PagesSuite) SetupTest() {
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
