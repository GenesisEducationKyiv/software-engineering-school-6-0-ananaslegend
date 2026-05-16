# Unit-Test Coverage Backfill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close seven coverage gaps from review by adding focused unit tests around `tokens.GenerateToken`, the email-template renderer, the Resend HTTP client, env-config parsing, the request-logger middleware, the handler's `errorStatus` mapping, and `pkg/transactor` (the only one needing a real Postgres testcontainer).

**Architecture:** Six new pure-unit `_test.go` files live next to the code under test and run under `make test` (no Docker). One file under `pkg/transactor/` carries `//go:build integration` and reuses the existing `tests/internal.NewPostgres` helper. The Resend client gets a thin test seam (`NewResendMailerWithClient`) so the resend-go SDK can be redirected at a `httptest.Server` via its existing `BaseURL`/`NewCustomClient` API.

**Tech Stack:** Go 1.26, `stretchr/testify`, `chi`, `zerolog`, `kelseyhightower/envconfig`, `resend-go/v2`, `pgx/v5`, existing `tests/internal` shared helpers.

---

## Reference docs

- **Design spec:** `docs/superpowers/specs/2026-05-15-unit-test-coverage-design.md`
- **Project conventions:** `CLAUDE.md` (error wrapping, param objects, no auto-commit per user memory).
- **Testing strategy:** `docs/adr/0011-testing-strategy.md` (real Postgres via testcontainers).
- **Transactor contract:** `docs/adr/0004-transactor-via-context.md`.

---

## Task 1: `domain.GenerateToken` — length, charset, uniqueness, parallel

**Files:**
- Create: `internal/subscription/domain/tokens_test.go`

- [ ] **Step 1: Write the tests**

```go
package domain

import (
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestGenerateToken_LengthAndCharset(t *testing.T) {
	tok, err := GenerateToken()
	require.NoError(t, err)
	assert.True(t, tokenRe.MatchString(tok),
		"token %q must be 43 chars of base64url alphabet", tok)
}

func TestGenerateToken_UniqueAcross10k(t *testing.T) {
	const n = 10_000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		tok, err := GenerateToken()
		require.NoError(t, err)
		_, dup := seen[tok]
		require.False(t, dup, "collision at iteration %d: %q", i, tok)
		seen[tok] = struct{}{}
	}
}

func TestGenerateToken_ParallelSafety(t *testing.T) {
	const workers = 50
	const perWorker = 200
	var mu sync.Mutex
	seen := make(map[string]struct{}, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				tok, err := GenerateToken()
				require.NoError(t, err)
				mu.Lock()
				_, dup := seen[tok]
				seen[tok] = struct{}{}
				mu.Unlock()
				require.False(t, dup, "concurrent collision: %q", tok)
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run, verify all three pass**

Run: `go test -run TestGenerateToken ./internal/subscription/domain/`
Expected: `ok ... 0.0Xs` (10 000 + 10 000 generations finish in well under a second).

If any fails: the implementation in `tokens.go` may have regressed; do not adjust the test to fit — fix the implementation. The regex is pinned to `^[A-Za-z0-9_-]{43}$` per `base64.RawURLEncoding(rand[32])`.

- [ ] **Step 3: DO NOT COMMIT**

Leave changes in working tree.

---

## Task 2: `emailer.tmpl` — render correctness and HTML escaping

**Files:**
- Create: `internal/notifier/emailer/tmpl_test.go`

The templates are package-private (`confirmationHTMLTmpl`, `releaseHTMLTmpl`, etc.). The test file lives in `package emailer` (same package, no `_test` suffix) so it can `Execute` them directly.

- [ ] **Step 1: Write the tests**

```go
package emailer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func TestConfirmationHTML_ContainsURLAndRepo(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, confirmationHTMLTmpl.Execute(&buf, domain.SendConfirmationParams{
		To:           "u@example.com",
		ConfirmURL:   "https://app.test/confirm/abc",
		RepoFullName: "golang/go",
	}))
	body := buf.String()
	assert.Contains(t, body, "https://app.test/confirm/abc")
	assert.Contains(t, body, "golang/go")
}

func TestReleaseHTML_ContainsTagAndReleaseURL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, releaseHTMLTmpl.Execute(&buf, domain.SendReleaseParams{
		To:             "u@example.com",
		RepoFullName:   "golang/go",
		ReleaseTag:     "v1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/v1.22.0",
		UnsubscribeURL: "https://app.test/unsubscribe/xyz",
	}))
	body := buf.String()
	assert.Contains(t, body, "v1.22.0")
	assert.Contains(t, body, "https://github.com/golang/go/releases/tag/v1.22.0")
	assert.Contains(t, body, "https://app.test/unsubscribe/xyz")
}

func TestReleaseHTML_EscapesHTMLInRepoName(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, releaseHTMLTmpl.Execute(&buf, domain.SendReleaseParams{
		RepoFullName: "<script>alert(1)</script>",
		ReleaseTag:   "v1",
		ReleaseURL:   "https://example.test",
	}))
	body := buf.String()
	assert.NotContains(t, body, "<script>alert(1)</script>",
		"raw script tag must be escaped by html/template")
	assert.True(t, strings.Contains(body, "&lt;script&gt;"),
		"expected HTML-escaped repo name in %q", body)
}

func TestConfirmationTXT_ContainsConfirmURL(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, confirmationTXTTmpl.Execute(&buf, domain.SendConfirmationParams{
		ConfirmURL:   "https://app.test/confirm/x",
		RepoFullName: "a/b",
	}))
	assert.Contains(t, buf.String(), "https://app.test/confirm/x")
}
```

- [ ] **Step 2: Run, verify all four pass**

Run: `go test -run "TestConfirmationHTML|TestReleaseHTML|TestConfirmationTXT" ./internal/notifier/emailer/`
Expected: PASS.

If `TestReleaseHTML_EscapesHTMLInRepoName` fails: the template was switched to `text/template`, which would NOT auto-escape — that is the regression this test exists to catch.

- [ ] **Step 3: DO NOT COMMIT**

---

## Task 3: `emailer.ResendMailer` — test seam + httptest mock

This task has TWO code changes: a tiny constructor refactor in `resend.go` for testability, and the test file.

**Files:**
- Modify: `internal/notifier/emailer/resend.go`
- Create: `internal/notifier/emailer/resend_test.go`

- [ ] **Step 1: Refactor `resend.go` to add `NewResendMailerWithClient` seam**

Open `internal/notifier/emailer/resend.go`. Replace the existing `NewResendMailer` with this pair:

```go
// NewResendMailer wires the real Resend HTTP client against the public API.
// Production callers stay unchanged.
func NewResendMailer(apiKey, from string) *ResendMailer {
	return NewResendMailerWithClient(resend.NewClient(apiKey), from)
}

// NewResendMailerWithClient is the seam unit tests use to inject a Resend
// client whose BaseURL points at a httptest.Server. Production code does not
// call this directly — use NewResendMailer.
func NewResendMailerWithClient(client *resend.Client, from string) *ResendMailer {
	return &ResendMailer{
		client: client,
		from:   from,
	}
}
```

Everything else in the file (the two `Send*` methods) is unchanged.

- [ ] **Step 2: Verify the package still compiles**

Run: `go build ./internal/notifier/emailer/...`
Expected: no output (success).

- [ ] **Step 3: Write the test**

Create `internal/notifier/emailer/resend_test.go`:

```go
package emailer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/resend/resend-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

// resendMailerAgainst spins up a fake Resend endpoint at srv and returns a
// ResendMailer whose internal client posts there. Re-uses the SDK's
// NewCustomClient + exported BaseURL — no monkey-patching of globals.
func resendMailerAgainst(t *testing.T, srv *httptest.Server, from string) *ResendMailer {
	t.Helper()
	c := resend.NewCustomClient(srv.Client(), "test-key")
	parsed, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	c.BaseURL = parsed
	return NewResendMailerWithClient(c, from)
}

func TestResend_SendConfirmation_PostsExpectedPayload(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/emails", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000"}`))
	}))
	t.Cleanup(srv.Close)

	m := resendMailerAgainst(t, srv, "noreply@app.test")
	require.NoError(t, m.SendConfirmation(context.Background(), domain.SendConfirmationParams{
		To:           "u@example.com",
		ConfirmURL:   "https://app.test/confirm/abc",
		RepoFullName: "golang/go",
	}))

	assert.Equal(t, "noreply@app.test", captured["from"])
	assert.Equal(t, []any{"u@example.com"}, captured["to"])
	subj, _ := captured["subject"].(string)
	assert.Contains(t, subj, "golang/go")
	assert.Contains(t, subj, "Confirm")
	html, _ := captured["html"].(string)
	assert.Contains(t, html, "https://app.test/confirm/abc")
	text, _ := captured["text"].(string)
	assert.NotEmpty(t, text, "plain-text fallback must be set")
}

func TestResend_SendRelease_PostsExpectedPayload(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000"}`))
	}))
	t.Cleanup(srv.Close)

	m := resendMailerAgainst(t, srv, "noreply@app.test")
	require.NoError(t, m.SendRelease(context.Background(), domain.SendReleaseParams{
		To:             "u@example.com",
		RepoFullName:   "golang/go",
		ReleaseTag:     "v1.22.0",
		ReleaseURL:     "https://github.com/golang/go/releases/tag/v1.22.0",
		UnsubscribeURL: "https://app.test/unsubscribe/xyz",
	}))

	subj, _ := captured["subject"].(string)
	assert.Contains(t, subj, "v1.22.0")
	assert.Contains(t, subj, "golang/go")
	html, _ := captured["html"].(string)
	assert.Contains(t, html, "https://github.com/golang/go/releases/tag/v1.22.0")
	assert.Contains(t, html, "https://app.test/unsubscribe/xyz")
}

func TestResend_SendConfirmation_PropagatesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"bad from"}`))
	}))
	t.Cleanup(srv.Close)

	m := resendMailerAgainst(t, srv, "bogus")
	err := m.SendConfirmation(context.Background(), domain.SendConfirmationParams{
		To: "u@example.com", ConfirmURL: "x", RepoFullName: "a/b",
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "send confirmation email"),
		"expected wrapped error from SendConfirmation, got %q", err)
}
```

- [ ] **Step 4: Run, verify all three pass**

Run: `go test -run TestResend ./internal/notifier/emailer/`
Expected: PASS, ~5 ms per test.

If `TestResend_SendConfirmation_PostsExpectedPayload` fails on the `subject` assertion, double-check the exact subject format in `resend.go` — it is `fmt.Sprintf("Confirm your subscription to %s", p.RepoFullName)` so both `"Confirm"` and `"golang/go"` are substrings.

- [ ] **Step 5: DO NOT COMMIT**

---

## Task 4: `config.Load` — env parsing semantics

**Files:**
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the tests**

```go
package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateEnv clears every variable Config reads. t.Setenv records the
// original and restores on Cleanup, so the parent shell environment cannot
// mask a failure.
func isolateEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"HTTP_ADDR", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
		"DATABASE_URL", "DB_MAX_CONNS", "REDIS_URL",
		"APP_BASE_URL", "CONFIRM_TOKEN_TTL",
		"LOG_LEVEL", "LOG_PRETTY",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_FROM", "SMTP_TLS_POLICY",
		"RESEND_API_KEY", "RESEND_FROM",
		"GITHUB_TOKEN",
		"SCANNER_INTERVAL", "NOTIFIER_INTERVAL", "CONFIRMER_INTERVAL",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	isolateEnv(t)
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_URL")
}

func TestLoad_AppliesDefaultsWhenOnlyRequiredSet(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/x?sslmode=disable")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "http://localhost:8080", cfg.AppBaseURL)
	assert.Equal(t, 24*time.Hour, cfg.ConfirmTokenTTL)
	assert.Equal(t, int32(10), cfg.DBMaxConns)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, "starttls", cfg.SMTPTLSPolicy)
	assert.Empty(t, cfg.RedisURL, "REDIS_URL must default to empty (silent fallback)")
	assert.Empty(t, cfg.GitHubToken, "GITHUB_TOKEN may be empty (WARN path lives elsewhere)")
}

func TestLoad_RedisURLOptional(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.RedisURL)
}

func TestLoad_InvalidDurationRejected(t *testing.T) {
	isolateEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	t.Setenv("CONFIRM_TOKEN_TTL", "twenty-four-hours")
	_, err := Load()
	require.Error(t, err)
}
```

- [ ] **Step 2: Run, verify all four pass**

Run: `go test -run TestLoad ./internal/config/`
Expected: PASS.

If `TestLoad_RequiresDatabaseURL` errors out without mentioning `DATABASE_URL`, `kelseyhightower/envconfig` may have changed its message format — relax the assertion to `assert.Error(t, err)` only after verifying the error is actually triggered by the missing var.

- [ ] **Step 3: DO NOT COMMIT**

---

## Task 5: `pkg/transactor` — testcontainer-backed integration tests

**Files:**
- Create: `pkg/transactor/transactor_test.go`

This task uses the existing `tests/internal.NewPostgres` helper, which carries `//go:build integration || e2e`. The new file therefore carries `//go:build integration`.

- [ ] **Step 1: Write the tests**

```go
//go:build integration

package transactor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/pkg/transactor"
	"github.com/ananaslegend/reposeetory/tests/internal"
)

// tname sanitises t.Name() into a valid SQL identifier suffix so each
// test method gets its own scratch table without colliding.
func tname(t *testing.T) string {
	out := make([]rune, 0, len(t.Name()))
	for _, r := range t.Name() {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// newScratchTable creates a single-column table and registers a DROP via
// t.Cleanup. Uses the pool directly (no Tx).
func newScratchTable(ctx context.Context, t *testing.T, pool transactor.Conn) string {
	name := "tx_scratch_" + tname(t)
	_, err := pool.Exec(ctx, "CREATE TABLE "+name+" (id INT PRIMARY KEY)")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+name) })
	return name
}

func TestWithinTransaction_CommitsOnSuccess(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)
	tbl := newScratchTable(ctx, t, pg.Pool)

	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		conn := transactor.ConnFromContext(ctx, pg.Pool)
		_, err := conn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
		return err
	})
	require.NoError(t, err)

	var n int
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+tbl).Scan(&n))
	assert.Equal(t, 1, n, "row must be visible after commit")
}

func TestWithinTransaction_RollsBackOnError(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)
	tbl := newScratchTable(ctx, t, pg.Pool)

	sentinel := errors.New("fn failed")
	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		conn := transactor.ConnFromContext(ctx, pg.Pool)
		_, _ = conn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	var n int
	require.NoError(t, pg.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+tbl).Scan(&n))
	assert.Zero(t, n, "row must NOT be visible after rollback")
}

func TestWithinTransaction_NestedStartsSeparateTransaction(t *testing.T) {
	// Documents the current contract: each WithinTransaction call begins
	// a fresh tx on the underlying pool; the inner call does NOT join the
	// outer one (see pkg/transactor/transactor.go — pool.BeginTx is called
	// every time). If anyone ever switches to savepoint semantics, this
	// test will fail loudly — that's the point.
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)
	tbl := newScratchTable(ctx, t, pg.Pool)

	outerErr := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		outerConn := transactor.ConnFromContext(ctx, pg.Pool)
		_, err := outerConn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
		require.NoError(t, err)

		innerErr := tx.WithinTransaction(ctx, func(ctx context.Context) error {
			innerConn := transactor.ConnFromContext(ctx, pg.Pool)
			_, err := innerConn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (2)")
			return err
		})
		require.NoError(t, innerErr)
		return errors.New("rollback outer")
	})
	require.Error(t, outerErr)

	var ids []int
	rows, err := pg.Pool.Query(ctx, "SELECT id FROM "+tbl+" ORDER BY id")
	require.NoError(t, err)
	for rows.Next() {
		var id int
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	rows.Close()
	assert.Equal(t, []int{2}, ids,
		"inner tx commits independently; outer rolls back its own row")
}

func TestConnFromContext_NoTxInContext_ReturnsPool(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)

	got := transactor.ConnFromContext(ctx, pg.Pool)
	var one int
	require.NoError(t, got.QueryRow(ctx, "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)
}

func TestConnFromContext_TxInContext_ReturnsTxNotPool(t *testing.T) {
	ctx := context.Background()
	pg := internal.NewPostgres(ctx, t)
	tx := transactor.New(pg.Pool)

	err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		conn := transactor.ConnFromContext(ctx, pg.Pool)
		_, ok := conn.(pgx.Tx)
		assert.True(t, ok,
			"ConnFromContext inside WithinTransaction must return pgx.Tx, got %T", conn)
		return nil
	})
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run, verify all five pass**

Run: `go test -tags=integration -count=1 ./pkg/transactor/...`
Expected: PASS. Each test starts its own Postgres container (~3-5 s of container setup once `NewPostgres` is called).

- [ ] **Step 3: Sanity-check that other integration tests still pass**

Run: `make test-integration`
Expected: every existing suite still green.

- [ ] **Step 4: DO NOT COMMIT**

---

## Task 6: `httpapi.RequestLogger` — middleware unit test

**Files:**
- Create: `internal/httpapi/middleware_request_logger_test.go`

- [ ] **Step 1: Write the tests**

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/httpapi"
)

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestRequestLogger_EmitsRequestScopedFields(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(httpapi.RequestLogger(logger))
	r.Get("/hello", func(w http.ResponseWriter, r *http.Request) {
		zerolog.Ctx(r.Context()).Info().Msg("from handler")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/hello")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	lines := splitNonEmpty(buf.String())
	require.GreaterOrEqual(t, len(lines), 2, "expected at least one handler log + the final request line")

	finalLine := lines[len(lines)-1]
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(finalLine), &rec))

	assert.NotEmpty(t, rec["request_id"], "request_id must be present")
	assert.Equal(t, "GET", rec["method"])
	assert.Equal(t, "/hello", rec["path"])
	assert.Equal(t, float64(http.StatusTeapot), rec["status"])
	assert.Equal(t, float64(2), rec["bytes"])
	assert.Contains(t, rec, "duration")
	assert.Equal(t, "request", rec["message"])
}

func TestRequestLogger_HandlerLogsCarryRequestID(t *testing.T) {
	var buf bytes.Buffer
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(httpapi.RequestLogger(zerolog.New(&buf)))
	r.Get("/x", func(w http.ResponseWriter, r *http.Request) {
		zerolog.Ctx(r.Context()).Info().Msg("inside handler")
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := srv.Client().Get(srv.URL + "/x")
	require.NoError(t, err)
	_ = resp.Body.Close()

	lines := splitNonEmpty(buf.String())
	require.NotEmpty(t, lines, "handler log line must be emitted")

	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.NotEmpty(t, first["request_id"], "handler log must inherit request_id from middleware")
	assert.Equal(t, "/x", first["path"])
}
```

- [ ] **Step 2: Run, verify both pass**

Run: `go test -run TestRequestLogger ./internal/httpapi/`
Expected: PASS, ~10 ms.

- [ ] **Step 3: DO NOT COMMIT**

---

## Task 7: `subscription/http.errorStatus` — sentinel → status mapping

`errorStatus` is package-private, so the test lives in `package http` (no `_test` suffix). `BadRequestError` and its constructor `errBadRequest(msg)` are also package-private; use `errBadRequest("…")` rather than instantiating the struct (its field `msg` is lowercase).

**Files:**
- Create: `internal/subscription/http/error_status_test.go`

- [ ] **Step 1: Write the test**

```go
package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func TestErrorStatus_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"invalid repo format", domain.ErrInvalidRepoFormat, http.StatusBadRequest},
		{"bad-request error type", errBadRequest("bad email"), http.StatusBadRequest},
		{"repo not found", domain.ErrRepoNotFound, http.StatusNotFound},
		{"token not found", domain.ErrTokenNotFound, http.StatusNotFound},
		{"already exists", domain.ErrAlreadyExists, http.StatusConflict},
		{"token expired", domain.ErrTokenExpired, http.StatusGone},
		{"unrelated error", errors.New("kaboom"), http.StatusInternalServerError},
		{"wrapped sentinel still matches", wrap(domain.ErrTokenExpired), http.StatusGone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, errorStatus(tc.err))
		})
	}
}

// wrap simulates a layer adding context via fmt.Errorf("...: %w", err) so
// the test verifies errors.Is still finds the sentinel.
func wrap(err error) error {
	return &wrappedErr{cause: err}
}

type wrappedErr struct{ cause error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.cause.Error() }
func (w *wrappedErr) Unwrap() error { return w.cause }
```

- [ ] **Step 2: Run, verify all eight sub-tests pass**

Run: `go test -run TestErrorStatus ./internal/subscription/http/`
Expected: PASS.

If any sub-test fails, do not adjust the test to fit — investigate the live `errorStatus` switch and the imported sentinels.

- [ ] **Step 3: DO NOT COMMIT**

---

## Task 8: Final sweep

After Tasks 1-7 are all green, run every tier and confirm nothing regressed.

- [ ] **Step 1: Unit tier**

Run: `make test`
Expected: every package passes, including the six new pure-unit files.

- [ ] **Step 2: Integration tier**

Run: `make test-integration`
Expected: green (existing suites + new `pkg/transactor` tests).

- [ ] **Step 3: Full sweep**

Run: `make test-all`
Expected: green (unit + integration + e2e from prior work).

- [ ] **Step 4: Lint**

Run: `make lint`
Expected: no new warnings introduced by these files.

- [ ] **Step 5: DO NOT COMMIT**

Per `feedback_no_auto_commit` memory: leave everything in the working tree. The user reviews and stages the changes themselves.

---

## Self-Review Notes

**Spec coverage:**
- §1 tokens → Task 1. ✓
- §2 tmpl → Task 2. ✓
- §3a Resend (with test-seam decision) → Task 3 (path (b) chosen because resend-go v2 exposes `NewCustomClient` + `Client.BaseURL` directly — no transport-swap hacks needed). ✓
- §3b SMTP — deferred, not in plan. ✓
- §4 config → Task 4. ✓
- §5 transactor → Task 5. ✓
- §6 RequestLogger → Task 6. ✓
- §7 errorStatus → Task 7 (with extra wrap-aware sub-test). ✓
- Final sweep → Task 8. ✓

**Type/name consistency:**
- `NewResendMailerWithClient(*resend.Client, string)` — defined in Task 3 step 1, called in Task 3 step 3 via `resendMailerAgainst` helper. Identical signature both places.
- `errBadRequest(string) error` — referenced in Task 7 step 1 as-is from `handler.go`. Verified the helper exists at `handler.go:223`.
- `internal.NewPostgres(ctx, t)` — same signature in Task 5 step 1 as in existing usage across `tests/e2e/` and `tests/integration/`.

**Placeholder scan:** none of "TBD", "TODO", "implement later", "fill in details", "similar to Task N" appear. Every code step contains complete code blocks.

**Tasks correspondence to spec § order:** Tasks 1-7 follow spec order; Task 8 is the final-sweep gate. No spec section lacks a task.
