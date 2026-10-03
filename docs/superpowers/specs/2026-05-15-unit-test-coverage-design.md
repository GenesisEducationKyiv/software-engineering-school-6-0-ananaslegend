# Unit-Test Coverage Backfill — Design Spec

**Date:** 2026-05-15

## Summary

Close the seven coverage gaps flagged by code review. Six are cheap, pure-Go
units that should not require Docker; one is the `pkg/transactor` package,
which must hit a real Postgres via testcontainer per the team's testing
strategy ([ADR-0011](../adr/0011-testing-strategy.md), CLAUDE.md). One item
from the review (full handler unit) is consciously narrowed to a focused
`errorStatus` test — the rest of the handler is exercised by
`tests/integration/subscription/`.

Result: `make test` gains ~7 new `_test.go` files (no Docker) covering
token generation, email-template rendering, the Resend client, env-config
parsing, the request-logger middleware, and the handler's error→status
mapping. `make test-integration` gains one file (`pkg/transactor`) under
the existing `//go:build integration` tag.

## Motivation

The review report listed the gaps explicitly:

| # | Code | Today |
| - | ---- | ----- |
| 1 | `internal/subscription/domain/tokens.go` | 0% — security-critical; integration only checks `NotEmpty`. |
| 2 | `internal/notifier/emailer/tmpl.go` | 0% — template regressions detected only via E2E (slow). |
| 3a | `internal/notifier/emailer/resend.go` | 0% — third-party SDK call with no unit fakes. |
| 3b | `internal/notifier/emailer/smtp.go` | 0% — deferred (Mailpit integration suite already covers SMTP). |
| 4 | `internal/config/config.go` | 0% — env-parsing semantics undocumented in tests. |
| 5 | `pkg/transactor/transactor.go` | 0% — ADR-0004 core, has zero direct coverage. |
| 6 | `internal/httpapi/middleware.go RequestLogger` | only `metrics` middleware tested; logger middleware untested. |
| 7 | `internal/subscription/http/handler.go errorStatus` | 0% unit — integration tests exercise it through full HTTP stack. |

The first three layers all live in pure-Go territory; only the transactor
needs a database connection. Item 3b is deferred (covered via existing
`tests/integration/crons/` suite). Item 7 is narrowed to the
sentinel-error → status branch table — that is the only piece of the
handler that has non-trivial pure logic; the rest is `chi` routing and
JSON marshalling already exercised by integration tests.

## Scope and Build-Tag Strategy

Pure-unit additions live next to the code under test, untagged, so they
flow through `make test`:

```
internal/subscription/domain/tokens_test.go             // new
internal/notifier/emailer/tmpl_test.go                  // new
internal/notifier/emailer/resend_test.go                // new
internal/config/config_test.go                          // new
internal/httpapi/middleware_request_logger_test.go      // new
internal/subscription/http/error_status_test.go         // new
```

Transactor tests need a real Postgres, so they go behind the existing
integration tag and reuse the shared `tests/internal.NewPostgres`:

```
pkg/transactor/transactor_test.go   // new, //go:build integration
```

CI implications: `make test` picks up six new files automatically. CI's
"verify vendor in sync" step is unaffected (no new imports beyond
`tests/internal` and the standard library). `make test-integration`
gains one file; `make test-all` (with the e2e tag from a previous PR) is
the full sweep.

## Per-File Test Plan

### 1. `internal/subscription/domain/tokens_test.go` (pure unit)

`GenerateToken()` returns `base64.RawURLEncoding(rand[32]) → 43-char
string` over the alphabet `[A-Za-z0-9_-]`.

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
    assert.True(t, tokenRe.MatchString(tok), "token %q does not match base64url-43", tok)
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

**Why these three:** length+charset catches a byte-truncation regression
(a one-byte token would slip past current `NotEmpty`); the 10 000-sample
uniqueness check exposes weak entropy if `crypto/rand` ever gets swapped
for `math/rand`; the parallel test guards against accidental shared state
in a future caching wrapper.

### 2. `internal/notifier/emailer/tmpl_test.go` (pure unit, same package)

Templates are package-private vars (`confirmationHTMLTmpl` etc.). The
test file lives in `package emailer` (no `_test` suffix) so it can call
`Execute` directly without exporting anything.

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
    assert.NotContains(t, body, "<script>alert(1)</script>", "raw script tag must be escaped")
    assert.True(t,
        strings.Contains(body, "&lt;script&gt;") || strings.Contains(body, "&lt;script&gt;alert"),
        "expected HTML-escaped repo name in %q", body)
}

func TestConfirmationTXT_NotEmpty(t *testing.T) {
    var buf bytes.Buffer
    require.NoError(t, confirmationTXTTmpl.Execute(&buf, domain.SendConfirmationParams{
        ConfirmURL:   "https://app.test/confirm/x",
        RepoFullName: "a/b",
    }))
    assert.Contains(t, buf.String(), "https://app.test/confirm/x")
}
```

**Why XSS:** `htmltpl` auto-escapes; the test pins that contract so a
future migration to `text/template` (which doesn't escape) is loud.

### 3a. `internal/notifier/emailer/resend_test.go` (pure unit, httptest)

`ResendMailer.client` is `*resend.Client` from `resend-go/v2`. The library
accepts a custom `http.Client`; in v2 the constructor is `resend.NewClient(apiKey)`
and the base URL is internal. Two viable approaches:

**(a) `http.Client` transport swap** — set `resend.NewClient(...).Client = &http.Client{Transport: roundTripperRedirecting(srv.URL)}`. Inspect the
v2 source to confirm the field name and behaviour.

**(b) Inject a wrapped client** — extract an interface like
`type emailSender interface { Send(*resend.SendEmailRequest) (*resend.SendEmailResponse, error) }`
and let `NewResendMailer` accept it.

Recommended: **(a)** if `resend-go/v2` exposes the underlying client
field/transport; falls back to **(b)** only if it does not. The test
spec below assumes (a) and will need to be adjusted (one extra
interface declaration in `resend.go`) if the SDK is hostile to it.

```go
package emailer

import (
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

func TestResend_SendConfirmation_PostsCorrectPayload(t *testing.T) {
    var captured map[string]any
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        require.Equal(t, http.MethodPost, r.Method)
        require.Equal(t, "/emails", r.URL.Path)
        body, _ := io.ReadAll(r.Body)
        require.NoError(t, json.Unmarshal(body, &captured))
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000"}`))
    }))
    t.Cleanup(srv.Close)

    m := newResendMailerForTest(srv.URL, "key", "noreply@app.test")
    require.NoError(t, m.SendConfirmation(context.Background(), domain.SendConfirmationParams{
        To:           "u@example.com",
        ConfirmURL:   "https://app.test/confirm/abc",
        RepoFullName: "golang/go",
    }))

    assert.Equal(t, "noreply@app.test", captured["from"])
    assert.Equal(t, []any{"u@example.com"}, captured["to"])
    assert.Contains(t, captured["subject"], "golang/go")
    assert.Contains(t, captured["html"], "https://app.test/confirm/abc")
    assert.NotEmpty(t, captured["text"])
}

func TestResend_SendConfirmation_PropagatesAPIError(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusBadRequest)
        _, _ = w.Write([]byte(`{"name":"validation_error","message":"bad from"}`))
    }))
    t.Cleanup(srv.Close)

    m := newResendMailerForTest(srv.URL, "key", "bogus")
    err := m.SendConfirmation(context.Background(), domain.SendConfirmationParams{
        To: "u@example.com", ConfirmURL: "x", RepoFullName: "a/b",
    })
    require.Error(t, err)
    assert.True(t, strings.Contains(err.Error(), "send confirmation email"),
        "expected wrapped error, got %q", err)
}
```

`newResendMailerForTest(baseURL, key, from string) *ResendMailer` is a
test-only constructor that wraps `NewResendMailer` and rewrites the
underlying transport (or, in fallback path (b), passes a mock sender).
Its exact signature depends on which path the implementer picks; this
spec only fixes the test cases, not the seam.

**Out of scope:** retries, rate-limit handling, `SendRelease` happy path.
`SendRelease` mirrors `SendConfirmation` 1:1 in `resend.go` — one
duplicate test pair is enough to lock the contract.

### 3b. `emailer/smtp.go` — deferred

Status: keep `tests/integration/crons/confirmer_test.go` as the only
SMTP coverage. SMTP-specific unit testing is hard (the `go-mail`
`Message` object is opaque); the existing Mailpit-based integration
suite asserts every property the reviewer flagged (`From`, `Subject`,
body). Document this decision in the eventual implementation PR.

### 4. `internal/config/config_test.go` (pure unit, `t.Setenv`)

`Config.Load()` runs `godotenv.Load()` (silently ignores missing files)
then `envconfig.Process("", &cfg)`. Tests must:

- Unset all variables of interest with `t.Setenv` (Go-1.17+ idiom
  reverts after the test, ignores parent env in subtests).
- Disable `.env` loading by `t.Setenv("PWD", t.TempDir())` and asserting
  no `.env` exists there.

Note about parallelism: `t.Setenv` precludes `t.Parallel()` — leave the
tests sequential.

```go
package config

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

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
    assert.Empty(t, cfg.GitHubToken, "GITHUB_TOKEN may be empty (WARN path)")
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

// isolateEnv clears every variable the Config struct reads, so the
// process-level environment leaking into the test cannot mask a failure.
// `t.Setenv("KEY", "")` records the original value and restores it on
// cleanup.
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
```

`envconfig`'s `required:"true"` rejects both unset and empty values for
`DATABASE_URL`, which is what we want. If the implementer finds an
upstream change in `kelseyhightower/envconfig` that breaks this, the
first test will fail loudly — that's the canary.

### 5. `pkg/transactor/transactor_test.go` (integration tag, testcontainer)

Build tag: `//go:build integration`. Imports `tests/internal` for the
testcontainer.

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

// helper table created once per test, dropped via t.Cleanup
func newScratchTable(ctx context.Context, t *testing.T, pool transactor.Conn) string {
    name := "tx_scratch_" + tname(t)
    _, err := pool.Exec(ctx, "CREATE TABLE "+name+" (id INT PRIMARY KEY)")
    require.NoError(t, err)
    t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP TABLE "+name) })
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
    // outer one. If we ever switch to savepoint semantics, this test
    // will fail loudly — that's the point.
    ctx := context.Background()
    pg := internal.NewPostgres(ctx, t)
    tx := transactor.New(pg.Pool)
    tbl := newScratchTable(ctx, t, pg.Pool)

    outerErr := tx.WithinTransaction(ctx, func(ctx context.Context) error {
        conn := transactor.ConnFromContext(ctx, pg.Pool)
        _, err := conn.Exec(ctx, "INSERT INTO "+tbl+" (id) VALUES (1)")
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

    // Inner committed independently → row 2 survives, row 1 rolled back.
    var ids []int
    rows, err := pg.Pool.Query(ctx, "SELECT id FROM "+tbl+" ORDER BY id")
    require.NoError(t, err)
    for rows.Next() {
        var id int
        require.NoError(t, rows.Scan(&id))
        ids = append(ids, id)
    }
    assert.Equal(t, []int{2}, ids,
        "inner tx commits independently; outer rolls back its own row")
}

func TestConnFromContext_NoTxInContext_ReturnsFallback(t *testing.T) {
    ctx := context.Background()
    pg := internal.NewPostgres(ctx, t)

    got := transactor.ConnFromContext(ctx, pg.Pool)
    // It must be usable as a Conn — exercise it.
    var one int
    require.NoError(t, got.QueryRow(ctx, "SELECT 1").Scan(&one))
    assert.Equal(t, 1, one)
}

func TestConnFromContext_TxInContext_ReturnsTxNotPool(t *testing.T) {
    ctx := context.Background()
    pg := internal.NewPostgres(ctx, t)
    tx := transactor.New(pg.Pool)

    // Inside the bubble we should see a pgx.Tx; outside, just the pool.
    err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
        conn := transactor.ConnFromContext(ctx, pg.Pool)
        _, ok := conn.(pgx.Tx)
        assert.True(t, ok, "ConnFromContext inside WithinTransaction must return pgx.Tx, got %T", conn)
        return nil
    })
    require.NoError(t, err)
}

// tname sanitises t.Name() into a valid SQL identifier suffix.
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
```

**Important note about nested-tx semantics:** the existing implementation
opens a fresh tx via `pool.BeginTx` on every call (no savepoint, no
context reuse). The third test documents this. If the team later decides
inner calls should join the outer tx, that's a separate change and the
test must be updated in the same PR — never silently.

### 6. `internal/httpapi/middleware_request_logger_test.go` (pure unit)

The middleware reads `middleware.GetReqID(ctx)` (set by `chi/middleware.RequestID`)
and emits a log line with `request_id/method/path/status/bytes/duration`.
The test wires a chi stack that mirrors production order, captures
zerolog output via a `bytes.Buffer`, and asserts on JSON keys.

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

func TestRequestLogger_EmitsRequestScopedFields(t *testing.T) {
    var buf bytes.Buffer
    logger := zerolog.New(&buf)

    r := chi.NewRouter()
    r.Use(chimiddleware.RequestID)
    r.Use(httpapi.RequestLogger(logger))
    r.Get("/hello", func(w http.ResponseWriter, r *http.Request) {
        // verify the request-scoped logger is available in context
        l := zerolog.Ctx(r.Context())
        l.Info().Msg("from handler")
        w.WriteHeader(http.StatusTeapot)
        _, _ = w.Write([]byte("ok"))
    })

    srv := httptest.NewServer(r)
    t.Cleanup(srv.Close)

    resp, err := srv.Client().Get(srv.URL + "/hello")
    require.NoError(t, err)
    require.NoError(t, resp.Body.Close())

    // buf contains at least two lines: "from handler" + the middleware's final "request".
    lines := splitNonEmpty(buf.String())
    require.GreaterOrEqual(t, len(lines), 2)

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
    resp, _ := srv.Client().Get(srv.URL + "/x")
    _ = resp.Body.Close()

    lines := splitNonEmpty(buf.String())
    // first emitted line is the handler's "inside handler"; it must carry
    // request_id/method/path injected by the middleware.
    var first map[string]any
    require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
    assert.NotEmpty(t, first["request_id"])
    assert.Equal(t, "/x", first["path"])
}

func splitNonEmpty(s string) []string {
    var out []string
    for _, line := range strings.Split(s, "\n") {
        if line != "" {
            out = append(out, line)
        }
    }
    return out
}
```

The two tests pin the two contracts the middleware actually has:
(a) it emits a finalising "request" line with status/bytes/duration;
(b) it makes `zerolog.Ctx(r.Context())` carry the request-scoped fields
so handler logs are correlatable.

### 7. `internal/subscription/http/error_status_test.go` (pure unit, focused)

Only the pure `errorStatus(err error) int` function is tested here. The
rest of the handler is covered by integration. This eliminates the
"discussion point" cost without losing the assertion that each sentinel
maps to the right code.

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
        {"bad-request error type", &BadRequestError{Reason: "bad email"}, http.StatusBadRequest},
        {"repo not found", domain.ErrRepoNotFound, http.StatusNotFound},
        {"token not found", domain.ErrTokenNotFound, http.StatusNotFound},
        {"already exists", domain.ErrAlreadyExists, http.StatusConflict},
        {"token expired", domain.ErrTokenExpired, http.StatusGone},
        {"unrelated error", errors.New("kaboom"), http.StatusInternalServerError},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            assert.Equal(t, tc.want, errorStatus(tc.err))
        })
    }
}
```

Tests live in `package http` (no `_test`) so they can call the
unexported `errorStatus` directly — same pattern as `tmpl_test.go`.

## Build/CI Impact

- `make test` runs ~7 new files. Estimated +1-2 s wall-clock.
- `make test-integration` runs the transactor file. Postgres container is
  reused per test only if `NewPostgres` is called once per package
  (consider promoting to package-level setup in a future pass — out of
  scope here).
- `make test-all` and `.github/workflows/ci.yml` need no edits — the new
  files are picked up by the existing matrix.

## Out of Scope

- Coverage thresholds in CI (no `-cover` gate is added).
- Refactoring `ResendMailer` to accept an interface unless the SDK
  cannot be redirected via a custom `http.Client` transport. Implementer
  to choose path (a) or (b) (see §3a).
- Deep `go-mail` SMTP-message-shape assertions on `internal/notifier/emailer/smtp.go`.
  Mailpit integration covers headers/body; pure-unit cost is not worth
  the SDK gymnastics.
- Full handler unit suite — `errorStatus` is the only piece worth
  duplicating outside integration; chi routing + JSON marshalling are
  framework-level concerns.
- Tightening `pkg/transactor` to support savepoint-style nesting; the
  test only documents the current behaviour.

## Open Questions

None — the three deferrals (3b, full handler, nested-tx semantics) are
explicit decisions, not unknowns.
