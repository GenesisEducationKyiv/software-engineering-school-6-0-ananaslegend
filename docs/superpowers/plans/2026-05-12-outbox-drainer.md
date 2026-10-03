# Outbox Drainer Refactor — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Commit policy:** User retains full control over git history. **Do NOT run `git commit` automatically** at any step. After each task, stop at a `Checkpoint` step — present the diff and let the user decide when/how to stage and commit.

**Goal:** Eliminate duplicated `Flush()` loop logic between `internal/notifier` and `internal/confirmer` by extracting a generic `internal/outbox.Drainer` that owns the top-level loop and the `<channel>_flush_duration_seconds` histogram. As a side-effect, close the metric drift (Confirmer gains `confirmer_flush_duration_seconds`).

**Architecture:** New `internal/outbox/` package exposes `Drainer{}` with one method `Flush(ctx, ProcessOne)` and a `ProcessOne func(ctx) (bool, error)` callback type. Drainer knows only about Prometheus registry, context, and the callback — **no** mailer / repo / transactor / domain knowledge. Feature packages (`notifier`, `confirmer`) keep their `WithinTransaction` body, counter metrics, and dependencies; they implement `processOne` as a private method and let the `*outbox.Drainer` own the loop. `Notifier.Flush(ctx)` / `Confirmer.Flush(ctx)` public signatures stay identical — they just delegate.

**Tech Stack:** Go 1.26, `github.com/prometheus/client_golang/prometheus` + `testutil`, `github.com/rs/zerolog`, `go.uber.org/mock/gomock` (existing tests untouched). No new external deps.

**Spec:** `docs/superpowers/specs/2026-05-11-outbox-drainer-design.md`

---

## File Map

| Action | Path | Responsibility |
|---|---|---|
| Create | `internal/outbox/drainer.go` | `Drainer`, `Config`, `ProcessOne`, `New`, `Flush` + inline histogram (~50 LOC) |
| Create | `internal/outbox/drainer_test.go` | 8 isolated tests via fake `ProcessOne` (no mocks, no transactor) |
| Modify | `internal/notifier/notifier.go` | Add `drainer *outbox.Drainer` field; `Flush` delegates; `processOne` private with tx body + new wrap prefix |
| Modify | `internal/notifier/metrics.go` | Remove `flushDuration` field and its registration (drainer owns it now) |
| Modify | `internal/confirmer/confirmer.go` | Mirror notifier: add `drainer`, `processOne`, delegate `Flush` |
| Unchanged | `internal/confirmer/metrics.go` | No histogram existed; nothing to remove |
| Unchanged | `internal/app/workers.go` | Worker wiring untouched |
| Unchanged | `internal/notifier/notifier_test.go`, `internal/confirmer/confirmer_test.go` | Must pass unmodified — regression contract |
| Unchanged | `internal/notifier/mocks/`, `internal/confirmer/mocks/` | No interface changes → no regeneration |

---

## Task 1: Bootstrap `internal/outbox` package with behavioral tests

**Files:**
- Create: `internal/outbox/drainer.go`
- Create: `internal/outbox/drainer_test.go`

This task delivers the loop semantics (5 of the 8 tests). Histogram comes in Task 2.

- [ ] **Step 1: Write the failing behavioral test file**

Create `internal/outbox/drainer_test.go`:

```go
package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/outbox"
)

// fakeProcess returns a ProcessOne that emits the given results sequentially.
// Each call consumes the next (processed, err) pair. Extra calls beyond the
// scripted results cause the test to fail — protects against unintended loops.
func fakeProcess(t *testing.T, results ...struct {
	processed bool
	err       error
}) (outbox.ProcessOne, *int) {
	t.Helper()
	i := 0
	calls := 0
	fn := func(ctx context.Context) (bool, error) {
		if i >= len(results) {
			t.Fatalf("ProcessOne called %d times, only %d results scripted", i+1, len(results))
		}
		r := results[i]
		i++
		calls++
		return r.processed, r.err
	}
	return fn, &calls
}

type pair = struct {
	processed bool
	err       error
}

func TestDrainer_Flush_EmptyOutbox_CallsProcessOnce(t *testing.T) {
	d := outbox.New(outbox.Config{ChannelName: "test"})
	fn, calls := fakeProcess(t, pair{false, nil})

	d.Flush(context.Background(), fn)

	require.Equal(t, 1, *calls)
}

func TestDrainer_Flush_OneItem_CallsProcessTwice(t *testing.T) {
	d := outbox.New(outbox.Config{ChannelName: "test"})
	fn, calls := fakeProcess(t,
		pair{true, nil},
		pair{false, nil},
	)

	d.Flush(context.Background(), fn)

	require.Equal(t, 2, *calls)
}

func TestDrainer_Flush_MultipleItems_CallsUntilEmpty(t *testing.T) {
	d := outbox.New(outbox.Config{ChannelName: "test"})
	fn, calls := fakeProcess(t,
		pair{true, nil},
		pair{true, nil},
		pair{true, nil},
		pair{false, nil},
	)

	d.Flush(context.Background(), fn)

	require.Equal(t, 4, *calls)
}

func TestDrainer_Flush_ProcessError_StopsLoop(t *testing.T) {
	d := outbox.New(outbox.Config{ChannelName: "test"})
	fn, calls := fakeProcess(t, pair{false, errors.New("boom")})

	d.Flush(context.Background(), fn)

	require.Equal(t, 1, *calls)
}

func TestDrainer_Flush_ProcessErrorAfterSuccess_StopsLoop(t *testing.T) {
	d := outbox.New(outbox.Config{ChannelName: "test"})
	fn, calls := fakeProcess(t,
		pair{true, nil},
		pair{false, errors.New("boom")},
	)

	d.Flush(context.Background(), fn)

	require.Equal(t, 2, *calls)
}
```

- [ ] **Step 2: Run test to confirm it fails to compile**

Run: `go test ./internal/outbox/...`

Expected: `package github.com/ananaslegend/reposeetory/internal/outbox: ...` build error — package does not exist yet.

- [ ] **Step 3: Implement minimal `drainer.go` (no histogram yet)**

Create `internal/outbox/drainer.go`:

```go
// Package outbox provides a generic outbox-draining loop shared by any
// feature that polls a queue table (notifier, confirmer, …). The loop, log,
// and timing metric live here; the per-row work — fetch, send, mark — lives
// in the feature, behind a ProcessOne callback.
package outbox

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// ProcessOne attempts to fetch and process exactly one outbox row.
//
// Return contract:
//   - (true,  nil) — one row processed successfully; Drainer continues the loop.
//   - (false, nil) — outbox empty; Drainer exits normally.
//   - (false, err) — failure (mailer or repository); Drainer logs and exits;
//     the next tick will retry.
type ProcessOne func(ctx context.Context) (processed bool, err error)

// Config configures a Drainer.
type Config struct {
	// ChannelName prefixes the flush_duration_seconds metric
	// (e.g. "notifier" → "notifier_flush_duration_seconds").
	ChannelName string

	// Registry registers the flush-duration histogram. Nil-safe:
	// when nil, the histogram is created but not registered (existing tests
	// that pass no Registry keep working).
	Registry *prometheus.Registry
}

// Drainer runs the generic outbox flush loop.
type Drainer struct {
	channelName string
}

// New creates a Drainer from cfg.
func New(cfg Config) *Drainer {
	return &Drainer{channelName: cfg.ChannelName}
}

// Flush calls process in a loop until it returns (false, _) or an error.
// On error Drainer logs and returns — the next Flush call retries the row.
func (d *Drainer) Flush(ctx context.Context, process ProcessOne) {
	for {
		processed, err := process(ctx)
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("channel", d.channelName).Msg("outbox: process next failed")
			return
		}
		if !processed {
			return
		}
	}
}

// _ prevents goimports from removing the prometheus import before Task 2 wires
// up the histogram. Replaced by real usage in the next task.
var _ = prometheus.NewHistogram
```

- [ ] **Step 4: Run tests to confirm all 5 pass**

Run: `go test ./internal/outbox/... -v`

Expected: 5 `PASS` lines (`TestDrainer_Flush_EmptyOutbox_CallsProcessOnce`, `..._OneItem_CallsProcessTwice`, `..._MultipleItems_CallsUntilEmpty`, `..._ProcessError_StopsLoop`, `..._ProcessErrorAfterSuccess_StopsLoop`).

- [ ] **Step 5: Build whole module to confirm no leakage**

Run: `go build ./...`

Expected: no output.

- [ ] **Step 6: Checkpoint — present diff for user review**

Run: `git status` and `git diff`

Stop. Hand the diff to the user. Do **not** stage and do **not** commit. Wait for explicit user instruction before proceeding to Task 2.

---

## Task 2: Wire the `flush_duration_seconds` histogram

**Files:**
- Modify: `internal/outbox/drainer.go`
- Modify: `internal/outbox/drainer_test.go`

- [ ] **Step 1: Add the prometheus import to the test file**

Replace the existing `import (...)` block at the top of `internal/outbox/drainer_test.go` with:

```go
import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/outbox"
)
```

- [ ] **Step 2: Append the 3 histogram tests to the test file**

Append these three functions to the end of `internal/outbox/drainer_test.go`:

```go
func TestDrainer_Flush_RegistersHistogramWithChannelName(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = outbox.New(outbox.Config{ChannelName: "notifier", Registry: reg})

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var names []string
	for _, mf := range mfs {
		names = append(names, mf.GetName())
	}
	require.Contains(t, names, "notifier_flush_duration_seconds")
}

func TestDrainer_Flush_NilRegistry_DoesNotPanic(t *testing.T) {
	d := outbox.New(outbox.Config{ChannelName: "test", Registry: nil})
	fn, _ := fakeProcess(t, pair{false, nil})

	require.NotPanics(t, func() { d.Flush(context.Background(), fn) })
}

func TestDrainer_Flush_ObservesDurationOnce(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := outbox.New(outbox.Config{ChannelName: "test", Registry: reg})
	fn, _ := fakeProcess(t, pair{false, nil})

	d.Flush(context.Background(), fn)

	mfs, err := reg.Gather()
	require.NoError(t, err)
	var found bool
	for _, mf := range mfs {
		if mf.GetName() != "test_flush_duration_seconds" {
			continue
		}
		require.Len(t, mf.Metric, 1)
		require.Equal(t, uint64(1), mf.Metric[0].Histogram.GetSampleCount())
		found = true
	}
	require.True(t, found, "test_flush_duration_seconds not gathered")
}
```

- [ ] **Step 3: Run tests to confirm 2 of 3 new ones fail**

Run: `go test ./internal/outbox/... -run TestDrainer_Flush -v`

Expected: `TestDrainer_Flush_RegistersHistogramWithChannelName` and `TestDrainer_Flush_ObservesDurationOnce` FAIL — the registry contains no metrics yet. The 5 behavioral tests from Task 1 still pass; `TestDrainer_Flush_NilRegistry_DoesNotPanic` passes (no metric required).

- [ ] **Step 4: Add histogram to `Drainer` — final implementation**

Replace the entire contents of `internal/outbox/drainer.go` with:

```go
// Package outbox provides a generic outbox-draining loop shared by any
// feature that polls a queue table (notifier, confirmer, …). The loop, log,
// and timing metric live here; the per-row work — fetch, send, mark — lives
// in the feature, behind a ProcessOne callback.
package outbox

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// ProcessOne attempts to fetch and process exactly one outbox row.
//
// Return contract:
//   - (true,  nil) — one row processed successfully; Drainer continues the loop.
//   - (false, nil) — outbox empty; Drainer exits normally.
//   - (false, err) — failure (mailer or repository); Drainer logs and exits;
//     the next tick will retry.
type ProcessOne func(ctx context.Context) (processed bool, err error)

// Config configures a Drainer.
type Config struct {
	// ChannelName prefixes the flush_duration_seconds metric
	// (e.g. "notifier" → "notifier_flush_duration_seconds").
	ChannelName string

	// Registry registers the flush-duration histogram. Nil-safe:
	// when nil, the histogram is created but not registered.
	Registry *prometheus.Registry
}

// Drainer runs the generic outbox flush loop and times each Flush call.
type Drainer struct {
	channelName   string
	flushDuration prometheus.Histogram
}

// New creates a Drainer from cfg.
func New(cfg Config) *Drainer {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    cfg.ChannelName + "_flush_duration_seconds",
		Help:    "Duration of one " + cfg.ChannelName + " Flush() call in seconds.",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
	})
	if cfg.Registry != nil {
		cfg.Registry.MustRegister(h)
	}
	return &Drainer{
		channelName:   cfg.ChannelName,
		flushDuration: h,
	}
}

// Flush calls process in a loop until it returns (false, _) or an error.
// On error Drainer logs and returns — the next Flush call retries the row.
func (d *Drainer) Flush(ctx context.Context, process ProcessOne) {
	start := time.Now()
	defer func() { d.flushDuration.Observe(time.Since(start).Seconds()) }()
	for {
		processed, err := process(ctx)
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("channel", d.channelName).Msg("outbox: process next failed")
			return
		}
		if !processed {
			return
		}
	}
}
```

(The placeholder `var _ = prometheus.NewHistogram` from Task 1 is removed — `prometheus` is now used directly.)

- [ ] **Step 5: Run the full outbox suite — all 8 tests pass**

Run: `go test ./internal/outbox/... -v`

Expected: 8 `PASS` lines.

- [ ] **Step 6: Run `go vet` on the new package**

Run: `go vet ./internal/outbox/...`

Expected: no output.

- [ ] **Step 7: Checkpoint — present diff for user review**

Run: `git status` and `git diff internal/outbox/`

Stop. Wait for user before proceeding.

---

## Task 3: Migrate `Notifier` to use `outbox.Drainer`

**Files:**
- Modify: `internal/notifier/notifier.go`
- Modify: `internal/notifier/metrics.go`
- Unchanged: `internal/notifier/notifier_test.go` (regression contract)

- [ ] **Step 1: Strip `flushDuration` from `notifierMetrics`**

Replace `internal/notifier/metrics.go` with:

```go
package notifier

import "github.com/prometheus/client_golang/prometheus"

type notifierMetrics struct {
	emailsSent *prometheus.CounterVec
}

func newNotifierMetrics(reg *prometheus.Registry) notifierMetrics {
	m := notifierMetrics{
		emailsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifier_emails_sent_total",
			Help: "Total number of release emails attempted.",
		}, []string{"result"}),
	}
	if reg != nil {
		reg.MustRegister(m.emailsSent)
	}
	return m
}
```

(`flushDuration` is gone — the drainer owns the histogram now.)

- [ ] **Step 2: Refactor `internal/notifier/notifier.go` to delegate to the drainer**

Replace the entire contents of `internal/notifier/notifier.go` with:

```go
package notifier

//go:generate mockgen -source=notifier.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ananaslegend/reposeetory/internal/outbox"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

// PendingNotification is one outbox row joined with subscription + repository data.
type PendingNotification struct {
	ID               int64
	Email            string
	RepoOwner        string
	RepoName         string
	ReleaseTag       string
	UnsubscribeToken string
}

// Repository is the storage contract for the notifier.
type Repository interface {
	GetNotificationsWithLock(ctx context.Context, limit int) ([]PendingNotification, error)
	MarkSent(ctx context.Context, id int64) error
}

// MailSender sends release notification emails.
type MailSender interface {
	SendRelease(ctx context.Context, p domain.SendReleaseParams) error
}

// Config holds Notifier dependencies.
type Config struct {
	Tx       transactor.Transactor
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
	Registry *prometheus.Registry
}

// Notifier periodically drains the release_notifications outbox by sending emails.
type Notifier struct {
	tx       transactor.Transactor
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
	drainer  *outbox.Drainer
	m        notifierMetrics
}

const notifyLimit = 1

// New creates a Notifier from cfg.
func New(cfg Config) *Notifier {
	return &Notifier{
		tx:       cfg.Tx,
		repo:     cfg.Repo,
		mailer:   cfg.Mailer,
		interval: cfg.Interval,
		baseURL:  cfg.BaseURL,
		drainer:  outbox.New(outbox.Config{ChannelName: "notifier", Registry: cfg.Registry}),
		m:        newNotifierMetrics(cfg.Registry),
	}
}

// Run blocks until ctx is cancelled, flushing the outbox on each interval.
func (n *Notifier) Run(ctx context.Context) {
	ticker := time.NewTicker(n.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.Flush(ctx)
		}
	}
}

// Flush drains all currently pending notifications. Exported for testing.
func (n *Notifier) Flush(ctx context.Context) {
	n.drainer.Flush(ctx, n.processOne)
}

// processOne fetches one pending notification, sends the email, and marks it
// sent — all within a single transaction. Returns (true, nil) on success,
// (false, nil) when the outbox is empty, and (false, err) on any failure.
func (n *Notifier) processOne(ctx context.Context) (bool, error) {
	var processed bool
	err := n.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		items, err := n.repo.GetNotificationsWithLock(ctx, notifyLimit)
		if err != nil {
			return fmt.Errorf("notifier.Notifier.processOne: Repository.GetNotificationsWithLock: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		p := items[0]

		if err := n.mailer.SendRelease(ctx, domain.SendReleaseParams{
			To:           p.Email,
			RepoFullName: p.RepoOwner + "/" + p.RepoName,
			ReleaseTag:   p.ReleaseTag,
			ReleaseURL: fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
				p.RepoOwner, p.RepoName, p.ReleaseTag),
			UnsubscribeURL: fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, p.UnsubscribeToken),
		}); err != nil {
			n.m.emailsSent.WithLabelValues("error").Inc()
			return fmt.Errorf("notifier.Notifier.processOne: MailSender.SendRelease: %w", err)
		}

		n.m.emailsSent.WithLabelValues("ok").Inc()
		if err := n.repo.MarkSent(ctx, p.ID); err != nil {
			return fmt.Errorf("notifier.Notifier.processOne: Repository.MarkSent: %w", err)
		}
		processed = true
		return nil
	})
	return processed, err
}
```

Note three things:
1. The `zerolog` import is gone — logging moved into the drainer.
2. Wrap-prefixes changed from `notifier.Notifier.Flush:` to `notifier.Notifier.processOne:`.
3. The `interval` field is unused by `Flush`/`processOne` but is still used by `Run` — keep it.

- [ ] **Step 3: Run the unchanged notifier test suite — must pass**

Run: `go test ./internal/notifier/... -v`

Expected: all five existing tests pass — `TestNotifier_FlushEmpty_NoMailer`, `TestNotifier_FlushOneNotification_MailerCalled`, `TestNotifier_FlushMailerError_NoMarkSentAndStops`, `TestNotifier_FlushMultipleNotifications_ProcessedInOrder`, `TestNotifier_Flush_IncrementsEmailSentMetric`.

If any fail because the test asserts on a wrap-prefix string, **stop** — the spec promises tests pass unmodified, so the failure is a real regression in the refactor, not a test bug. Re-read the failing test against `notifier.go` and fix the implementation, not the test.

- [ ] **Step 4: Build the module**

Run: `go build ./...`

Expected: no output. (Catches any wiring issue in `cmd/api/main.go` or `internal/app/workers.go` — there should be none, because `Config` and `New` signatures are unchanged.)

- [ ] **Step 5: Vet the notifier package**

Run: `go vet ./internal/notifier/...`

Expected: no output.

- [ ] **Step 6: Checkpoint — present diff for user review**

Run: `git status` and `git diff internal/notifier/`

Stop. Wait for user before proceeding.

---

## Task 4: Migrate `Confirmer` to use `outbox.Drainer`

**Files:**
- Modify: `internal/confirmer/confirmer.go`
- Unchanged: `internal/confirmer/metrics.go` (no histogram existed)
- Unchanged: `internal/confirmer/confirmer_test.go` (regression contract)

- [ ] **Step 1: Refactor `internal/confirmer/confirmer.go` to delegate to the drainer**

Replace the entire contents of `internal/confirmer/confirmer.go` with:

```go
package confirmer

//go:generate mockgen -source=confirmer.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ananaslegend/reposeetory/internal/outbox"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/pkg/transactor"
)

// PendingConfirmation is one outbox row joined with subscription + repository data.
type PendingConfirmation struct {
	ID           int64
	Email        string
	ConfirmToken string
	RepoOwner    string
	RepoName     string
}

// Repository is the storage contract for the confirmer.
type Repository interface {
	GetConfirmationsWithLock(ctx context.Context, limit int) ([]PendingConfirmation, error)
	MarkSent(ctx context.Context, id int64) error
}

// MailSender sends confirmation emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
}

// Config holds Confirmer dependencies.
type Config struct {
	Tx       transactor.Transactor
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
	Registry *prometheus.Registry
}

// Confirmer periodically drains the confirmation_notifications outbox by sending emails.
type Confirmer struct {
	tx       transactor.Transactor
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
	drainer  *outbox.Drainer
	m        confirmerMetrics
}

const confirmLimit = 1

// New creates a Confirmer from cfg.
func New(cfg Config) *Confirmer {
	return &Confirmer{
		tx:       cfg.Tx,
		repo:     cfg.Repo,
		mailer:   cfg.Mailer,
		interval: cfg.Interval,
		baseURL:  cfg.BaseURL,
		drainer:  outbox.New(outbox.Config{ChannelName: "confirmer", Registry: cfg.Registry}),
		m:        newConfirmerMetrics(cfg.Registry),
	}
}

// Run blocks until ctx is cancelled, flushing the outbox on each interval.
func (c *Confirmer) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Flush(ctx)
		}
	}
}

// Flush drains all currently pending confirmations. Exported for testing.
func (c *Confirmer) Flush(ctx context.Context) {
	c.drainer.Flush(ctx, c.processOne)
}

// processOne fetches one pending confirmation, sends the email, and marks it
// sent — all within a single transaction.
func (c *Confirmer) processOne(ctx context.Context) (bool, error) {
	var processed bool
	err := c.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		items, err := c.repo.GetConfirmationsWithLock(ctx, confirmLimit)
		if err != nil {
			return fmt.Errorf("confirmer.Confirmer.processOne: Repository.GetConfirmationsWithLock: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		p := items[0]

		if err := c.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
			To:           p.Email,
			ConfirmURL:   c.baseURL + "/api/confirm/" + p.ConfirmToken,
			RepoFullName: p.RepoOwner + "/" + p.RepoName,
		}); err != nil {
			c.m.emailsSent.WithLabelValues("error").Inc()
			return fmt.Errorf("confirmer.Confirmer.processOne: MailSender.SendConfirmation: %w", err)
		}

		c.m.emailsSent.WithLabelValues("ok").Inc()
		if err := c.repo.MarkSent(ctx, p.ID); err != nil {
			return fmt.Errorf("confirmer.Confirmer.processOne: Repository.MarkSent: %w", err)
		}
		processed = true
		return nil
	})
	return processed, err
}
```

(Same three changes as Notifier: no `zerolog` import, `Flush` prefix → `processOne`, drainer field wired in `New`.)

- [ ] **Step 2: Run the unchanged confirmer test suite — must pass**

Run: `go test ./internal/confirmer/... -v`

Expected: all five existing tests pass — `TestConfirmer_FlushEmpty_NoMailer`, `TestConfirmer_FlushOne_MailerCalled`, `TestConfirmer_FlushMailerError_NoMarkSentAndStops`, `TestConfirmer_FlushMultiple_ProcessedInOrder`, `TestConfirmer_Flush_IncrementsEmailSentMetric`.

- [ ] **Step 3: Vet the confirmer package**

Run: `go vet ./internal/confirmer/...`

Expected: no output.

- [ ] **Step 4: Checkpoint — present diff for user review**

Run: `git status` and `git diff internal/confirmer/`

Stop. Wait for user before proceeding.

---

## Task 5: Full-stack verification

**Files:** none modified — verification only.

- [ ] **Step 1: Full build**

Run: `go build ./...`

Expected: no output.

- [ ] **Step 2: Full vet**

Run: `go vet ./...`

Expected: no output.

- [ ] **Step 3: Full test suite**

Run: `go test ./...`

Expected: all green. Pay specific attention to:
- `internal/outbox/...` — 8 PASS
- `internal/notifier/...` — 5 PASS (unmodified)
- `internal/confirmer/...` — 5 PASS (unmodified)
- `cmd/api/...`, `internal/app/...`, `internal/scanner/...`, `internal/subscription/...` — unchanged, must still pass

If any test outside the three feature packages fails, the refactor broke a hidden dependency on the old `Flush` wrap-prefix or the old `notifierMetrics.flushDuration` field — fix the implementation, not the test.

- [ ] **Step 4: Lint the touched packages**

Run: `golangci-lint run ./internal/outbox/... ./internal/notifier/... ./internal/confirmer/...`

Expected: no warnings. Specifically verify:
- `wrapcheck` — every error from `n.repo.*`, `n.mailer.*`, `c.repo.*`, `c.mailer.*` is wrapped with the new `processOne` prefix.
- `wrapcheck` — `tx.WithinTransaction` return is **not** re-wrapped at the Drainer layer (callback wraps already; double wrap would trigger).
- No unused-import or unused-var warnings (we removed `zerolog` from notifier/confirmer; if the linter flags a leftover, drop it).

- [ ] **Step 5: Spot-check that `confirmer_flush_duration_seconds` now exists**

Run: `go test ./internal/confirmer/... -run TestConfirmer_Flush_IncrementsEmailSentMetric -v` — already passing in Task 4, but inspect that the test's registry now also gathers `confirmer_flush_duration_seconds` (drift closure side-effect from the spec).

This can be verified by temporarily adding (and **reverting** before commit) a `t.Log` of `reg.Gather()` inside the test, or by writing an ad-hoc one-liner:

```bash
go test ./internal/confirmer/... -run TestConfirmer_Flush_IncrementsEmailSentMetric -v 2>&1 | head -20
```

If formal proof is desired, add a new test `TestConfirmer_New_RegistersFlushDurationHistogram` to `confirmer_test.go` — but the spec lists `confirmer_test.go` as **unchanged**, so this is optional and should be discussed with the user before adding.

- [ ] **Step 6: Final checkpoint — present cumulative diff for user review**

Run:

```bash
git status
git diff --stat
git diff internal/outbox/ internal/notifier/ internal/confirmer/
```

Summarise to the user:
- Files created: `internal/outbox/drainer.go`, `internal/outbox/drainer_test.go`.
- Files modified: `internal/notifier/notifier.go`, `internal/notifier/metrics.go`, `internal/confirmer/confirmer.go`.
- Build / vet / test / lint all green.
- New metric `confirmer_flush_duration_seconds` exists (drift closed).
- Existing tests untouched and passing.

Stop. **Do not commit.** Let the user decide on the commit message, staging granularity, and whether to bundle this into one commit or split per-task.

---

## Out of scope (do not implement)

- Removing the `Interval` field or any `Config` change in notifier/confirmer.
- Touching `internal/app/workers.go`.
- Regenerating `internal/notifier/mocks/` or `internal/confirmer/mocks/` (no interface changed).
- Deduplicating test helpers between `notifier_test.go` and `confirmer_test.go` (separate refactor).
- Fixing the "counter increments before MarkSent" double-count-on-retry invariant (preserved as-is).
- Integration tests with real Postgres.
- Counter consolidation (`emails_sent_total` deliberately stays per-feature).