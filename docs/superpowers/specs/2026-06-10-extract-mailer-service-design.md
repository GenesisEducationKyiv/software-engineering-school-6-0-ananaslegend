# Design: Extract email delivery into a standalone `mailer` service

**Date:** 2026-06-10
**Status:** Approved (pending implementation plan)

## Problem

The application is a modular monolith. Folder boundaries (screaming
architecture, ADR-0001) and behavioural boundaries (consumer-side
interfaces, ADR-0002) are clean, but there is one real boundary leak:
`internal/subscription/domain` has become a de-facto shared kernel.
Every other feature imports it for *data types*:

| Module | Imports from `subscription/domain` |
|---|---|
| `github` | `GitHubRepo`, `RepoExistsParams` |
| `scanner` | `GitHubRepo` |
| `notifier` / `notifier/emailer` | `SendReleaseParams` |
| `confirmer` / `notifier/emailer` | `SendConfirmationParams` |

The assignment requires: clearly separate domains, define module
boundaries, and extract at least one domain into a standalone
microservice.

## Decision summary

Extract **email delivery** (rendering + sending) into a standalone,
stateless HTTP service `mailer`. Both the release-notification drainer
(`notifier`) and the confirmation drainer (`confirmer`) call it over
HTTP instead of sending email locally.

Decisions locked during brainstorming:

1. **Domain to extract:** email delivery (the `emailer` adapters +
   templates). The drainers stay in the monolith because they own the
   outbox tables.
2. **Service boundary:** stateless email-gateway. The monolith keeps
   both outbox tables and the drainer loops. No new database, no new
   migrations.
3. **Confirmer included:** both `notifier` and `confirmer` route through
   the service, so the monolith fully sheds Resend/SMTP and templates.
4. **Transport:** synchronous HTTP `POST`.
5. **URL building stays in the monolith.** The monolith owns its own
   routes (`/api/confirm/...`, `/api/unsubscribe/...`) and `BaseURL`, so
   it builds `ReleaseURL`, `UnsubscribeURL`, `ConfirmURL` and passes
   resolved strings. The service only renders templates and delivers.
6. **Layout:** monorepo, same `go.mod`, new `cmd/mailer` binary
   (separate deployable, separate Dockerfile, separate Railway service).
7. **Shared DTOs:** a small `contract` package, imported by both sides,
   to avoid type drift. No logic in it — only the wire structs.

## Responsibility split

| | Monolith (`cmd/api`) | Mailer service (`cmd/mailer`) |
|---|---|---|
| Owns | subscriptions, repositories, **both outboxes**, drainer loops, routes `/api/confirm` + `/api/unsubscribe`, URL building | Resend/SMTP providers, **email templates**, rendering + delivery |
| Database | yes (unchanged) | none (stateless) |
| External contract | — | HTTP `POST /v1/emails/release`, `POST /v1/emails/confirmation` |

## Target structure (monorepo, same module)

```
cmd/api/main.go              # monolith entrypoint — unchanged
cmd/mailer/main.go           # NEW: config + emailer + HTTP server + /metrics + /healthz
internal/mailer/
  contract/                  # JSON DTOs (SendReleaseRequest, SendConfirmationRequest) — shared by both sides
  emailer/                   # MOVED from internal/notifier/emailer (resend.go, smtp.go, stub.go, templates/)
  http/                      # handlers: decode DTO -> call emailer
internal/mailerclient/       # NEW HTTP client; satisfies both notifier.MailSender and confirmer.MailSender
internal/notifier/           # stays (drainer); drops dependency on subscription/domain
internal/confirmer/          # stays (drainer); drops dependency on subscription/domain
```

## HTTP contract

Two endpoints, JSON in / status out. DTOs mirror today's params
(URLs pre-resolved by the caller).

`POST /v1/emails/release`
```json
{
  "to": "user@example.com",
  "repo_full_name": "owner/name",
  "release_tag": "v1.2.3",
  "release_url": "https://github.com/owner/name/releases/tag/v1.2.3",
  "unsubscribe_url": "https://app.example.com/api/unsubscribe/<token>"
}
```

`POST /v1/emails/confirmation`
```json
{
  "to": "user@example.com",
  "confirm_url": "https://app.example.com/api/confirm/<token>",
  "repo_full_name": "owner/name"
}
```

Responses:
- `2xx` — email accepted/sent by the provider. Caller marks `sent_at`.
- `4xx` — malformed request (bad JSON / missing field). This can only
  happen on a monolith-side bug, since the monolith builds every
  payload. Treated as a **permanent** failure: the caller logs at error
  level, increments the error counter, and marks `sent_at` to avoid a
  poison-pill loop (the email is dropped, loudly). Not retried.
- `5xx` / timeout / connection error — **transient**. Caller does NOT
  mark `sent_at`; the row stays in the outbox and retries next tick.

Additional service endpoints: `GET /healthz`, `GET /metrics`.

## Data flow (release; confirmation is identical in shape)

```
scanner -> INSERT release_notifications            (unchanged)
notifier drainer (monolith):
  tx -> SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1
      -> build ReleaseURL + UnsubscribeURL from BaseURL
      -> mailerclient.SendRelease(ctx, req)  --HTTP POST-->  mailer /v1/emails/release -> Resend
      -> 2xx? MarkSent(sent_at = NOW()); COMMIT
      -> transient error? do NOT mark -> retried next tick
```

## Delivery semantics

At-least-once, unchanged. `mailerclient` surfaces a typed error
distinguishing **permanent** (4xx) from **transient** (5xx / network /
timeout). The drainer marks `sent_at` on success and on permanent
failure (dropping a poison message), and leaves the row unmarked only on
transient failure. On a transient HTTP failure the outbox row is
left unmarked and retried on the next drain tick. A rare double-send is
possible if the POST reaches the service but the response is lost — same
risk class as the current local-mailer outbox. An `Idempotency-Key`
(the outbox row id) is a documented future option, not implemented now
(YAGNI).

## Coupling cleanup (in scope)

Extracting email delivery removes the email-related shared-kernel leak:

- `SendReleaseParams` / `SendConfirmationParams` move out of
  `subscription/domain` and become DTOs in `internal/mailer/contract`.
- `notifier`, `confirmer`, and the moved `emailer` no longer import
  `subscription/domain`.

Out of scope (documented as a known remaining issue, not fixed here):
the `github -> subscription/domain` leak (`GitHubRepo`,
`RepoExistsParams`).

## Configuration

**Mailer service:**
- `RESEND_API_KEY` / `RESEND_FROM` or `SMTP_HOST` + `SMTP_*` / `SMTP_FROM`
- `MAILER_HTTP_ADDR` (listen address)
- log level / pretty flags (reuse existing config conventions)

**Monolith:**
- NEW `MAILER_URL` — base URL of the mailer service
- REMOVE `RESEND_API_KEY` / `SMTP_*` (no longer needed by the monolith)
- `APP_BASE_URL` stays (used to build confirm/unsubscribe URLs)

## Metrics

- Drainer side (monolith): existing `emailsSent{result=ok|error}`
  counters retained; they now count HTTP-call outcomes.
- Mailer service: own `/metrics` with delivery counters (per email type,
  per provider result).

## Testing

- `mailerclient`: tested against `httptest.Server` (2xx, 4xx, 5xx,
  timeout paths).
- `mailer/http` handlers: table tests with a mock emailer (valid DTO,
  malformed JSON, provider error).
- `mailer/emailer`: existing emailer tests move with the package.
- Drainers (`notifier`, `confirmer`): unchanged test approach — mock the
  `MailSender` interface. Signatures keep the same method names; only the
  param type changes (from `domain.*` to `contract.*`).

## Documentation

- New `docs/adr/0015-extract-mailer-service.md` (rationale, HTTP
  transport choice, at-least-once + double-send note, stateless boundary).
- Update `docs/architecture.md` (component diagram, external-deps table,
  process diagrams: drainer now calls mailer over HTTP).
- Update `CLAUDE.md` navigation + Configuration notes.
