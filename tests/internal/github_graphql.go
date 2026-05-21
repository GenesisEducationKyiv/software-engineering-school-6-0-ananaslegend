//go:build integration || e2e

package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
)

// GitHubGraphQLFixture is an in-process double for the GitHub GraphQL
// endpoint that internal/github/client.go calls from the scanner. It only
// understands the field-aliased shape `r{id}: repository(owner: "X", name:
// "Y") { latestRelease { tagName } }` because that is the only query the
// scanner ever sends.
type GitHubGraphQLFixture struct {
	server *httptest.Server

	mu   sync.Mutex
	tags map[string]string // "owner/name" -> latest tag
}

// NewGitHubGraphQLFixture starts the fixture server. It is closed
// automatically when the calling test finishes.
func NewGitHubGraphQLFixture(t testing.TB) *GitHubGraphQLFixture {
	f := &GitHubGraphQLFixture{tags: make(map[string]string)}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// URL returns the base URL to pass as GraphQLURL into githubclient.Config.
func (f *GitHubGraphQLFixture) URL() string { return f.server.URL }

// SetLatestTag programs the fixture so that repo (formatted as "owner/name")
// is reported with latestRelease.tagName == tag on subsequent requests.
func (f *GitHubGraphQLFixture) SetLatestTag(repo, tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags[repo] = tag
}

// Reset clears every programmed tag. Call from SetupTest of a suite.
func (f *GitHubGraphQLFixture) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags = make(map[string]string)
}

var aliasRe = regexp.MustCompile(`r(\d+):\s*repository\(owner:\s*"([^"]+)"\s*,\s*name:\s*"([^"]+)"\)`)

func (f *GitHubGraphQLFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	tags := make(map[string]string, len(f.tags))
	for k, v := range f.tags {
		tags[k] = v
	}
	f.mu.Unlock()

	type release struct {
		TagName string `json:"tagName"`
	}
	type repo struct {
		LatestRelease *release `json:"latestRelease"`
	}

	data := make(map[string]repo)
	for _, m := range aliasRe.FindAllStringSubmatch(body.Query, -1) {
		alias := "r" + m[1]
		key := m[2] + "/" + m[3]
		if tag, ok := tags[key]; ok {
			data[alias] = repo{LatestRelease: &release{TagName: tag}}
			continue
		}
		data[alias] = repo{LatestRelease: nil}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
