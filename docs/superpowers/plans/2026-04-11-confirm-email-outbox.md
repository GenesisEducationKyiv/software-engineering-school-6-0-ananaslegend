# Confirm Email Outbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace synchronous `SendConfirmation` in `Subscribe()` with an outbox table (`confirmation_notifications`) drained by a new `internal/confirmer/` cron module.

**Architecture:** `Subscribe()` inserts a subscription row and a `confirmation_notifications` outbox row in a single DB transaction — no email sent inline. A new `Confirmer` goroutine (identical pattern to `Notifier`) polls the outbox, sends the email, and marks `sent_at`. The subscription service loses its `MailSender` dependency entirely.

**Tech Stack:** Go, pgx/v5, go.uber.org/mock, zerolog, github.com/wneessen/go-mail (via existing emailer package)

---

## File Map

| Action | Path |
|---|---|
| Create | `migrations/000003_confirmation_notifications.up.sql` |
| Create | `migrations/000003_confirmation_notifications.down.sql` |
| Modify | `internal/subscription/repository/subscriptions.go` |
| Modify | `internal/subscription/service/service.go` |
| Modify | `internal/subscription/service/service_test.go` |
| Regenerate | `internal/subscription/service/mocks/mock_interfaces.go` |
| Create | `internal/confirmer/confirmer.go` |
| Create | `internal/confirmer/confirmer_test.go` |
| Generate | `internal/confirmer/mocks/mock_interfaces.go` |
| Create | `internal/confirmer/repository/db.go` |
| Modify | `internal/config/config.go` |
| Modify | `cmd/api/main.go` |
| Modify | `.env.example` |

---

## Task 1: Migration — `confirmation_notifications` table

**Files:**
- Create: `migrations/000003_confirmation_notifications.up.sql`
- Create: `migrations/000003_confirmation_notifications.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- migrations/000003_confirmation_notifications.up.sql
CREATE TABLE confirmation_notifications (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);

-- Partial index: only pending rows (sent_at IS NULL), ordered by created_at for FIFO processing
CREATE INDEX idx_confirmation_notifications_pending
    ON confirmation_notifications (created_at)
    WHERE sent_at IS NULL;
```

- [ ] **Step 2: Write the down migration**

```sql
-- migrations/000003_confirmation_notifications.down.sql
DROP TABLE IF EXISTS confirmation_notifications;
```

- [ ] **Step 3: Apply the migration locally**

```bash
make migrate-up
```

Expected: `migrations applied` (or `migrate: no change` if already applied in a prior run).

- [ ] **Step 4: Commit**

```bash
git add migrations/000003_confirmation_notifications.up.sql migrations/000003_confirmation_notifications.down.sql
git commit -m "feat: add confirmation_notifications outbox table"
```

---

## Task 2: Wrap `CreateSubscription` in a transaction that also queues the outbox row

**Files:**
- Modify: `internal/subscription/repository/subscriptions.go`

Currently `CreateSubscription` runs a single `QueryRow`. We need to wrap it in a transaction so the subscription row and the `confirmation_notifications` row are inserted atomically. The `service.Repository` interface does **not** change.

- [ ] **Step 1: Replace `CreateSubscription` with the transactional version**

Replace the entire `CreateSubscription` function in `internal/subscription/repository/subscriptions.go`:

```go
func (r *Repository) CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("create subscription: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var sub domain.Subscription
	err = tx.QueryRow(ctx, `
		INSERT INTO subscriptions
			(email, repository_id, confirm_token, confirm_token_expires_at, unsubscribe_token)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, email, repository_id, confirmed_at, confirm_token, confirm_token_expires_at, unsubscribe_token, created_at
	`, p.Email, p.RepositoryID, p.ConfirmToken, p.ConfirmTokenExpiresAt, p.UnsubscribeToken).Scan(
		&sub.ID, &sub.Email, &sub.RepositoryID,
		&sub.ConfirmedAt, &sub.ConfirmToken, &sub.ConfirmTokenExpiresAt,
		&sub.UnsubscribeToken, &sub.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return nil, domain.ErrAlreadyExists
		}
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO confirmation_notifications (subscription_id) VALUES ($1)
	`, sub.ID); err != nil {
		return nil, fmt.Errorf("create subscription: queue confirmation: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create subscription: commit: %w", err)
	}

	return &sub, nil
}
```

Add `"github.com/jackc/pgx/v5"` to imports (it's needed for `pgx.TxOptions{}`).

The full imports block for `subscriptions.go` becomes:

```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/subscription/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/subscription/repository/subscriptions.go
git commit -m "feat: enqueue confirmation_notification inside CreateSubscription tx"
```

---

## Task 3: Remove `MailSender` from subscription service + update tests + regenerate mocks

**Files:**
- Modify: `internal/subscription/service/service.go`
- Modify: `internal/subscription/service/service_test.go`
- Regenerate: `internal/subscription/service/mocks/mock_interfaces.go`

- [ ] **Step 1: Remove `MailSender` from `service.go`**

Replace the entire file `internal/subscription/service/service.go` with:

```go
package service

//go:generate mockgen -source=service.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
)

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// normalizeRepo extracts "owner/name" from a URL or a plain "owner/name" string.
// Strips trailing ".git" and takes the last two slash-separated segments.
func normalizeRepo(s string) string {
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return s
}

// Repository is the storage contract expected by this service.
type Repository interface {
	UpsertRepo(ctx context.Context, p domain.UpsertRepoParams) (int64, error)
	CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)
	GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error)
	MarkConfirmed(ctx context.Context, p domain.MarkConfirmedParams) error
	DeleteByUnsubscribeToken(ctx context.Context, token string) (bool, error)
}

// RemoteRepositoryProvider checks whether a GitHub repository exists.
type RemoteRepositoryProvider interface {
	RepoExists(ctx context.Context, p domain.RepoExistsParams) (bool, error)
}

// Config holds all dependencies and settings for Service.
type Config struct {
	Repo            Repository
	GitHub          RemoteRepositoryProvider
	AppBaseURL      string
	ConfirmTokenTTL time.Duration
}

type Service struct {
	repo            Repository
	github          RemoteRepositoryProvider
	appBaseURL      string
	confirmTokenTTL time.Duration
}

func New(cfg Config) *Service {
	return &Service{
		repo:            cfg.Repo,
		github:          cfg.GitHub,
		appBaseURL:      cfg.AppBaseURL,
		confirmTokenTTL: cfg.ConfirmTokenTTL,
	}
}

func (s *Service) Subscribe(ctx context.Context, p domain.SubscribeParams) error {
	p.Repository = normalizeRepo(p.Repository)
	if !repoNameRe.MatchString(p.Repository) {
		return domain.ErrInvalidRepoFormat
	}
	parts := strings.SplitN(p.Repository, "/", 2)
	owner, name := parts[0], parts[1]

	exists, err := s.github.RepoExists(ctx, domain.RepoExistsParams{Owner: owner, Name: name})
	if err != nil {
		return fmt.Errorf("check repo existence: %w", err)
	}
	if !exists {
		return domain.ErrRepoNotFound
	}

	repoID, err := s.repo.UpsertRepo(ctx, domain.UpsertRepoParams{Owner: owner, Name: name})
	if err != nil {
		return fmt.Errorf("upsert repo: %w", err)
	}

	confirmToken, err := domain.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate confirm token: %w", err)
	}
	unsubscribeToken, err := domain.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate unsubscribe token: %w", err)
	}

	_, err = s.repo.CreateSubscription(ctx, domain.CreateSubscriptionParams{
		Email:                 p.Email,
		RepositoryID:          repoID,
		ConfirmToken:          confirmToken,
		ConfirmTokenExpiresAt: time.Now().Add(s.confirmTokenTTL),
		UnsubscribeToken:      unsubscribeToken,
	})
	if err != nil {
		return err // ErrAlreadyExists propagated as-is
	}

	zerolog.Ctx(ctx).Info().
		Str("email", p.Email).
		Str("repo", p.Repository).
		Int64("repo_id", repoID).
		Msg("subscription created")
	return nil
}

func (s *Service) Confirm(ctx context.Context, token string) error {
	sub, err := s.repo.GetByConfirmToken(ctx, token)
	if err != nil {
		return err
	}

	now := time.Now()
	if sub.ConfirmTokenExpiresAt != nil && now.After(*sub.ConfirmTokenExpiresAt) {
		return domain.ErrTokenExpired
	}

	if err := s.repo.MarkConfirmed(ctx, domain.MarkConfirmedParams{ID: sub.ID, Now: now}); err != nil {
		return fmt.Errorf("mark confirmed: %w", err)
	}

	zerolog.Ctx(ctx).Info().Int64("subscription_id", sub.ID).Msg("subscription confirmed")
	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	deleted, err := s.repo.DeleteByUnsubscribeToken(ctx, token)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if !deleted {
		return domain.ErrTokenNotFound
	}

	zerolog.Ctx(ctx).Info().Msg("unsubscribed")
	return nil
}
```

- [ ] **Step 2: Regenerate service mocks**

```bash
go generate ./internal/subscription/service/...
```

Expected: `internal/subscription/service/mocks/mock_interfaces.go` is rewritten without `MockMailSender`.

- [ ] **Step 3: Update `service_test.go`**

Replace the entire file `internal/subscription/service/service_test.go`:

```go
package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/ananaslegend/reposeetory/internal/subscription/service"
	"github.com/ananaslegend/reposeetory/internal/subscription/service/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newSvc(t *testing.T) (*service.Service, *mocks.MockRepository, *mocks.MockRemoteRepositoryProvider) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	gh := mocks.NewMockRemoteRepositoryProvider(ctrl)
	svc := service.New(service.Config{
		Repo:            repo,
		GitHub:          gh,
		AppBaseURL:      "http://localhost:8080",
		ConfirmTokenTTL: 24 * time.Hour,
	})
	return svc, repo, gh
}

var validSubscribeParams = domain.SubscribeParams{
	Email:      "vasya@example.com",
	Repository: "golang/go",
}

// --- Subscribe ---

func TestSubscribe_HappyPath(t *testing.T) {
	svc, repo, gh := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), domain.RepoExistsParams{Owner: "golang", Name: "go"}).Return(true, nil)
	repo.EXPECT().UpsertRepo(gomock.Any(), domain.UpsertRepoParams{Owner: "golang", Name: "go"}).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	require.NoError(t, err)
}

func TestSubscribe_InvalidRepoFormat(t *testing.T) {
	svc, _, _ := newSvc(t)

	err := svc.Subscribe(context.Background(), domain.SubscribeParams{Email: "vasya@example.com", Repository: "not-a-repo"})
	assert.ErrorIs(t, err, domain.ErrInvalidRepoFormat)
}

func TestSubscribe_FullRepoURL(t *testing.T) {
	urls := []string{
		"https://github.com/golang/go",
		"https://github.com/golang/go.git",
		"http://github.com/golang/go",
		"https://gitlab.com/golang/go",
		"https://gitlab.com/golang/go.git",
	}
	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			svc, repo, gh := newSvc(t)
			gh.EXPECT().RepoExists(gomock.Any(), domain.RepoExistsParams{Owner: "golang", Name: "go"}).Return(true, nil)
			repo.EXPECT().UpsertRepo(gomock.Any(), domain.UpsertRepoParams{Owner: "golang", Name: "go"}).Return(int64(1), nil)
			repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(&domain.Subscription{ID: 1}, nil)

			err := svc.Subscribe(context.Background(), domain.SubscribeParams{Email: "vasya@example.com", Repository: url})
			require.NoError(t, err)
		})
	}
}

func TestSubscribe_RepoNotFound(t *testing.T) {
	svc, _, gh := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(false, nil)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	assert.ErrorIs(t, err, domain.ErrRepoNotFound)
}

func TestSubscribe_AlreadyExists(t *testing.T) {
	svc, repo, gh := newSvc(t)

	gh.EXPECT().RepoExists(gomock.Any(), gomock.Any()).Return(true, nil)
	repo.EXPECT().UpsertRepo(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(nil, domain.ErrAlreadyExists)

	err := svc.Subscribe(context.Background(), validSubscribeParams)
	assert.ErrorIs(t, err, domain.ErrAlreadyExists)
}

// --- Confirm ---

func TestConfirm_HappyPath(t *testing.T) {
	svc, repo, _ := newSvc(t)

	exp := time.Now().Add(time.Hour)
	token := "validtoken"
	repo.EXPECT().GetByConfirmToken(gomock.Any(), token).
		Return(&domain.Subscription{ID: 1, ConfirmToken: &token, ConfirmTokenExpiresAt: &exp}, nil)
	repo.EXPECT().MarkConfirmed(gomock.Any(), gomock.Any()).Return(nil)

	err := svc.Confirm(context.Background(), token)
	require.NoError(t, err)
}

func TestConfirm_TokenNotFound(t *testing.T) {
	svc, repo, _ := newSvc(t)

	repo.EXPECT().GetByConfirmToken(gomock.Any(), "nosuchtoken").Return(nil, domain.ErrTokenNotFound)

	err := svc.Confirm(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}

func TestConfirm_TokenExpired(t *testing.T) {
	svc, repo, _ := newSvc(t)

	past := time.Now().Add(-time.Hour)
	token := "expiredtoken"
	repo.EXPECT().GetByConfirmToken(gomock.Any(), token).
		Return(&domain.Subscription{ID: 1, ConfirmToken: &token, ConfirmTokenExpiresAt: &past}, nil)

	err := svc.Confirm(context.Background(), token)
	assert.ErrorIs(t, err, domain.ErrTokenExpired)
}

// --- Unsubscribe ---

func TestUnsubscribe_HappyPath(t *testing.T) {
	svc, repo, _ := newSvc(t)

	repo.EXPECT().DeleteByUnsubscribeToken(gomock.Any(), "sometoken").Return(true, nil)

	err := svc.Unsubscribe(context.Background(), "sometoken")
	require.NoError(t, err)
}

func TestUnsubscribe_TokenNotFound(t *testing.T) {
	svc, repo, _ := newSvc(t)

	repo.EXPECT().DeleteByUnsubscribeToken(gomock.Any(), "nosuchtoken").Return(false, nil)

	err := svc.Unsubscribe(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, domain.ErrTokenNotFound)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/subscription/...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/subscription/service/service.go \
        internal/subscription/service/service_test.go \
        internal/subscription/service/mocks/mock_interfaces.go
git commit -m "refactor: remove MailSender from subscription service"
```

---

## Task 4: Create `internal/confirmer/` — interfaces, Confirmer, tests, mocks

**Files:**
- Create: `internal/confirmer/confirmer.go`
- Create: `internal/confirmer/confirmer_test.go`
- Generate: `internal/confirmer/mocks/mock_interfaces.go`

- [ ] **Step 1: Create `internal/confirmer/confirmer.go`**

```go
package confirmer

//go:generate mockgen -source=confirmer.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"time"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/rs/zerolog"
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
// ProcessNext begins a transaction, selects one pending confirmation_notifications row
// FOR UPDATE SKIP LOCKED, calls fn with it. If fn returns nil: marks sent_at and
// commits. If fn returns an error: rolls back. Returns (false, nil) when the queue is empty.
type Repository interface {
	ProcessNext(ctx context.Context, fn func(ctx context.Context, c PendingConfirmation) error) (bool, error)
}

// MailSender sends confirmation emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
}

// Config holds Confirmer dependencies.
type Config struct {
	Repo     Repository
	Mailer   MailSender
	Interval time.Duration
	BaseURL  string
}

// Confirmer periodically drains the confirmation_notifications outbox by sending emails.
type Confirmer struct {
	repo     Repository
	mailer   MailSender
	interval time.Duration
	baseURL  string
}

// New creates a Confirmer from cfg.
func New(cfg Config) *Confirmer {
	return &Confirmer{repo: cfg.Repo, mailer: cfg.Mailer, interval: cfg.Interval, baseURL: cfg.BaseURL}
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
	for {
		processed, err := c.repo.ProcessNext(ctx, func(ctx context.Context, pending PendingConfirmation) error {
			return c.mailer.SendConfirmation(ctx, domain.SendConfirmationParams{
				To:           pending.Email,
				ConfirmURL:   c.baseURL + "/api/confirm/" + pending.ConfirmToken,
				RepoFullName: pending.RepoOwner + "/" + pending.RepoName,
			})
		})
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Msg("confirmer: process next failed")
			return
		}
		if !processed {
			return
		}
	}
}
```

- [ ] **Step 2: Generate mocks**

```bash
go generate ./internal/confirmer/...
```

Expected: `internal/confirmer/mocks/mock_interfaces.go` is created with `MockRepository` and `MockMailSender`.

- [ ] **Step 3: Create `internal/confirmer/confirmer_test.go`**

```go
package confirmer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	"github.com/ananaslegend/reposeetory/internal/confirmer/mocks"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newConfirmer(t *testing.T) (*confirmer.Confirmer, *mocks.MockRepository, *mocks.MockMailSender) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	m := mocks.NewMockMailSender(ctrl)
	c := confirmer.New(confirmer.Config{Repo: repo, Mailer: m, BaseURL: "http://localhost:8080"})
	return c, repo, m
}

var testPending = confirmer.PendingConfirmation{
	ID:           1,
	Email:        "user@example.com",
	ConfirmToken: "tok-abc123",
	RepoOwner:    "golang",
	RepoName:     "go",
}

// invokeProcessNext makes the mock call fn with the given confirmation.
func invokeProcessNext(p confirmer.PendingConfirmation, returnProcessed bool) func(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
	return func(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
		if err := fn(ctx, p); err != nil {
			return false, err
		}
		return returnProcessed, nil
	}
}

func TestConfirmer_FlushEmpty_NoMailer(t *testing.T) {
	c, repo, _ := newConfirmer(t)

	repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushOne_MailerCalled(t *testing.T) {
	c, repo, m := newConfirmer(t)

	gomock.InOrder(
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(testPending, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), domain.SendConfirmationParams{
		To:           "user@example.com",
		ConfirmURL:   "http://localhost:8080/api/confirm/tok-abc123",
		RepoFullName: "golang/go",
	}).Return(nil)

	c.Flush(context.Background())
}

func TestConfirmer_FlushMailerError_RollsBackAndStops(t *testing.T) {
	c, repo, m := newConfirmer(t)

	smtpErr := errors.New("smtp timeout")
	repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
			err := fn(ctx, testPending)
			require.Error(t, err)
			return false, err
		},
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(smtpErr)

	c.Flush(context.Background())
}

func TestConfirmer_FlushMultiple_ProcessedInOrder(t *testing.T) {
	c, repo, m := newConfirmer(t)

	second := confirmer.PendingConfirmation{ID: 2, Email: "b@example.com", ConfirmToken: "tok-xyz", RepoOwner: "foo", RepoName: "bar"}

	gomock.InOrder(
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(testPending, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).DoAndReturn(invokeProcessNext(second, true)),
		repo.EXPECT().ProcessNext(gomock.Any(), gomock.Any()).Return(false, nil),
	)
	m.EXPECT().SendConfirmation(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	c.Flush(context.Background())
}

// suppress unused import warning
var _ = assert.New
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/confirmer/...
```

Expected: all 4 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/confirmer/
git commit -m "feat: add confirmer module with outbox drain loop"
```

---

## Task 5: Create `internal/confirmer/repository/db.go`

**Files:**
- Create: `internal/confirmer/repository/db.go`

- [ ] **Step 1: Create the file**

```go
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ananaslegend/reposeetory/internal/confirmer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements confirmer.Repository using pgx.
type Repository struct {
	pool *pgxpool.Pool
}

// New creates a Repository.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ProcessNext selects one pending confirmation FOR UPDATE SKIP LOCKED, calls fn,
// and commits (or rolls back on fn error).
func (r *Repository) ProcessNext(ctx context.Context, fn func(context.Context, confirmer.PendingConfirmation) error) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("confirmer: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var c confirmer.PendingConfirmation
	err = tx.QueryRow(ctx, `
		SELECT cn.id, s.email, s.confirm_token, r.owner, r.name
		FROM confirmation_notifications cn
		JOIN subscriptions  s ON cn.subscription_id = s.id
		JOIN repositories   r ON s.repository_id   = r.id
		WHERE cn.sent_at IS NULL
		ORDER BY cn.created_at
		LIMIT 1
		FOR UPDATE OF cn SKIP LOCKED
	`).Scan(&c.ID, &c.Email, &c.ConfirmToken, &c.RepoOwner, &c.RepoName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("confirmer: lock next pending: %w", err)
	}

	if err := fn(ctx, c); err != nil {
		return false, fmt.Errorf("confirmer: process confirmation %d: %w", c.ID, err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE confirmation_notifications SET sent_at = $1 WHERE id = $2
	`, time.Now(), c.ID); err != nil {
		return false, fmt.Errorf("confirmer: mark sent %d: %w", c.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("confirmer: commit: %w", err)
	}
	return true, nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/confirmer/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/confirmer/repository/db.go
git commit -m "feat: add confirmer pgx repository"
```

---

## Task 6: Config, `.env.example`, and `main.go` wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `.env.example`
- Modify: `cmd/api/main.go`

- [ ] **Step 1: Add `ConfirmerInterval` to config**

In `internal/config/config.go`, add the field after `NotifierInterval`:

```go
ScannerInterval  time.Duration `envconfig:"SCANNER_INTERVAL" default:"5m"`
NotifierInterval time.Duration `envconfig:"NOTIFIER_INTERVAL" default:"30s"`
ConfirmerInterval time.Duration `envconfig:"CONFIRMER_INTERVAL" default:"30s"`
```

- [ ] **Step 2: Update `.env.example`**

Replace the last line:

```
# Scanner / Notifier
SCANNER_INTERVAL=5m
NOTIFIER_INTERVAL=30s
```

with:

```
# Scanner / Notifier / Confirmer
SCANNER_INTERVAL=5m
NOTIFIER_INTERVAL=30s
CONFIRMER_INTERVAL=30s
```

- [ ] **Step 3: Wire the confirmer in `main.go`**

Add imports:

```go
"github.com/ananaslegend/reposeetory/internal/confirmer"
confirmerrepo "github.com/ananaslegend/reposeetory/internal/confirmer/repository"
```

Declare a combined mailer interface (unexported, local to main) just above `func main()`:

```go
type fullMailer interface {
	notifier.MailSender
	confirmer.MailSender
}
```

Change the `mailSender` variable declaration from `service.MailSender` to `fullMailer`:

```go
var mailSender fullMailer
```

Remove the `notifier` import alias if it's not already imported — it's already used, so no change needed there.

Add the confirmer wiring after `notify := notifier.New(...)` and `go notify.Run(ctx)`:

```go
confirm := confirmer.New(confirmer.Config{
    Repo:     confirmerrepo.New(pool),
    Mailer:   mailSender,
    Interval: cfg.ConfirmerInterval,
    BaseURL:  cfg.AppBaseURL,
})
go confirm.Run(ctx)
```

Remove `Mailer: mailSender` from `service.Config`:

```go
svc := service.New(service.Config{
    Repo:            repository.New(pool),
    GitHub:          githubclient.NewStubClient(),
    AppBaseURL:      cfg.AppBaseURL,
    ConfirmTokenTTL: cfg.ConfirmTokenTTL,
})
```

- [ ] **Step 4: Build the whole binary**

```bash
go build ./cmd/api/...
```

Expected: no errors.

- [ ] **Step 5: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go .env.example cmd/api/main.go
git commit -m "feat: wire confirmer into main and config"
```
