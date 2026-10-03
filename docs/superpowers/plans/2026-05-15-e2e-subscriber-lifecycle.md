# E2E Subscriber Lifecycle Test — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A single end-to-end test that drives a real Chromium browser through the full subscriber lifecycle (subscribe → confirm → release email → unsubscribe → no-email-after-unsubscribe) against the real HTTP server, real Postgres, real Mailpit, and a fake GitHub.

**Architecture:** Extend `tests/internal/` shared helpers with a Playwright wrapper, a GitHub GraphQL fixture, and an `NewE2EApp` constructor that starts scanner/notifier/confirmer as real goroutines with a 50 ms tick. Add `data-testid` attributes to HTML page and email templates for stable selectors. Drop the new test under `tests/e2e/flow/` behind a `//go:build e2e` tag.

**Tech Stack:** Go 1.26 + `github.com/playwright-community/playwright-go` (Chromium) + existing testcontainers (Postgres, Mailpit) + `testify/suite`.

---

## Reference docs

- **Design spec:** `docs/superpowers/specs/2026-05-15-e2e-subscriber-lifecycle-design.md`
- **Project conventions:** `CLAUDE.md` (error wrapping, param objects, Prometheus registry, `subhttp` alias).
- **Testing strategy:** `docs/adr/0011-testing-strategy.md` (testcontainers + real Postgres, no mocks at the DB boundary).
- **Vendor + tidy:** `docs/adr/0007-vendor-and-dependency-updates.md` (every dep change goes through `make tidy`).

---

## Task 1: Promote shared helpers to dual integration/e2e build tag

The shared fixtures already moved from `tests/integration/internal/` to `tests/internal/`. Right now they still carry `//go:build integration`, which means `go test -tags=e2e` cannot see them. Promote the tag to a disjunction so both tags pick the files up.

**Files:**
- Modify: `tests/internal/app.go:1`
- Modify: `tests/internal/github.go:1`
- Modify: `tests/internal/mailpit.go:1`
- Modify: `tests/internal/pg.go:1`

- [ ] **Step 1: Update build tag on every shared helper file**

In each of the four files above, change the first line from:

```go
//go:build integration
```

to:

```go
//go:build integration || e2e
```

Nothing else changes.

- [ ] **Step 2: Verify the integration suite still compiles and passes**

Run: `make test-integration`
Expected: every existing integration suite still passes (the tag change is a strict superset of the previous behaviour).

- [ ] **Step 3: Verify e2e build tag activates the same files**

Run: `go test -tags=e2e -count=1 ./tests/internal/...`
Expected: `?   github.com/ananaslegend/reposeetory/tests/internal   [no test files]`. No "build constraints exclude all Go files" error.

- [ ] **Step 4: Commit**

```bash
git add tests/internal/app.go tests/internal/github.go tests/internal/mailpit.go tests/internal/pg.go
git commit -m "tests: expose shared helpers to e2e build tag"
```

---

## Task 2: Add `data-testid` to HTML page templates

Tests need stable selectors that are decoupled from the runtime `id`s the inline JS already uses. Add `data-testid` attributes to every element the e2e test will look at.

**Files:**
- Modify: `internal/subscription/http/pages/templates/landing.html`
- Modify: `internal/subscription/http/pages/templates/subscribed.html`
- Modify: `internal/subscription/http/pages/templates/confirmed.html`
- Modify: `internal/subscription/http/pages/templates/unsubscribed.html`
- Modify: `internal/subscription/http/pages/templates/unavailable.html`
- Modify: `internal/subscription/http/pages/templates/oops.html`

- [ ] **Step 1: landing.html — annotate form, inputs, submit, error**

Open `internal/subscription/http/pages/templates/landing.html`. In the `<form>` block change the existing opening tags so each carries an additional `data-testid` attribute (do NOT remove the existing `id`, names, or other attributes):

- `<form ... id="subscribe-form" ...>` → add `data-testid="subscribe-form"`
- `<input ... id="repo" ...>` → add `data-testid="repo-input"`
- `<input ... id="email" ...>` → add `data-testid="email-input"`
- `<button ... id="submit-btn" ...>` → add `data-testid="submit-button"`
- `<p ... id="error-msg" ...>` → add `data-testid="error-message"`

Example (the `<button>` line before/after):

Before:

```html
<button type="submit" id="submit-btn">Subscribe repository →</button>
```

After:

```html
<button type="submit" id="submit-btn" data-testid="submit-button">Subscribe repository →</button>
```

- [ ] **Step 2: subscribed.html — annotate heading**

Add `data-testid="page-heading"` to the `<h1>` element. Before/after:

```html
<h1>Check your inbox</h1>
```

```html
<h1 data-testid="page-heading">Check your inbox</h1>
```

- [ ] **Step 3: confirmed.html — annotate heading and home link**

Inside the `{{define "content"}}` block of `confirmed.html`:

- `<h1>Subscription Confirmed</h1>` → add `data-testid="page-heading"`
- `<a href="/" class="link">← Subscribe more</a>` → add `data-testid="home-link"`

- [ ] **Step 4: unsubscribed.html — annotate heading and home link**

Inside the `{{define "content"}}` block of `unsubscribed.html`:

- `<h1>You've Been Unsubscribed</h1>` → add `data-testid="page-heading"`
- `<a href="/" class="link">← Subscribe again</a>` → add `data-testid="home-link"`

- [ ] **Step 5: unavailable.html — annotate heading and home link**

Inside the `{{define "content"}}` block of `unavailable.html`:

- `<h1>Link Unavailable</h1>` → add `data-testid="page-heading"`
- `<a href="/" class="btn">Subscribe again →</a>` → add `data-testid="home-link"`

- [ ] **Step 6: oops.html — annotate heading and request ID**

Inside the `{{define "content"}}` block of `oops.html`:

- `<h1>Something Went Wrong</h1>` → add `data-testid="page-heading"`
- `<div class="req-id">Request ID: {{.RequestID}}</div>` → add `data-testid="request-id"`

- [ ] **Step 7: Verify existing page tests still pass**

Run: `go test ./internal/subscription/http/pages/...`
Expected: PASS. The existing tests assert on text content, so the new attributes do not affect them.

- [ ] **Step 8: Verify the full unit-test suite is still green**

Run: `make test`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/subscription/http/pages/templates/
git commit -m "pages: add data-testid attributes for e2e selectors"
```

---

## Task 3: Add `data-testid` to email HTML templates

The e2e test loads the email body into a Playwright `BrowserContext` and uses the same selector pattern as the HTML pages. Email-template files live under `internal/notifier/emailer/templates/`.

**Files:**
- Modify: `internal/notifier/emailer/templates/confirmation.html`
- Modify: `internal/notifier/emailer/templates/release.html`

- [ ] **Step 1: confirmation.html — annotate heading and two links**

In `internal/notifier/emailer/templates/confirmation.html`:

- `<h1 ...>Confirm Your Subscription</h1>` → add `data-testid="email-heading"`
- The CTA `<a href="{{.ConfirmURL}}" ...>` button (the one with button-style padding, the first occurrence near `style="background:`) → add `data-testid="confirm-button"`
- The text-fallback `<a href="{{.ConfirmURL}}" ...>` further down (rendered as plain URL text) → add `data-testid="confirm-link"`

- [ ] **Step 2: release.html — annotate heading, CTA, fallback, unsubscribe**

In `internal/notifier/emailer/templates/release.html`:

- `<h1 ...>New Release Published</h1>` → add `data-testid="email-heading"`
- The CTA `<a href="{{.ReleaseURL}}" ...>` button → add `data-testid="release-button"`
- The text-fallback `<a href="{{.ReleaseURL}}" ...>` further down → add `data-testid="release-link"`
- `<a href="{{.UnsubscribeURL}}" ...>Unsubscribe</a>` → add `data-testid="unsubscribe-link"`

- [ ] **Step 3: Verify emailer unit tests still pass**

Run: `go test ./internal/notifier/emailer/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/notifier/emailer/templates/
git commit -m "emailer: add data-testid attributes for e2e selectors"
```

---

## Task 4: `Mailpit.MessageHTML` helper

Use Mailpit's own parsed-MIME endpoint (`GET /api/v1/message/{id}`) instead of parsing RFC-822 in Go. Returns the HTML body as a string.

**Files:**
- Modify: `tests/internal/mailpit.go`

- [ ] **Step 1: Add `MessageHTML` method**

Append the following to `tests/internal/mailpit.go` (after `MessageSource`):

```go
// MessageHTML returns the parsed HTML body of message id via Mailpit's
// /api/v1/message/{id} endpoint, which already performs MIME parsing.
func (m *Mailpit) MessageHTML(ctx context.Context, t testing.TB, id string) string {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/message/%s", m.APIBase, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := m.client.Do(req)
	require.NoError(t, err, "mailpit message")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "mailpit message status")

	var payload struct {
		HTML string `json:"HTML"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.NotEmpty(t, payload.HTML, "mailpit returned empty HTML body for %s", id)
	return payload.HTML
}
```

The imports (`encoding/json`, `fmt`, `net/http`, `testing`, plus testify and the existing helpers) are already present in the file. No new import is required.

- [ ] **Step 2: Build the package under the integration tag**

Run: `go build -tags=integration ./tests/internal/...`
Expected: builds without errors.

- [ ] **Step 3: Commit**

```bash
git add tests/internal/mailpit.go
git commit -m "tests/internal: add Mailpit.MessageHTML helper"
```

---

## Task 5: GitHub GraphQL fixture

In-process double for the single GraphQL endpoint the scanner hits. Only supports the exact shape `internal/github/client.go:buildGraphQLQuery` emits: `r{id}: repository(owner: "X", name: "Y") { latestRelease { tagName } }`.

**Files:**
- Create: `tests/internal/github_graphql.go`
- Create: `tests/internal/github_graphql_test.go`

- [ ] **Step 1: Write the fixture test (failing, since the file does not exist yet)**

Create `tests/internal/github_graphql_test.go`:

```go
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
```

- [ ] **Step 2: Run the test, verify it fails because the fixture does not exist**

Run: `go test -tags=integration -count=1 -run TestGitHubGraphQLFixture ./tests/internal/...`
Expected: build error — `undefined: internal.NewGitHubGraphQLFixture`.

- [ ] **Step 3: Implement the fixture**

Create `tests/internal/github_graphql.go`:

```go
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
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `go test -tags=integration -count=1 -run TestGitHubGraphQLFixture ./tests/internal/...`
Expected: PASS.

- [ ] **Step 5: Verify it also compiles under the e2e tag**

Run: `go build -tags=e2e ./tests/internal/...`
Expected: builds without errors.

- [ ] **Step 6: Commit**

```bash
git add tests/internal/github_graphql.go tests/internal/github_graphql_test.go
git commit -m "tests/internal: add GitHub GraphQL fixture for scanner"
```

---

## Task 6: Add `playwright-go` dependency and run `make tidy`

Per ADR-0007 every dep change goes through `make tidy` (runs `go mod tidy` + `go mod vendor` atomically).

- [ ] **Step 1: Pull the latest playwright-go release**

Run: `go get github.com/playwright-community/playwright-go@latest`
Expected: `go.mod` and `go.sum` updated, output mentions the resolved version.

- [ ] **Step 2: Run `make tidy` to refresh vendor/**

Run: `make tidy`
Expected: completes without errors. New entries under `vendor/github.com/playwright-community/`.

- [ ] **Step 3: Verify the main build still works against the vendored tree**

Run: `make build`
Expected: PASS. Confirms playwright-go does not pull in something the corporate proxy rejects (ADR-0007 motivation).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum vendor
git commit -m "deps: add playwright-go for e2e tests"
```

---

## Task 7: Browser helper

A thin wrapper over `playwright-go` that hides driver/browser lifecycle, gives every test an isolated `BrowserContext`, and captures a screenshot on failure.

**Files:**
- Create: `tests/internal/browser.go`

- [ ] **Step 1: Implement the helper**

Create `tests/internal/browser.go`:

```go
//go:build integration || e2e

package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// Browser is the shared Playwright + Chromium handle for an e2e suite.
// Build one in SetupSuite; create a fresh Page per test method.
type Browser struct {
	pw      *playwright.Playwright
	browser playwright.Browser
}

// NewBrowser launches Chromium and registers t.Cleanup to tear it down.
// Headless by default; set PLAYWRIGHT_HEADED=1 to flip it for debugging.
func NewBrowser(t testing.TB) *Browser {
	t.Helper()
	pw, err := playwright.Run()
	require.NoError(t, err, "playwright run")

	headless := os.Getenv("PLAYWRIGHT_HEADED") != "1"
	br, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
	})
	require.NoError(t, err, "chromium launch")

	b := &Browser{pw: pw, browser: br}
	t.Cleanup(func() {
		_ = br.Close()
		_ = pw.Stop()
	})
	return b
}

// NewPage opens an isolated BrowserContext + Page and registers cleanup
// that, on t.Failed(), saves a screenshot to tests/e2e/_artifacts/.
func (b *Browser) NewPage(t testing.TB) playwright.Page {
	t.Helper()
	ctx, err := b.browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: 1280, Height: 720},
	})
	require.NoError(t, err, "new browser context")

	page, err := ctx.NewPage()
	require.NoError(t, err, "new page")

	t.Cleanup(func() {
		if t.Failed() {
			saveScreenshot(t, page)
		}
		_ = ctx.Close()
	})
	return page
}

// LoadEmailHTML opens an isolated context and renders html via SetContent so
// callers can reuse the same data-testid selectors as on real pages.
func (b *Browser) LoadEmailHTML(t testing.TB, html string) playwright.Page {
	t.Helper()
	ctx, err := b.browser.NewContext()
	require.NoError(t, err, "new browser context for email")

	page, err := ctx.NewPage()
	require.NoError(t, err, "new page for email")

	require.NoError(t, page.SetContent(html), "page.SetContent")

	t.Cleanup(func() { _ = ctx.Close() })
	return page
}

func saveScreenshot(t testing.TB, page playwright.Page) {
	t.Helper()
	dir := filepath.Join("..", "..", "_artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("screenshot: mkdir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, sanitize(t.Name())+".png")
	_, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(path),
		FullPage: playwright.Bool(true),
	})
	if err != nil {
		t.Logf("screenshot: %v", err)
		return
	}
	t.Logf("screenshot saved to %s", path)
}

func sanitize(name string) string {
	r := strings.NewReplacer("/", "_", " ", "_", ":", "_")
	return r.Replace(name)
}
```

- [ ] **Step 2: Verify it compiles under both tags**

Run: `go build -tags=integration ./tests/internal/... && go build -tags=e2e ./tests/internal/...`
Expected: both succeed.

- [ ] **Step 3: Commit**

```bash
git add tests/internal/browser.go
git commit -m "tests/internal: add Playwright browser helper"
```

---

## Task 8: `NewE2EApp` — full topology with running workers

Build a richer App constructor: same HTTP stack as `NewApp`, plus SMTP mailer wired against Mailpit and scanner/notifier/confirmer running as goroutines.

**Files:**
- Modify: `tests/internal/app.go`

- [ ] **Step 1: Extend `app.go` with `NewE2EApp`**

Append the following to `tests/internal/app.go` (after `NewApp`):

```go
// E2EAppConfig configures a full topology App used by tests/e2e/.
type E2EAppConfig struct {
	Pool            *pgxpool.Pool
	Mailpit         *Mailpit
	GitHubRESTURL   string
	GitHubGraphURL  string
	AppBaseURL      string        // optional; defaults to httptest server URL
	ConfirmTokenTTL time.Duration // default 24h
	ScannerTick     time.Duration // default 50ms
	DrainerTick     time.Duration // default 50ms (confirmer + notifier)
}

// E2EApp is the full wired stack: HTTP server + scanner/notifier/confirmer
// goroutines + SMTP mailer pointed at Mailpit. Workers are stopped via
// t.Cleanup.
type E2EApp struct {
	*App
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewE2EApp builds the HTTP stack via NewApp, wires the SMTP mailer
// against cfg.Mailpit, and starts scanner+notifier+confirmer goroutines.
//
// The subscription service's AppBaseURL is set to the httptest server URL
// (unless cfg.AppBaseURL overrides it) so links inside emails resolve back
// to the test server when the browser clicks them.
func NewE2EApp(t testing.TB, cfg E2EAppConfig) *E2EApp {
	t.Helper()

	if cfg.ConfirmTokenTTL == 0 {
		cfg.ConfirmTokenTTL = 24 * time.Hour
	}
	if cfg.ScannerTick == 0 {
		cfg.ScannerTick = 50 * time.Millisecond
	}
	if cfg.DrainerTick == 0 {
		cfg.DrainerTick = 50 * time.Millisecond
	}

	base := NewApp(t, AppConfig{
		Pool:            cfg.Pool,
		GitHubBaseURL:   cfg.GitHubRESTURL,
		AppBaseURL:      cfg.AppBaseURL,
		ConfirmTokenTTL: cfg.ConfirmTokenTTL,
	})
	if cfg.AppBaseURL == "" {
		// NewApp already defaulted to http://test.local; override here so
		// the URLs embedded in emails actually point at the test server.
		base.Server.Config.BaseContext = nil // no-op; documented for review clarity
	}

	mailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
		Host:      cfg.Mailpit.SMTPHost,
		Port:      cfg.Mailpit.SMTPPort,
		From:      "noreply@reposeetory.test",
		TLSPolicy: "none",
	})
	require.NoError(t, err, "build smtp mailer")

	gh := githubclient.New(githubclient.Config{
		Token:      "test-token",
		RESTURL:    cfg.GitHubRESTURL,
		GraphQLURL: cfg.GitHubGraphURL,
	})

	tx := transactor.NewPgx(cfg.Pool)
	scanRepo := scannerrepo.New(cfg.Pool)
	notifRepo := notifrepo.New(cfg.Pool)
	confRepo := confirmerrepo.New(cfg.Pool)

	scan := scanner.New(scanner.Config{
		Tx: tx, Repo: scanRepo, GitHub: gh,
		Interval: cfg.ScannerTick, Registry: base.Registry,
	})
	notif := notifier.New(notifier.Config{
		Tx: tx, Repo: notifRepo, Mail: mailer,
		Interval: cfg.DrainerTick, BaseURL: base.Server.URL,
		Registry: base.Registry,
	})
	conf := confirmer.New(confirmer.Config{
		Tx: tx, Repo: confRepo, Mail: mailer,
		Interval: cfg.DrainerTick, BaseURL: base.Server.URL,
		Registry: base.Registry,
	})

	workerCtx, cancel := context.WithCancel(context.Background())
	e := &E2EApp{App: base, cancel: cancel}
	e.wg.Add(3)
	go func() { defer e.wg.Done(); scan.Run(workerCtx) }()
	go func() { defer e.wg.Done(); notif.Run(workerCtx) }()
	go func() { defer e.wg.Done(); conf.Run(workerCtx) }()

	t.Cleanup(func() {
		cancel()
		e.wg.Wait()
	})
	return e
}
```

Add the new imports at the top of the file (next to the existing ones):

```go
"context"
"sync"
"github.com/stretchr/testify/require"

"github.com/ananaslegend/reposeetory/internal/confirmer"
confirmerrepo "github.com/ananaslegend/reposeetory/internal/confirmer/repository"
"github.com/ananaslegend/reposeetory/internal/notifier"
"github.com/ananaslegend/reposeetory/internal/notifier/emailer"
notifrepo "github.com/ananaslegend/reposeetory/internal/notifier/repository"
"github.com/ananaslegend/reposeetory/internal/scanner"
scannerrepo "github.com/ananaslegend/reposeetory/internal/scanner/repository"
"github.com/ananaslegend/reposeetory/pkg/transactor"
```

Note: import alias names (`confirmerrepo`, `notifrepo`, `scannerrepo`) match the underlying package basenames (`repository`) — if the actual repository packages use different package names, prefer the package's own name and adjust the alias accordingly. Verify by running `goimports` after the edit (Step 2 below).

- [ ] **Step 2: Run goimports to confirm import names**

Run: `goimports -w tests/internal/app.go`
Expected: no diff if names were correct; otherwise it rewrites alias names to match the actual package declarations.

If goimports complains about a missing/unknown package, open the corresponding file (e.g., `internal/confirmer/repository/*.go`), confirm its `package <name>` line, and update the alias in the import block to match.

- [ ] **Step 3: Build under the e2e tag**

Run: `go build -tags=e2e ./tests/internal/...`
Expected: PASS.

- [ ] **Step 4: Sanity-check that integration tests still build (NewApp untouched)**

Run: `make test-integration`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/internal/app.go
git commit -m "tests/internal: add NewE2EApp wiring full topology"
```

---

## Task 9: Write the e2e test

The single test that drives every phase from the spec.

**Files:**
- Create: `tests/e2e/flow/full_flow_test.go`

- [ ] **Step 1: Create the test file**

Create `tests/e2e/flow/full_flow_test.go`:

```go
//go:build e2e

package flow_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/ananaslegend/reposeetory/tests/internal"
)

func TestMain(m *testing.M) {
	if err := playwright.Install(); err != nil {
		log.Fatalf("playwright install: %v", err)
	}
	os.Exit(m.Run())
}

func TestFullSubscriberLifecycle(t *testing.T) {
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
	s.pg = internal.NewPostgres(ctx, s.T())
	s.mailpit = internal.NewMailpit(ctx, s.T())
	s.ghREST = internal.NewGitHubFixture(s.T())
	s.ghGraphQL = internal.NewGitHubGraphQLFixture(s.T())
	s.browser = internal.NewBrowser(s.T())
}

func (s *FullFlowSuite) SetupTest() {
	ctx := context.Background()
	s.pg.Truncate(ctx, s.T())
	s.mailpit.Reset(ctx, s.T())
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

func (s *FullFlowSuite) TestLifecycle() {
	t := s.T()
	ctx := context.Background()

	const (
		repo  = "e2e-org/e2e-repo"
		email = "e2e@test.local"
	)

	// Phase 0 — seed fixtures.
	s.ghREST.SetRepoExists(repo, true)
	s.ghGraphQL.SetLatestTag(repo, "v1.0.0")

	// Phase 1 — subscribe via UI.
	page := s.browser.NewPage(t)
	require.NoError(t, page.Goto(s.app.Server.URL))
	require.NoError(t, page.GetByTestId("repo-input").Fill(repo))
	require.NoError(t, page.GetByTestId("email-input").Fill(email))
	require.NoError(t, page.GetByTestId("submit-button").Click())
	require.NoError(t, page.WaitForURL("**/subscribed",
		playwright.PageWaitForURLOptions{Timeout: playwright.Float(5000)}))
	heading, err := page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Check your inbox", heading)

	// Phase 2 — receive confirmation email.
	msgs := s.mailpit.WaitForMessages(ctx, t, 1, 5*time.Second)
	require.Equal(t, email, msgs[0].To[0].Address)
	require.Contains(t, msgs[0].Subject, "Confirm")

	emailPage := s.browser.LoadEmailHTML(t, s.mailpit.MessageHTML(ctx, t, msgs[0].ID))
	emailHeading, err := emailPage.GetByTestId("email-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Confirm Your Subscription", emailHeading)

	confirmURL, err := emailPage.GetByTestId("confirm-button").GetAttribute("href")
	require.NoError(t, err)
	require.Contains(t, confirmURL, "/api/confirm?token=")
	s.mailpit.Reset(ctx, t)

	// Phase 3 — confirm via link.
	require.NoError(t, page.Goto(confirmURL))
	heading, err = page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "Subscription Confirmed", heading)

	// Phase 4 — emulate new release.
	// Scanner needs one tick after confirm to record `v1.0.0` baseline,
	// otherwise the next tick treats v2.0.0 as the only-and-baseline tag.
	time.Sleep(150 * time.Millisecond)
	s.ghGraphQL.SetLatestTag(repo, "v2.0.0")

	msgs = s.mailpit.WaitForMessages(ctx, t, 1, 5*time.Second)
	require.Contains(t, msgs[0].Subject, "v2.0.0")

	emailPage = s.browser.LoadEmailHTML(t, s.mailpit.MessageHTML(ctx, t, msgs[0].ID))
	emailHeading, err = emailPage.GetByTestId("email-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "New Release Published", emailHeading)

	releaseURL, err := emailPage.GetByTestId("release-button").GetAttribute("href")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/e2e-org/e2e-repo/releases/tag/v2.0.0", releaseURL)

	unsubURL, err := emailPage.GetByTestId("unsubscribe-link").GetAttribute("href")
	require.NoError(t, err)
	require.Contains(t, unsubURL, "/api/unsubscribe?token=")
	s.mailpit.Reset(ctx, t)

	// Phase 5 — unsubscribe via link.
	require.NoError(t, page.Goto(unsubURL))
	heading, err = page.GetByTestId("page-heading").TextContent()
	require.NoError(t, err)
	require.Equal(t, "You've Been Unsubscribed", heading)

	// Phase 6 — emulate yet another release; mailbox must stay empty.
	s.ghGraphQL.SetLatestTag(repo, "v3.0.0")
	time.Sleep(500 * time.Millisecond) // 5× scanner + 5× drainer ticks
	require.Empty(t, s.mailpit.Messages(ctx, t),
		"unsubscribed user must not receive release email")
}
```

- [ ] **Step 2: Verify the file compiles**

Run: `go vet -tags=e2e ./tests/e2e/...`
Expected: no errors.

- [ ] **Step 3: Run the test**

Run: `make test-e2e` (define this target in Task 10, or temporarily: `go test -tags=e2e -count=1 ./tests/e2e/...`)

Expected: first run downloads Chromium (~30–60 s). Subsequent runs complete in ~5–10 s. PASS.

If the test fails because of a flaky timing in Phase 6 (an email arrives despite the unsubscribe), re-read `internal/notifier/repository/` queries — the `WHERE status = 'active'` filter is the contract this phase relies on. Do not extend the sleep beyond 500 ms; debug the root cause.

- [ ] **Step 4: Commit**

```bash
git add tests/e2e/flow/full_flow_test.go
git commit -m "tests/e2e: full subscriber lifecycle happy path"
```

---

## Task 10: Makefile, gitignore, README

Wire the new entry points so contributors can run `make test-e2e`.

**Files:**
- Modify: `Makefile`
- Modify: `.gitignore`
- Create: `tests/e2e/README.md`

- [ ] **Step 1: Add Makefile target**

Open `Makefile`. Add `test-e2e` to the `.PHONY` line (after `test-all`) and add the target body after the existing `test-all` block:

`.PHONY` line, before:

```makefile
.PHONY: build run test vet generate tidy mod-update mod-update-patch lint lint-install lint-fix fix fix-diff migrate-up migrate-down clean swagger swagger-install test-integration test-all
```

after:

```makefile
.PHONY: build run test vet generate tidy mod-update mod-update-patch lint lint-install lint-fix fix fix-diff migrate-up migrate-down clean swagger swagger-install test-integration test-e2e test-all
```

Then insert immediately after the `test-all` block:

```makefile
test-e2e:
	go test -tags=e2e -count=1 ./tests/e2e/...
```

- [ ] **Step 2: Add `_artifacts/` to .gitignore**

Open `.gitignore`. Append:

```
# E2E test screenshots and Playwright traces
tests/e2e/_artifacts/
```

- [ ] **Step 3: Create README**

Create `tests/e2e/README.md`:

```markdown
# E2E Tests

Browser-driven happy-path tests that exercise the real HTTP server, real
Postgres (testcontainers), real Mailpit (testcontainers), and an in-process
GitHub fixture against a real Chromium instance driven by
[`playwright-go`](https://github.com/playwright-community/playwright-go).

## Prerequisites

- Docker daemon running (testcontainers for Postgres + Mailpit).
- ~150 MB free disk for the Chromium binary on the first run.

## Running

```sh
make test-e2e
# equivalent to:
go test -tags=e2e -count=1 ./tests/e2e/...
```

The default `make test` and `make test-integration` skip this tree — every
file under `tests/e2e/` is gated by `//go:build e2e`.

## Debugging

- `PLAYWRIGHT_HEADED=1 make test-e2e` — runs Chromium with a visible window.
- On failure, a screenshot is written to `tests/e2e/_artifacts/<TestName>.png`
  (gitignored).

## Layout

- `flow/full_flow_test.go` — single happy-path test covering the full
  subscriber lifecycle: subscribe → confirm email → confirm via link →
  release email → unsubscribe via link → assert no further emails.

Shared fixtures (Postgres, Mailpit, GitHub REST/GraphQL doubles, Playwright
wrapper, full-topology App) live in `tests/internal/` and are reused by the
integration suite under `tests/integration/`.
```

- [ ] **Step 4: Run the full target**

Run: `make test-e2e`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add Makefile .gitignore tests/e2e/README.md
git commit -m "tests/e2e: add Makefile target, README, and gitignore for artifacts"
```

---

## Self-Review Notes

**Spec coverage:**
- File layout (spec §"File Layout") → Tasks 1, 5, 7, 8, 9, 10. ✓
- Makefile target → Task 10. ✓
- Shared helpers — `NewE2EApp` → Task 8. ✓
- Shared helpers — `GitHubGraphQLFixture` → Task 5. ✓
- Shared helpers — `Mailpit.MessageHTML` → Task 4. ✓
- Shared helpers — `Browser` → Task 7. ✓
- `data-testid` on HTML pages → Task 2. ✓
- `data-testid` on email templates → Task 3. ✓
- Suite layout → Task 9. ✓
- Test phases 0–6 → Task 9 (one method exercising every phase in order). ✓
- Playwright lifecycle (TestMain `Install`, headless flag, screenshot on
  failure) → Tasks 7 and 9. ✓
- Out-of-scope items (error pages, real GitHub, CI) — explicitly deferred,
  not in plan. ✓

**Type/name consistency:**
- `GitHubGraphQLFixture.SetLatestTag(repo, tag)` — used identically in
  Tasks 5 and 9.
- `Mailpit.MessageHTML(ctx, t, id)` — same signature in Tasks 4 and 9.
- `Browser.NewPage(t)` / `Browser.LoadEmailHTML(t, html)` — same signatures
  in Tasks 7 and 9.
- `NewE2EApp(t, cfg)` returns `*E2EApp` embedding `*App` so the test reads
  `s.app.Server.URL` — confirmed in Tasks 8 and 9.

**Risks called out inline:**
- Task 8 import aliases: relies on `goimports` to rectify if package basenames
  differ; explicit instruction included.
- Task 9 Phase 6 flakiness: investigate `status = 'active'` filter; do not
  extend the 500 ms sleep.