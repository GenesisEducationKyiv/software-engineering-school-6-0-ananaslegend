# Confirm Email Outbox — Design Spec

**Date:** 2026-04-11

## Summary

Move confirmation email sending from a synchronous call inside `Subscribe()` to an outbox pattern identical to `release_notifications`. A new `internal/confirmer/` module drains the `confirmation_notifications` table on a cron interval.

## Motivation

Currently `Subscribe()` sends the confirmation email synchronously. If the mailer is slow or unavailable, the HTTP request fails (or times out) even though the subscription was successfully created. The outbox pattern decouples persistence from delivery, enables retries, and makes the system consistent with how release notifications already work.

## Database

New migration `000003_confirmation_notifications`:

```sql
CREATE TABLE confirmation_notifications (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);

CREATE INDEX idx_confirmation_notifications_pending
    ON confirmation_notifications (created_at)
    WHERE sent_at IS NULL;
```

No extra columns needed — the confirmer JOINs `subscriptions` and `repositories` to get email, confirm_token, repo owner/name.

## Subscription Repository Change

`CreateSubscription` in `subscription/repository/subscriptions.go` wraps both inserts (subscription row + confirmation_notifications row) in a single DB transaction. The `service.Repository` interface does **not** change — atomicity is an implementation detail of the repository.

## New Module: `internal/confirmer/`

```
internal/confirmer/
  confirmer.go          # Confirmer struct, Repository + MailSender interfaces
  confirmer_test.go     # unit tests with mocks
  mocks/                # mockgen output
  repository/
    db.go               # pgx implementation of Repository.ProcessNext
```

### Types

```go
type PendingConfirmation struct {
    ID           int64
    Email        string
    ConfirmToken string
    RepoOwner    string
    RepoName     string
}

type Repository interface {
    ProcessNext(ctx context.Context, fn func(ctx context.Context, c PendingConfirmation) error) (bool, error)
}

type MailSender interface {
    SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error
}

type Config struct {
    Repo     Repository
    Mailer   MailSender
    Interval time.Duration
    BaseURL  string
}
```

### Confirmer behaviour

`Confirmer.Flush(ctx)` — same loop as `Notifier.Flush`:
1. `ProcessNext` locks one pending row FOR UPDATE SKIP LOCKED.
2. Builds `ConfirmURL = baseURL + "/api/confirm/" + confirmToken`.
3. Calls `mailer.SendConfirmation`.
4. On success: marks `sent_at = NOW()`, commits.
5. On mailer error: rollback, log, return (retry on next tick).

`ProcessNext` SQL:
```sql
SELECT cn.id, s.email, s.confirm_token, r.owner, r.name
FROM confirmation_notifications cn
JOIN subscriptions  s ON cn.subscription_id = s.id
JOIN repositories   r ON s.repository_id   = r.id
WHERE cn.sent_at IS NULL
ORDER BY cn.created_at
LIMIT 1
FOR UPDATE OF cn SKIP LOCKED
```

## Subscription Service Changes

- `MailSender` interface — **removed** entirely from `service.go`
- `mailer` field — **removed** from `Service` and `Config`
- `s.mailer.SendConfirmation(...)` call — **removed** from `Subscribe()`
- Regenerate mocks: `go generate ./internal/subscription/service/...`

## main.go Changes

- Remove `Mailer` from `service.Config`
- Add `confirmer.New(confirmer.Config{...})` wired with the shared `mailSender` and pool
- Add `go confirmer.Run(ctx)` goroutine

### New env var

| Variable | Default | Description |
|---|---|---|
| `CONFIRMER_INTERVAL` | `30s` | Interval between confirmer ticks |

Add to `internal/config/config.go` and `.env.example`.

## Error Handling

- Mailer failure: rollback, log error, leave row pending for next tick. No HTTP-level impact.
- Subscription created but confirmer down: user never receives email until confirmer recovers — acceptable trade-off, consistent with notifier behaviour.

## Testing

- `confirmer_test.go`: unit tests with mock `Repository` and mock `MailSender` — same pattern as `notifier_test.go`.
- `subscription/service/service_test.go`: update `Subscribe` tests — no longer need to assert `SendConfirmation` was called.
- `subscription/repository` integration tests (if any): verify `CreateSubscription` inserts into `confirmation_notifications`.
