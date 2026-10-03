# Full Subscriber Lifecycle — E2E Test

A single browser-driven test that exercises every public-facing surface of
the product in one go: the landing page form, the inline JS that posts to
`/api/subscribe`, the confirmation email, the click-through to `/confirmed`,
the scanner → notifier pipeline that produces a release email, the
unsubscribe link, and the post-unsubscribe quiet state. It also verifies a
re-subscribe path on top of the previously unsubscribed row.

## What the test does

The single method `FullFlowSuite.TestLifecycle` runs eleven phases against a
shared `Browser` (one Chromium instance for the whole suite, fresh
`BrowserContext` per `NewPage`) and an `E2EApp` rebuilt per test
(`SetupTest`):

| Phase | Action | Asserted |
| ----- | ------ | -------- |
| 0 | Seed `ghREST.SetRepoExists(repo, true)`, `ghGraphQL.SetLatestTag(repo, "v1.0.0")`. | — |
| 1 | Browser fills the form and clicks Submit. | URL becomes `**/subscribed`; `page-heading == "Check your inbox"`. |
| 2 | Wait for one Mailpit message; load its HTML body into a Playwright page via `Browser.LoadEmailHTML`. | Recipient is the form email; subject contains "Confirm"; `email-heading == "Confirm Your Subscription"`; the confirm button's `href` contains `/api/confirm/`. |
| 3 | Browser navigates to the confirm URL. | `page-heading == "Subscription Confirmed"`. |
| 4 | Poll `repositories.last_seen_tag` until it equals `v1.0.0` (proof a scanner Tick ran); bump the GraphQL fixture to `v2.0.0`. | Mailpit receives a release email; subject contains `v2.0.0`; `email-heading == "New Release Published"`; `release-button[href]` equals the composed URL `https://github.com/<owner>/<name>/releases/tag/v2.0.0`; `unsubscribe-link[href]` contains `/api/unsubscribe/`. |
| 5 | Browser navigates to the unsubscribe URL. | `page-heading == "You've Been Unsubscribed"`. |
| 6 | Bump fixture to `v3.0.0`; poll `last_seen_tag` until it equals `v3.0.0`. | Zero `release_notifications` rows for `v3.0.0`; Mailpit stays empty. |
| 7 | Re-subscribe through the UI with the same email/repo. | URL becomes `**/subscribed`; heading is `"Check your inbox"`. |
| 8 | Wait for a new confirmation email. | Same shape as phase 2 (a fresh confirm token). |
| 9 | Navigate to the new confirm URL. | `"Subscription Confirmed"`. |
| 10 | Bump fixture to `v4.0.0` — no baseline sleep needed, `last_seen_tag = v3.0.0` is already in place from phase 6. | Release email arrives; subject contains `v4.0.0`; `release-button[href]` ends with `releases/tag/v4.0.0`. |

What none of those see:

- **The inline JS in `landing.html`** — the `fetch('/api/subscribe')` call
  and the `window.location.href = '/subscribed'` redirect. A typo in that
  script ships silently otherwise.
- **The full pipeline as one unit** — scanner ticks → notifier drains →
  email lands → user clicks → state machine flips → next scanner tick is
  quiet. Integration suites validate each cron in isolation; only this test
  proves they compose correctly with the HTTP layer and the database in
  between.
- **The email HTML actually rendering** — `Browser.LoadEmailHTML` loads the
  raw HTML body into a fresh `BrowserContext` and runs `data-testid`
  selectors on it. If a template variable fails to expand, or if a CTA link
  loses its href, the structural assertions fail loudly.
- **The unsubscribe link being correct end-to-end** — its `href` must be a
  real token the backend accepts; the browser actually navigates to it and
  the `/unsubscribed` page must render.
- **Re-subscribe after unsubscribe** — phases 7–10 prove that the same
  email/repo pair can flow through the lifecycle again without the
  unsubscribed history blocking it.

## Important details

### Scanner notification rule (phases 4, 6, 10)

`scanner.shouldNotify` ([scanner.go:128](../../../internal/scanner/scanner.go))
returns `true` only when `LastSeenTag != nil && latestTag != *LastSeenTag`.
That means the very first tick after a subscription becomes active records
the current tag as `last_seen` **without emitting a notification**.

- **Phase 4** polls `repositories.last_seen_tag` until it equals `v1.0.0`
  via `Postgres.WaitForLastSeen`. That row update is the side effect that
  proves a `scanner.Tick` has completed. Only then does the test bump the
  fixture to `v2.0.0` — without the baseline write the next tick would see
  `v2.0.0` as the only-and-baseline tag and stay silent.
- **Phase 6** publishes `v3.0.0`, polls for `last_seen_tag = v3.0.0`, and
  then calls `Postgres.AssertNoReleaseNotifications`. `UpsertLastSeen` and
  `InsertNotifications` share a single transaction
  (`internal/scanner/scanner.go`), so once the baseline tag is visible the
  negative check is decisive: no row was inserted for the inactive
  subscription, so nothing downstream can deliver an email. The mailbox
  assertion is kept as a belt-and-suspenders observable.
- **Phase 10** skips the baseline poll because `last_seen_tag = v3.0.0` is
  already persisted from phase 6. Setting `v4.0.0` is a fresh transition
  on the next tick, so the existing `WaitForMessages` is sufficient.

Both polls have a 5 s deadline — comfortably above the 50 ms worker tick.
`testing/synctest` does not fit here because pgx and SMTP run on real
`net.Conn` and break out of the bubble.

### Release URL is composed, not fetched

`internal/notifier/notifier.go:107` builds the release URL as
`https://github.com/<owner>/<name>/releases/tag/<tag>`. The GraphQL fixture
only programs `latestRelease.tagName`; the URL the email carries is
synthesized by the notifier. The phase 4 assertion compares against the
composed string directly.

### BaseURL wiring inside `E2EApp`

`NewE2EApp` ([tests/internal/app.go](../../internal/app.go)) starts the
`httptest.Server` with a placeholder handler first so the OS binds a port
and `server.URL` is known, then builds the subscription service and router
with that URL as `AppBaseURL`, then swaps the real handler in via
`server.Config.Handler = router`. This is why the links the browser clicks
in phases 2, 4, 5, 8 actually resolve to the test server — the integration
default `http://test.local` would not.

### Selectors are stable

Every element the test reads or clicks carries a `data-testid` attribute,
both on the HTML pages
([internal/subscription/http/pages/templates](../../../internal/subscription/http/pages/templates))
and the email templates
([internal/notifier/emailer/templates](../../../internal/notifier/emailer/templates)).
The pages also keep their existing `id` attributes (the inline JS in
`landing.html` keys off them); `data-testid` is added in parallel for the
test contract alone, so the page's runtime behavior and the test
selectors can change independently.

### Browser lifecycle

One `Browser` instance per suite (`SetupSuite`) launches a single Chromium.
Each `Browser.NewPage(t)` / `Browser.LoadEmailHTML(t, html)` creates a
fresh `BrowserContext` and closes it on `t.Cleanup`, so cookies/storage do
not leak between tests. On `t.Failed()` the page wrapper writes a
screenshot to `tests/e2e/_artifacts/<TestName>.png` (gitignored).

### Workers are real goroutines

`NewE2EApp` starts `scanner.Run`, `notifier.Run`, and `confirmer.Run` on
the same `context.CancelFunc` so `t.Cleanup` stops all three before the
testcontainer pool is closed. There is no manual `Flush()` orchestration —
emails arrive the same way they do in production, and the test polls
Mailpit via the existing `WaitForMessages` helper to bridge the
indexing-delay window.

### Email HTML rendered, not regex-parsed

`Mailpit.MessageHTML` calls Mailpit's `/api/v1/message/{id}` endpoint
which already does MIME parsing for us; the returned HTML body is fed
into `Browser.LoadEmailHTML` via `page.SetContent`. From there the test
runs `data-testid` selectors and `GetAttribute("href")` like on any other
page. This catches broken templates that a `strings.Contains(body, "http")`
integration assertion would silently accept.

## Running

```sh
make test-e2e
# debug a failure with a visible browser:
PLAYWRIGHT_HEADED=1 go test -tags=e2e -count=1 -v ./tests/e2e/flow/
```

First run downloads Chromium (~150 MB, 30–60 s). Subsequent runs complete
in ~5 s on a warm machine. A failing run leaves a screenshot in
`tests/e2e/_artifacts/`.
