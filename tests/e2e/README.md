# E2E Tests

Browser-driven happy-path tests that exercise the real HTTP server, real
Postgres (testcontainers), real Mailpit (testcontainers), and an in-process
GitHub fixture against a real Chromium instance driven by
[`playwright-go`](https://github.com/playwright-community/playwright-go).

## Prerequisites

- Docker daemon running (testcontainers for Postgres + Mailpit).
- ~150 MB free disk for the Chromium binary on the first run.

## Running

```sh
make test-e2e
# equivalent to:
go test -tags=e2e -count=1 ./tests/e2e/...
```

The default `make test` and `make test-integration` skip this tree — every
file under `tests/e2e/` is gated by `//go:build e2e`.

## Debugging

- `PLAYWRIGHT_HEADED=1 make test-e2e` — runs Chromium with a visible window.
- On failure, a screenshot is written to `tests/e2e/_artifacts/<TestName>.png`
  (gitignored).

## Layout

Shared fixtures live in `tests/internal/` and are reused by the
integration suite under `tests/integration/`.
