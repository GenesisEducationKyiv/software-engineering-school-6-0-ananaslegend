# ADR-0012: Host the service on Railway

Status: accepted · 2026-05-10 · @ananaslegend · infra

## Context

The service needs a place to run continuously: a Go binary, a Postgres
instance, optionally Redis. For a small study / hobby project we want
a managed PaaS — git-driven deploys, low operational overhead, no VMs
to patch, no certificates to rotate, no systemd units to write.
Candidates: Railway, Fly.io, Render, Heroku, DigitalOcean App
Platform. They differ on pricing model, regional availability, build
pipeline, and outbound-network policy.

## Decision

Host on **Railway**. The service deploys from the project
`Dockerfile` (see
[ADR-0007](./0007-vendor-and-dependency-updates.md)); Postgres is a
Railway managed plugin; Redis is added the same way when caching is
wired in. `DATABASE_URL` is injected automatically by the platform.

This decision **forces email through Resend** rather than SMTP:
Railway blocks all outbound SMTP ports (25, 465, 587) at the network
level. Resend (HTTPS API on 443) is the production mailer; the SMTP
code path remains in `internal/app/mailer.go` as a fallback for local
and self-hosted environments only.

## Consequences

### Positive
- One platform hosts binary + DB + add-ons; one billing account, one
  dashboard, one place to read logs.
- Git-push deploy with automatic builds from `Dockerfile` — no
  separate CI deploy step to maintain.
- `DATABASE_URL` (and Redis URL when added) is wired in by Railway;
  no manual secret juggling for the primary store.
- Low operational overhead: no VMs, no patching, no certificate
  rotation.

### Negative
- Outbound SMTP is blocked at the network level, so transactional
  email must go through an HTTPS provider (Resend). One more vendor
  in the dependency surface, plus per-message API cost.
- Vendor lock-in to Railway's pricing and feature roadmap; migration
  to another PaaS later means re-doing the deploy pipeline.

### Constraints
- New egress dependencies must reach their target over HTTPS (443) or
  another explicitly-allowed port — raw TCP egress on common service
  ports (SMTP, IRC, BitTorrent, etc.) is not available.
- The `Dockerfile` is the load-bearing build artefact — it must stay
  working, fast, and reproducible (multi-stage, vendored deps; see
  [ADR-0007](./0007-vendor-and-dependency-updates.md)).
- Local development must not assume Railway-injected env vars — every
  variable consumed in `internal/config/` must also be settable via
  `.env`.

## Links

- `Dockerfile` — the build Railway runs.
- `internal/app/mailer.go` — Resend is chosen first; SMTP path
  retained as a non-Railway fallback.
- `docs/superpowers/specs/2026-04-10-resend-mailer-design.md` —
  original spec recording the SMTP-port-block reason.
- [ADR-0007](./0007-vendor-and-dependency-updates.md) — vendoring
  keeps the Docker build reproducible on Railway as well as locally.
