# Docker — Design Spec

**Date:** 2026-04-09  
**Goal:** Containerize the application with a multi-stage Dockerfile and a docker-compose that brings up the full dev/test environment with a single `docker compose up`.

---

## Context

The codebase is a Go HTTP API (`cmd/api/main.go`) that depends on:
- PostgreSQL (pgx/v5, migrations via `golang-migrate`)
- Mailpit (SMTP for email testing)

Currently the app runs only locally with a manual `.env` setup. The goal is `docker compose up` → everything starts, migrations run, Mailpit is ready, API is accessible.

---

## Dockerfile (multi-stage)

Two stages:

**Stage 1 — `builder` (`golang:1.26-alpine`)**
- Copy `go.mod` + `go.sum` first, run `go mod download` (layer cache)
- Copy source, build: `go build -ldflags="-s -w" -o /api ./cmd/api`

**Stage 2 — `runtime` (`alpine:3.21`)**
- Install `ca-certificates` (needed for GitHub API HTTPS) and `tzdata`
- Copy binary from builder
- `EXPOSE 8080`
- `ENTRYPOINT ["/api"]`

`alpine` over `distroless` for debuggability (`docker exec -it`).

---

## docker-compose.yml

Four services:

### `postgres`
- Image: `postgres:17-alpine`
- Named volume `pgdata` for data persistence across restarts
- Healthcheck: `pg_isready -U postgres` with retries
- Credentials: `POSTGRES_USER=postgres`, `POSTGRES_PASSWORD=pass`, `POSTGRES_DB=postgres`

### `mailpit`
- Image: `axllent/mailpit:latest`
- Port `8025` exposed to host (web UI)
- Port `1025` internal only (SMTP)

### `migrate`
- Image: `migrate/migrate:latest`
- Mounts `./migrations:/migrations:ro`
- Command: `-path /migrations -database <DATABASE_URL> up`
- `depends_on: { postgres: { condition: service_healthy } }`
- `restart: on-failure` — retries if postgres isn't ready yet
- Exits with code 0 on success (no migrations = no-op, also code 0)

### `api`
- `build: .` — builds from the Dockerfile in the repo root
- `depends_on`:
  - `migrate: { condition: service_completed_successfully }`
  - `mailpit: { condition: service_started }`
- Port `8080:8080`
- `env_file: .env` — user's local `.env` overrides defaults
- Inline env vars for defaults (DATABASE_URL, SMTP_HOST, etc.)

---

## Configuration

Environment variables are defined inline in `docker-compose.yml` with sensible defaults for local dev. The `env_file: .env` layer sits on top so users can override anything without touching compose.

`.env.example` is updated to document the docker-specific defaults.

---

## Files to create/modify

| File | Action |
|---|---|
| `Dockerfile` | Create |
| `docker-compose.yml` | Create |
| `.env.example` | Add note about docker defaults |
| `.dockerignore` | Create (exclude bin/, .env, .git, etc.) |

---

## Verification

```sh
docker compose up --build
# → postgres healthy → migrate exits 0 → api starts

curl -X POST http://localhost:8080/api/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"email":"test@example.com","repository":"golang/go"}'
# → HTTP 202

# Open http://localhost:8025 → confirmation email visible
# Click confirm link → HTTP 200
# Click unsubscribe link → HTTP 200
```
