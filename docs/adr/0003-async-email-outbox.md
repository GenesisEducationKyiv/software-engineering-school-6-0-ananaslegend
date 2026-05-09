# ADR-0003: Async email delivery via outbox pattern

Status: accepted · 2026-05-09 · @ananaslegend · reliability

## Context

The subscription flow must send a confirmation email after creating a
subscription, and a release-notification email when the scanner detects
a new tag. Sending SMTP synchronously inside the HTTP handler couples
request latency to the mail server, and opens a window where the DB
commits but the email fails (or the email sends but the DB rolls back) —
losing or duplicating notifications silently. We need at-least-once
delivery that is atomic with the business write.

## Decision

Each outgoing email gets a row in an outbox table written in the **same
DB transaction** as the business event. A background goroutine drains
the table using `SELECT ... FOR UPDATE SKIP LOCKED`, sends the email,
then sets `sent_at = NOW()` in the same transaction.

Two tables, one drainer per email type:

- `confirmation_notifications` — drained by `internal/confirmer/`
- `release_notifications` — drained by `internal/notifier/`

```mermaid
sequenceDiagram
    actor C as Client
    participant H as HTTP handler
    participant DB as PostgreSQL
    participant W as Confirmer
    participant SMTP

    C->>H: POST /api/subscribe
    H->>DB: BEGIN
    H->>DB: INSERT subscriptions
    H->>DB: INSERT confirmation_notifications
    H->>DB: COMMIT
    H-->>C: 202 Accepted

    loop every CONFIRMER_INTERVAL (30s)
        W->>DB: SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1
        DB-->>W: pending row
        W->>SMTP: send email
        SMTP-->>W: ok
        W->>DB: UPDATE sent_at = NOW(); COMMIT
    end
```

## Consequences

- Atomic with the business write — the email is committed iff the
  subscription is.
- Survives process restarts; the drainer retries any row where
  `sent_at IS NULL`.
- `SKIP LOCKED` lets multiple workers drain the same table without a
  distributed lock — horizontal scaling without coordination.
- Email delivery is delayed by the drain interval (`30s` default).
- Two extra tables and migrations to maintain.
- No dead-letter handling — a permanently failing row retries forever.
  Tracked as an open question for a follow-up ADR.

## Links

- `internal/confirmer/`, `internal/notifier/` — outbox drainers.
- `migrations/000002_release_notifications.up.sql`,
  `migrations/000003_confirmation_notifications.up.sql` — outbox schemas.
