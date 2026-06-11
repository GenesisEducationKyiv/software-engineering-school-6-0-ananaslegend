# RED metrics ctx-based API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `RED.Track(*string)` with a ctx-based recorder API (`Start` / `SetSuccess` / `SetError` / `SetResult`) and drop the `result="empty"` label across all workers.

**Architecture:** Stash a recorder on `ctx` in `redmetrics.Start`, mutate its result string via named setter functions, and emit the observation in the deferred `stop` closure — but only if a setter was called. Idle ticks call no setter and produce no sample.

**Tech Stack:** Go 1.22, `prometheus/client_golang`, `context.Context` with private struct key.

**Spec:** [`docs/superpowers/specs/2026-06-03-red-metrics-ctx-api-design.md`](../specs/2026-06-03-red-metrics-ctx-api-design.md)

**Working branch:** `HW-6`

**Commit cadence:** the user controls commits. Tasks do **not** include `git commit` steps. Verify diff cleanliness at the end of each task, then proceed; the user will commit when ready.

---

## Task 1: redmetrics ctx-based API + remove Track

**Files:**
- Modify: `internal/observability/redmetrics/redmetrics.go`
- Modify: `internal/observability/redmetrics/redmetrics_test.go`

This task is TDD: write the new tests first, watch them fail, implement, watch them pass, then delete the obsolete `Track` tests.

- [ ] **Step 1: Write new tests for the ctx API**

Replace the file `internal/observability/redmetrics/redmetrics_test.go` with the following content. (This keeps the three existing `TestNew_*` tests and adds the new ctx-API tests; the two `TestRED_Track_*` tests are removed because their target API is being deleted.)

```go
package redmetrics_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
)

func TestNew_RegistersMetricsWithCorrectNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo", Registry: reg})

	red.Observe("ok", 10*time.Millisecond)

	const expected = `
# HELP demo_requests_total Total number of demo operations.
# TYPE demo_requests_total counter
demo_requests_total{result="ok"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "demo_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestNew_NilRegistryDoesNotPanic(t *testing.T) {
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo"})
	red.Observe("ok", 5*time.Millisecond) // must not panic
}

func TestNew_WithExtraLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{
		Subsystem:   "email",
		ExtraLabels: []string{"driver"},
		Registry:    reg,
	})

	red.Observe("ok", 5*time.Millisecond, "resend")
	red.Observe("error", 5*time.Millisecond, "smtp")

	const expected = `
# HELP email_requests_total Total number of email operations.
# TYPE email_requests_total counter
email_requests_total{driver="resend",result="ok"} 1
email_requests_total{driver="smtp",result="error"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "email_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestStart_SetSuccessRecordsOk(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo", Registry: reg})

	func() {
		ctx, stop := redmetrics.Start(context.Background(), red)
		defer stop()
		redmetrics.SetSuccess(ctx)
	}()

	const expected = `
# HELP demo_requests_total Total number of demo operations.
# TYPE demo_requests_total counter
demo_requests_total{result="ok"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "demo_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestStart_SetErrorRecordsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo", Registry: reg})

	func() {
		ctx, stop := redmetrics.Start(context.Background(), red)
		defer stop()
		redmetrics.SetError(ctx)
	}()

	const expected = `
# HELP demo_requests_total Total number of demo operations.
# TYPE demo_requests_total counter
demo_requests_total{result="error"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "demo_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestStart_NoSetterSkipsObservation(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo", Registry: reg})

	func() {
		_, stop := redmetrics.Start(context.Background(), red)
		defer stop()
		// intentionally no setter
	}()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "demo_requests_total" {
			for _, m := range f.GetMetric() {
				if m.GetCounter().GetValue() != 0 {
					t.Fatalf("expected counter 0, got %v", m.GetCounter().GetValue())
				}
			}
		}
	}
}

func TestStart_SetResultCustomLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := redmetrics.New(redmetrics.Config{Subsystem: "demo", Registry: reg})

	func() {
		ctx, stop := redmetrics.Start(context.Background(), red)
		defer stop()
		redmetrics.SetResult(ctx, "rate_limited")
	}()

	const expected = `
# HELP demo_requests_total Total number of demo operations.
# TYPE demo_requests_total counter
demo_requests_total{result="rate_limited"} 1
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(expected), "demo_requests_total"); err != nil {
		t.Fatal(err)
	}
}

func TestStart_NilREDIsNoOp(t *testing.T) {
	ctx, stop := redmetrics.Start(context.Background(), nil)
	defer stop()
	redmetrics.SetSuccess(ctx)
	redmetrics.SetError(ctx)
	redmetrics.SetResult(ctx, "anything")
}

func TestSetters_WithoutStartAreNoOp(t *testing.T) {
	ctx := context.Background()
	redmetrics.SetSuccess(ctx)
	redmetrics.SetError(ctx)
	redmetrics.SetResult(ctx, "anything")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/observability/redmetrics/... -run TestStart -v`

Expected: compile error — `redmetrics.Start`, `redmetrics.SetSuccess`, `redmetrics.SetError`, `redmetrics.SetResult` undefined.

- [ ] **Step 3: Replace the helper file with the new implementation**

Overwrite `internal/observability/redmetrics/redmetrics.go` with:

```go
// Package redmetrics provides a small helper for the RED methodology
// (rate / errors / duration) — a uniform counter + histogram pair per subsystem.
//
// Usage with the ctx-based API:
//
//	ctx, stop := redmetrics.Start(ctx, n.red)
//	defer stop()
//	if err != nil { redmetrics.SetError(ctx); return }
//	redmetrics.SetSuccess(ctx)
//
// If no setter is called before stop, no observation is recorded — this makes
// "no work to do" branches naturally invisible in the metric stream.
package redmetrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Config controls how a RED metric pair is built.
type Config struct {
	// Subsystem becomes the metric prefix (e.g. "github_client" → "github_client_requests_total").
	Subsystem string
	// ExtraLabels are appended after the mandatory "result" label.
	// Example: []string{"driver"} for the email subsystem.
	ExtraLabels []string
	// Buckets overrides histogram buckets (nil → DefaultBuckets).
	Buckets []float64
	// Registry receives the metrics. nil is allowed (useful in tests).
	Registry *prometheus.Registry
}

// RED is a counter + histogram pair following the RED methodology.
type RED struct {
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	extraLabels []string
}

// DefaultBuckets cover sub-second latencies typical of HTTP / outbound calls.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// New constructs a RED pair and registers it on cfg.Registry (if non-nil).
func New(cfg Config) *RED {
	buckets := cfg.Buckets
	if buckets == nil {
		buckets = DefaultBuckets
	}
	labels := append([]string{"result"}, cfg.ExtraLabels...)
	r := &RED{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: cfg.Subsystem + "_requests_total",
			Help: "Total number of " + cfg.Subsystem + " operations.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    cfg.Subsystem + "_request_duration_seconds",
			Help:    "Duration of " + cfg.Subsystem + " operations in seconds.",
			Buckets: buckets,
		}, labels),
		extraLabels: cfg.ExtraLabels,
	}
	if cfg.Registry != nil {
		cfg.Registry.MustRegister(r.requests, r.duration)
	}
	return r
}

// Observe records one operation. extraLabels must be passed in the same order
// as Config.ExtraLabels; if no extra labels were declared, pass nothing.
func (m *RED) Observe(result string, dur time.Duration, extraLabels ...string) {
	if m == nil {
		return
	}
	values := append([]string{result}, extraLabels...)
	m.requests.WithLabelValues(values...).Inc()
	m.duration.WithLabelValues(values...).Observe(dur.Seconds())
}

type recorderKey struct{}

type recorder struct {
	red    *RED
	start  time.Time
	extras []string
	result string
}

// Start binds a recorder to ctx and returns a derived ctx plus a stop closure.
//
// The recorder starts in skip mode: if stop() fires without any setter
// (SetSuccess / SetError / SetResult) having been called, no observation is
// recorded.
//
// Nil-safe: when m is nil, Start returns the original ctx and a no-op stop.
func Start(ctx context.Context, m *RED, extraLabels ...string) (context.Context, func()) {
	if m == nil {
		return ctx, func() {}
	}
	r := &recorder{red: m, start: time.Now(), extras: extraLabels}
	return context.WithValue(ctx, recorderKey{}, r), func() {
		if r.result == "" {
			return
		}
		m.Observe(r.result, time.Since(r.start), r.extras...)
	}
}

// SetSuccess marks the operation bound to ctx as "ok".
func SetSuccess(ctx context.Context) { setResult(ctx, "ok") }

// SetError marks the operation bound to ctx as "error".
func SetError(ctx context.Context) { setResult(ctx, "error") }

// SetResult marks the operation bound to ctx with a domain-specific label
// (e.g. "rate_limited" for scanner, "cached" for github_client).
// Prefer SetSuccess / SetError for the universal cases.
func SetResult(ctx context.Context, result string) { setResult(ctx, result) }

func setResult(ctx context.Context, result string) {
	if r, ok := ctx.Value(recorderKey{}).(*recorder); ok {
		r.result = result
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/observability/redmetrics/... -v`

Expected: all 9 tests pass (3 `TestNew_*` + 6 new `TestStart_*` / `TestSetters_*`).

- [ ] **Step 5: Lint clean**

Run: `make lint`

Expected: `0 issues.` (If `wrapcheck` flags anything, recheck — none of the new code wraps errors, so it should be clean.)

---

## Task 2: Migrate scanner.Tick

**Files:**
- Modify: `internal/scanner/scanner.go`

The scanner's existing test `TestScanner_TickRecordsRED` already asserts only `result="ok"` for the success path, and `TestScanner_TickEmptyRun` (if present, search to confirm) — note: this codebase has no test asserting `result="empty"` on scanner, so no test edit is required. Verify in Step 3.

- [ ] **Step 1: Replace the Tick body**

In `internal/scanner/scanner.go`, replace the `Tick` function (currently at lines 85–141) with:

```go
// Tick executes one scan cycle. Exported for testing.
func (s *Scanner) Tick(ctx context.Context) error {
	ctx, stop := redmetrics.Start(ctx, s.red)
	defer stop()

	var emptyRun bool
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		repos, err := s.repo.GetRepositoriesWithLock(ctx, scanLimit)
		if err != nil {
			return fmt.Errorf("get repos with lock: %w", err)
		}

		if len(repos) == 0 {
			emptyRun = true
			return nil
		}
		s.m.reposScanned.Add(float64(len(repos)))

		tags, err := s.github.GetLatestReleases(ctx, githubclient.GetLatestReleasesParams{Repos: repos})
		if err != nil {
			return fmt.Errorf("get latest releases: %w", err)
		}

		for _, repo := range repos {
			latestTag := tags[repo.ID]

			if err = s.repo.UpsertLastSeen(ctx, repo.ID, latestTag); err != nil {
				return fmt.Errorf("scanner.Scanner.Tick: Repository.UpsertLastSeen: %w", err)
			}

			if shouldNotify(repo, latestTag) {
				if err = s.repo.InsertNotifications(ctx, repo.ID, latestTag); err != nil {
					return fmt.Errorf("scanner.Scanner.Tick: Repository.InsertNotifications: %w", err)
				}
			}
		}
		return nil
	})

	if err != nil {
		if errors.Is(err, githubclient.ErrRateLimited) {
			s.m.rateLimitedTotal.Inc()
			redmetrics.SetResult(ctx, "rate_limited")
			zerolog.Ctx(ctx).Warn().Err(err).Msg("github rate limited, skipping tick")
			return nil
		}
		redmetrics.SetError(ctx)
		return fmt.Errorf("scanner.Scanner.Tick: %w", err)
	}

	if emptyRun {
		return nil
	}
	redmetrics.SetSuccess(ctx)
	return nil
}
```

Add the `redmetrics` import if not already present:

```go
"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
```

- [ ] **Step 2: Verify no test references `result="empty"` for scanner**

Run: `grep -n '"empty"' internal/scanner/*.go internal/scanner/**/*.go 2>/dev/null`

Expected: no matches. If matches exist, replace them with the success-path or skip-path expectation as appropriate (skip = the metric family will not appear in `Gather()` for that call, since no sample was recorded).

- [ ] **Step 3: Run scanner tests**

Run: `go test ./internal/scanner/... -v`

Expected: all tests pass.

- [ ] **Step 4: Lint clean**

Run: `make lint`

Expected: `0 issues.`

---

## Task 3: Migrate notifier.Flush

**Files:**
- Modify: `internal/notifier/notifier.go`
- Modify: `internal/notifier/notifier_test.go`

`TestNotifier_FlushRecordsRED` (line 159) exercises an empty Flush and asserts metric-family presence. With skip-by-default, an empty flush emits no sample and the family will not appear in `Gather()`. The test must be updated to exercise the success path (mirrors `TestScanner_TickRecordsRED`).

- [ ] **Step 1: Replace the Flush body**

In `internal/notifier/notifier.go`, replace the `Flush` function (currently at lines 91–150) with:

```go
// Flush drains all currently pending notifications. Exported for testing.
func (n *Notifier) Flush(ctx context.Context) {
	start := time.Now()
	ctx, stop := redmetrics.Start(ctx, n.red)
	defer stop()
	defer func() { n.m.flushDuration.Observe(time.Since(start).Seconds()) }()

	processedAny := false
	for {
		var processed bool
		err := n.tx.WithinTransaction(ctx, func(ctx context.Context) error {
			items, err := n.repo.GetNotificationsWithLock(ctx, notifyLimit)
			if err != nil {
				return fmt.Errorf("notifier.Notifier.Flush: Repository.GetNotificationsWithLock: %w", err)
			}
			if len(items) == 0 {
				return nil
			}
			p := items[0]
			sendErr := n.mailer.SendRelease(ctx, domain.SendReleaseParams{
				To:           p.Email,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
				ReleaseTag:   p.ReleaseTag,
				ReleaseURL: fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
					p.RepoOwner, p.RepoName, p.ReleaseTag),
				UnsubscribeURL: fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, p.UnsubscribeToken),
			})
			if sendErr != nil {
				n.m.emailsSent.WithLabelValues("error").Inc()
				return fmt.Errorf("notifier.Notifier.Flush: MailSender.SendRelease: %w", sendErr)
			}
			n.m.emailsSent.WithLabelValues("ok").Inc()
			if err = n.repo.MarkSent(ctx, p.ID); err != nil {
				return fmt.Errorf("notifier.Notifier.Flush: Repository.MarkSent: %w", err)
			}
			processed = true
			return nil
		})
		if err != nil {
			redmetrics.SetError(ctx)
			zerolog.Ctx(ctx).Error().Err(err).Msg("notifier: process next failed")
			return
		}
		if !processed {
			if processedAny {
				redmetrics.SetSuccess(ctx)
			}
			return
		}
		processedAny = true
	}
}
```

Add the `redmetrics` import if not already present:

```go
"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
```

- [ ] **Step 2: Replace `TestNotifier_FlushRecordsRED`**

In `internal/notifier/notifier_test.go`, replace the test at lines 159–188 with:

```go
func TestNotifier_FlushRecordsRED(t *testing.T) {
	n, tx, repo, m, reg := newNotifierWithRegistry(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return([]notifier.PendingNotification{testPending}, nil),
		repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil)

	n.Flush(context.Background())

	expected := strings.NewReader(`
		# HELP notifier_requests_total Total number of notifier operations.
		# TYPE notifier_requests_total counter
		notifier_requests_total{result="ok"} 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "notifier_requests_total"))
}
```

If the unused `dto` / `hasMetric` symbols are no longer referenced anywhere in `notifier_test.go`, remove their imports/definitions. Run `goimports -w internal/notifier/notifier_test.go` if available, otherwise the compiler will name the unused symbols.

- [ ] **Step 3: Add a separate empty-flush test asserting no RED sample is emitted**

Append to `internal/notifier/notifier_test.go`:

```go
func TestNotifier_EmptyFlushEmitsNoREDSample(t *testing.T) {
	n, tx, repo, _, reg := newNotifierWithRegistry(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil)

	n.Flush(context.Background())

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "notifier_requests_total" {
			for _, m := range f.GetMetric() {
				if m.GetCounter().GetValue() != 0 {
					t.Fatalf("expected notifier_requests_total counter 0, got %v", m.GetCounter().GetValue())
				}
			}
		}
	}
}
```

- [ ] **Step 4: Run notifier tests**

Run: `go test ./internal/notifier/... -v`

Expected: all tests pass, including the two updated/new ones.

- [ ] **Step 5: Lint clean**

Run: `make lint`

Expected: `0 issues.`

---

## Task 4: Migrate confirmer.Flush

**Files:**
- Modify: `internal/confirmer/confirmer.go`
- Modify: `internal/confirmer/confirmer_test.go`

Same shape as Task 3. `TestConfirmer_FlushRecordsRED` (line 158) needs the same conversion.

- [ ] **Step 1: Replace the Flush body**

In `internal/confirmer/confirmer.go`, replace the `Flush` function (currently at lines 90–145) with:

```go
// Flush drains all currently pending confirmations. Exported for testing.
func (c *Confirmer) Flush(ctx context.Context) {
	ctx, stop := redmetrics.Start(ctx, c.red)
	defer stop()

	processedAny := false
	for {
		var processed bool
		err := c.tx.WithinTransaction(ctx, func(ctx context.Context) error {
			items, err := c.repo.GetConfirmationsWithLock(ctx, confirmLimit)
			if err != nil {
				return fmt.Errorf("confirmer.Confirmer.Flush: Repository.GetConfirmationsWithLock: %w", err)
			}
			if len(items) == 0 {
				return nil
			}
			p := items[0]
			err = c.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
				To:           p.Email,
				ConfirmURL:   c.baseURL + "/api/confirm/" + p.ConfirmToken,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
			})
			if err != nil {
				c.m.emailsSent.WithLabelValues("error").Inc()
				return fmt.Errorf("confirmer.Confirmer.Flush: MailSender.SendConfirmation: %w", err)
			}
			c.m.emailsSent.WithLabelValues("ok").Inc()
			if err = c.repo.MarkSent(ctx, p.ID); err != nil {
				return fmt.Errorf("confirmer.Confirmer.Flush: Repository.MarkSent: %w", err)
			}
			processed = true
			return nil
		})
		if err != nil {
			redmetrics.SetError(ctx)
			zerolog.Ctx(ctx).Error().Err(err).Msg("confirmer: process next failed")
			return
		}
		if !processed {
			if processedAny {
				redmetrics.SetSuccess(ctx)
			}
			return
		}
		processedAny = true
	}
}
```

Add the `redmetrics` import if not already present:

```go
"github.com/ananaslegend/reposeetory/internal/observability/redmetrics"
```

- [ ] **Step 2: Replace `TestConfirmer_FlushRecordsRED`**

In `internal/confirmer/confirmer_test.go`, replace the test at lines 158–188 with a success-path version. The exact test-helper names (`newConfirmerWithRegistry`, `testPendingConfirmation`, etc.) must match those already used in the file — inspect the file before substituting; use the same fixtures used by `TestConfirmer_FlushOne_MailerCalled` and `TestConfirmer_Flush_IncrementsEmailSentMetric`:

```go
func TestConfirmer_FlushRecordsRED(t *testing.T) {
	c, tx, repo, m, reg := newConfirmerWithRegistry(t)

	gomock.InOrder(
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
		tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction),
	)
	gomock.InOrder(
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return([]confirmer.PendingConfirmation{testPendingConfirmation}, nil),
		repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().MarkSent(gomock.Any(), gomock.Any()).Return(nil)

	c.Flush(context.Background())

	expected := strings.NewReader(`
		# HELP confirmer_requests_total Total number of confirmer operations.
		# TYPE confirmer_requests_total counter
		confirmer_requests_total{result="ok"} 1
	`)
	require.NoError(t, testutil.GatherAndCompare(reg, expected, "confirmer_requests_total"))
}
```

If the actual fixture in confirmer_test.go is named differently than `testPendingConfirmation` or the pending type is `PendingConfirmation` (verify with `grep -n "type Pending" internal/confirmer/`), adjust the literal to match. The mechanical pattern is identical to `TestNotifier_FlushRecordsRED`.

- [ ] **Step 3: Add a separate empty-flush test asserting no RED sample is emitted**

Append to `internal/confirmer/confirmer_test.go`:

```go
func TestConfirmer_EmptyFlushEmitsNoREDSample(t *testing.T) {
	c, tx, repo, _, reg := newConfirmerWithRegistry(t)

	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).DoAndReturn(invokeWithinTransaction)
	repo.EXPECT().GetConfirmationsWithLock(gomock.Any(), 1).Return(nil, nil)

	c.Flush(context.Background())

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "confirmer_requests_total" {
			for _, m := range f.GetMetric() {
				if m.GetCounter().GetValue() != 0 {
					t.Fatalf("expected confirmer_requests_total counter 0, got %v", m.GetCounter().GetValue())
				}
			}
		}
	}
}
```

- [ ] **Step 4: Run confirmer tests**

Run: `go test ./internal/confirmer/... -v`

Expected: all tests pass.

- [ ] **Step 5: Lint clean**

Run: `make lint`

Expected: `0 issues.`

---

## Task 5: Update ADR-0018

**Files:**
- Modify: `docs/adr/0018-red-metrics-conventions.md`

- [ ] **Step 1: Drop `empty` from scanner's domain-specific values**

In the **Decision** section (current line 15), change the `empty` and `rate_limited` bullet:

```diff
- - **Mandatory label:** `result`, with bounded values `ok | error`. Subsystem may add domain-specific values (e.g. `cached` for github_client, `empty` and `rate_limited` for scanner).
+ - **Mandatory label:** `result`, with bounded values `ok | error`. Subsystem may add domain-specific values (e.g. `cached` for github_client, `rate_limited` for scanner). Workers emit no sample for idle ticks (see ctx-API note below).
```

- [ ] **Step 2: Document the ctx-API**

Append to the **Decision** section, immediately after the bullet list:

```markdown
### Call-site API: `Start` + named setters

Call sites use a ctx-based recorder rather than direct `Observe` or a pointer-mutated result variable:

```go
ctx, stop := redmetrics.Start(ctx, n.red)
defer stop()
if err != nil { redmetrics.SetError(ctx); return }
redmetrics.SetSuccess(ctx)
```

`Start` binds a recorder to ctx; `SetSuccess` / `SetError` / `SetResult` mark the result label; `stop` emits the observation in a deferred closure. The recorder defaults to **skip mode** — if no setter is called before `stop` fires, no observation is recorded. This makes "no work to do" branches naturally invisible in the rate / duration series; liveness is covered by Prometheus' built-in `up` metric.

This mirrors the project's existing pattern for cross-cutting concerns (logger via [ADR-0006](0006-logger-via-context.md), transaction via [ADR-0004](0004-transactor-via-context.md)).

`Observe` remains available for decorator-style instrumentation (`emailermetrics.Wrap`, `github.CachingReleaseProvider`) where wrapping a single call needs a direct one-shot recording rather than the bind/setter/stop dance.
```

- [ ] **Step 3: Add to Links**

In the **Links** section, add:

```markdown
- Related: [ADR-0006](0006-logger-via-context.md), [ADR-0004](0004-transactor-via-context.md) — ctx-based cross-cutting concerns pattern.
```

(Append to the existing `Related:` line if it ends with the existing entries.)

---

## Task 6: Audit Grafana dashboard for `empty` filters

**Files:**
- Modify: `infra/grafana/provisioning/dashboards/reposeetory-red.json` (only if it contains `empty`-filtered expressions)

- [ ] **Step 1: Search for `empty` references**

Run: `grep -n 'empty' infra/grafana/provisioning/dashboards/reposeetory-red.json`

Expected paths (from spec audit run): no matches. The dashboard was authored after the convention was adopted but before `empty` proliferated in queries; verify this. If no matches, this task is a no-op — proceed to Task 7.

- [ ] **Step 2: If matches exist, simplify the expressions**

For each PromQL expression containing `{result="empty"}` or `{result!="empty"}`:
- `rate(*_requests_total{result!="empty"}[5m])` → `rate(*_requests_total[5m])`
- `rate(*_requests_total{result="empty"}[5m])` → remove the panel/expression entirely (idle ticks are no longer represented as a series).
- Compound filters like `{result="error",...}` are unaffected — only filters on `empty` need to change.

After edits, run: `python3 -m json.tool infra/grafana/provisioning/dashboards/reposeetory-red.json > /dev/null`

Expected: valid JSON, no parse errors.

---

## Task 7: Final verification

**Files:** none modified

- [ ] **Step 1: Full build**

Run: `go build ./...`

Expected: no output (success).

- [ ] **Step 2: Full test suite**

Run: `go test ./...`

Expected: all packages pass.

- [ ] **Step 3: Lint**

Run: `make lint`

Expected: `0 issues.`

- [ ] **Step 4: Final grep for stale references**

Run: `grep -nF '"empty"' internal/scanner/ internal/notifier/ internal/confirmer/ -r 2>/dev/null`

Expected: no matches in worker code.

Run: `grep -nF '.Track(' internal/ -r 2>/dev/null`

Expected: no matches anywhere (the pointer-API is fully removed).

- [ ] **Step 5: Diff review**

Run: `git status` and `git diff --stat HEAD`

Expected: changes are confined to the files listed in tasks 1–6. Eight files total (plus the spec file already on disk):

```
docs/adr/0018-red-metrics-conventions.md
docs/superpowers/plans/2026-06-04-red-metrics-ctx-api.md
docs/superpowers/specs/2026-06-03-red-metrics-ctx-api-design.md
infra/grafana/provisioning/dashboards/reposeetory-red.json    (only if Task 6 applied)
internal/confirmer/confirmer.go
internal/confirmer/confirmer_test.go
internal/notifier/notifier.go
internal/notifier/notifier_test.go
internal/observability/redmetrics/redmetrics.go
internal/observability/redmetrics/redmetrics_test.go
internal/scanner/scanner.go
```

If unexpected files are modified, investigate before handing back to the user.

- [ ] **Step 6: Hand off to user for commit**

Surface the file list above to the user with a short summary of what changed. The user controls the commit cadence — do **not** auto-commit. A reasonable suggested message (the user may rewrite):

```
refactor(observability): ctx-based RED recorder; drop "empty" label
```
