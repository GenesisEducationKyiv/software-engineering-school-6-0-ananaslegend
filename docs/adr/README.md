# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for the
`reposeetory` project. We use a tightened MADR variant — three sections
(Context / Decision / Consequences) plus a one-line header. Inspired by
[MADR](https://adr.github.io/madr/), trimmed for readability.

## Index

| #    | Title                                                                  | Tag             | Status   | Date       |
|------|------------------------------------------------------------------------|-----------------|----------|------------|
| 0001 | [Screaming Architecture](0001-screaming-architecture.md)               | architecture    | accepted | 2026-05-08 |
| 0002 | [Consumer-side interfaces](0002-consumer-side-interfaces.md)           | architecture    | accepted | 2026-05-09 |
| 0003 | [Async email delivery via outbox pattern](0003-async-email-outbox.md)  | reliability     | accepted | 2026-05-09 |
| 0004 | [Transactions via context (`Transactor`)](0004-transactor-via-context.md) | persistence  | accepted | 2026-05-09 |
| 0005 | [Wrap every error with package and method context](0005-error-wrapping-convention.md) | observability | accepted | 2026-05-09 |
| 0006 | [Inject the logger through context](0006-logger-via-context.md)        | observability   | accepted | 2026-05-09 |
| 0007 | [Vendor dependencies and batch weekly updates with cooldown](0007-vendor-and-dependency-updates.md) | infra | superseded by [0014](0014-drop-vendoring.md) | 2026-05-09 |
| 0008 | [Use GitHub GraphQL for batch release fetching](0008-github-graphql-batch-fetch.md) | integrations | accepted | 2026-05-09 |
| 0009 | [Generate API documentation from code annotations with `swaggo/swag`](0009-swagger-from-annotations.md) | http | accepted | 2026-05-09 |
| 0010 | [Server-rendered HTML with embedded templates, no JS framework](0010-server-rendered-html-no-js.md) | http | accepted | 2026-05-09 |
| 0011 | [Testing strategy — mock interfaces, run real for stateful systems](0011-testing-strategy.md) | testing | superseded by [0015](0015-testing-trophy.md) + [`docs/testing.md`](../testing.md) | 2026-05-16 |
| 0012 | [Host the service on Railway](0012-host-on-railway.md)                 | infra           | accepted | 2026-05-10 |
| 0013 | [Use Resend as the email provider](0013-resend-email-provider.md)      | integrations    | accepted | 2026-05-10 |
| 0014 | [Drop vendoring — rely on Go modules with the proxy cache](0014-drop-vendoring.md) | infra | accepted | 2026-05-16 |
| 0015 | [Adopt the Testing Trophy as the project's testing philosophy](0015-testing-trophy.md) | testing | accepted | 2026-05-16 |

## Conventions

* **Filename** — `NNNN-kebab-case-title.md`, where `NNNN` is a 4-digit sequence.
* **One file = one decision.** Once an ADR is accepted, do not edit the body.
  To revisit, open a new ADR and set the old one's status to
  `superseded by ADR-XXXX`.
* **Status lifecycle** — `proposed` (open PR) → `accepted` (merged) →
  `deprecated` (no longer applies) | `superseded` (replaced by a newer ADR).
* **Tags** — pick from the controlled vocabulary below to keep the index
  searchable.
* **Size** — target minimum count lines of body to clearly communicate the decision.
*  If you can't fit the decision, the scope is too wide — split it into multiple ADRs.
* **Language** — English, matching the rest of the codebase.
* **Code links** — reference packages and paths (`internal/<feature>/`),
  not branches or commits, which rot over time.

## Tag vocabulary

Pick one primary tag per ADR. Multi-tagging dilutes filterability;
when an ADR genuinely spans two concerns, choose the one that best
describes the *artefact*, not the *motivation*.

| Tag             | Use for                                                                |
|-----------------|------------------------------------------------------------------------|
| `architecture`  | Package organisation, module boundaries.                               |
| `persistence`   | DB schema, transactions, repositories.                                 |
| `reliability`   | Outbox, retries, async delivery, idempotency.                          |
| `observability` | Logs, metrics, error context.                                          |
| `http`          | HTTP API surface, docs, rendering.                                     |
| `infra`         | Build, vendor, Docker, CI, deployment / hosting platform.              |
| `integrations`  | External services (GitHub, SMTP, ...).                                 |
| `security`      | Auth, secrets, hardening. *(aspirational — no ADR yet)*                |
| `testing`       | Test strategy, mocks.                                                  |

## Adding a new ADR

1. Copy `template.md` to `NNNN-your-title.md` (next free number).
2. Fill in the sections; keep it tight.
3. Open a PR with status `proposed`.
4. On merge, flip status to `accepted` and add a row to the index above.
