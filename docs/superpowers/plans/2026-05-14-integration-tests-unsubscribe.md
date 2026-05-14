# Integration Tests for `/api/unsubscribe/{token}` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an integration-tagged test suite covering the `GET /api/unsubscribe/{token}` endpoint end-to-end (HTTP → service → real Postgres → FK CASCADE), verifying the GDPR hard-delete contract, idempotency, and exactly-once success under concurrent requests.

**Architecture:** Six new `(s *SubscriptionSuite) TestUnsubscribe_*` methods in a new file `tests/integration/subscription/unsubscribe_test.go`, mounted on the existing `SubscriptionSuite`. Three small additions to `suite_test.go`: (1) refactor `assertCreatedCounter` into a parameterised `assertCounter`, (2) add a `get` HTTP helper, (3) add five unsubscribe-specific DB / metric helpers. Reuses the Postgres testcontainer, GitHub REST fixture, and `App` composition root already in place from the `/api/subscribe` work — no production code changes, no new test infrastructure files.

**Tech Stack:** Go 1.x · `testify/suite` · `testcontainers-go/postgres` · `httptest.Server` · `pgx/v5` · `prometheus/client_golang` · `chi/v5` · `gofakeit/v7`.

**Spec:** `docs/superpowers/specs/2026-05-14-integration-tests-unsubscribe-design.md`.

**Commit policy:** The user controls git history. Every "Commit" step below shows the exact `git add` / `git commit` command, but the engineer should pause and let the user execute it (or invoke `commit-commands:commit`) rather than running it autonomously.

---

## File Structure

**New file:**
- `tests/integration/subscription/unsubscribe_test.go` — six `TestUnsubscribe_*` methods on `SubscriptionSuite`. Build tag `//go:build integration`. ~250 lines.

**Modified file:**
- `tests/integration/subscription/suite_test.go` — add `assertCounter` (parameterised), keep `assertCreatedCounter` as a one-line wrapper; add `get` HTTP helper; add `getUnsubscribeTokenForEmail`, `countSubscriptionsByEmail`, `countReleaseNotifications`, `insertReleaseNotification`, `assertDeletedCounter`. ~70 lines net add.

**Untouched:**
- `tests/integration/internal/{pg,github,app}.go` — reused as-is.
- `internal/subscription/{http,service,repository,domain,http/pages}/*.go` — no production code changes (spec confirms).
- `migrations/*.sql` — unchanged.
- `Makefile` — `make test-integration` and `make test-all` already cover `tests/integration/...` via the build tag.

---

## Task 1: Refactor `assertCreatedCounter` into parameterised `assertCounter`

**Files:**
- Modify: `tests/integration/subscription/suite_test.go`

Extract the metric-name lookup loop into `assertCounter(name, want)` so it can be reused for `subscriptions_deleted_total` (and, separately, for `subscriptions_confirmed_total` in the confirm work). Keep `assertCreatedCounter` as a thin wrapper so the existing 8 subscribe tests stay untouched.

- [ ] **Step 1.1: Replace `assertCreatedCounter` with `assertCounter` + wrapper**

In `tests/integration/subscription/suite_test.go`, find the existing block:

```go
// assertCreatedCounter sums every observed subscriptions_created_total sample
// in the suite's registry. Works whether or not the counter has been touched
// (testutil.GatherAndCompare requires the metric to be present, which is not
// guaranteed in error-path tests where service.Subscribe never runs).
func (s *SubscriptionSuite) assertCreatedCounter(want float64) {
	s.T().Helper()
	mf, err := s.app.Registry.Gather()
	require.NoError(s.T(), err)
	var got float64
	for _, m := range mf {
		if m.GetName() != "subscriptions_created_total" {
			continue
		}
		for _, metric := range m.GetMetric() {
			got += metric.GetCounter().GetValue()
		}
	}
	require.Equal(s.T(), want, got, "subscriptions_created_total")
}
```

Replace it with:

```go
// assertCounter sums every observed sample for the named counter and asserts
// it equals want. Works whether or not the counter has been touched (the
// metric may not appear in Gather() output if it was never incremented, which
// testutil.GatherAndCompare cannot handle).
func (s *SubscriptionSuite) assertCounter(name string, want float64) {
	s.T().Helper()
	mf, err := s.app.Registry.Gather()
	require.NoError(s.T(), err)
	var got float64
	for _, m := range mf {
		if m.GetName() != name {
			continue
		}
		for _, metric := range m.GetMetric() {
			got += metric.GetCounter().GetValue()
		}
	}
	require.Equal(s.T(), want, got, name)
}

func (s *SubscriptionSuite) assertCreatedCounter(want float64) {
	s.T().Helper()
	s.assertCounter("subscriptions_created_total", want)
}
```

- [ ] **Step 1.2: Run the existing subscribe suite to verify no regression**

Run: `make test-integration`

Expected: all `TestSubscribe_*` cases pass. (The refactor is behaviour-preserving; if anything fails, revert and investigate before continuing.)

- [ ] **Step 1.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/suite_test.go
git commit -m "test(integration): extract assertCounter helper for reuse"
```

---

## Task 2: Add `get` HTTP helper

**Files:**
- Modify: `tests/integration/subscription/suite_test.go`

The suite already has `post(path, body)`. Add the GET-shaped equivalent. No production code or test calls it yet — Task 4 will be its first consumer.

- [ ] **Step 2.1: Add the `get` helper**

In `tests/integration/subscription/suite_test.go`, immediately below the existing `post` method (in the `// --- HTTP helpers ---` section), append:

```go
func (s *SubscriptionSuite) get(path string) *http.Response {
	s.T().Helper()
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.app.Server.URL+path, nil)
	require.NoError(s.T(), err)

	resp, err := s.app.Client.Do(req)
	require.NoError(s.T(), err)
	return resp
}
```

- [ ] **Step 2.2: Verify compilation**

Run: `go build -tags=integration ./tests/integration/...`

Expected: exit 0, no output. (No tests to run yet — the helper is unreferenced; Go's `unused` lint is satisfied because methods on a struct aren't flagged.)

- [ ] **Step 2.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/suite_test.go
git commit -m "test(integration): add get HTTP helper to SubscriptionSuite"
```

---

## Task 3: Add unsubscribe-specific DB and metric helpers

**Files:**
- Modify: `tests/integration/subscription/suite_test.go`

Five helpers required by the test cases:
1. `getUnsubscribeTokenForEmail` — read the token a test will send back at the endpoint.
2. `countSubscriptionsByEmail` — sharper than `len(selectSubscriptionsByEmail(...))` for presence/absence.
3. `countReleaseNotifications` — prove CASCADE removed the seeded outbox row.
4. `insertReleaseNotification` — seed the release outbox manually (no production path creates these at subscribe time).
5. `assertDeletedCounter` — one-liner mirror of `assertCreatedCounter`.

- [ ] **Step 3.1: Add DB helpers in the `// --- DB helpers ---` section**

In `tests/integration/subscription/suite_test.go`, immediately below the existing `selectRepository` method, append:

```go
func (s *SubscriptionSuite) getUnsubscribeTokenForEmail(email string) string {
	s.T().Helper()
	var token string
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT unsubscribe_token FROM subscriptions WHERE email = $1`, email,
	).Scan(&token))
	require.NotEmpty(s.T(), token)
	return token
}

func (s *SubscriptionSuite) countSubscriptionsByEmail(email string) int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM subscriptions WHERE email = $1`, email,
	).Scan(&n))
	return n
}

func (s *SubscriptionSuite) countReleaseNotifications(subID int64) int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM release_notifications WHERE subscription_id = $1`, subID,
	).Scan(&n))
	return n
}

func (s *SubscriptionSuite) insertReleaseNotification(subID, repoID int64, tag string) {
	s.T().Helper()
	_, err := s.pg.Pool.Exec(s.ctx,
		`INSERT INTO release_notifications (subscription_id, repository_id, release_tag) VALUES ($1, $2, $3)`,
		subID, repoID, tag,
	)
	require.NoError(s.T(), err)
}
```

- [ ] **Step 3.2: Add metric helper in the `// --- Metrics ---` section**

At the end of `tests/integration/subscription/suite_test.go`, after `assertCreatedCounter`, append:

```go
func (s *SubscriptionSuite) assertDeletedCounter(want float64) {
	s.T().Helper()
	s.assertCounter("subscriptions_deleted_total", want)
}
```

- [ ] **Step 3.3: Verify compilation**

Run: `go build -tags=integration ./tests/integration/...`

Expected: exit 0.

- [ ] **Step 3.4: Re-run existing subscribe suite to confirm no behaviour drift**

Run: `make test-integration`

Expected: all 8 `TestSubscribe_*` cases pass.

- [ ] **Step 3.5: Commit (user runs this)**

```bash
git add tests/integration/subscription/suite_test.go
git commit -m "test(integration): add unsubscribe-specific suite helpers"
```

---

## Task 4: Scaffold `unsubscribe_test.go` with the simplest case (token not found)

**Files:**
- Create: `tests/integration/subscription/unsubscribe_test.go`

Start with the case that needs zero setup: a fresh DB and a path parameter that matches no row. Validates that the new file compiles, the build tag is correct, the suite picks up the new method, and the route is wired.

- [ ] **Step 4.1: Create the file with package header and the first test**

Create `tests/integration/subscription/unsubscribe_test.go` with the following content:

```go
//go:build integration

package subscription_test

import (
	"io"
	"net/http"
	"strings"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *SubscriptionSuite) TestUnsubscribe_TokenNotFound() {
	resp := s.get("/api/unsubscribe/non-existent-token-abc123")
	defer resp.Body.Close()

	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	// No subscription rows exist (truncated by SetupTest).
	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(""))
	s.assertDeletedCounter(0)
}

// _ silences "imported and not used" if a future test removes its only consumer.
var _ = strings.Contains
```

(The `var _ = strings.Contains` line is a hack to keep the `strings` import legal during incremental development — remove it in the last task once a real consumer exists. Alternative: omit the `strings` import entirely from this initial scaffold and add it later when needed.)

**Cleaner alternative — drop the unused import outright:**

```go
//go:build integration

package subscription_test

import (
	"io"
	"net/http"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *SubscriptionSuite) TestUnsubscribe_TokenNotFound() {
	resp := s.get("/api/unsubscribe/non-existent-token-abc123")
	defer resp.Body.Close()

	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(""))
	s.assertDeletedCounter(0)
}
```

Use the cleaner version.

- [ ] **Step 4.2: Run the new test**

Run: `go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_TokenNotFound' ./tests/integration/subscription/...`

Expected: PASS. (The handler exists, the route is mounted, the renderer returns 404 + HTML for an unknown token.)

- [ ] **Step 4.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/unsubscribe_test.go
git commit -m "test(integration): add TestUnsubscribe_TokenNotFound"
```

---

## Task 5: `TestUnsubscribe_LongRandomToken`

**Files:**
- Modify: `tests/integration/subscription/unsubscribe_test.go`

A 1024-character path parameter — defensive check that the route doesn't panic, truncate, or return 500. Same expected outcome as Task 4 (404 + HTML), but exercises path-handling robustness.

- [ ] **Step 5.1: Add the test and the `strings` import**

In `tests/integration/subscription/unsubscribe_test.go`, update the import block to include `strings`:

```go
import (
	"io"
	"net/http"
	"strings"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Append the test below `TestUnsubscribe_TokenNotFound`:

```go
func (s *SubscriptionSuite) TestUnsubscribe_LongRandomToken() {
	// 1024 chars — far above legitimate token length (~43 base64url chars),
	// well under any reasonable URL-length limit.
	token := strings.Repeat("a1b2c3d4", 128)
	require.Equal(s.T(), 1024, len(token))

	resp := s.get("/api/unsubscribe/" + token)
	defer resp.Body.Close()

	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	s.assertDeletedCounter(0)
}
```

- [ ] **Step 5.2: Run the new test**

Run: `go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_LongRandomToken' ./tests/integration/subscription/...`

Expected: PASS.

- [ ] **Step 5.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/unsubscribe_test.go
git commit -m "test(integration): add TestUnsubscribe_LongRandomToken"
```

---

## Task 6: `TestUnsubscribe_HappyPath` — the GDPR CASCADE test

**Files:**
- Modify: `tests/integration/subscription/unsubscribe_test.go`

The headline test. Subscribe via API (which creates a `confirmation_notifications` row in the same transaction), manually INSERT a `release_notifications` row to seed the second outbox, then unsubscribe and prove every dependent row is gone — that's the GDPR hard-delete contract end-to-end.

- [ ] **Step 6.1: Append the test**

In `tests/integration/subscription/unsubscribe_test.go`, append:

```go
func (s *SubscriptionSuite) TestUnsubscribe_HappyPath() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	// Subscribe via the real HTTP path so the suite mirrors production flow.
	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	// Capture subID and repoID before mutating anything.
	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	subID := subs[0].ID

	repoID, ok := s.selectRepository("golang", "go")
	require.True(s.T(), ok)

	// Seed a release_notifications row to exercise the FK CASCADE on that table.
	s.insertReleaseNotification(subID, repoID, "v1.0.0")
	require.Equal(s.T(), 1, s.countReleaseNotifications(subID))
	require.Equal(s.T(), 1, s.countConfirmationNotifications(subID))

	// Capture token and unsubscribe.
	token := s.getUnsubscribeTokenForEmail(email)
	unsubResp := s.get("/api/unsubscribe/" + token)
	defer unsubResp.Body.Close()

	require.Equal(s.T(), http.StatusOK, unsubResp.StatusCode)
	assert.Contains(s.T(), unsubResp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(unsubResp.Body)
	require.NoError(s.T(), err)
	assert.NotEmpty(s.T(), body)

	// GDPR hard delete: subscription + both outbox tables purged.
	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	assert.Equal(s.T(), 0, s.countConfirmationNotifications(subID), "CASCADE on confirmation_notifications.subscription_id")
	assert.Equal(s.T(), 0, s.countReleaseNotifications(subID), "CASCADE on release_notifications.subscription_id")

	s.assertDeletedCounter(1)
}
```

- [ ] **Step 6.2: Run the new test**

Run: `go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_HappyPath$' ./tests/integration/subscription/...`

(The `$` anchor prevents matching `TestUnsubscribe_HappyPath_AfterConfirm`, which doesn't exist yet but will in Task 7.)

Expected: PASS.

- [ ] **Step 6.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/unsubscribe_test.go
git commit -m "test(integration): add TestUnsubscribe_HappyPath with GDPR CASCADE check"
```

---

## Task 7: `TestUnsubscribe_HappyPath_AfterConfirm`

**Files:**
- Modify: `tests/integration/subscription/unsubscribe_test.go`

Twin of Task 6, but the subscription is confirmed first via `GET /api/confirm/{token}`. Proves the unsubscribe path is independent of `confirmed_at`. Catches future "defensive" edits that might inadvertently scope `DeleteByUnsubscribeToken` to confirmed rows.

This test needs the confirm token. Helper note: the existing suite does not have a `getConfirmTokenForEmail` helper (only the confirm spec adds it). We'll inline the SQL here rather than add a helper used by exactly one test — keeping Task 3's helper list lean.

- [ ] **Step 7.1: Append the test**

In `tests/integration/subscription/unsubscribe_test.go`, append:

```go
func (s *SubscriptionSuite) TestUnsubscribe_HappyPath_AfterConfirm() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	// Subscribe.
	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	// Read both tokens in one query — confirm token first, unsubscribe token second.
	var confirmToken *string
	var unsubscribeToken string
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT confirm_token, unsubscribe_token FROM subscriptions WHERE email = $1`, email,
	).Scan(&confirmToken, &unsubscribeToken))
	require.NotNil(s.T(), confirmToken)
	require.NotEmpty(s.T(), *confirmToken)
	require.NotEmpty(s.T(), unsubscribeToken)

	// Confirm via the HTTP path.
	confResp := s.get("/api/confirm/" + *confirmToken)
	require.Equal(s.T(), http.StatusOK, confResp.StatusCode)
	confResp.Body.Close()

	// Sanity: the row is now confirmed and the confirm_token has been cleared,
	// but unsubscribe_token is preserved.
	postConfirm := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), postConfirm, 1)
	require.NotNil(s.T(), postConfirm[0].ConfirmedAt)
	require.Nil(s.T(), postConfirm[0].ConfirmToken)
	require.Equal(s.T(), unsubscribeToken, postConfirm[0].UnsubscribeToken)

	// Unsubscribe.
	unsubResp := s.get("/api/unsubscribe/" + unsubscribeToken)
	defer unsubResp.Body.Close()

	require.Equal(s.T(), http.StatusOK, unsubResp.StatusCode)
	assert.Contains(s.T(), unsubResp.Header.Get("Content-Type"), "text/html")

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	s.assertDeletedCounter(1)
}
```

- [ ] **Step 7.2: Run the new test**

Run: `go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_HappyPath_AfterConfirm' ./tests/integration/subscription/...`

Expected: PASS.

- [ ] **Step 7.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/unsubscribe_test.go
git commit -m "test(integration): add TestUnsubscribe_HappyPath_AfterConfirm"
```

---

## Task 8: `TestUnsubscribe_Idempotency`

**Files:**
- Modify: `tests/integration/subscription/unsubscribe_test.go`

Two sequential `GET /api/unsubscribe/{token}` calls on the same token. First returns 200, second returns 404 (the row is gone). The deleted-counter must read **exactly** 1 — proving the service short-circuits before `Inc()` when `RowsAffected == 0`.

- [ ] **Step 8.1: Append the test**

In `tests/integration/subscription/unsubscribe_test.go`, append:

```go
func (s *SubscriptionSuite) TestUnsubscribe_Idempotency() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	token := s.getUnsubscribeTokenForEmail(email)

	// First call: 200.
	r1 := s.get("/api/unsubscribe/" + token)
	require.Equal(s.T(), http.StatusOK, r1.StatusCode)
	r1.Body.Close()

	// Second call: 404 — the row is gone.
	r2 := s.get("/api/unsubscribe/" + token)
	defer r2.Body.Close()
	require.Equal(s.T(), http.StatusNotFound, r2.StatusCode)
	assert.Contains(s.T(), r2.Header.Get("Content-Type"), "text/html")

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	s.assertDeletedCounter(1) // increment exactly once
}
```

- [ ] **Step 8.2: Run the new test**

Run: `go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_Idempotency' ./tests/integration/subscription/...`

Expected: PASS.

- [ ] **Step 8.3: Commit (user runs this)**

```bash
git add tests/integration/subscription/unsubscribe_test.go
git commit -m "test(integration): add TestUnsubscribe_Idempotency"
```

---

## Task 9: `TestUnsubscribe_Concurrent`

**Files:**
- Modify: `tests/integration/subscription/unsubscribe_test.go`

Fire 5 goroutines at the same token simultaneously. Postgres' atomic `DELETE` under READ COMMITTED guarantees **exactly one** transaction sees `RowsAffected > 0`; the rest return `ErrTokenNotFound` → 404. Asserts: one 200, four 404s, no 500s, no panics, single counter increment, no subscription row left behind.

- [ ] **Step 9.1: Add `sync` to the import block**

In `tests/integration/subscription/unsubscribe_test.go`, update the import block to include `sync`:

```go
import (
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 9.2: Append the test**

```go
func (s *SubscriptionSuite) TestUnsubscribe_Concurrent() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	resp := s.post("/api/subscribe", map[string]string{
		"email":      email,
		"repository": "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	token := s.getUnsubscribeTokenForEmail(email)

	const N = 5
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		codes  = make([]int, 0, N)
		ready  = make(chan struct{})
	)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // align launch to maximise overlap
			r := s.get("/api/unsubscribe/" + token)
			mu.Lock()
			codes = append(codes, r.StatusCode)
			mu.Unlock()
			r.Body.Close()
		}()
	}
	close(ready)
	wg.Wait()

	// Exactly one 200, four 404s. No 500s, no panics (Recoverer middleware would
	// have turned a panic into a 500, which we'd fail on).
	var ok, notFound int
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusNotFound:
			notFound++
		default:
			s.T().Fatalf("unexpected status code %d in concurrent batch: %v", c, codes)
		}
	}
	assert.Equal(s.T(), 1, ok, "exactly one DELETE should observe RowsAffected>0")
	assert.Equal(s.T(), N-1, notFound, "the remaining requests should see the row already gone")

	assert.Equal(s.T(), 0, s.countSubscriptionsByEmail(email))
	s.assertDeletedCounter(1)
}
```

- [ ] **Step 9.3: Run the new test**

Run: `go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_Concurrent' ./tests/integration/subscription/...`

Expected: PASS.

If the strict `ok == 1` assertion ever flakes on the host CI, that is a real signal that someone introduced a non-atomic delete (e.g. a SELECT-then-DELETE pattern). Do **not** weaken the assertion without first checking the SQL.

- [ ] **Step 9.4: Run race detector for extra safety**

Run: `go test -tags=integration -race -count=1 -run 'TestSubscriptionSuite/TestUnsubscribe_Concurrent' ./tests/integration/subscription/...`

Expected: PASS, no `WARNING: DATA RACE` output.

- [ ] **Step 9.5: Commit (user runs this)**

```bash
git add tests/integration/subscription/unsubscribe_test.go
git commit -m "test(integration): add TestUnsubscribe_Concurrent"
```

---

## Task 10: Full-suite sanity run and final commit

**Files:**
- None (verification only).

- [ ] **Step 10.1: Run the entire integration suite**

Run: `make test-integration`

Expected output (last line):

```
ok  	github.com/ananaslegend/reposeetory/tests/integration/subscription	<duration>s
```

All `TestSubscribe_*` + all six new `TestUnsubscribe_*` cases pass. Total ~14 cases.

- [ ] **Step 10.2: Run the full test suite (unit + integration)**

Run: `make test-all`

Expected: every package passes. The refactor in Task 1 must not have broken any non-integration test (it can't — the helpers live in `_test.go` files tagged `integration` — but verifying is cheap).

- [ ] **Step 10.3: Lint**

Run: `make lint`

Expected: clean exit. The new code uses pre-existing patterns (helpers on `SubscriptionSuite`, `require`/`assert` from testify, `sync` from stdlib) and should not introduce any new lint warnings.

- [ ] **Step 10.4: Confirm the working tree state matches expectations**

Run: `git status`

Expected: working tree clean if the user committed after every task. If not, the only modified/untracked files should be:
- `tests/integration/subscription/suite_test.go` (modified)
- `tests/integration/subscription/unsubscribe_test.go` (untracked, then committed in earlier tasks)

No production code should appear in the diff.

---

## Self-Review Summary

**Spec coverage:**
- [x] §"File Layout" → Tasks 1–9 modify only the two files specified.
- [x] §"Dependencies" → no new go.mod entries; verified via no `go get` step.
- [x] §"Production Code Changes: None" → no task touches `internal/...` or `migrations/...`.
- [x] §"New Suite Helpers" — all five mapped to Task 3, plus `get` to Task 2 and `assertCounter` refactor to Task 1.
- [x] §"Test Cases" — all six mapped 1-to-1 to Tasks 4–9 (TokenNotFound → 4, LongRandomToken → 5, HappyPath → 6, HappyPath_AfterConfirm → 7, Idempotency → 8, Concurrent → 9).
- [x] §"Error Path Coverage" — `default → 500` explicitly noted out of scope in the plan's task selection.
- [x] §"Test Isolation" — relies on existing `SetupTest` `TRUNCATE ... RESTART IDENTITY CASCADE`; no new reset logic.
- [x] §"Makefile / CI / Docs: No changes" → confirmed in File Structure.

**Placeholder scan:** every code step has a complete code block; no "TBD" / "TODO" / "similar to" references; every test method shows the full body.

**Type consistency:** helper signatures match the spec (`getUnsubscribeTokenForEmail(email string) string`, `countReleaseNotifications(subID int64) int`, etc.). All test method bodies call helpers exactly as they're defined in Tasks 1–3. The `_AfterConfirm` test does not use `getConfirmTokenForEmail` (which is not introduced here) — it inlines the SQL, which is consistent with the plan's stated choice to keep Task 3's helper list lean.

**Edge-case sanity:**
- Task 6 inserts the release_notifications row **before** calling `/api/unsubscribe`, so its presence is a true precondition rather than incidental.
- Task 7 reads `confirm_token` as `*string` (matches the schema's nullable column).
- Task 9 closes response bodies inside the goroutine, before `wg.Done()`, to avoid leaking connections.
