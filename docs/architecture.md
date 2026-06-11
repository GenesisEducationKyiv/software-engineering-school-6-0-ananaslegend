# Architecture Overview

A high-level reference for the `reposeetory` service. Describes the
*current* shape of the system: components, data, processes, external
dependencies. For *why* each piece looks this way, follow the links
to the relevant [ADR](./adr/README.md).

---

## 1. System context

```mermaid
flowchart LR
    U([Subscriber<br/>web browser])
    R[cmd/api<br/>monolith]
    M[cmd/notifications-svc<br/>notifications service]
    GH[(GitHub<br/>GraphQL API)]
    RS[(Resend<br/>HTTPS email API)]
    PG[(Postgres)]
    RD[(Redis<br/>optional)]

    U <-->|HTTP / HTML + JSON| R
    R -->|polls latest releases<br/>per SCANNER_INTERVAL| GH
    R -->|confirmation +<br/>release emails<br/>HTTP POST /v1/notifications/*| M
    M -->|deliver| RS
    R <-->|reads / writes| PG
    R <-->|cache reads / writes| RD
```

A subscriber lands on the HTML page, submits an email + repo, gets a
confirmation email, and from then on receives a notification each
time a new release tag appears on that repo.

---

## 2. Components

Two binaries: the monolith `cmd/api` and the standalone
`cmd/notifications-svc` notifications service (email is the current
delivery channel). Internal packages are organised as feature folders
(see [ADR-0001](./adr/0001-screaming-architecture.md)).

```mermaid
flowchart TB
    subgraph Api [cmd/api binary]
        APP[app<br/>composition root]
        HTTPAPI[httpapi<br/>router + middleware]

        subgraph Features
            SUB[subscription<br/>http / service / repo]
            SCAN[scanner]
            NOTIF[notifier]
            CONF[confirmer]
        end

        subgraph Shared
            GHC[github<br/>client + Redis cache]
            TXR[transactor]
            MC[notifications/client<br/>HTTP client]
        end
    end

    subgraph Notif [cmd/notifications-svc binary]
        MTR[transport<br/>HTTP handlers]
        MEM[email<br/>Resend / SMTP / Stub + templates]
    end

    RS[(Resend / SMTP)]

    APP --> HTTPAPI
    APP --> SUB
    APP --> SCAN
    APP --> NOTIF
    APP --> CONF

    HTTPAPI --> SUB
    SUB --> TXR
    SCAN --> TXR
    NOTIF --> TXR
    CONF --> TXR

    SCAN --> GHC
    NOTIF --> MC
    CONF --> MC
    MC -->|HTTP POST /v1/notifications/*| MTR
    MTR --> MEM
    MEM --> RS
```

| Component           | Responsibility                                                             | Key ADRs |
|---------------------|----------------------------------------------------------------------------|----------|
| `subscription/`     | HTTP API, business rules, persistence for subscriptions and repositories. | 0001, 0002, 0010 |
| `scanner/`          | Periodic GitHub poll, batch detection of new release tags.                | 0008 |
| `notifier/`         | Drains `release_notifications` outbox; sends via `notifications/client` over HTTP. | 0003 |
| `confirmer/`        | Drains `confirmation_notifications` outbox; sends via `notifications/client` over HTTP. | 0003 |
| `github/`           | GraphQL client + Redis caching decorator (`CachingReleaseProvider`).       | 0008 |
| `transactor/`       | Transaction boundaries via `context.Context`.                              | 0004 |
| `notifications/client/` | Monolith-side HTTP client to the notifications service; satisfies the drainers' `NotificationsSender`. | — |
| `cmd/notifications-svc`        | Standalone, stateless notifications service: Resend/SMTP + templates, `POST /v1/notifications/*`. | 0013 |
| `httpapi/`          | Cross-cutting HTTP: chi router, middleware, error mapping, metrics.        | 0005, 0006 |
| `app/`              | Composition root: wires everything based on env config.                   | 0001 |

---

## 3. Data model

```mermaid
erDiagram
    REPOSITORIES ||--o{ SUBSCRIPTIONS : "has"
    SUBSCRIPTIONS ||--o{ RELEASE_NOTIFICATIONS : "outbox"
    SUBSCRIPTIONS ||--o{ CONFIRMATION_NOTIFICATIONS : "outbox"
    REPOSITORIES ||--o{ RELEASE_NOTIFICATIONS : "for"

    REPOSITORIES {
        bigserial id PK
        text owner
        text name
        text last_seen_tag
        timestamptz last_checked_at
        timestamptz created_at
    }
    SUBSCRIPTIONS {
        bigserial id PK
        citext email
        bigint repository_id FK
        timestamptz confirmed_at
        text confirm_token
        timestamptz confirm_token_expires_at
        text unsubscribe_token
        timestamptz created_at
    }
    RELEASE_NOTIFICATIONS {
        bigserial id PK
        bigint subscription_id FK
        bigint repository_id FK
        text release_tag
        timestamptz created_at
        timestamptz sent_at
    }
    CONFIRMATION_NOTIFICATIONS {
        bigserial id PK
        bigint subscription_id FK
        timestamptz created_at
        timestamptz sent_at
    }
```

### Key invariants

- **Subscription state is encoded in a CHECK constraint:**
  `confirmed_at IS NULL ↔ confirm_token IS NOT NULL`. Pending and
  confirmed are mutually exclusive states; no orphan tokens.
- **Outbox FIFO:** partial index on `(created_at) WHERE sent_at IS NULL`
  keeps drain queries cheap regardless of historical row count.
- **Cascade on delete:** unsubscribe is a hard delete on
  `subscriptions`; outbox rows cascade away. GDPR-clean.
- **Unique `(email, repository_id)`:** one subscription per pair; a
  re-subscribe regenerates the confirm token rather than duplicating.

See [ADR-0003](./adr/0003-async-email-outbox.md) for outbox-pattern
rationale.

---

## 4. Processes

### 4.1 Subscribe

```mermaid
sequenceDiagram
    actor U as Browser
    participant H as HTTP handler
    participant S as subscription.Service
    participant T as Transactor
    participant DB as Postgres

    U->>H: POST /api/subscribe<br/>{email, repository}
    H->>S: Subscribe(...)
    S->>T: WithinTransaction
    T->>DB: BEGIN
    S->>DB: UPSERT repositories
    S->>DB: INSERT subscriptions<br/>(confirm_token, unsubscribe_token)
    S->>DB: INSERT confirmation_notifications<br/>(subscription_id)
    T->>DB: COMMIT
    H-->>U: 202 Accepted
    Note over U,DB: Confirmer drains outbox asynchronously
```

### 4.2 Confirm subscription

```mermaid
sequenceDiagram
    actor U as Browser
    participant H as HTTP handler
    participant S as subscription.Service
    participant DB as Postgres
    participant P as pages.Renderer

    U->>H: GET /api/confirm/{token}
    H->>S: Confirm(token)
    S->>DB: SELECT WHERE confirm_token=$1
    alt token valid + not expired
        S->>DB: UPDATE confirmed_at=now(),<br/>confirm_token=NULL
        H->>P: Confirmed(w)
        P-->>U: 200 HTML (dark hero)
    else token not found
        H->>P: Unavailable(w, 404)
        P-->>U: 404 HTML
    else token expired
        H->>P: Unavailable(w, 410)
        P-->>U: 410 HTML
    end
```

### 4.3 Scan & notify

Two independent loops; the outbox is the only contract between them.

```mermaid
sequenceDiagram
    participant Sc as Scanner
    participant DB as Postgres
    participant GH as GitHub<br/>(via CachingProvider)
    participant N as Notifier
    participant M as notifications service
    participant RS as Resend

    loop every SCANNER_INTERVAL
        Sc->>DB: BEGIN
        Sc->>DB: SELECT repositories<br/>FOR UPDATE SKIP LOCKED LIMIT 100
        Sc->>GH: batch GraphQL (one query, N aliases)
        GH-->>Sc: latestRelease per repo
        loop per repo with new tag
            Sc->>DB: UPSERT last_seen_tag, last_checked_at
            Sc->>DB: INSERT release_notifications<br/>(one row per confirmed subscription)
        end
        Sc->>DB: COMMIT
    end

    loop every NOTIFIER_INTERVAL
        N->>DB: BEGIN
        N->>DB: SELECT release_notifications<br/>FOR UPDATE SKIP LOCKED LIMIT 1
        N->>M: POST /v1/notifications/release (HTTP)
        M->>RS: POST /emails (release notification)
        Note over N,M: 2xx → mark sent · 4xx → drop · 5xx/timeout → retry next tick
        N->>DB: UPDATE sent_at = now()
        N->>DB: COMMIT
    end
```

### 4.4 Unsubscribe

```mermaid
sequenceDiagram
    actor U as Browser
    participant H as HTTP handler
    participant S as subscription.Service
    participant DB as Postgres
    participant P as pages.Renderer

    U->>H: GET /api/unsubscribe/{token}
    H->>S: Unsubscribe(token)
    S->>DB: DELETE WHERE unsubscribe_token=$1<br/>(cascades to outbox rows)
    H->>P: Unsubscribed(w)
    P-->>U: 200 HTML
```

---

## 5. External dependencies

| Dependency        | Required | Purpose                                          | Fallback if absent |
|-------------------|----------|--------------------------------------------------|--------------------|
| Postgres          | yes      | Source of truth for subscriptions + outboxes (monolith). | monolith refuses to start |
| GitHub GraphQL    | yes      | Latest release per repo, batched.                | scanner skips startup with WARN |
| Notifications service (HTTP) | yes  | Email rendering + delivery for the monolith.     | drainers retry via outbox until it is reachable |
| Resend (HTTPS)    | no       | Outbound transactional email (used by the notifications service). | `StubMailer` (logs to stdout) |
| Redis             | no       | 10-min cache of GitHub releases.                 | `CachingReleaseProvider` is not wired in |

The notifications service is the clearest example of the swap-friendly design:
adding or replacing a provider is a new file under
`internal/notifications/email/` plus one `case` in `cmd/notifications-svc/main.go` —
`notifier/`, `confirmer/`, and the business logic stay untouched.

External integrations are hidden behind small interfaces inside
`internal/github/` and `internal/notifications/email/`. See
[ADR-0008](./adr/0008-github-graphql-batch-fetch.md) for GraphQL
rationale, [ADR-0002](./adr/0002-consumer-side-interfaces.md) for the
interface-placement convention.

---

## 6. Configuration

All configuration is environment-based (`envconfig` + optional `.env`).
The full surface — every variable, default, and meaning — lives in
[CLAUDE.md › Configuration](../CLAUDE.md#configuration-env-vars). This
document does not duplicate it.

Key knobs that shape runtime behaviour:

- `SCANNER_INTERVAL`, `NOTIFIER_INTERVAL`, `CONFIRMER_INTERVAL` —
  loop cadence.
- `GITHUB_TOKEN` — gate for the scanner.
- `REDIS_URL` — gate for the GitHub caching decorator.
- `RESEND_API_KEY` — selects Resend (production default); without it,
  `StubMailer` logs to stdout.

---

## 7. Cross-references

| Concern                          | ADR |
|----------------------------------|-----|
| Per-feature folder layout        | [0001](./adr/0001-screaming-architecture.md) |
| Interfaces in consumer packages  | [0002](./adr/0002-consumer-side-interfaces.md) |
| Outbox pattern for email         | [0003](./adr/0003-async-email-outbox.md) |
| Transactions via context         | [0004](./adr/0004-transactor-via-context.md) |
| Error wrapping convention        | [0005](./adr/0005-error-wrapping-convention.md) |
| Logger via context               | [0006](./adr/0006-logger-via-context.md) |
| Vendoring + dep cooldown         | [0007](./adr/0007-vendor-and-dependency-updates.md) |
| GitHub GraphQL batch fetching    | [0008](./adr/0008-github-graphql-batch-fetch.md) |
| Swagger from annotations         | [0009](./adr/0009-swagger-from-annotations.md) |
| Server-rendered HTML, no JS      | [0010](./adr/0010-server-rendered-html-no-js.md) |
| Testing strategy                 | [0011](./adr/0011-testing-strategy.md) |
