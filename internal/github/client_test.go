package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		token:      "",
		httpClient: srv.Client(),
		graphqlURL: srv.URL,
	}
}

func newTestClientREST(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		token:      "",
		httpClient: srv.Client(),
		graphqlURL: "http://unused",
		restURL:    srv.URL,
	}
}

func TestRepoExists_Exists(t *testing.T) {
	c := newTestClientREST(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodHead, r.Method)
		assert.Equal(t, "/repos/golang/go", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	exists, err := c.RepoExists(context.Background(), domain.RepoExistsParams{Owner: "golang", Name: "go"})
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestRepoExists_NotFound(t *testing.T) {
	c := newTestClientREST(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodHead, r.Method)
		w.WriteHeader(http.StatusNotFound)
	})

	exists, err := c.RepoExists(context.Background(), domain.RepoExistsParams{Owner: "foo", Name: "missing"})
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRepoExists_ServerError(t *testing.T) {
	c := newTestClientREST(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := c.RepoExists(context.Background(), domain.RepoExistsParams{Owner: "foo", Name: "bar"})
	require.Error(t, err)
}

func TestRepoExists_BearerTokenSent(t *testing.T) {
	var gotAuth string
	c := newTestClientREST(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	c.token = "mytoken"

	_, err := c.RepoExists(context.Background(), domain.RepoExistsParams{Owner: "foo", Name: "bar"})
	require.NoError(t, err)
	assert.Equal(t, "Bearer mytoken", gotAuth)
}

func TestGetLatestReleases_EmptyInput(t *testing.T) {
	c := &Client{httpClient: &http.Client{}, graphqlURL: "http://unused"}
	result, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetLatestReleases_HappyPath(t *testing.T) {
	repos := []domain.GitHubRepo{
		{ID: 1, Owner: "golang", Name: "go"},
		{ID: 2, Owner: "torvalds", Name: "linux"},
	}

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"r1": map[string]any{"latestRelease": map[string]any{"tagName": "go1.22.0"}},
				"r2": map[string]any{"latestRelease": nil},
			},
		}))
	})

	tags, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{Repos: repos})
	require.NoError(t, err)
	assert.Equal(t, "go1.22.0", tags[1])
	_, exists := tags[2]
	assert.False(t, exists, "repo with no release should be absent from map")
}

func TestGetLatestReleases_NonOKStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{
		Repos: []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestGetLatestReleases_BearerTokenSent(t *testing.T) {
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"r1": map[string]any{"latestRelease": map[string]any{"tagName": "v1.0"}},
		}}))
	})
	c.token = "mytoken"

	_, err := c.GetLatestReleases(context.Background(), GetLatestReleasesParams{
		Repos: []domain.GitHubRepo{{ID: 1, Owner: "foo", Name: "bar"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer mytoken", gotAuth)
}
