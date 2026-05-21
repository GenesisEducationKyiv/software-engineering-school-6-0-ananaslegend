//go:build integration || e2e

package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// GitHubFixture is an in-process double for the GitHub REST API. It only
// implements HEAD /repos/{owner}/{name} — enough for service.Subscribe.
type GitHubFixture struct {
	server *httptest.Server

	mu       sync.Mutex
	repos    map[string]bool
	forceErr bool
	reqCount int
}

// NewGitHubFixture starts the fixture server. It is closed automatically when
// the calling test finishes.
func NewGitHubFixture(t testing.TB) *GitHubFixture {
	f := &GitHubFixture{repos: make(map[string]bool)}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// URL returns the base URL to pass as RESTURL into githubclient.Config.
func (f *GitHubFixture) URL() string { return f.server.URL }

// SetRepoExists programs the fixture's response for a single repository.
// repo is formatted as "owner/name".
func (f *GitHubFixture) SetRepoExists(repo string, exists bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[repo] = exists
}

// ForceError makes the fixture return 500 on every subsequent call.
func (f *GitHubFixture) ForceError() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forceErr = true
}

// Reset clears the programmed map, the forced-error flag, and the request
// counter. Call from SetupTest of a suite.
func (f *GitHubFixture) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos = make(map[string]bool)
	f.forceErr = false
	f.reqCount = 0
}

// RequestCount returns the number of requests handled since the last Reset.
func (f *GitHubFixture) RequestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqCount
}

func (f *GitHubFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqCount++
	forceErr := f.forceErr
	repos := f.repos
	f.mu.Unlock()

	if forceErr {
		http.Error(w, "forced error", http.StatusInternalServerError)
		return
	}

	// Expected path: /repos/{owner}/{name}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "repos" {
		http.Error(w, fmt.Sprintf("unexpected path: %s", r.URL.Path), http.StatusBadRequest)
		return
	}
	key := parts[1] + "/" + parts[2]
	if exists, ok := repos[key]; ok && exists {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}
