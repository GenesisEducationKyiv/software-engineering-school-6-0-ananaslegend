//go:build integration || e2e

package internal_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	githubclient "github.com/ananaslegend/reposeetory/internal/github"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/tests/internal"
)

func TestGitHubGraphQLFixture_RoundTrip(t *testing.T) {
	fx := internal.NewGitHubGraphQLFixture(t)
	fx.SetLatestTag("octocat/Hello-World", "v1.2.3")

	client := githubclient.New(githubclient.Config{
		GraphQLURL: fx.URL(),
		Token:      "test-token",
	})

	tags, err := client.GetLatestReleases(context.Background(), githubclient.GetLatestReleasesParams{
		Repos: []domain.GitHubRepo{
			{ID: 7, Owner: "octocat", Name: "Hello-World"},
			{ID: 99, Owner: "unknown", Name: "repo"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", tags[7])
	_, present := tags[99]
	assert.False(t, present, "unprogrammed repo must not appear in tags map")
}

func TestGitHubGraphQLFixture_Reset(t *testing.T) {
	fx := internal.NewGitHubGraphQLFixture(t)
	fx.SetLatestTag("a/b", "v1")
	fx.Reset()
	fx.SetLatestTag("a/b", "v2")

	client := githubclient.New(githubclient.Config{GraphQLURL: fx.URL()})
	tags, err := client.GetLatestReleases(context.Background(), githubclient.GetLatestReleasesParams{
		Repos: []domain.GitHubRepo{{ID: 1, Owner: "a", Name: "b"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "v2", tags[1])
}
