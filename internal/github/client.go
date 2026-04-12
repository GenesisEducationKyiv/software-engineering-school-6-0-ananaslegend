package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

// ErrRateLimited is returned when GitHub responds with 429 Too Many Requests.
var ErrRateLimited = errors.New("github: rate limited")

// GetLatestReleasesParams is the input to GetLatestReleases.
type GetLatestReleasesParams struct {
	Repos []domain.GitHubRepo
}

// Client is a GitHub API client. Use NewClient to create one.
// The zero value is not usable.
type Client struct {
	token      string
	httpClient *http.Client
	graphqlURL string
	restURL    string
}

// NewClient returns a Client targeting the real GitHub API.
// token is optional; without it the rate limit is 60 req/h.
func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{},
		graphqlURL: "https://api.github.com/graphql",
		restURL:    "https://api.github.com",
	}
}

// GetLatestReleases fetches the latest release tag for each repo in a single
// GraphQL request. Repos with no releases are absent from the returned map.
func (c *Client) GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error) {
	if len(p.Repos) == 0 {
		return nil, nil
	}

	query := buildGraphQLQuery(p.Repos)
	body, _ := json.Marshal(map[string]string{"query": query})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build github graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github graphql request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w (retry-after: %s)", ErrRateLimited, resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github graphql: unexpected status %d", resp.StatusCode)
	}

	var result struct {
		Data map[string]struct {
			LatestRelease *struct {
				TagName string `json:"tagName"`
			} `json:"latestRelease"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode github graphql response: %w", err)
	}

	tags := make(map[int64]string, len(p.Repos))
	for _, repo := range p.Repos {
		alias := "r" + strconv.FormatInt(repo.ID, 10)
		if data, ok := result.Data[alias]; ok && data.LatestRelease != nil {
			tags[repo.ID] = data.LatestRelease.TagName
		}
	}
	return tags, nil
}

// RepoExists checks whether the given GitHub repository is accessible via REST HEAD.
// Returns false (no error) for 404; returns an error for unexpected status codes.
func (c *Client) RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/%s", c.restURL, p.Owner, p.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, fmt.Errorf("build github rest request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("github rest request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusTooManyRequests:
		return false, fmt.Errorf("%w (retry-after: %s)", ErrRateLimited, resp.Header.Get("Retry-After"))
	default:
		return false, fmt.Errorf("github rest: unexpected status %d", resp.StatusCode)
	}
}

// buildGraphQLQuery builds a batched GraphQL query aliasing each repo as r{id}.
// Owner/name are safe: validated by repoNameRe on subscribe ([A-Za-z0-9._-]+).
func buildGraphQLQuery(repos []domain.GitHubRepo) string {
	var sb strings.Builder
	sb.WriteString("query {")
	for _, r := range repos {
		fmt.Fprintf(&sb, ` r%d: repository(owner: %q, name: %q) { latestRelease { tagName } }`,
			r.ID, r.Owner, r.Name)
	}
	sb.WriteString(" }")
	return sb.String()
}
