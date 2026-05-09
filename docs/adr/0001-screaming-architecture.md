# ADR-0001: Screaming Architecture

Status: accepted · 2026-05-08 · @ananaslegend · architecture

## Context

`internal/` should reveal the domain, not horizontal layers. Layer-based
layout (`handlers/`, `services/`, `repositories/`) hides what the
service *does* and grows a `services/` dumping ground. DDD bounded
contexts are overkill for ~5 features.

## Decision

Each feature lives in `internal/<feature>/` with its own `domain/`,
`http/`, `service/`, `repository/`. Cross-cutting code lives only in
`internal/app/` (composition root) and `internal/httpapi/`. Cross-feature
calls go through interfaces defined on the consumer side.

## Consequences

- Top-level reads as a product TOC: *subscribe, scan, notify, confirm.*
- Feature change touches one folder.
- A feature is extractable into a separate service by moving its folder
  and rewiring `internal/app/`.
- Some duplication across features (each owns its repository and HTTP
  layer).
- Requires consumer-side interfaces to avoid import cycles — a
  convention contributors must learn.