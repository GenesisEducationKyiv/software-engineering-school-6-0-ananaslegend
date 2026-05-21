# E2E Test for Subscriber Full-Flow Lifecycle — Design Spec

**Date:** 2026-05-15

## Summary

Add a new end-to-end test suite `tests/e2e/flow/` that drives a real Chromium
browser through the full subscriber lifecycle against the running HTTP server,
real Postgres (testcontainers), real Mailpit (testcontainers), and a fake
GitHub (REST + GraphQL) in-process fixture. The scanner, notifier, and
confirmer cron workers run as real goroutines with a fast tick (50 ms) so
emails arrive via the same code path as production.

The single test `TestSubscriberLifecycle` covers six phases:

1. Open landing page, fill the form, submit.
2. Receive confirmation email in Mailpit; load its HTML into a Playwright page
   and pull the confirm-link out of a `data-testid` selector.
3. Navigate the browser to the confirm link; assert the "Subscription
   Confirmed" page rendered.
4. Mutate the GitHub GraphQL fixture to publish a new release; wait for the
   scanner + notifier to produce a release email; assert its structure and
   pull the unsubscribe link.
5. Navigate the browser to the unsubscribe link; assert the "Unsubscribed"
   page rendered.
6. Publish yet another release; wait long enough for the pipeline to fire;
   assert the mailbox stays empty — an inactive subscription must not get
   notified.

## Motivation

Existing test coverage stops at the HTTP boundary:

- `internal/subscription/http/pages/pages_test.go` renders templates in
  isolation but does not load them in a real browser.
- `internal/subscription/http/handler_test.go` exercises handlers against
  `httptest`.
- `tests/integration/subscription/` and `tests/integration/crons/` drive
  HTTP + DB and `Flush()` the outbox manually with in-memory or Mailpit
  fixtures, but never click anything.

What is not covered:

- The inline JS in `landing.html` — the actual `fetch('/api/subscribe')`
  and redirect to `/subscribed`. A typo there is a silent product bug.
- The full pipeline as a unit: scanner ticks → notifier drains → email
  reaches inbox → user clicks the unsubscribe link → subscription deactivates
  → next release tick does *not* email.
- That confirmation and release email HTML actually contains a working CTA
  and unsubscribe link (templates can render broken markup that a
  `Contains(body, "http")` integration assert silently accepts).

A single happy-path e2e test fills these gaps without duplicating the deep
edge-case coverage already done at lower layers. Error paths
(`/oops`, `/unavailable`, etc.) are out of scope for this spec — they belong
to follow-up suites under `tests/e2e/`.

## File Layout

```
tests/
├── internal/                          # shared, already exists
│   ├── app.go                         # EXTEND — add NewE2EApp
│   ├── github.go                      # unchanged (REST fixture)
│   ├── github_graphql.go              # NEW — GraphQL fixture for scanner
│   ├── mailpit.go                     # EXTEND — add MessageHTML
│   ├── pg.go                          # unchanged
│   └── browser.go                     # NEW — Playwright lifecycle + helpers
└── e2e/
    ├── README.md                      # NEW
    ├── _artifacts/                    # NEW, gitignored — screenshots + traces
    └── flow/
        └── full_flow_test.go          # NEW
```

Shared helpers in `tests/internal/` carry the disjunction build tag
`//go:build integration || e2e` so both `make test-integration` and
`make test-e2e` see them. The new `browser.go` and `github_graphql.go`
follow the same convention even though browser is e2e-only (keeps the build
tags uniform across the package and lets a future integration test reuse the
GraphQL fixture without retagging).

`tests/e2e/flow/full_flow_test.go` carries `//go:build e2e`. The default
`make test` skips both trees.

## Makefile

Add a single target:

```makefile
test-e2e:
	go test -tags=e2e -count=1 ./tests/e2e/...
```

No `playwright-install` target — `playwright.Install()` runs lazily from
`TestMain` (see "Playwright lifecycle" below).

## Shared Test Helpers (`tests/internal/`)

### `app.go` — `NewE2EApp`

The existing `NewApp` builds the HTTP stack with **no** cron workers and is
left untouched (integration tests rely on `Flush()`-driving). A second
constructor wires the full topology:

```go
type E2EAppConfig struct {
    Pool            *pgxpool.Pool
    Mailpit         *Mailpit
    GitHubRESTURL   string
    GitHubGraphURL  string
    AppBaseURL      string
    ConfirmTokenTTL time.Duration
    ScannerTick     time.Duration  // default 50 ms
    DrainerTick     time.Duration  // default 50 ms (shared by confirmer + notifier)
}

type E2EApp struct {
    *App                       // embeds the regular app (HTTP server, etc.)
    cancel context.CancelFunc  // stops all worker goroutines
    wg     sync.WaitGroup
}

func NewE2EApp(t testing.TB, cfg E2EAppConfig) *E2EApp
```

`NewE2EApp`:

- Builds the base `App` exactly like `NewApp` (handler, service, registry,
  httptest server), but with `GitHubBaseURL = cfg.GitHubRESTURL` and the
  subscription service's `AppBaseURL` set to the httptest server URL so the
  links inside confirmation/release emails point back to the test server.
- Wires an SMTP `Mailer` (`internal/notifier/emailer/smtp.go`) against
  `cfg.Mailpit.SMTPHost:SMTPPort`. The composite `fullMailer` interface from
  `cmd/api/main.go` does not need to be reused — we wire a single SMTP mailer
  and inject it into both `confirmer.Config.Mail` and `notifier.Config.Mail`.
- Wires the GitHub GraphQL client against `cfg.GitHubGraphURL` (used by
  scanner). REST URL is wired separately for subscribe-time existence checks.
- Starts three goroutines:
  - `confirmer.New(...).Run(workerCtx)` — `cfg.DrainerTick`
  - `notifier.New(...).Run(workerCtx)` — `cfg.DrainerTick`
  - `scanner.New(...).Run(workerCtx)` — `cfg.ScannerTick`
- Registers `t.Cleanup` that cancels `workerCtx`, waits on `sync.WaitGroup`,
  then closes the httptest server (server close already chained from `App`).

The Prometheus registry is shared by all components and the HTTP router, same
as production. Tests do not assert on metrics in e2e — that lives in
integration suites already.

### `github_graphql.go` — GraphQL fixture

The scanner uses GitHub GraphQL with field-aliases (ADR-0008). The REST
`GitHubFixture` only answers `HEAD /repos/{owner}/{name}` and cannot be
reused. New file:

```go
type GitHubGraphQLFixture struct {
    server *httptest.Server

    mu   sync.Mutex
    tags map[string]string   // "owner/name" -> latest tag
}

func NewGitHubGraphQLFixture(t testing.TB) *GitHubGraphQLFixture
func (f *GitHubGraphQLFixture) URL() string                  // POST endpoint
func (f *GitHubGraphQLFixture) SetLatestTag(repo, tag string)
func (f *GitHubGraphQLFixture) Reset()
```

`server.Handler` is a single POST endpoint at root path "/". It:

1. Decodes the request body as `{ "query": "..." }`.
2. Greps the query for `r{id}: repository(owner: "X", name: "Y")` aliases
   (the exact shape produced by `internal/github/client.go:buildGraphQLQuery`).
3. For every alias whose `"X/Y"` is programmed, emits
   `data.r{id}.latestRelease.tagName = <tag>`. For unprogrammed repos emits
   `data.r{id}.latestRelease = null`.

The release URL and name are **not** fetched from GitHub — the notifier
builds the URL with `fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
owner, name, tag)` (`internal/notifier/notifier.go:107`). The fixture only
needs to drive `latestRelease.tagName`.

The handler is intentionally narrow — it only supports the shape
`internal/github` actually sends. No support for arbitrary GraphQL.

### `mailpit.go` — `MessageHTML`

```go
// MessageHTML returns the parsed HTML body of message id via Mailpit's
// /api/v1/message/{id} endpoint, which already parses MIME for us.
func (m *Mailpit) MessageHTML(ctx context.Context, t testing.TB, id string) string
```

Replaces the per-test parsing of raw RFC-822 that would otherwise be needed.
Existing `MessageSource` stays for callers that need the raw form.

### `browser.go` — Playwright wrapper

```go
type Browser struct {
    pw      *playwright.Playwright
    browser playwright.Browser
}

func NewBrowser(t testing.TB) *Browser    // chromium, headless by default

// NewPage opens an isolated BrowserContext and returns its page.
// Registers t.Cleanup that, on t.Failed(), saves a screenshot to
// tests/e2e/_artifacts/<TestName>.png and writes a Playwright trace when
// PLAYWRIGHT_TRACE=1.
func (b *Browser) NewPage(t testing.TB) playwright.Page

// LoadEmailHTML opens an isolated BrowserContext and loads html as the
// page content via page.SetContent. Use it to assert on data-testid
// selectors and extract hrefs the same way as on real pages.
func (b *Browser) LoadEmailHTML(t testing.TB, html string) playwright.Page
```

Headless is the default; `PLAYWRIGHT_HEADED=1` flips it. One `Browser`
instance per suite, one `BrowserContext` per page call. Each context is
closed via `t.Cleanup`.

`page.OnConsole` and `page.OnRequestFailed` are wired into `zerolog.Ctx(t)`
so JS errors and failed requests surface in test logs without manual
assertions.

## Required `data-testid` Additions

### HTML pages — `internal/subscription/http/pages/templates/`

| Template | Element | `data-testid` |
| --- | --- | --- |
| `landing.html` | `<form id="subscribe-form">` | `subscribe-form` |
| `landing.html` | `<input id="repo">` | `repo-input` |
| `landing.html` | `<input id="email">` | `email-input` |
| `landing.html` | `<button id="submit-btn">` | `submit-button` |
| `landing.html` | `<p id="error-msg">` | `error-message` |
| `subscribed.html` | `<h1>` | `page-heading` |
| `confirmed.html` | `<h1>` | `page-heading` |
| `confirmed.html` | `<a class="link" href="/">` | `home-link` |
| `unsubscribed.html` | `<h1>` | `page-heading` |
| `unsubscribed.html` | `<a class="link" href="/">` | `home-link` |
| `unavailable.html` | `<h1>` | `page-heading` |
| `unavailable.html` | `<a class="btn" href="/">` | `home-link` |
| `oops.html` | `<h1>` | `page-heading` |
| `oops.html` | `<div class="req-id">` | `request-id` |

Existing `id` attributes stay (the page's own JS keys off `#repo`,
`#email`, `#submit-btn`, `#error-msg`). The `data-testid` is purely for
tests so the page's runtime contract and the test contract are decoupled.

`internal/subscription/http/pages/pages_test.go` keeps asserting on `<h1>`
text content and is not affected by the new attribute.

### Email templates — `internal/notifier/emailer/templates/`

| Template | Element | `data-testid` |
| --- | --- | --- |
| `confirmation.html` | `<h1>Confirm Your Subscription</h1>` | `email-heading` |
| `confirmation.html` | `<a href="{{.ConfirmURL}}">` (CTA button) | `confirm-button` |
| `confirmation.html` | `<a href="{{.ConfirmURL}}">` (text fallback) | `confirm-link` |
| `release.html` | `<h1>New Release Published</h1>` | `email-heading` |
| `release.html` | `<a href="{{.ReleaseURL}}">` (CTA button) | `release-button` |
| `release.html` | `<a href="{{.ReleaseURL}}">` (text fallback) | `release-link` |
| `release.html` | `<a href="{{.UnsubscribeURL}}">Unsubscribe</a>` | `unsubscribe-link` |

Text-part variants (`*.txt`) are untouched — `data-testid` is HTML-only.

## Suite Layout

```go
//go:build e2e

package flow_test

func TestMain(m *testing.M) {
    if err := playwright.Install(); err != nil {
        log.Fatalf("playwright install: %v", err)
    }
    os.Exit(m.Run())
}

func TestSubscriberLifecycle(t *testing.T) {
    suite.Run(t, new(FullFlowSuite))
}

type FullFlowSuite struct {
    suite.Suite

    pg        *internal.Postgres
    mailpit   *internal.Mailpit
    ghREST    *internal.GitHubFixture
    ghGraphQL *internal.GitHubGraphQLFixture
    browser   *internal.Browser
    app       *internal.E2EApp
}

func (s *FullFlowSuite) SetupSuite() {
    ctx := context.Background()
    s.pg        = internal.NewPostgres(ctx, s.T())
    s.mailpit   = internal.NewMailpit(ctx, s.T())
    s.ghREST    = internal.NewGitHubFixture(s.T())
    s.ghGraphQL = internal.NewGitHubGraphQLFixture(s.T())
    s.browser   = internal.NewBrowser(s.T())
}

func (s *FullFlowSuite) SetupTest() {
    internal.Truncate(s.T(), s.pg.Pool)
    s.mailpit.Reset(context.Background(), s.T())
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
```

## Test Phases

Constants used below:

- `repo = "e2e-org/e2e-repo"`
- `email = "e2e@test.local"`

### Phase 0 — Seed fixtures

```go
s.ghREST.SetRepoExists(repo, true)           // POST /api/subscribe → 202
s.ghGraphQL.SetLatestTag(repo, "v1.0.0")     // scanner baseline after confirm
```

Setting `v1.0.0` upfront means the very first scanner tick after the
subscription becomes active will record `last_seen = v1.0.0` without
emitting a notification (see scanner.go:128 — `shouldNotify` requires a
non-nil prior `LastSeenTag`).

### Phase 1 — Subscribe via UI

```go
page := s.browser.NewPage(s.T())

require.NoError(s.T(), page.Goto(s.app.URL()))
require.NoError(s.T(), page.GetByTestId("repo-input").Fill(repo))
require.NoError(s.T(), page.GetByTestId("email-input").Fill(email))
require.NoError(s.T(), page.GetByTestId("submit-button").Click())

require.NoError(s.T(), page.WaitForURL("**/subscribed",
    playwright.PageWaitForURLOptions{Timeout: playwright.Float(5_000)}))

heading, _ := page.GetByTestId("page-heading").TextContent()
require.Equal(s.T(), "Check your inbox", heading)
```

### Phase 2 — Receive confirmation email

```go
ctx := context.Background()

msgs := s.mailpit.WaitForMessages(ctx, s.T(), 1, 5*time.Second)
require.Equal(s.T(), email, msgs[0].To[0].Address)
require.Contains(s.T(), msgs[0].Subject, "Confirm")

emailPage := s.browser.LoadEmailHTML(s.T(),
    s.mailpit.MessageHTML(ctx, s.T(), msgs[0].ID))
emailHeading, _ := emailPage.GetByTestId("email-heading").TextContent()
require.Equal(s.T(), "Confirm Your Subscription", emailHeading)

confirmURL, _ := emailPage.GetByTestId("confirm-button").GetAttribute("href")
require.Contains(s.T(), confirmURL, "/api/confirm?token=")

s.mailpit.Reset(ctx, s.T())
```

### Phase 3 — Confirm subscription via link

```go
require.NoError(s.T(), page.Goto(confirmURL))
heading, _ = page.GetByTestId("page-heading").TextContent()
require.Equal(s.T(), "Subscription Confirmed", heading)
```

### Phase 4 — Emulate new release

The scanner needs at least one tick after the subscription became active to
record its `v1.0.0` baseline before we publish a new tag — otherwise the
first tick would treat `v2.0.0` as the only-and-therefore-baseline release
and skip the notification.

```go
time.Sleep(150 * time.Millisecond)            // 3× scanner tick, baseline recorded

s.ghGraphQL.SetLatestTag(repo, "v2.0.0")      // emit "new release"

msgs = s.mailpit.WaitForMessages(ctx, s.T(), 1, 5*time.Second)
require.Contains(s.T(), msgs[0].Subject, "v2.0.0")

emailPage = s.browser.LoadEmailHTML(s.T(),
    s.mailpit.MessageHTML(ctx, s.T(), msgs[0].ID))
emailHeading, _ = emailPage.GetByTestId("email-heading").TextContent()
require.Equal(s.T(), "New Release Published", emailHeading)

releaseURL, _ := emailPage.GetByTestId("release-button").GetAttribute("href")
// notifier composes the URL: "https://github.com/{owner}/{name}/releases/tag/{tag}"
require.Equal(s.T(),
    "https://github.com/e2e-org/e2e-repo/releases/tag/v2.0.0", releaseURL)

unsubURL, _ := emailPage.GetByTestId("unsubscribe-link").GetAttribute("href")
require.Contains(s.T(), unsubURL, "/api/unsubscribe?token=")

s.mailpit.Reset(ctx, s.T())
```

### Phase 5 — Unsubscribe via link

```go
require.NoError(s.T(), page.Goto(unsubURL))
heading, _ = page.GetByTestId("page-heading").TextContent()
require.Equal(s.T(), "You've Been Unsubscribed", heading)
```

### Phase 6 — Emulate another release, expect no email

```go
s.ghGraphQL.SetLatestTag(repo, "v3.0.0")

// 5× scanner tick + 5× drainer tick = 500 ms. Comfortably more than the
// observed propagation in phases 4–5 (typically < 300 ms).
time.Sleep(500 * time.Millisecond)

require.Empty(s.T(), s.mailpit.Messages(ctx, s.T()),
    "unsubscribed user must not receive release email")
```

The negative assertion relies on the scanner contract — after unsubscribe,
the subscription is no longer active, so `Repository.InsertNotifications`
does not insert a row for that subscription, so the notifier has nothing to
drain. The test asserts the end state (empty inbox) rather than the
intermediate DB state.

## Playwright Lifecycle

- `playwright.Install()` runs once per `go test` invocation from `TestMain`.
  Idempotent — re-running after the browsers are present is a fast no-op.
  First run downloads ~150 MB (Chromium + driver) and takes 30–60 s.
- `internal.NewBrowser(t)` launches a single Chromium instance per suite
  (`SetupSuite`), registered for `t.Cleanup`. `Browser.Close` stops the
  driver as well.
- Each call to `browser.NewPage(t)` or `browser.LoadEmailHTML(t, ...)`
  creates a fresh `BrowserContext` so cookies and storage from a previous
  page do not leak. Both register their own `t.Cleanup` to close the
  context.
- On `t.Failed()`, the cleanup writes a screenshot to
  `tests/e2e/_artifacts/<TestName>.png`. When `PLAYWRIGHT_TRACE=1` is set,
  it also flushes a Playwright trace `.zip` so `playwright show-trace
  trace.zip` reproduces the run.
- `_artifacts/` is added to `.gitignore`.

## Out of Scope

- Error pages (`/oops`, `/unavailable`), expired confirm token, duplicate
  subscribe, malformed input — separate e2e suites once the harness is in
  place.
- Real GitHub network — the GraphQL fixture mocks just enough of the
  schema for the scanner.
- CI integration — landing this spec only requires `make test-e2e` to pass
  locally. A follow-up PR will wire it into CI with a runner image that has
  Chromium dependencies pre-installed (Playwright's official runner image
  is the obvious candidate).
- Metric assertions — already covered in integration suites; e2e focuses on
  user-observable behaviour.

## Open Questions

None — all decisions resolved in the brainstorming round (Playwright Go +
real worker goroutines + GraphQL fixture + email-HTML-in-browser pattern +
lazy `TestMain` install).