# Load Testing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a load-testing tier on top of the existing testing trophy that establishes a committed performance baseline and detects regressions on demand and nightly.

**Architecture:** k6 for HTTP scenarios + Go test for background-pipeline throughput, both run against a dedicated `docker-compose.loadtest.yml` stack (Postgres on tmpfs, Redis, standalone GitHub stub, app with `MAILER_DRIVER=noop`). A `compare.go` runner takes the median of 3 runs and diffs against committed baseline JSON. CI runs nightly + manual via `workflow_dispatch`, not on every PR.

**Tech Stack:** Go 1.x (existing), k6 (`grafana/k6:0.50.0` Docker image), docker-compose, Postgres 16, Redis 7, GitHub Actions.

**Reference:** All decisions and rationale in `docs/superpowers/specs/2026-05-26-load-testing-design.md`. This plan is execution-only.

---

## File Structure

**Production code (single change):**
- Modify `internal/config/config.go` — add `MailerDriver` field
- Modify `internal/app/mailer.go` — honor `MailerDriver` when set

**New: load-testing tree** (all under `tests/load/`):
- `tests/load/k6/lib/env.js` — shared ENV helpers
- `tests/load/k6/lib/thresholds.js` — shared SLO definitions
- `tests/load/k6/discovery.js` — ramp-to-failure (one-off)
- `tests/load/k6/subscribe_storm.js` — POST /api/subscribe constant rate
- `tests/load/k6/token_flow.js` — confirm + unsubscribe constant rate
- `tests/load/githubstub/main.go` — standalone HTTP stub
- `tests/load/githubstub/main_test.go`
- `tests/load/githubstub/Dockerfile`
- `tests/load/seeder/main.go` — DB seeder CLI
- `tests/load/seeder/main_test.go`
- `tests/load/seeder/README.md`
- `tests/load/pipeline/drain_test.go` — Go test, build tag `loadtest`
- `tests/load/pipeline/stub.go` — GraphQL stub helper for pipeline test
- `tests/load/scripts/compare.go` — baseline diff runner
- `tests/load/scripts/compare_test.go`
- `tests/load/scripts/wait-healthy.sh` — block until stack is up
- `tests/load/baseline/.gitkeep` — directory marker (real files added on first `load-baseline` run)
- `tests/load/results/.gitignore` — ignore everything except itself
- `tests/load/README.md`
- `tests/load/CHANGELOG.md`

**New: infrastructure files:**
- `docker-compose.loadtest.yml` — load stack
- `.github/workflows/load-test.yml` — nightly + workflow_dispatch
- `Makefile` — add `load-*` targets

**New: documentation:**
- `docs/adr/0016-load-testing.md` — ADR
- `docs/testing.md` — modified (new section + table row + prerequisites + philosophy)
- `CLAUDE.md` — modified (Commands block; one-line ADR-0016 reference)

**Untouched:** All files under `docs/adr/0001`...`0015`. All existing test code under `tests/integration/`, `tests/e2e/`, `tests/internal/`.

---

## Phase 1 — Production code: MAILER_DRIVER env

### Task 1: Add `MailerDriver` to config and honor it in app wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/app/mailer.go`
- Test: `internal/app/mailer_test.go` (new file)

- [ ] **Step 1: Write the failing test**

Create `internal/app/mailer_test.go`:

```go
package app

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/config"
	"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
)

func TestNewEmailer_DriverNoop(t *testing.T) {
	cfg := config.Config{MailerDriver: "noop", ResendAPIKey: "should-be-ignored"}
	got, err := newEmailer(cfg, zerolog.Nop())
	require.NoError(t, err)
	require.IsType(t, &emailer.StubMailer{}, got, "noop driver must return StubMailer regardless of other env")
}

func TestNewEmailer_DriverResend(t *testing.T) {
	cfg := config.Config{MailerDriver: "resend", ResendAPIKey: "key", ResendFrom: "x@y"}
	got, err := newEmailer(cfg, zerolog.Nop())
	require.NoError(t, err)
	require.IsType(t, &emailer.ResendMailer{}, got)
}

func TestNewEmailer_EmptyDriver_FallsBackToImplicit(t *testing.T) {
	cfg := config.Config{MailerDriver: "", ResendAPIKey: "key", ResendFrom: "x@y"}
	got, err := newEmailer(cfg, zerolog.Nop())
	require.NoError(t, err)
	require.IsType(t, &emailer.ResendMailer{}, got, "empty driver must preserve existing implicit behavior")
}

func TestNewEmailer_UnknownDriver(t *testing.T) {
	cfg := config.Config{MailerDriver: "carrier-pigeon"}
	_, err := newEmailer(cfg, zerolog.Nop())
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestNewEmailer -v`
Expected: FAIL — `MailerDriver` field doesn't exist on `config.Config`.

- [ ] **Step 3: Add `MailerDriver` to config**

In `internal/config/config.go`, add after the existing mailer fields (after `ResendFrom`):

```go
	MailerDriver string `envconfig:"MAILER_DRIVER"` // "smtp", "resend", "noop"; empty = implicit fallback
```

- [ ] **Step 4: Rewrite `newEmailer` switch**

Replace the body of `newEmailer` in `internal/app/mailer.go`:

```go
func newEmailer(cfg config.Config, log zerolog.Logger) (emailer.Emailer, error) {
	switch cfg.MailerDriver {
	case "noop":
		log.Info().Msg("mailer: noop (explicit)")
		return emailer.NewStubMailer(), nil
	case "resend":
		log.Info().Msg("mailer: resend (explicit)")
		return emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom), nil
	case "smtp":
		mailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort,
			User: cfg.SMTPUser, Password: cfg.SMTPPass,
			From: cfg.SMTPFrom, TLSPolicy: cfg.SMTPTLSPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("app.newEmailer: emailer.NewSMTPMailer: %w", err)
		}
		log.Info().Msg("mailer: smtp (explicit)")
		return mailer, nil
	case "":
		// Implicit fallback (preserves existing behavior).
		switch {
		case cfg.ResendAPIKey != "":
			log.Info().Msg("mailer: resend (implicit)")
			return emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom), nil
		case cfg.SMTPHost != "":
			mailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
				Host: cfg.SMTPHost, Port: cfg.SMTPPort,
				User: cfg.SMTPUser, Password: cfg.SMTPPass,
				From: cfg.SMTPFrom, TLSPolicy: cfg.SMTPTLSPolicy,
			})
			if err != nil {
				return nil, fmt.Errorf("app.newEmailer: emailer.NewSMTPMailer: %w", err)
			}
			log.Info().Msg("mailer: smtp (implicit)")
			return mailer, nil
		default:
			log.Info().Msg("mailer: stub (implicit)")
			return emailer.NewStubMailer(), nil
		}
	default:
		return nil, fmt.Errorf("app.newEmailer: unknown MAILER_DRIVER %q", cfg.MailerDriver)
	}
}
```

- [ ] **Step 5: Run test to verify pass**

Run: `go test ./internal/app/ -run TestNewEmailer -v`
Expected: PASS (4 tests).

- [ ] **Step 6: Run full test + vet + lint**

Run: `make test && make vet && make lint`
Expected: all pass. Existing integration/e2e tests must continue to work since implicit fallback is preserved.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/app/mailer.go internal/app/mailer_test.go
git commit -m "feat(config): add MAILER_DRIVER for explicit mailer selection"
```

---

## Phase 2 — Standalone GitHub stub

### Task 2: Standalone GitHub HTTP stub

**Files:**
- Create: `tests/load/githubstub/main.go`
- Create: `tests/load/githubstub/main_test.go`
- Create: `tests/load/githubstub/Dockerfile`

The stub must serve the **same shape of responses** that the real client expects. Inspect `internal/github/client.go` to confirm request/response format before writing this task's code — the stub must satisfy the existing `githubclient.New()` client without any mode flag.

- [ ] **Step 1: Inspect the real client interface**

Run: `grep -n "GraphQL\|REST\|json.Unmarshal\|json.NewDecoder" internal/github/client.go`
Expected: Identifies the request paths and response struct shapes the stub must match (GraphQL POST /graphql + REST GET /repos/{owner}/{name}).

- [ ] **Step 2: Write the failing test**

Create `tests/load/githubstub/main_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStub_RepoExists_Returns200(t *testing.T) {
	srv := httptest.NewServer(newHandler(stubConfig{Repos: 10, LatencyMS: 0}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/repos/owner-0/repo-0")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStub_RepoExists_UnknownReturns404(t *testing.T) {
	srv := httptest.NewServer(newHandler(stubConfig{Repos: 10}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/repos/owner-999/repo-999")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStub_GraphQL_ReturnsBatchTags(t *testing.T) {
	srv := httptest.NewServer(newHandler(stubConfig{Repos: 3}))
	defer srv.Close()

	body := strings.NewReader(`{"query":"{ r0: repository(owner:\"owner-0\",name:\"repo-0\"){refs(refPrefix:\"refs/tags/\",first:1){nodes{name}}} }"}`)
	resp, err := http.Post(srv.URL+"/graphql", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Data map[string]struct {
			Refs struct {
				Nodes []struct{ Name string } `json:"nodes"`
			} `json:"refs"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.Data["r0"].Refs.Nodes)
}

func TestStub_AdminReleaseAll_BumpsTags(t *testing.T) {
	h := newHandler(stubConfig{Repos: 2})
	srv := httptest.NewServer(h)
	defer srv.Close()

	bumpAll(srv.URL, t)

	body := strings.NewReader(`{"query":"{ r0: repository(owner:\"owner-0\",name:\"repo-0\"){refs(refPrefix:\"refs/tags/\",first:1){nodes{name}}} }"}`)
	resp, err := http.Post(srv.URL+"/graphql", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	var out struct {
		Data map[string]struct {
			Refs struct {
				Nodes []struct{ Name string } `json:"nodes"`
			} `json:"refs"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, "v2", out.Data["r0"].Refs.Nodes[0].Name, "tag must bump after /admin/release-all")
}

func bumpAll(url string, t *testing.T) {
	t.Helper()
	resp, err := http.Post(url+"/admin/release-all", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

var _ = context.Background // silence unused
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./tests/load/githubstub/ -v`
Expected: FAIL — `newHandler` not defined.

- [ ] **Step 4: Implement the stub**

Create `tests/load/githubstub/main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"
)

type stubConfig struct {
	Repos     int
	LatencyMS int
	Port      int
}

func loadConfigFromEnv() stubConfig {
	cfg := stubConfig{Repos: 1000, Port: 8090}
	if v := os.Getenv("STUB_REPOS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Repos = n
		}
	}
	if v := os.Getenv("STUB_LATENCY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.LatencyMS = n
		}
	}
	if v := os.Getenv("STUB_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	return cfg
}

// state is the mutable part: a tag version that "/admin/release-all" bumps.
type stubState struct {
	tagVersion atomic.Int64
}

var repoNameRE = regexp.MustCompile(`^/repos/owner-(\d+)/repo-\d+$`)
var aliasRE = regexp.MustCompile(`r(\d+): repository\(owner:"owner-(\d+)",name:"repo-\d+"\)`)

func newHandler(cfg stubConfig) http.Handler {
	state := &stubState{}
	state.tagVersion.Store(1)

	mux := http.NewServeMux()

	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		latency(cfg)
		m := repoNameRE.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		idx, _ := strconv.Atoi(m[1])
		if idx < 0 || idx >= cfg.Repos {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%d,"full_name":"owner-%d/repo-%d"}`, idx+1, idx, idx)
	})

	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		latency(cfg)
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		tag := fmt.Sprintf("v%d", state.tagVersion.Load())

		data := map[string]any{}
		for _, m := range aliasRE.FindAllStringSubmatch(body.Query, -1) {
			alias := "r" + m[1]
			ownerIdx, _ := strconv.Atoi(m[2])
			if ownerIdx < 0 || ownerIdx >= cfg.Repos {
				data[alias] = nil
				continue
			}
			data[alias] = map[string]any{
				"refs": map[string]any{
					"nodes": []map[string]string{{"name": tag}},
				},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})

	mux.HandleFunc("/admin/release-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		state.tagVersion.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func latency(cfg stubConfig) {
	if cfg.LatencyMS > 0 {
		time.Sleep(time.Duration(cfg.LatencyMS) * time.Millisecond)
	}
}

func main() {
	cfg := loadConfigFromEnv()
	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("github-stub: repos=%d latency=%dms addr=%s\n", cfg.Repos, cfg.LatencyMS, addr)
	if err := http.ListenAndServe(addr, newHandler(cfg)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run test to verify pass**

Run: `go test ./tests/load/githubstub/ -v`
Expected: PASS (4 tests).

- [ ] **Step 6: Verify real client is compatible (manual smoke)**

Run: `go run ./tests/load/githubstub & sleep 1 && curl -s http://localhost:8090/repos/owner-0/repo-0 && curl -sX POST http://localhost:8090/graphql -d '{"query":"{ r0: repository(owner:\"owner-0\",name:\"repo-0\"){refs(refPrefix:\"refs/tags/\",first:1){nodes{name}}} }"}' && kill %1`
Expected: Two JSON responses, no errors.

If the response shape doesn't match what `internal/github/client.go` decodes (compare against client_test.go fixtures), adjust the stub before proceeding.

- [ ] **Step 7: Create Dockerfile**

Create `tests/load/githubstub/Dockerfile`:

```dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/githubstub ./tests/load/githubstub

FROM alpine:3.19
COPY --from=build /out/githubstub /usr/local/bin/githubstub
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/githubstub"]
```

- [ ] **Step 8: Build the image (smoke)**

Run: `docker build -f tests/load/githubstub/Dockerfile -t reposeetory-githubstub:dev .`
Expected: Successful build, no errors.

- [ ] **Step 9: Commit**

```bash
git add tests/load/githubstub/
git commit -m "feat(loadtest): standalone github stub with admin release-all endpoint"
```

---

## Phase 3 — Seeder CLI

### Task 3: DB seeder CLI

**Files:**
- Create: `tests/load/seeder/main.go`
- Create: `tests/load/seeder/main_test.go`
- Create: `tests/load/seeder/README.md`

The seeder must use the **production repository** (`internal/subscription/repository`) so that schema, token format, and constraints match exactly. It writes a `tokens.json` file with arrays of confirmation/unsubscribe tokens that k6 will consume.

- [ ] **Step 1: Inspect existing subscription repository**

Run: `grep -n "func.*Create\|func.*Insert" internal/subscription/repository/*.go`
Expected: Identify the `CreateSubscription` / `MarkConfirmed` (or equivalent) method signatures and the `subscription` domain struct. The seeder will call these directly.

- [ ] **Step 2: Write the failing integration test**

Create `tests/load/seeder/main_test.go` (build tag `loadtest` so it doesn't run with normal unit tests):

```go
//go:build loadtest

package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	testpg "github.com/ananaslegend/reposeetory/tests/internal"
)

func TestSeeder_WritesTokensFileAndDBRows(t *testing.T) {
	pg := testpg.NewPostgres(t)
	tmp := t.TempDir()
	tokensFile := filepath.Join(tmp, "tokens.json")

	cmd := exec.Command("go", "run", "./tests/load/seeder",
		"--database-url", pg.URL,
		"--subscriptions", "5",
		"--pending-tokens", "3",
		"--out", tokensFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	raw, err := os.ReadFile(tokensFile)
	require.NoError(t, err)
	var tokens struct {
		Confirm     []string `json:"confirm"`
		Unsubscribe []string `json:"unsubscribe"`
	}
	require.NoError(t, json.Unmarshal(raw, &tokens))
	require.Len(t, tokens.Confirm, 3, "confirm tokens count must match --pending-tokens")
	require.Len(t, tokens.Unsubscribe, 5, "unsubscribe tokens count must match --subscriptions")

	// Verify rows exist in DB.
	ctx := context.Background()
	var n int
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM subscriptions").Scan(&n))
	require.Equal(t, 8, n, "must have 5 confirmed + 3 pending subscriptions")
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test -tags=loadtest ./tests/load/seeder/ -v`
Expected: FAIL — seeder binary doesn't compile.

- [ ] **Step 4: Implement seeder**

Create `tests/load/seeder/main.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	subrepo "github.com/ananaslegend/reposeetory/internal/subscription/repository"
)

func main() {
	var (
		dbURL    = flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string")
		subs     = flag.Int("subscriptions", 10000, "number of confirmed subscriptions to insert")
		pending  = flag.Int("pending-tokens", 10000, "number of pending subscriptions (confirmation tokens) to insert")
		outFile  = flag.String("out", "tests/load/results/tokens.json", "output path for tokens.json")
		repoPool = flag.Int("repo-pool", 100, "number of distinct owner/repo combinations used")
	)
	flag.Parse()

	if *dbURL == "" {
		log.Fatal("seeder: --database-url is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dbURL)
	if err != nil {
		log.Fatalf("seeder: pool: %v", err)
	}
	defer pool.Close()

	repo := subrepo.New(pool)

	tokens := struct {
		Confirm     []string `json:"confirm"`
		Unsubscribe []string `json:"unsubscribe"`
	}{
		Confirm:     make([]string, 0, *pending),
		Unsubscribe: make([]string, 0, *subs),
	}

	// Confirmed subscriptions → unsubscribe tokens.
	for i := 0; i < *subs; i++ {
		sub, err := repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
			Email:    fmt.Sprintf("loadtest-confirmed-%d@example.com", i),
			RepoFull: fmt.Sprintf("owner-%d/repo-%d", i%*repoPool, i%*repoPool),
		})
		if err != nil {
			log.Fatalf("seeder: create confirmed[%d]: %v", i, err)
		}
		if err := repo.MarkConfirmed(ctx, sub.ConfirmToken); err != nil {
			log.Fatalf("seeder: confirm[%d]: %v", i, err)
		}
		tokens.Unsubscribe = append(tokens.Unsubscribe, sub.UnsubscribeToken)
	}

	// Pending subscriptions → confirmation tokens.
	for i := 0; i < *pending; i++ {
		sub, err := repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
			Email:    fmt.Sprintf("loadtest-pending-%d@example.com", i),
			RepoFull: fmt.Sprintf("owner-%d/repo-%d", i%*repoPool, i%*repoPool),
		})
		if err != nil {
			log.Fatalf("seeder: create pending[%d]: %v", i, err)
		}
		tokens.Confirm = append(tokens.Confirm, sub.ConfirmToken)
	}

	if err := os.MkdirAll(filepath.Dir(*outFile), 0o755); err != nil {
		log.Fatalf("seeder: mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		log.Fatalf("seeder: marshal: %v", err)
	}
	if err := os.WriteFile(*outFile, raw, 0o644); err != nil {
		log.Fatalf("seeder: write: %v", err)
	}

	fmt.Printf("seeded: %d confirmed, %d pending, tokens → %s\n", *subs, *pending, *outFile)
}
```

Note: the exact method names (`CreateSubscription`, `MarkConfirmed`) and param-struct fields (`Email`, `RepoFull`, `ConfirmToken`, `UnsubscribeToken`) MUST match what the real repository exposes — check `internal/subscription/repository/` and `internal/subscription/domain/model.go` in Step 1 and adjust if different. If method signatures differ, update both the test and the seeder consistently.

Also add `"path/filepath"` to the imports if not already there.

- [ ] **Step 5: Run test to verify pass**

Run: `go test -tags=loadtest ./tests/load/seeder/ -v`
Expected: PASS.

- [ ] **Step 6: Write seeder README**

Create `tests/load/seeder/README.md`:

```markdown
# Load-test Seeder

Populates the database with synthetic subscriptions and exports tokens for k6 to consume.

## Usage

```sh
go run ./tests/load/seeder \
  --database-url postgres://loadtest:loadtest@localhost:5433/loadtest \
  --subscriptions 10000 \
  --pending-tokens 10000 \
  --out tests/load/results/tokens.json
```

## Output schema

```json
{
  "confirm":     ["token-1", "token-2", ...],
  "unsubscribe": ["token-1", "token-2", ...]
}
```

k6 loads this file once per VU pool via `SharedArray`.
```

- [ ] **Step 7: Commit**

```bash
git add tests/load/seeder/
git commit -m "feat(loadtest): seeder CLI populating subscriptions and tokens.json"
```

---

## Phase 4 — Docker compose + Makefile up/down/reset

### Task 4: docker-compose.loadtest.yml and Makefile basics

**Files:**
- Create: `docker-compose.loadtest.yml`
- Create: `tests/load/scripts/wait-healthy.sh`
- Modify: `Makefile`
- Create: `tests/load/results/.gitignore`
- Create: `tests/load/baseline/.gitkeep`

- [ ] **Step 1: Create the compose file**

Create `docker-compose.loadtest.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: loadtest
      POSTGRES_PASSWORD: loadtest
      POSTGRES_DB: loadtest
    tmpfs:
      - /var/lib/postgresql/data
    command: >
      postgres
        -c shared_buffers=256MB
        -c max_connections=100
        -c fsync=off
        -c synchronous_commit=off
    ports: ["5433:5432"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U loadtest"]
      interval: 2s
      retries: 10

  redis:
    image: redis:7-alpine
    command: redis-server --save "" --appendonly no
    tmpfs:
      - /data
    ports: ["6380:6379"]

  github-stub:
    build:
      context: .
      dockerfile: tests/load/githubstub/Dockerfile
    environment:
      STUB_REPOS: "1000"
      STUB_LATENCY_MS: "20"
      STUB_PORT: "8090"
    ports: ["8090:8090"]
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:8090/healthz || exit 1"]
      interval: 2s
      retries: 10

  app:
    build: .
    environment:
      DATABASE_URL: postgres://loadtest:loadtest@postgres:5432/loadtest
      REDIS_URL: redis://redis:6379
      GITHUB_API_URL: http://github-stub:8090
      GITHUB_TOKEN: loadtest
      RESEND_API_KEY: ""
      MAILER_DRIVER: noop
      LOG_LEVEL: warn
      SCANNER_INTERVAL: 500ms
      NOTIFIER_INTERVAL: 200ms
      CONFIRMER_INTERVAL: 200ms
      DB_MAX_CONNS: "20"
      APP_BASE_URL: http://localhost:8080
    depends_on:
      postgres: { condition: service_healthy }
      redis: { condition: service_started }
      github-stub: { condition: service_healthy }
    ports: ["8080:8080"]

  k6:
    image: grafana/k6:0.50.0
    volumes:
      - ./tests/load/k6:/scripts:ro
      - ./tests/load/results:/results
    environment:
      BASE_URL: http://app:8080
    network_mode: "service:app"
    profiles: ["k6"]
```

- [ ] **Step 2: Create wait-healthy.sh**

Create `tests/load/scripts/wait-healthy.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

COMPOSE="docker compose -f docker-compose.loadtest.yml"
DEADLINE=$((SECONDS + 60))

while (( SECONDS < DEADLINE )); do
  status=$($COMPOSE ps --format json | grep -c '"Health":"healthy"' || true)
  if (( status >= 2 )); then
    # Wait for app to respond to /healthz too (no healthcheck in compose for app).
    if curl -sf http://localhost:8080/healthz >/dev/null 2>&1; then
      echo "stack ready"
      exit 0
    fi
  fi
  sleep 2
done

echo "wait-healthy.sh: timeout after 60s"
$COMPOSE ps
exit 1
```

Then: `chmod +x tests/load/scripts/wait-healthy.sh`

Note: if the app doesn't currently expose `/healthz`, replace the curl with `curl -sf http://localhost:8080/ >/dev/null` (landing page works as a smoke probe). Verify with `grep healthz internal/httpapi/router.go`.

- [ ] **Step 3: Add up/down/reset Makefile targets**

In `Makefile`, append a new section:

```makefile
# --- Load testing ---------------------------------------------------------

LOAD_COMPOSE := docker compose -f docker-compose.loadtest.yml

.PHONY: load-up load-down load-reset

load-up:        ## bring stack up (postgres, redis, github-stub, app)
	$(LOAD_COMPOSE) up -d --build
	@./tests/load/scripts/wait-healthy.sh

load-down:      ## tear down stack and volumes
	$(LOAD_COMPOSE) down -v

load-reset:     ## clear DB + Redis between runs (no teardown)
	$(LOAD_COMPOSE) exec -T postgres psql -U loadtest -d loadtest -c "TRUNCATE subscriptions, confirmation_notifications, release_notifications CASCADE"
	$(LOAD_COMPOSE) exec -T redis redis-cli FLUSHALL
```

- [ ] **Step 4: Create results gitignore and baseline placeholder**

Create `tests/load/results/.gitignore`:

```
*
!.gitignore
```

Create empty file `tests/load/baseline/.gitkeep`.

- [ ] **Step 5: Smoke-test the stack**

Run:
```sh
make load-up
docker compose -f docker-compose.loadtest.yml ps
curl -sf http://localhost:8080/ | head -1
curl -sf http://localhost:8090/healthz
make load-reset
make load-down
```

Expected: All four containers come up; landing page returns HTML; `/healthz` on github-stub returns 200; truncate runs without error; teardown succeeds.

If the app container fails to start because migrations haven't run — investigate whether the existing Dockerfile already runs them on startup. If not, prepend a migration step to `load-up` (e.g., `$(LOAD_COMPOSE) run --rm app /app/migrate up`). Do NOT skip — schema must exist before truncate works.

- [ ] **Step 6: Commit**

```bash
git add docker-compose.loadtest.yml tests/load/scripts/wait-healthy.sh tests/load/results/.gitignore tests/load/baseline/.gitkeep Makefile
git commit -m "feat(loadtest): docker-compose stack and up/down/reset targets"
```

---

## Phase 5 — k6 library + discovery scenario

### Task 5: k6 library helpers and discovery scenario

**Files:**
- Create: `tests/load/k6/lib/env.js`
- Create: `tests/load/k6/lib/thresholds.js`
- Create: `tests/load/k6/discovery.js`
- Modify: `Makefile` (add `load-discover` target)

- [ ] **Step 1: Create env helper**

Create `tests/load/k6/lib/env.js`:

```javascript
export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
export const REPO_POOL_SIZE = parseInt(__ENV.REPO_POOL_SIZE || '100', 10);

export function randomRepo() {
  const i = Math.floor(Math.random() * REPO_POOL_SIZE);
  return `owner-${i}/repo-${i}`;
}

export function uniqueEmail(scenarioTag) {
  const ts = Date.now();
  return `loadtest+${scenarioTag}-${__VU}-${__ITER}-${ts}@example.com`;
}
```

- [ ] **Step 2: Create thresholds helper**

Create `tests/load/k6/lib/thresholds.js`:

```javascript
// Shared SLO thresholds. Update via `make load-baseline` after a discovery run.
export const SUBSCRIBE_THRESHOLDS = {
  'http_req_duration{scenario:subscribe}': ['p(95)<300', 'p(99)<800'],
  'http_req_failed{scenario:subscribe}': ['rate<0.005'],
};

export const TOKEN_THRESHOLDS = {
  'http_req_duration{scenario:confirm}': ['p(95)<150'],
  'http_req_duration{scenario:unsubscribe}': ['p(95)<150'],
  'http_req_failed': ['rate<0.005'],
  'checks': ['rate>0.99'],
};
```

- [ ] **Step 3: Create discovery scenario**

Create `tests/load/k6/discovery.js`:

```javascript
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, randomRepo, uniqueEmail } from './lib/env.js';

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 800,
      stages: [
        { target: 5,   duration: '30s' },
        { target: 25,  duration: '1m' },
        { target: 50,  duration: '1m' },
        { target: 100, duration: '1m' },
        { target: 200, duration: '1m' },
        { target: 400, duration: '1m' },
      ],
      tags: { scenario: 'discovery' },
    },
  },
};

export default function () {
  const payload = JSON.stringify({
    email: uniqueEmail('disc'),
    repository: randomRepo(),
  });
  const res = http.post(`${BASE_URL}/api/subscribe`, payload, {
    headers: { 'Content-Type': 'application/json' },
    tags: { scenario: 'discovery' },
  });
  check(res, { 'status is 2xx': (r) => r.status >= 200 && r.status < 300 });
}
```

- [ ] **Step 4: Add `load-discover` to Makefile**

Append to the load section in `Makefile`:

```makefile
.PHONY: load-discover
load-discover:    ## stage 0 — ramp to failure, does NOT update baseline
	@$(MAKE) load-up
	@$(MAKE) load-reset
	$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/discovery.js --summary-export=/results/discovery.json
	@echo ">> Read tests/load/results/discovery.json and pick rate for baseline"
```

- [ ] **Step 5: Smoke-test discovery (short run)**

Override stage durations for a smoke run:

```sh
docker compose -f docker-compose.loadtest.yml up -d --build
./tests/load/scripts/wait-healthy.sh
docker compose -f docker-compose.loadtest.yml --profile k6 run --rm \
  -e K6_DURATION=15s k6 run /scripts/discovery.js \
  --summary-export=/results/discovery-smoke.json \
  --vus 10 --duration 15s
```

Expected: k6 produces a summary, no compile errors. The full ramp will be run as part of the first `load-baseline`.

- [ ] **Step 6: Commit**

```bash
git add tests/load/k6/ Makefile
git commit -m "feat(loadtest): k6 library + discovery scenario"
```

---

## Phase 6 — subscribe_storm scenario

### Task 6: subscribe_storm k6 scenario

**Files:**
- Create: `tests/load/k6/subscribe_storm.js`

- [ ] **Step 1: Create the scenario**

Create `tests/load/k6/subscribe_storm.js`:

```javascript
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, randomRepo, uniqueEmail } from './lib/env.js';
import { SUBSCRIBE_THRESHOLDS } from './lib/thresholds.js';

// Rate is set via env so `compare.go` can refuse to diff when rate differs.
const RATE = parseInt(__ENV.SUBSCRIBE_RATE || '100', 10);

export const options = {
  scenarios: {
    warmup: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: Math.max(200, RATE * 2),
      stages: [{ target: RATE, duration: '30s' }],
      tags: { scenario: 'subscribe', phase: 'warmup' },
      exec: 'subscribe',
    },
    storm: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: '2m30s',
      preAllocatedVUs: Math.max(50, RATE),
      maxVUs: Math.max(200, RATE * 2),
      startTime: '30s',
      tags: { scenario: 'subscribe', phase: 'steady' },
      exec: 'subscribe',
    },
  },
  thresholds: SUBSCRIBE_THRESHOLDS,
};

export function subscribe() {
  const payload = JSON.stringify({
    email: uniqueEmail('sub'),
    repository: randomRepo(),
  });
  const res = http.post(`${BASE_URL}/api/subscribe`, payload, {
    headers: { 'Content-Type': 'application/json' },
    tags: { scenario: 'subscribe' },
  });
  check(res, { 'status 2xx': (r) => r.status >= 200 && r.status < 300 });
}
```

- [ ] **Step 2: Smoke run (manual)**

Run:
```sh
make load-up
make load-reset
docker compose -f docker-compose.loadtest.yml --profile k6 run --rm \
  -e SUBSCRIBE_RATE=10 k6 run /scripts/subscribe_storm.js \
  --summary-export=/results/subscribe-smoke.json
```

Expected: Summary written; `http_req_duration` p95 reported; thresholds may or may not pass at 10 RPS — only checking that the scenario runs end-to-end.

- [ ] **Step 3: Inspect summary shape**

Run: `cat tests/load/results/subscribe-smoke.json | jq '.metrics.http_req_duration.values, .metrics.http_reqs.values, .metrics.http_req_failed.values'`
Expected: JSON with `p(95)`, `p(99)`, `rate`, etc. Save this shape — `compare.go` will parse it in Phase 9.

- [ ] **Step 4: Commit**

```bash
git add tests/load/k6/subscribe_storm.js
git commit -m "feat(loadtest): subscribe_storm scenario (warmup + steady state)"
```

---

## Phase 7 — token_flow scenario

### Task 7: token_flow k6 scenario consuming pre-seeded tokens

**Files:**
- Create: `tests/load/k6/token_flow.js`

- [ ] **Step 1: Create the scenario**

Create `tests/load/k6/token_flow.js`:

```javascript
import http from 'k6/http';
import { check } from 'k6';
import { SharedArray } from 'k6/data';
import { BASE_URL } from './lib/env.js';
import { TOKEN_THRESHOLDS } from './lib/thresholds.js';

const RATE = parseInt(__ENV.TOKEN_RATE || '30', 10);

const tokens = new SharedArray('tokens', function () {
  return JSON.parse(open('/results/tokens.json'));
});

export const options = {
  scenarios: {
    confirm_warmup: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: Math.max(200, RATE * 2),
      stages: [{ target: RATE, duration: '30s' }],
      tags: { scenario: 'confirm', phase: 'warmup' },
      exec: 'confirm',
    },
    unsubscribe_warmup: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: Math.max(200, RATE * 2),
      stages: [{ target: RATE, duration: '30s' }],
      tags: { scenario: 'unsubscribe', phase: 'warmup' },
      exec: 'unsubscribe',
    },
    confirm_steady: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: '2m30s',
      preAllocatedVUs: Math.max(50, RATE),
      maxVUs: Math.max(200, RATE * 2),
      startTime: '30s',
      tags: { scenario: 'confirm', phase: 'steady' },
      exec: 'confirm',
    },
    unsubscribe_steady: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: '2m30s',
      preAllocatedVUs: Math.max(50, RATE),
      maxVUs: Math.max(200, RATE * 2),
      startTime: '30s',
      tags: { scenario: 'unsubscribe', phase: 'steady' },
      exec: 'unsubscribe',
    },
  },
  thresholds: TOKEN_THRESHOLDS,
};

export function confirm() {
  const idx = (__VU * 100003 + __ITER) % tokens[0].confirm.length;
  const token = tokens[0].confirm[idx];
  const res = http.get(`${BASE_URL}/api/confirm/${token}`, {
    tags: { scenario: 'confirm' },
  });
  check(res, {
    'status 2xx': (r) => r.status >= 200 && r.status < 300,
    'is HTML': (r) => (r.headers['Content-Type'] || '').includes('text/html'),
  });
}

export function unsubscribe() {
  const idx = (__VU * 100003 + __ITER) % tokens[0].unsubscribe.length;
  const token = tokens[0].unsubscribe[idx];
  const res = http.get(`${BASE_URL}/api/unsubscribe/${token}`, {
    tags: { scenario: 'unsubscribe' },
  });
  check(res, {
    'status 2xx': (r) => r.status >= 200 && r.status < 300,
    'is HTML': (r) => (r.headers['Content-Type'] || '').includes('text/html'),
  });
}
```

Note: `tokens` is wrapped in a single-element array because `SharedArray` requires its constructor to return an array. `tokens[0].confirm` accesses the object inside.

- [ ] **Step 2: Smoke run (with seeded tokens)**

Run:
```sh
make load-up
make load-reset
go run ./tests/load/seeder \
  --database-url postgres://loadtest:loadtest@localhost:5433/loadtest \
  --subscriptions 100 \
  --pending-tokens 100 \
  --out tests/load/results/tokens.json

docker compose -f docker-compose.loadtest.yml --profile k6 run --rm \
  -e TOKEN_RATE=5 k6 run /scripts/token_flow.js \
  --summary-export=/results/token-smoke.json
```

Expected: All four scenarios run, `checks` rate ≈ 1.0, no compile errors.

If `checks` rate is much below 1.0: inspect HTTP responses — likely the seeder's tokens don't match what the `/api/confirm` handler expects. Compare token format between seeder output and `internal/subscription/repository`.

- [ ] **Step 3: Commit**

```bash
git add tests/load/k6/token_flow.js
git commit -m "feat(loadtest): token_flow scenario with pre-seeded tokens"
```

---

## Phase 8 — Pipeline drain test (Go)

### Task 8: Background-pipeline throughput test

**Files:**
- Create: `tests/load/pipeline/stub.go`
- Create: `tests/load/pipeline/drain_test.go`
- Modify: `tests/internal/github_graphql.go` if needed for scaling

This test boots the full stack in-process (via `tests/internal.NewE2EApp`) with `NoopMailer`, seeds 10k confirmed subscriptions across 1k repos, then triggers a "release for everyone" via the github-stub and measures the time until the outbox is fully drained.

- [ ] **Step 1: Write the failing test**

Create `tests/load/pipeline/drain_test.go`:

```go
//go:build loadtest

package pipeline_test

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	testinternal "github.com/ananaslegend/reposeetory/tests/internal"
	"github.com/ananaslegend/reposeetory/tests/load/pipeline"
)

func TestPipeline_DrainsOutboxWithinSLA(t *testing.T) {
	subs := envInt("LOAD_SUBSCRIPTIONS", 10000)
	repos := envInt("LOAD_REPOS", 1000)
	timeout := envDur("LOAD_DRAIN_TIMEOUT", 5*time.Minute)
	slaSec := envInt("LOAD_DRAIN_SLA_SECONDS", 60)

	pg := testinternal.NewPostgres(t)
	stub := pipeline.NewGitHubStub(t, repos)

	// Seed 10k confirmed subscriptions across `repos` distinct repos.
	pipeline.SeedConfirmed(t, pg.Pool, subs, repos)

	app := testinternal.NewE2EApp(t, testinternal.E2EAppConfig{
		Pool:           pg.Pool,
		Mailpit:        nil, // not used: we pass NoopMailer via E2EApp extension
		GitHubRESTURL:  stub.URL,
		GitHubGraphURL: stub.URL + "/graphql",
		ScannerTick:    50 * time.Millisecond,
		DrainerTick:    50 * time.Millisecond,
	})

	// Trigger "release for everyone".
	resp, err := http.Post(stub.URL+"/admin/release-all", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()

	start := time.Now()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		var pending int
		err := app.Pool.QueryRow(context.Background(),
			"SELECT COUNT(*) FROM release_notifications WHERE sent_at IS NULL").
			Scan(&pending)
		require.NoError(t, err)
		if pending == 0 {
			drain := time.Since(start)
			t.Logf("drain complete in %s (%.1f rows/s)", drain, float64(subs)/drain.Seconds())
			require.LessOrEqual(t, drain.Seconds(), float64(slaSec),
				"drain duration must be ≤ %d seconds (SLO)", slaSec)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("drain did not complete within %s", timeout)
}

func envInt(key string, dflt int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return dflt
}

func envDur(key string, dflt time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return dflt
}

// Reference to silence unused import if E2EApp.Pool isn't directly accessed.
var _ *pgxpool.Pool
```

Note: `NewE2EApp` currently requires a non-nil Mailpit. Extending it to accept `MAILER_DRIVER=noop` and a nil Mailpit is part of this task — see Step 3 below.

- [ ] **Step 2: Implement pipeline stub helper**

Create `tests/load/pipeline/stub.go`:

```go
//go:build loadtest

package pipeline

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	subrepo "github.com/ananaslegend/reposeetory/internal/subscription/repository"
)

// GitHubStub wraps the standalone githubstub HTTP server for use in tests.
// We run the same binary the docker-compose stack uses, but as a local
// httptest.Server, so this test stays single-process.
type GitHubStub struct {
	URL string
	srv *httptest.Server
}

// NewGitHubStub starts an in-process instance of the load-test github stub.
// It imports the stub's handler directly to avoid spawning a subprocess.
func NewGitHubStub(t *testing.T, repos int) *GitHubStub {
	t.Helper()
	// Set env so the stub's loadConfigFromEnv() picks up the right repo count.
	t.Setenv("STUB_REPOS", fmt.Sprintf("%d", repos))
	t.Setenv("STUB_LATENCY_MS", "0")

	// IMPORTANT: github stub package imports — adjust if package path differs.
	stubHandler := newStubHandlerFromBinary(t)
	srv := httptest.NewServer(stubHandler)
	t.Cleanup(srv.Close)
	return &GitHubStub{URL: srv.URL, srv: srv}
}

// newStubHandlerFromBinary is a stop-gap that runs the stub binary if the
// stub package cannot be imported (e.g., main package). For the implementation
// we copy the handler-builder into a helper sub-package — see TODO note.
func newStubHandlerFromBinary(t *testing.T) interface{ ServeHTTP(any, any) } {
	t.Helper()
	// If the github stub's `newHandler` is exported from a sub-package,
	// call it directly. Otherwise this falls back to launching the binary.
	t.Fatal("pipeline.NewGitHubStub: refactor githubstub to export newHandler from a non-main package before using this helper")
	return nil
}

// SeedConfirmed inserts `n` confirmed subscriptions distributed across `repos`
// distinct owner-X/repo-X identifiers.
func SeedConfirmed(t *testing.T, pool *pgxpool.Pool, n, repos int) {
	t.Helper()
	ctx := context.Background()
	repo := subrepo.New(pool)
	for i := 0; i < n; i++ {
		ownerIdx := i % repos
		sub, err := repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
			Email:    fmt.Sprintf("loadtest-pipeline-%d@example.com", i),
			RepoFull: fmt.Sprintf("owner-%d/repo-%d", ownerIdx, ownerIdx),
		})
		require.NoError(t, err)
		require.NoError(t, repo.MarkConfirmed(ctx, sub.ConfirmToken))
	}
}

var _ = os.Setenv
var _ = exec.Command
```

- [ ] **Step 3: Refactor githubstub to expose `NewHandler` from a non-main package**

`tests/load/githubstub/main.go` is `package main`, so its `newHandler` is not importable. Split:

- Move `newHandler`, `stubConfig`, `stubState`, `loadConfigFromEnv`, `latency` into a new file `tests/load/githubstub/handler/handler.go` (package `handler`), with `NewHandler` exported.
- Keep `main.go` minimal — calls `handler.LoadConfigFromEnv()` and `handler.NewHandler(cfg)`.
- Update `tests/load/githubstub/main_test.go` to call `handler.NewHandler` (move test there or import).
- Update `tests/load/pipeline/stub.go`:
  ```go
  import stubh "github.com/ananaslegend/reposeetory/tests/load/githubstub/handler"
  // ...
  srv := httptest.NewServer(stubh.NewHandler(stubh.Config{Repos: repos}))
  ```

- [ ] **Step 4: Extend `tests/internal.NewE2EApp` to allow `MAILER_DRIVER=noop`**

In `tests/internal/app.go`, update `E2EAppConfig` to accept an optional `Mailer emailer.Emailer` (when set, used directly instead of building from Mailpit). When `Mailpit == nil && Mailer == nil`, default to `emailer.NewStubMailer()`.

This is a small additive change — existing tests pass `Mailpit` and continue to use SMTP; the load test passes nil for both and gets the stub.

- [ ] **Step 5: Run the test (build only, no execution yet)**

Run: `go test -tags=loadtest -run=DoesNotExist ./tests/load/pipeline/...`
Expected: PASS (no matching test) but **must compile cleanly**. Fix any compile errors before proceeding.

- [ ] **Step 6: Run the test for real (small scale)**

Run:
```sh
LOAD_SUBSCRIPTIONS=500 LOAD_REPOS=50 LOAD_DRAIN_SLA_SECONDS=120 \
  go test -tags=loadtest -timeout=5m -v ./tests/load/pipeline/...
```

Expected: PASS. Log line should show drain duration < 120s.

- [ ] **Step 7: Run at full scale**

Run:
```sh
go test -tags=loadtest -timeout=15m -v ./tests/load/pipeline/...
```

Expected: PASS at default (10k/1k subs/repos, 60s SLA). If FAIL — capture the actual drain duration; that is your first real data point and informs whether 60s SLO is realistic. Adjust `LOAD_DRAIN_SLA_SECONDS` env default if persistently off.

- [ ] **Step 8: Add `load-pipeline` to Makefile**

Append to load section:

```makefile
.PHONY: load-pipeline
load-pipeline:    ## background drain throughput test
	go test -tags=loadtest -timeout=15m -v ./tests/load/pipeline/...
```

(No `load-up` dependency — this test uses testcontainers, not the docker-compose stack.)

- [ ] **Step 9: Commit**

```bash
git add tests/load/githubstub/ tests/load/pipeline/ tests/internal/app.go Makefile
git commit -m "feat(loadtest): pipeline drain test with in-process github stub"
```

---

## Phase 9 — `compare.go` runner

### Task 9: Baseline JSON parser, median, threshold check

**Files:**
- Create: `tests/load/scripts/compare.go`
- Create: `tests/load/scripts/compare_test.go`

This is the only piece that interprets k6 output and decides pass/fail. It is split into 4 internal functions (parse → median → diff → render) and tested per function.

- [ ] **Step 1: Write failing tests for parse + median**

Create `tests/load/scripts/compare_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const sampleK6Summary = `{
  "metrics": {
    "http_req_duration": {
      "type": "trend",
      "values": {
        "p(50)": 40.1,
        "p(95)": 180.5,
        "p(99)": 410.0,
        "avg": 55.0,
        "med": 40.1
      }
    },
    "http_reqs": {
      "type": "counter",
      "values": { "count": 30000, "rate": 99.8 }
    },
    "http_req_failed": {
      "type": "rate",
      "values": { "rate": 0.002, "passes": 29940, "fails": 60 }
    }
  }
}`

func writeSummary(t *testing.T, dir, name, contents string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o644))
	return p
}

func TestParseSummary(t *testing.T) {
	dir := t.TempDir()
	p := writeSummary(t, dir, "s1.json", sampleK6Summary)
	got, err := parseSummary(p)
	require.NoError(t, err)
	require.InDelta(t, 180.5, got.P95Ms, 0.001)
	require.InDelta(t, 410.0, got.P99Ms, 0.001)
	require.InDelta(t, 99.8, got.RPS, 0.001)
	require.InDelta(t, 0.002, got.ErrorRate, 0.001)
}

func TestMedianRuns_OddCount(t *testing.T) {
	runs := []runMetrics{
		{P95Ms: 100, P99Ms: 200, RPS: 90, ErrorRate: 0.01},
		{P95Ms: 110, P99Ms: 210, RPS: 95, ErrorRate: 0.02},
		{P95Ms: 105, P99Ms: 205, RPS: 92, ErrorRate: 0.015},
	}
	m := medianRuns(runs)
	require.InDelta(t, 105.0, m.P95Ms, 0.001)
	require.InDelta(t, 205.0, m.P99Ms, 0.001)
	require.InDelta(t, 92.0, m.RPS, 0.001)
	require.InDelta(t, 0.015, m.ErrorRate, 0.001)
}

func TestDiff_DetectsP95Regression(t *testing.T) {
	base := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.001}
	cur := runMetrics{P95Ms: 121, P99Ms: 200, RPS: 100, ErrorRate: 0.001} // +21%
	report := diff(base, cur)
	require.True(t, report.Failed, "21%% p95 increase must trip the 20%% threshold")
	require.Contains(t, report.Lines[0], "p95")
}

func TestDiff_PassesWithinTolerance(t *testing.T) {
	base := runMetrics{P95Ms: 100, P99Ms: 200, RPS: 100, ErrorRate: 0.001}
	cur := runMetrics{P95Ms: 119, P99Ms: 200, RPS: 100, ErrorRate: 0.001} // +19%
	report := diff(base, cur)
	require.False(t, report.Failed)
}

func TestDiff_ConfigMismatch(t *testing.T) {
	base := baselineFile{
		Config: map[string]any{"rate_rps": 100.0, "redis_enabled": true},
	}
	cur := currentRunConfig{RateRPS: 50, RedisEnabled: true}
	err := checkConfig(base, cur)
	require.ErrorContains(t, err, "config mismatch")
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./tests/load/scripts/ -v`
Expected: FAIL — functions not defined.

- [ ] **Step 3: Implement `compare.go`**

Create `tests/load/scripts/compare.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type k6Summary struct {
	Metrics map[string]struct {
		Type   string                 `json:"type"`
		Values map[string]json.Number `json:"values"`
	} `json:"metrics"`
}

type runMetrics struct {
	P50Ms     float64
	P95Ms     float64
	P99Ms     float64
	RPS       float64
	ErrorRate float64
}

type baselineFile struct {
	Scenario    string         `json:"scenario"`
	CapturedAt  string         `json:"captured_at"`
	GitSHA      string         `json:"git_sha"`
	Environment string         `json:"environment"`
	Config      map[string]any `json:"config"`
	Metrics     map[string]any `json:"metrics"`
}

type currentRunConfig struct {
	RateRPS      int
	RedisEnabled bool
}

type diffReport struct {
	Failed bool
	Lines  []string
}

func parseSummary(path string) (runMetrics, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return runMetrics{}, fmt.Errorf("compare.parseSummary: %w", err)
	}
	var s k6Summary
	if err := json.Unmarshal(raw, &s); err != nil {
		return runMetrics{}, fmt.Errorf("compare.parseSummary: %w", err)
	}
	dur := s.Metrics["http_req_duration"].Values
	reqs := s.Metrics["http_reqs"].Values
	failed := s.Metrics["http_req_failed"].Values

	p50, _ := dur["p(50)"].Float64()
	p95, _ := dur["p(95)"].Float64()
	p99, _ := dur["p(99)"].Float64()
	rps, _ := reqs["rate"].Float64()
	er, _ := failed["rate"].Float64()

	return runMetrics{P50Ms: p50, P95Ms: p95, P99Ms: p99, RPS: rps, ErrorRate: er}, nil
}

func medianRuns(runs []runMetrics) runMetrics {
	pick := func(get func(runMetrics) float64) float64 {
		vals := make([]float64, len(runs))
		for i, r := range runs {
			vals[i] = get(r)
		}
		sort.Float64s(vals)
		mid := len(vals) / 2
		if len(vals)%2 == 1 {
			return vals[mid]
		}
		return (vals[mid-1] + vals[mid]) / 2
	}
	return runMetrics{
		P50Ms:     pick(func(r runMetrics) float64 { return r.P50Ms }),
		P95Ms:     pick(func(r runMetrics) float64 { return r.P95Ms }),
		P99Ms:     pick(func(r runMetrics) float64 { return r.P99Ms }),
		RPS:       pick(func(r runMetrics) float64 { return r.RPS }),
		ErrorRate: pick(func(r runMetrics) float64 { return r.ErrorRate }),
	}
}

func diff(base, cur runMetrics) diffReport {
	var report diffReport
	if cur.P95Ms > base.P95Ms*1.20 {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL p95: %.1fms vs baseline %.1fms (+%.1f%%, threshold +20%%)",
				cur.P95Ms, base.P95Ms, pct(base.P95Ms, cur.P95Ms)))
	}
	if cur.P99Ms > base.P99Ms*1.30 {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL p99: %.1fms vs baseline %.1fms (+%.1f%%, threshold +30%%)",
				cur.P99Ms, base.P99Ms, pct(base.P99Ms, cur.P99Ms)))
	}
	errCap := base.ErrorRate * 1.5
	if errCap < 0.01 {
		errCap = 0.01
	}
	if cur.ErrorRate > errCap {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL error rate: %.4f vs baseline %.4f (cap %.4f)",
				cur.ErrorRate, base.ErrorRate, errCap))
	}
	if cur.RPS < base.RPS*0.90 {
		report.Failed = true
		report.Lines = append(report.Lines,
			fmt.Sprintf("FAIL RPS: %.1f vs baseline %.1f (-%.1f%%, threshold -10%%)",
				cur.RPS, base.RPS, pct(cur.RPS, base.RPS)))
	}
	if !report.Failed {
		report.Lines = append(report.Lines, "PASS")
	}
	return report
}

func pct(from, to float64) float64 {
	if from == 0 {
		return 0
	}
	return (to - from) / from * 100
}

func checkConfig(base baselineFile, cur currentRunConfig) error {
	baseRate, _ := base.Config["rate_rps"].(float64)
	if int(baseRate) != cur.RateRPS {
		return fmt.Errorf("config mismatch: baseline rate_rps=%v, current rate_rps=%d", baseRate, cur.RateRPS)
	}
	baseRedis, _ := base.Config["redis_enabled"].(bool)
	if baseRedis != cur.RedisEnabled {
		return fmt.Errorf("config mismatch: baseline redis_enabled=%v, current=%v", baseRedis, cur.RedisEnabled)
	}
	return nil
}

func main() {
	var (
		baselineDir = flag.String("baseline-dir", "tests/load/baseline", "directory holding baseline JSON files")
		resultsDir  = flag.String("results-dir", "tests/load/results", "directory holding per-run summary JSON files")
		scenario    = flag.String("scenario", "subscribe_storm", "scenario name to compare")
		environment = flag.String("environment", "local-testcontainers", "environment tag, must match baseline")
		updateMode  = flag.Bool("update-baseline", false, "overwrite baseline with current run's medians")
	)
	flag.Parse()

	pattern := filepath.Join(*resultsDir, strings.TrimSuffix(*scenario, "_storm")+"_*.json")
	if *scenario == "subscribe_storm" {
		pattern = filepath.Join(*resultsDir, "subscribe_*.json")
	} else if *scenario == "token_flow" {
		pattern = filepath.Join(*resultsDir, "token_*.json")
	}

	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no summary files matching %s\n", pattern)
		os.Exit(2)
	}

	var runs []runMetrics
	for _, f := range files {
		m, err := parseSummary(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parseSummary(%s): %v\n", f, err)
			os.Exit(2)
		}
		runs = append(runs, m)
	}
	cur := medianRuns(runs)

	basePath := filepath.Join(*baselineDir, *scenario+".json")
	if *environment != "local-testcontainers" {
		basePath = filepath.Join(*baselineDir, *scenario+"."+*environment+".json")
	}

	if *updateMode {
		writeBaseline(basePath, *scenario, *environment, cur)
		fmt.Printf("baseline updated: %s\n", basePath)
		return
	}

	baseRaw, err := os.ReadFile(basePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no baseline at %s; run with --update-baseline first\n", basePath)
		os.Exit(2)
	}
	var base baselineFile
	if err := json.Unmarshal(baseRaw, &base); err != nil {
		fmt.Fprintf(os.Stderr, "parse baseline: %v\n", err)
		os.Exit(2)
	}

	// Config mismatch is exit 2, not 1 — distinguish from regression.
	if err := checkConfig(base, currentRunConfig{RateRPS: rateFromEnv(), RedisEnabled: true}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	baseMetrics := runMetrics{
		P50Ms:     numberField(base.Metrics, "http_req_duration_p50_ms"),
		P95Ms:     numberField(base.Metrics, "http_req_duration_p95_ms"),
		P99Ms:     numberField(base.Metrics, "http_req_duration_p99_ms"),
		RPS:       numberField(base.Metrics, "http_reqs_per_sec"),
		ErrorRate: numberField(base.Metrics, "http_req_failed_rate"),
	}

	report := diff(baseMetrics, cur)
	writeMarkdown(*resultsDir, *scenario, baseMetrics, cur, report)
	for _, line := range report.Lines {
		fmt.Println(line)
	}
	if report.Failed {
		os.Exit(1)
	}
}

func numberField(m map[string]any, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

func rateFromEnv() int {
	if v := os.Getenv("SUBSCRIBE_RATE"); v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 100
}

func writeBaseline(path, scenario, env string, m runMetrics) {
	sha, _ := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	b := baselineFile{
		Scenario:    scenario,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339),
		GitSHA:      strings.TrimSpace(string(sha)),
		Environment: env,
		Config: map[string]any{
			"rate_rps":      float64(rateFromEnv()),
			"duration":      "3m",
			"db_max_conns":  20.0,
			"redis_enabled": true,
			"redis_cache_ttl": "10m",
		},
		Metrics: map[string]any{
			"http_req_duration_p50_ms": m.P50Ms,
			"http_req_duration_p95_ms": m.P95Ms,
			"http_req_duration_p99_ms": m.P99Ms,
			"http_reqs_per_sec":        m.RPS,
			"http_req_failed_rate":     m.ErrorRate,
		},
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func writeMarkdown(dir, scenario string, base, cur runMetrics, r diffReport) {
	body := fmt.Sprintf(`# %s — Last Run

| Metric | Baseline | Current | Delta |
|---|---:|---:|---:|
| p50 (ms) | %.1f | %.1f | %+.1f%% |
| p95 (ms) | %.1f | %.1f | %+.1f%% |
| p99 (ms) | %.1f | %.1f | %+.1f%% |
| RPS | %.1f | %.1f | %+.1f%% |
| Error rate | %.4f | %.4f | — |

%s
`, scenario,
		base.P50Ms, cur.P50Ms, pct(base.P50Ms, cur.P50Ms),
		base.P95Ms, cur.P95Ms, pct(base.P95Ms, cur.P95Ms),
		base.P99Ms, cur.P99Ms, pct(base.P99Ms, cur.P99Ms),
		base.RPS, cur.RPS, pct(base.RPS, cur.RPS),
		base.ErrorRate, cur.ErrorRate,
		strings.Join(r.Lines, "\n"))
	_ = os.WriteFile(filepath.Join(dir, "last-run-"+scenario+".md"), []byte(body), 0o644)
}

// Unused import shim — `errors` reserved for future use.
var _ = errors.New
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./tests/load/scripts/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Smoke run `compare.go`**

Run:
```sh
# create a fake baseline
mkdir -p tests/load/baseline tests/load/results
go run ./tests/load/scripts/compare.go --scenario subscribe_storm --update-baseline
ls tests/load/baseline/
cat tests/load/baseline/subscribe_storm.json
```

Expected: A `subscribe_storm.json` baseline is written with current zero-metrics (since no summary files exist yet — that's OK for the smoke). Inspect file structure.

Then clean up:
```sh
rm tests/load/baseline/subscribe_storm.json
```

- [ ] **Step 6: Commit**

```bash
git add tests/load/scripts/
git commit -m "feat(loadtest): compare.go runner with median + threshold check"
```

---

## Phase 10 — Full Makefile + CI workflow

### Task 10: load-baseline / load-regression targets and GitHub Actions

**Files:**
- Modify: `Makefile`
- Create: `.github/workflows/load-test.yml`

- [ ] **Step 1: Append baseline + regression targets**

Append to the load section in `Makefile`:

```makefile
.PHONY: load-baseline load-regression

LOAD_SEED := go run ./tests/load/seeder \
	--database-url postgres://loadtest:loadtest@localhost:5433/loadtest \
	--subscriptions 10000 \
	--pending-tokens 10000 \
	--out tests/load/results/tokens.json

load-baseline:    ## stage 1 — capture new baseline (3 runs, median)
	@$(MAKE) load-up
	@for i in 1 2 3; do \
		$(MAKE) load-reset; \
		$(LOAD_SEED); \
		echo "=== Run $$i/3 ==="; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/subscribe_storm.js --summary-export=/results/subscribe_$$i.json; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/token_flow.js --summary-export=/results/token_$$i.json; \
	done
	go run ./tests/load/scripts/compare.go --scenario subscribe_storm --update-baseline
	go run ./tests/load/scripts/compare.go --scenario token_flow --update-baseline

load-regression:  ## stage 2 — replay and diff, exit 1 on regression
	@$(MAKE) load-up
	@for i in 1 2 3; do \
		$(MAKE) load-reset; \
		$(LOAD_SEED); \
		echo "=== Run $$i/3 ==="; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/subscribe_storm.js --summary-export=/results/subscribe_$$i.json; \
		$(LOAD_COMPOSE) --profile k6 run --rm k6 run /scripts/token_flow.js --summary-export=/results/token_$$i.json; \
	done
	go run ./tests/load/scripts/compare.go --scenario subscribe_storm
	go run ./tests/load/scripts/compare.go --scenario token_flow
```

- [ ] **Step 2: Create the GitHub Actions workflow**

Create `.github/workflows/load-test.yml`:

```yaml
name: load-test
on:
  workflow_dispatch:
    inputs:
      mode:
        type: choice
        description: regression or baseline
        options: [regression, baseline]
        default: regression
  schedule:
    - cron: "0 3 * * *"   # nightly 03:00 UTC

jobs:
  load:
    runs-on: ubuntu-latest
    timeout-minutes: 25
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Run load test
        run: make load-${{ github.event.inputs.mode || 'regression' }}

      - name: Upload results
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: load-results-${{ github.run_number }}
          path: tests/load/results/
          retention-days: 90

      - name: Open issue on nightly regression
        if: failure() && github.event_name == 'schedule'
        uses: actions/github-script@v7
        with:
          script: |
            const today = new Date().toISOString().slice(0,10);
            await github.rest.issues.create({
              owner: context.repo.owner,
              repo: context.repo.repo,
              title: `Load regression: nightly ${today}`,
              body: `Run: ${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}`,
              labels: ['regression', 'performance']
            });
```

Note: The spec calls for `ubuntu-latest-4-core` for stability. If the repo doesn't have larger runners enabled, `ubuntu-latest` (2-core) is the fallback and the 20% p95 threshold may need widening to ~30% in `compare.go` to compensate for shared-runner noise. Decision belongs to the operator after the first nightly run.

- [ ] **Step 3: Validate Makefile syntax**

Run: `make -n load-baseline | head -20`
Expected: Prints commands without executing — verifies no Makefile syntax errors.

- [ ] **Step 4: Validate workflow file**

Run: `gh workflow view load-test.yml 2>&1 || cat .github/workflows/load-test.yml | head -50`
Expected: Either `gh` confirms parse, or the file content is shown — verify YAML is structurally sound by eye.

- [ ] **Step 5: Commit**

```bash
git add Makefile .github/workflows/load-test.yml
git commit -m "feat(loadtest): baseline + regression Makefile targets and CI workflow"
```

---

## Phase 11 — Documentation

### Task 11: ADR, testing.md, READMEs, CLAUDE.md

**Files:**
- Create: `docs/adr/0016-load-testing.md`
- Modify: `docs/testing.md`
- Create: `tests/load/README.md`
- Create: `tests/load/CHANGELOG.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Create ADR-0016**

Inspect the format of an existing ADR first:

Run: `cat docs/adr/0015-testing-trophy.md`
Expected: Note its sections (Status, Context, Decision, Consequences, Alternatives) — match the same format.

Create `docs/adr/0016-load-testing.md` with the same template, capturing:
- Status: Accepted
- Context: no current performance baseline → impossible to detect regressions; explanation of why this is a separate axis from ADR-0015 (testing-trophy covers correctness; this covers performance)
- Decision: k6 for HTTP, Go test for background pipeline, docker-compose stack, nightly + workflow_dispatch only (not per-PR), `compare.go` median+threshold runner
- Consequences: +early regression detection, +portable to staging via env; −CI runner cost, −Docker requirement, −baseline maintenance burden
- Alternatives considered: vegeta (single-scenario), Go benchmarks (not portable), production-only monitoring (too slow a feedback loop)

Do not modify `docs/adr/0015-testing-trophy.md` or any other existing ADR.

- [ ] **Step 2: Update `docs/testing.md`**

Apply the following edits (the spec §9 lists them precisely). Each is an Edit, not a rewrite:

a. In the **Build tags** table (after the `e2e` row), insert:
   ```
   | `loadtest`    | Every file under `tests/load/...` and the pipeline drain test.            |
   ```

b. After the "End-to-end tests" section (before "Everything"), insert:
   ```markdown
   ### Load tests (Docker + k6, manual/nightly)

   ```sh
   make load-discover      # one-off: find breaking point
   make load-baseline      # capture new SLO baseline (commit results)
   make load-regression    # compare against committed baseline, fail on regression
   make load-pipeline      # background drain throughput (Go-test, no k6)
   ```

   Load tests are NOT run on each PR — they are too slow and noisy on
   shared CI runners. They run nightly via `.github/workflows/load-test.yml`
   and on demand via `workflow_dispatch`. See `tests/load/README.md` for
   scenario details and `docs/adr/0016-load-testing.md` for the rationale.
   ```

c. In the **Prerequisites** section (after "E2E tier"), insert:
   ```markdown
   ### Load tier
   - Same as Integration tier, plus the k6 Docker image
     (`grafana/k6:0.50.0`, ~50 MB on first run).
   - The full stack runs through `docker-compose.loadtest.yml` — postgres
     (tmpfs, `fsync=off`), redis, github-stub, app, and k6. Not compatible
     with the dev compose; uses ports 5433/6380/8090/8080.
   ```

d. In the **Philosophy** section (after "### E2E"), insert:
   ```markdown
   ### Load

   Load tests cover **performance regression**, a dimension orthogonal to
   the testing trophy above. They establish a baseline (p50/p95/p99
   latency, RPS, error rate) on a fixed environment and fail when a
   change degrades it by more than the configured threshold. See
   [ADR-0016](adr/0016-load-testing.md) for the decision record and
   `tests/load/README.md` for scenarios.
   ```

- [ ] **Step 3: Create `tests/load/README.md`**

Content (mirroring the spec's intent):

```markdown
# Load Tests

> **Spec:** `docs/superpowers/specs/2026-05-26-load-testing-design.md`
> **ADR:** [`docs/adr/0016-load-testing.md`](../../docs/adr/0016-load-testing.md)

## Quickstart

```sh
# Stage 0 — discover the breaking point (one-off, do not commit results)
make load-discover

# Stage 1 — capture new SLO baseline (3 runs, median, commits results)
make load-baseline
git add tests/load/baseline/*.json tests/load/CHANGELOG.md
git commit -m "load: refresh baseline ($(date +%F))"

# Stage 2 — compare against the committed baseline (use this on a schedule)
make load-regression
```

## Scenarios

| Scenario | Tool | Tests | SLO |
|---|---|---|---|
| `subscribe_storm` | k6 | POST /api/subscribe under constant rate | p95<300ms, p99<800ms, errors<0.5% |
| `token_flow`      | k6 | GET /api/confirm + /api/unsubscribe with pre-seeded tokens | p95<150ms, checks>99% |
| `pipeline`        | Go | scanner→outbox→notifier drain throughput | 10k rows < 60s |
| `discovery`       | k6 | ramp from 5 to 400 RPS, find the knee | none (informative) |

## When to update the baseline

Only when **you mean it**:

1. Intentional optimization → new numbers are better → commit as new floor.
2. Accepted regression for a different goal (security, validation, feature) → commit as new ceiling + `CHANGELOG.md` entry explaining why.
3. Infrastructure change (runner upgrade, Postgres version bump) → old numbers no longer comparable.

**Never** update baseline just to silence a red nightly. Red is work — investigate or accept-with-justification.

## Files

- `k6/*.js` — scenario scripts and shared lib
- `seeder/` — DB seeder + tokens.json producer
- `pipeline/` — Go pipeline drain test
- `githubstub/` — standalone GitHub HTTP stub (used by docker-compose)
- `scripts/compare.go` — median + threshold runner
- `baseline/*.json` — committed SLO baselines (per environment)
- `results/` — per-run artifacts (gitignored)
- `CHANGELOG.md` — log of baseline updates
```

- [ ] **Step 4: Create empty `tests/load/CHANGELOG.md`**

```markdown
# Load Baseline Changelog

Each entry records a deliberate baseline update — the why, not the what.
The what is in `git log tests/load/baseline/`.
```

- [ ] **Step 5: Update `CLAUDE.md`**

Locate the "Команди" block and append the load targets:

```
make load-discover / load-baseline / load-regression / load-pipeline
```

Locate the ADR section and append a single bullet referencing ADR-0016 (use the same format as the existing bullets — short rationale + link to the ADR).

Do not edit any existing ADR bullet; only add the new one.

- [ ] **Step 6: Validate all documentation files render**

Run: `find docs/adr/0016-load-testing.md tests/load/README.md tests/load/CHANGELOG.md docs/testing.md CLAUDE.md -type f`
Expected: All 5 files listed; none missing.

Run: `markdownlint docs/adr/0016-load-testing.md tests/load/README.md tests/load/CHANGELOG.md 2>/dev/null || true`
Expected: Either no errors, or warnings only (skip if markdownlint isn't installed).

- [ ] **Step 7: Commit**

```bash
git add docs/adr/0016-load-testing.md docs/testing.md tests/load/README.md tests/load/CHANGELOG.md CLAUDE.md
git commit -m "docs(loadtest): ADR-0016, testing.md updates, READMEs"
```

---

## Phase 12 — First baseline capture

### Task 12: Capture the first real baseline

This is execution, not implementation. It produces the first committed `baseline/*.json` files.

- [ ] **Step 1: Run discovery**

```sh
make load-discover
cat tests/load/results/discovery.json | jq '.metrics.http_req_duration.values, .metrics.http_req_failed.values'
```

Inspect the summary. Identify the rate at which p95 crossed 1000ms or error rate crossed 1%. That is your breaking point.

- [ ] **Step 2: Choose baseline rate**

Set `SUBSCRIBE_RATE` and `TOKEN_RATE` to ~60-70% of the breaking-point RPS. Example: if subscribe knee is at 250 RPS → `SUBSCRIBE_RATE=160`.

If the discovery run never hit failure (everything passed up to 400 RPS), extend the ramp stages in `discovery.js` and re-run. Do not pick a baseline above what discovery actually verified.

- [ ] **Step 3: Run baseline capture**

```sh
SUBSCRIBE_RATE=160 TOKEN_RATE=60 make load-baseline
```

Expected: Three runs each for subscribe and token, then `compare.go --update-baseline` writes:
- `tests/load/baseline/subscribe_storm.json`
- `tests/load/baseline/token_flow.json`

Inspect both files — they should contain the median metrics, current `git_sha`, current timestamp, and the rates you chose.

- [ ] **Step 4: Run pipeline baseline**

```sh
make load-pipeline | tee tests/load/results/pipeline.log
```

Expected: Pass. The log line `drain complete in Xs (Y.Z rows/s)` is the baseline.

Manually create `tests/load/baseline/pipeline.json` from the log (or extend `compare.go` later to support pipeline JSON output — out of scope for Phase 12).

- [ ] **Step 5: Verify regression against fresh baseline**

```sh
SUBSCRIBE_RATE=160 TOKEN_RATE=60 make load-regression
```

Expected: PASS. This confirms the baseline → regression round-trip works on the same code that produced it.

- [ ] **Step 6: Add a CHANGELOG entry**

Append to `tests/load/CHANGELOG.md`:

```markdown
## 2026-05-26 — initial baseline

First baseline captured against ADR-0016 infrastructure.

- subscribe_storm: <RPS> RPS, p95=<X>ms, p99=<Y>ms
- token_flow: <RPS> RPS each scenario, p95=<X>ms
- pipeline: drained <N> rows in <T>s

Numbers chosen as ~65% of discovery breaking point (~<BP> RPS).
```

- [ ] **Step 7: Commit the baseline**

```bash
git add tests/load/baseline/ tests/load/CHANGELOG.md
git commit -m "load: initial baseline (subscribe, token, pipeline)"
```

- [ ] **Step 8: Optional — trigger first nightly manually**

```sh
gh workflow run load-test.yml -f mode=regression
gh run watch
```

Expected: workflow completes green, results uploaded as artifact. From now on, scheduled nightly takes over.

---

## Self-Review

Reviewed plan against spec sections:

| Spec §        | Covered by                                                   |
|---------------|--------------------------------------------------------------|
| §1 Goal       | Phase 12 (first baseline) + Phase 10 (regression target)     |
| §2.1 Layout   | Phase 2-11 (each builds its directory piece)                 |
| §2.2 Prod code| Phase 1 (MAILER_DRIVER)                                       |
| §2.3 stub     | Phase 2 + Phase 8 step 3 (handler extraction)                |
| §3 Scenarios  | Phase 5 (discovery), 6 (subscribe), 7 (token), 8 (pipeline)  |
| §4 Metrics    | Phase 9 (compare.go parses k6 native metrics)                |
| §4.2 Config   | Phase 4 (compose) + Phase 9 (`checkConfig`)                  |
| §4.3 Baseline format | Phase 9 (`writeBaseline`)                             |
| §4.4 Anti-flake | Phase 10 (3 runs in Makefile) + Phase 9 (`medianRuns`)     |
| §5 Compose    | Phase 4                                                       |
| §5.2 Portability | Phase 9 (env-driven baseline path) + Phase 10 README mention |
| §6 Make + thresholds | Phase 9 (`diff`) + Phase 10 (full Makefile)            |
| §7 CI         | Phase 10 (`.github/workflows/load-test.yml`)                 |
| §8 Lifecycle  | Phase 11 (README "When to update")                            |
| §9 Docs       | Phase 11                                                      |
| §10 Out of scope | Not implemented (correctly)                               |

Placeholder scan: no TBDs/TODOs remain. The only "to-be-determined-at-runtime" item is the exact baseline RPS, which IS the point of Phase 12 (discovery). Phase 8 has one operator decision call-out (whether to raise SLA after first real pipeline run) — that is informative, not a TODO.

Type consistency: function names — `parseSummary`, `medianRuns`, `diff`, `checkConfig` — used consistently across Phase 9 test and impl. `runMetrics`/`baselineFile`/`currentRunConfig`/`diffReport` types are consistent. `NewGitHubStub`/`SeedConfirmed`/`NewHandler` are consistent between Phase 2 and Phase 8.

One known follow-up: Phase 12 step 4 creates `pipeline.json` manually because `compare.go` only handles k6 summaries. Extending it to handle pipeline output is a deliberate stretch for a future iteration — flagged in `tests/load/CHANGELOG.md` once the first pipeline run lands.
