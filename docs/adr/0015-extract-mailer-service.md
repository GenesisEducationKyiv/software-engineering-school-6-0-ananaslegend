# ADR-0015: Extract email delivery into a standalone mailer service

Status: accepted · 2026-06-10 · @ananaslegend · architecture

## Context

The service began as a modular monolith: feature folders under
`internal/<feature>/` ([ADR-0001](./0001-screaming-architecture.md))
with consumer-side interfaces ([ADR-0002](./0002-consumer-side-interfaces.md)).
Behavioural boundaries were clean, but one data-level boundary had
leaked: `internal/subscription/domain` had become a shared kernel.
`scanner`, `notifier`, `confirmer`, and `github` all imported it for
types — including the email payloads `SendReleaseParams` /
`SendConfirmationParams`, which are not subscription-domain concepts at
all.

The modular-architecture goal called for extracting at least one domain
into a standalone service with an explicit contract. Email delivery is
the natural candidate: it owns clearly separable concerns (providers,
templates, rendering) and the existing `Emailer` interface already
isolated it.

## Decision

Extract **email rendering + delivery** into a standalone, stateless
HTTP service, `cmd/mailer`.

- The monolith keeps both outbox tables and the drainer loops
  (`internal/notifier`, `internal/confirmer`) — it owns the data and the
  `/api/confirm` + `/api/unsubscribe` routes, so it builds the
  confirmation / unsubscribe / release URLs and passes resolved strings.
- The mailer service owns the Resend/SMTP providers and the email
  templates. It exposes `POST /v1/emails/release` and
  `POST /v1/emails/confirmation`, plus `/healthz` and `/metrics`.
- The drainers send via `internal/mailerclient` (an HTTP client that
  satisfies both `notifier.MailSender` and `confirmer.MailSender`)
  instead of a local mailer.
- The wire DTOs (`SendReleaseRequest`, `SendConfirmationRequest`) and a
  status-classification sentinel (`ErrPermanent`) live in a small shared
  `internal/mailer/contract` package, imported by both sides to prevent
  type drift.

Layout: monorepo, same `go.mod`, second binary `cmd/mailer` with its own
`Dockerfile.mailer` and Railway service.

### Delivery semantics

At-least-once, unchanged. `mailerclient` classifies failures:

- `2xx` → success; the drainer marks `sent_at`.
- `4xx` → **permanent** (`ErrPermanent`): the request is malformed, so
  re-sending can never succeed. Can only result from a monolith-side
  bug. The drainer logs at error level, increments the error counter,
  and marks `sent_at` to drop the poison message rather than loop.
- `5xx` / network / timeout → **transient**: the drainer leaves the row
  unmarked; the outbox retries it next tick.

A rare double-send is possible if the POST reaches the service but the
response is lost — the same risk class as the previous local-mailer
outbox.

## Consequences

### Positive
- `internal/subscription/domain` no longer carries email payloads;
  `notifier`, `confirmer`, and `emailer` dropped their dependency on it.
- The monolith no longer links Resend/SMTP or email templates — email
  delivery scales, deploys, and fails independently.
- The `Emailer` interface and provider-selection logic moved wholesale
  into the service; adding a provider is still a single `case`, now in
  `cmd/mailer`.

### Negative
- A new network hop sits between a business event and email delivery;
  the mailer service must be reachable for email to flow (transient
  failures retry via the outbox, so availability is degraded-not-lost).
- Two binaries to build, deploy, and operate instead of one.
- A shared `contract` package couples the two services at the type
  level; in a true multi-repo split this would be a generated or
  duplicated contract.

### Constraints
- New email types must go through the mailer HTTP contract — do not call
  the Resend/SMTP SDKs from monolith feature packages.
- URL building stays in the monolith (it owns its routes); the mailer
  service must not assume route shapes.

### Known follow-up (out of scope)
- `github → subscription/domain` still leaks (`GitHubRepo`,
  `RepoExistsParams`); not addressed here.
- No `Idempotency-Key` yet; double-send tolerated as before.

## Links

- `cmd/mailer/main.go` — service entrypoint (config, provider selection,
  router).
- `internal/mailer/contract/` — wire DTOs + `ErrPermanent`.
- `internal/mailer/emailer/` — providers + templates (moved from
  `internal/notifier/emailer/`).
- `internal/mailer/transport/` — HTTP handlers + delivery metrics.
- `internal/mailerclient/` — monolith-side HTTP client.
- [ADR-0002](./0002-consumer-side-interfaces.md) — the `MailSender`
  interfaces the client satisfies live in the drainers.
- [ADR-0003](./0003-async-email-outbox.md) — the outbox the drainers
  still own; the HTTP call sits at its tail.
- [ADR-0013](./0013-resend-email-provider.md) — Resend, now hosted
  inside the mailer service.
