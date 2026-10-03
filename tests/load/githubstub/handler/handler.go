// Package handler exposes the GitHub stub HTTP handler so it can be reused
// both by the standalone CLI binary (tests/load/githubstub) and by in-process
// integration tests (tests/load/pipeline) that need a fake GitHub API without
// the overhead of a docker-compose stack.
package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"
)

// Config controls the behaviour of a stub instance.
//
//   - Repos: how many owner-{idx}/repo-{idx} identifiers the stub answers for.
//     Anything outside [0, Repos) is treated as 404 / nil latestRelease.
//   - LatencyMS: synthetic per-request latency injected before each REST or
//     GraphQL response. Used to simulate a slow upstream.
//   - Port: only consumed by the CLI binary; in-process callers pick their own
//     listener via httptest.NewServer.
type Config struct {
	Repos     int
	LatencyMS int
	Port      int
}

// LoadConfigFromEnv reads STUB_REPOS / STUB_LATENCY_MS / STUB_PORT, applying
// the defaults used by docker-compose (1000 repos, 0 ms latency, port 8090).
// Invalid values are logged to stderr but never fatal — the defaults stand in.
func LoadConfigFromEnv() Config {
	cfg := Config{Repos: 1000, Port: 8090}
	if v := os.Getenv("STUB_REPOS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Repos = n
		} else {
			fmt.Fprintf(os.Stderr, "github-stub: invalid STUB_REPOS=%q, using default %d\n", v, cfg.Repos)
		}
	}
	if v := os.Getenv("STUB_LATENCY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.LatencyMS = n
		} else {
			fmt.Fprintf(os.Stderr, "github-stub: invalid STUB_LATENCY_MS=%q, using default %d\n", v, cfg.LatencyMS)
		}
	}
	if v := os.Getenv("STUB_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		} else {
			fmt.Fprintf(os.Stderr, "github-stub: invalid STUB_PORT=%q, using default %d\n", v, cfg.Port)
		}
	}
	return cfg
}

// stubState holds mutable per-instance state. tagVersion starts at 1
// and is atomically incremented by POST /admin/release-all.
type stubState struct {
	tagVersion atomic.Int64
}

// Repos are named owner-{idx}/repo-{idx} for idx in [0, cfg.Repos).
// The REST endpoint is HEAD or GET /repos/owner-{idx}/repo-{idx}.
var repoPathRE = regexp.MustCompile(`^/repos/owner-(\d+)/repo-(\d+)$`)

// The real client sends:
//
//	r{id}: repository(owner: "owner-{idx}", name: "repo-{idx}") { latestRelease { tagName } }
//
// where id = idx+1 (1-based DB IDs used as GraphQL aliases).
// We capture the numeric suffix of the alias (== id) to derive idx.
var aliasRE = regexp.MustCompile(`r(\d+):\s*repository\(owner:\s*"owner-(\d+)",\s*name:\s*"repo-\d+"\)`)

// NewHandler builds the stub mux. Each instance keeps its own tagVersion so
// concurrent test runs (e.g. parallel httptest.Server instances) don't share
// release state.
func NewHandler(cfg Config) http.Handler {
	state := &stubState{}
	state.tagVersion.Store(1)

	mux := http.NewServeMux()

	// REST: HEAD or GET /repos/owner-{idx}/repo-{idx}
	// The real github.Client.RepoExists uses HEAD, but GET must also work for
	// smoke-testing with curl.
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		applyLatency(cfg)
		defer func() {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}()
		m := repoPathRE.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		ownerIdx, _ := strconv.Atoi(m[1])
		repoIdx, _ := strconv.Atoi(m[2])
		if ownerIdx != repoIdx || ownerIdx < 0 || ownerIdx >= cfg.Repos {
			http.NotFound(w, r)
			return
		}
		// Return 200; body only for GET (HEAD drops it automatically).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			id := ownerIdx + 1 // 1-based ID, matching real DB IDs
			_, _ = fmt.Fprintf(w, `{"id":%d,"full_name":"owner-%d/repo-%d"}`, id, ownerIdx, ownerIdx)
		}
	})

	// GraphQL: POST /graphql
	// Response shape mirrors what internal/github/client.go decodes:
	//   {"data":{"r{id}":{"latestRelease":{"tagName":"v{n}"}}}}
	// Aliases are 1-based (r1 = owner-0/repo-0), matching the real scanner.
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		applyLatency(cfg)
		defer func() {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var reqBody struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		tag := fmt.Sprintf("v%d", state.tagVersion.Load())
		data := map[string]any{}

		for _, m := range aliasRE.FindAllStringSubmatch(reqBody.Query, -1) {
			alias := "r" + m[1] // e.g. "r1"
			id, _ := strconv.Atoi(m[1])
			ownerIdx := id - 1 // convert 1-based id back to 0-based index
			if ownerIdx < 0 || ownerIdx >= cfg.Repos {
				data[alias] = map[string]any{"latestRelease": nil}
				continue
			}
			data[alias] = map[string]any{
				"latestRelease": map[string]string{"tagName": tag},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})

	// Admin: POST /admin/release-all — atomically bumps the tag version.
	mux.HandleFunc("/admin/release-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		state.tagVersion.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})

	// Health: GET /healthz — used by docker-compose health checks.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func applyLatency(cfg Config) {
	if cfg.LatencyMS > 0 {
		time.Sleep(time.Duration(cfg.LatencyMS) * time.Millisecond)
	}
}
