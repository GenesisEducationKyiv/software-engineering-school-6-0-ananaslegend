# Integration Tests for `/api/confirm/{token}` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 6 integration tests for `GET /api/confirm/{token}` on the existing `SubscriptionSuite`, reusing the Postgres container, GitHub REST fixture, and wired `App` from the subscribe suite. No production code changes.

**Architecture:** Tests are added as methods on `SubscriptionSuite` (`tests/integration/subscription/`). One new file `confirm_test.go` holds the 6 tests; `suite_test.go` gains 3 helpers (`get`, `getConfirmTokenForEmail`, `setConfirmTokenExpired`) and one refactor (extract `assertCounter` from `assertCreatedCounter`, add `assertConfirmedCounter`). Build tag `//go:build integration` keeps the default `go test ./...` Docker-free.

**Tech Stack:** Go, `testify/suite`, `testify/require`, `testify/assert`, `testcontainers-go` (Postgres), `gofakeit/v7`, `prometheus/client_golang`, `chi/v5`.

**Commit policy for this session:** Per the user-confirmed session preference, **do not commit per task**. Verify with `go test` after each task; the user batches all work into a single commit at the end.

**Spec:** `docs/superpowers/specs/2026-05-14-integration-tests-confirm-design.md`.

---

## Task 1: Extend `suite_test.go` with helpers and refactor `assertCounter`

**Files:**
- Modify: `tests/integration/subscription/suite_test.go`

This task is a pure addition + tiny refactor. After it, the 8 existing subscribe tests must still pass without changes (they call `assertCreatedCounter`, which becomes a one-line wrapper over the new `assertCounter`).

- [ ] **Step 1.1: Confirm no import changes are needed for Task 1**

All packages required by the new helpers (`net/http`, `time`, `context`) are already imported by `suite_test.go`. No edits to the import block in this task.

- [ ] **Step 1.2: Add `get` helper to the `// --- HTTP helpers ---` section, immediately after `decodeJSON`**

Insert after the `decodeJSON` method:

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

- [ ] **Step 1.3: Add `getConfirmTokenForEmail` and `setConfirmTokenExpired` to the `// --- DB helpers ---` section, immediately after `selectRepository`**

Insert after the `selectRepository` method:

```go
func (s *SubscriptionSuite) getConfirmTokenForEmail(email string) string {
	s.T().Helper()
	var token *string
	require.NoError(s.T(), s.pg.Pool.QueryRow(s.ctx,
		`SELECT confirm_token FROM subscriptions WHERE email = $1`,
		email,
	).Scan(&token))
	require.NotNil(s.T(), token, "confirm_token expected for email %s", email)
	return *token
}

func (s *SubscriptionSuite) setConfirmTokenExpired(subID int64, expiresAt time.Time) {
	s.T().Helper()
	_, err := s.pg.Pool.Exec(s.ctx,
		`UPDATE subscriptions SET confirm_token_expires_at = $1 WHERE id = $2`,
		expiresAt, subID,
	)
	require.NoError(s.T(), err)
}
```

- [ ] **Step 1.4: Refactor `assertCreatedCounter` into `assertCounter` + two wrappers**

Replace the entire current `assertCreatedCounter` method (the only method under `// --- Metrics ---`) with the following three methods:

```go
// assertCounter sums every observed sample of the named counter in the suite's
// registry and asserts it equals want. Works whether or not the counter has
// been touched (the metric may be absent from Gather() output entirely if it
// has never been incremented, which yields got=0).
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

func (s *SubscriptionSuite) assertConfirmedCounter(want float64) {
	s.T().Helper()
	s.assertCounter("subscriptions_confirmed_total", want)
}
```

- [ ] **Step 1.5: Verify the file compiles and existing subscribe tests still pass**

Run:

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite' -v ./tests/integration/subscription/...
```

Expected: 8 PASS (all `TestSubscribe_*` methods). No `TestConfirm_*` yet — they don't exist.

If anything fails: revert step 1.4 — the refactor is the only behaviour-touching change. Inspect the diff of `assertCounter` vs the old `assertCreatedCounter` carefully (look for typos in the counter name or the require message label).

- [ ] **Step 1.6: STOP — do not commit. Report Task 1 done.**

---

## Task 2: `TestConfirm_HappyPath`

**Files:**
- Create: `tests/integration/subscription/confirm_test.go`

This task creates the new file with the build tag, imports, and the first test method.

- [ ] **Step 2.1: Create `confirm_test.go` with build tag, package, imports, and `TestConfirm_HappyPath`**

Use exactly this import block. `sync` (Task 6) and `strings` (Task 7) are intentionally **not** imported yet — the Go compiler rejects unused imports, so they must be added at the moment of first use.

```go
//go:build integration

package subscription_test

import (
	"io"
	"net/http"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)

func (s *SubscriptionSuite) TestConfirm_HappyPath() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	resp := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	sub := subs[0]
	require.NotNil(s.T(), sub.ConfirmedAt)
	assert.True(s.T(), sub.ConfirmedAt.After(time.Now().Add(-time.Minute)),
		"confirmed_at should be recent: %v", sub.ConfirmedAt)
	assert.Nil(s.T(), sub.ConfirmToken, "confirm_token must be cleared after confirmation")
	assert.Nil(s.T(), sub.ConfirmTokenExpiresAt, "confirm_token_expires_at must be cleared after confirmation")

	s.assertConfirmedCounter(1)
}
```

- [ ] **Step 2.2: Run `TestConfirm_HappyPath`**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestConfirm_HappyPath' -v ./tests/integration/subscription/...
```

Expected: PASS. The production code is already correct; this test verifies the end-to-end wiring.

- [ ] **Step 2.3: STOP — do not commit. Report Task 2 done.**

---

## Task 3: `TestConfirm_TokenNotFound`

**Files:**
- Modify: `tests/integration/subscription/confirm_test.go`

- [ ] **Step 3.1: Append `TestConfirm_TokenNotFound` to `confirm_test.go`**

Append below `TestConfirm_HappyPath`:

```go
func (s *SubscriptionSuite) TestConfirm_TokenNotFound() {
	resp := s.get("/api/confirm/non-existent-token-abc123")
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	s.assertConfirmedCounter(0)
}
```

- [ ] **Step 3.2: Run it**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestConfirm_TokenNotFound' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 3.3: STOP — do not commit. Report Task 3 done.**

---

## Task 4: `TestConfirm_TokenExpired`

**Files:**
- Modify: `tests/integration/subscription/confirm_test.go`

- [ ] **Step 4.1: Append `TestConfirm_TokenExpired`**

Append below `TestConfirm_TokenNotFound`:

```go
func (s *SubscriptionSuite) TestConfirm_TokenExpired() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	subs := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), subs, 1)
	require.NotNil(s.T(), subs[0].ConfirmToken)
	token := *subs[0].ConfirmToken
	subID := subs[0].ID

	s.setConfirmTokenExpired(subID, time.Now().Add(-time.Hour))

	resp := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusGone, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	after := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), after, 1)
	assert.Nil(s.T(), after[0].ConfirmedAt, "confirmed_at must remain NULL when token is expired")
	require.NotNil(s.T(), after[0].ConfirmToken, "confirm_token must NOT be cleared when token is expired")
	assert.Equal(s.T(), token, *after[0].ConfirmToken)

	s.assertConfirmedCounter(0)
}
```

- [ ] **Step 4.2: Run it**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestConfirm_TokenExpired' -v ./tests/integration/subscription/...
```

Expected: PASS. The expired branch in `service.Confirm` returns `ErrTokenExpired` before reaching `MarkConfirmed`, so the token and `confirmed_at` are unchanged in DB.

- [ ] **Step 4.3: STOP — do not commit. Report Task 4 done.**

---

## Task 5: `TestConfirm_TokenIsConsumed`

**Files:**
- Modify: `tests/integration/subscription/confirm_test.go`

- [ ] **Step 5.1: Append `TestConfirm_TokenIsConsumed`**

```go
func (s *SubscriptionSuite) TestConfirm_TokenIsConsumed() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	resp1 := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusOK, resp1.StatusCode)
	resp1.Body.Close()

	// MarkConfirmed sets confirm_token = NULL, so the same URL must now return 404.
	resp2 := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusNotFound, resp2.StatusCode)
	assert.Contains(s.T(), resp2.Header.Get("Content-Type"), "text/html")
	resp2.Body.Close()

	s.assertConfirmedCounter(1)
}
```

- [ ] **Step 5.2: Run it**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestConfirm_TokenIsConsumed' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 5.3: STOP — do not commit. Report Task 5 done.**

---

## Task 6: `TestConfirm_Concurrent`

**Files:**
- Modify: `tests/integration/subscription/confirm_test.go`

This task adds `sync` to the import block.

- [ ] **Step 6.1: Add `sync` to the import block**

Edit the imports in `confirm_test.go` so the block becomes:

```go
import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)
```

- [ ] **Step 6.2: Append `TestConfirm_Concurrent`**

```go
func (s *SubscriptionSuite) TestConfirm_Concurrent() {
	s.githubFx.SetRepoExists("golang/go", true)
	email := s.randomEmail()

	subResp := s.post("/api/subscribe", subhttp.SubscribeRequest{
		Email:      email,
		Repository: "golang/go",
	})
	require.Equal(s.T(), http.StatusAccepted, subResp.StatusCode)
	subResp.Body.Close()

	token := s.getConfirmTokenForEmail(email)

	const concurrency = 5
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		statuses []int
	)
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			resp := s.get("/api/confirm/" + token)
			resp.Body.Close()
			mu.Lock()
			statuses = append(statuses, resp.StatusCode)
			mu.Unlock()
		}()
	}
	wg.Wait()

	require.Len(s.T(), statuses, concurrency)
	successes := 0
	for _, st := range statuses {
		switch st {
		case http.StatusOK:
			successes++
		case http.StatusNotFound:
			// expected once the token has been consumed by a sibling goroutine
		default:
			s.T().Fatalf("unexpected status code: %d (statuses=%v)", st, statuses)
		}
	}
	assert.GreaterOrEqual(s.T(), successes, 1, "at least one concurrent request must succeed")

	after := s.selectSubscriptionsByEmail(email)
	require.Len(s.T(), after, 1)
	assert.NotNil(s.T(), after[0].ConfirmedAt, "confirmed_at must be set after concurrent confirm")
	assert.Nil(s.T(), after[0].ConfirmToken, "confirm_token must be cleared after concurrent confirm")

	// Counter is incremented once per successful service.Confirm — by construction
	// it should match the number of HTTP 200 responses we observed.
	s.assertCounter("subscriptions_confirmed_total", float64(successes))
}
```

- [ ] **Step 6.3: Run it**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestConfirm_Concurrent' -v ./tests/integration/subscription/...
```

Expected: PASS. Re-run 3 times to confirm stability:

```bash
go test -tags=integration -count=3 -run 'TestSubscriptionSuite/TestConfirm_Concurrent' -v ./tests/integration/subscription/...
```

Note: `-count=3` reuses the same Postgres container (set up in `SetupSuite`) but re-runs `SetupTest` per iteration. If flakes appear in the form of `500` or panics, that's a real find — escalate before changing the test.

- [ ] **Step 6.4: STOP — do not commit. Report Task 6 done.**

---

## Task 7: `TestConfirm_LongRandomToken`

**Files:**
- Modify: `tests/integration/subscription/confirm_test.go`

This task adds `strings` to the import block.

- [ ] **Step 7.1: Add `strings` to the import block**

Edit imports so the block becomes:

```go
import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
)
```

- [ ] **Step 7.2: Append `TestConfirm_LongRandomToken`**

```go
func (s *SubscriptionSuite) TestConfirm_LongRandomToken() {
	// 1024 hex characters — far above legitimate token length (64 hex),
	// safely under any URL-length limit. Asserts the route does not panic
	// or 500 on pathological path parameters.
	token := strings.Repeat("abcdef0123456789", 64)
	require.Len(s.T(), token, 1024)

	resp := s.get("/api/confirm/" + token)
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
	assert.Contains(s.T(), resp.Header.Get("Content-Type"), "text/html")

	body, err := io.ReadAll(resp.Body)
	require.NoError(s.T(), err)
	resp.Body.Close()
	assert.NotEmpty(s.T(), body)

	s.assertConfirmedCounter(0)
}
```

- [ ] **Step 7.3: Run it**

```bash
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestConfirm_LongRandomToken' -v ./tests/integration/subscription/...
```

Expected: PASS.

- [ ] **Step 7.4: STOP — do not commit. Report Task 7 done.**

---

## Task 8: Full-suite verification + vet + lint

This is the consolidation task — run everything from a cold start, ensure no regressions in subscribe, no lint complaints in the new file.

- [ ] **Step 8.1: Run the entire subscription integration suite**

```bash
go test -tags=integration -count=1 -v ./tests/integration/subscription/...
```

Expected: 14 PASS total (8 `TestSubscribe_*` + 6 `TestConfirm_*`).

- [ ] **Step 8.2: Run the full project unit-test suite to confirm no surface-area damage**

```bash
go test ./...
```

Expected: all PASS. (Unit tests don't touch the integration package; this guards against accidental changes elsewhere.)

- [ ] **Step 8.3: Run `make test-all`**

```bash
make test-all
```

Expected: all PASS. This is the user-facing "full" command — exercising it catches any Makefile drift.

- [ ] **Step 8.4: Run `go vet`**

```bash
go vet -tags=integration ./tests/integration/...
```

Expected: no output (clean).

- [ ] **Step 8.5: Run linter**

```bash
golangci-lint run --build-tags=integration ./tests/integration/...
```

Expected: no issues. If golangci-lint isn't installed locally, run `make lint-install` first.

If any issue surfaces (e.g., `errcheck` complaining about un-checked `resp.Body.Close()`), prefer the smallest fix that keeps the test readable: assign and discard explicitly (`_ = resp.Body.Close()`) — do **not** suppress lint rules globally.

- [ ] **Step 8.6: Report Task 8 done — work is verified and ready for commit**

**Do not commit.** Print a summary listing all changed files (`git status`) and tell the user the suite is green and ready for the single batch commit they will trigger.

---

## Verification Map (Spec ↔ Plan)

| Spec section | Plan task |
| --- | --- |
| New helpers `get`, `getConfirmTokenForEmail`, `setConfirmTokenExpired` | Task 1 |
| `assertCounter` refactor + `assertConfirmedCounter` wrapper | Task 1 |
| Existing subscribe tests must keep passing | Task 1 (step 1.5) |
| Test #1 `TestConfirm_HappyPath` | Task 2 |
| Test #2 `TestConfirm_TokenNotFound` | Task 3 |
| Test #3 `TestConfirm_TokenExpired` | Task 4 |
| Test #4 `TestConfirm_TokenIsConsumed` | Task 5 |
| Test #5 `TestConfirm_Concurrent` (floor assertion) | Task 6 |
| Test #6 `TestConfirm_LongRandomToken` | Task 7 |
| HTML asserts limited to `Content-Type` + non-empty body | Tasks 2–7 |
| No production code changes | All tasks (none of them touch `internal/...`) |
| `//go:build integration` gating | Task 2 (step 2.1, top of file) |
| Final full-suite run + lint | Task 8 |