# Testing

## Running

### Unit tests (fast, no Docker)

```sh
make test
# equivalent:
go test ./...
```

### Integration tests (Docker daemon required)

```sh
make test-integration
# equivalent:
go test -tags=integration -count=1 ./tests/integration/...
```

### End-to-end tests (Docker + Chromium)

```sh
make test-e2e
# equivalent:
go test -tags=e2e -count=1 ./tests/e2e/...
```

### Everything (unit + integration + e2e)

```sh
make test-all
# equivalent:
go test -tags=integration,e2e -count=1 -p 1 ./...
```

`-p 1` runs test packages sequentially. Without it, two packages that each
boot their own `testcontainers-go` Postgres race for Docker resources and
occasionally fail with `port "5432/tcp" not found`.

## Prerequisites

### Unit tier
Nothing beyond Go. No Docker, no network.

### Integration tier
- A running Docker daemon — `testcontainers-go` boots `postgres:17-alpine`
  and `axllent/mailpit:latest` as test fixtures.
- First run pulls images from Docker Hub (~80 MB Postgres, ~25 MB Mailpit);
  subsequent runs use the local cache.

### E2E tier
Everything from the integration tier, **plus**:
- A Chromium binary, downloaded on first run by `playwright.Install()`
  inside `TestMain` (~150 MB, takes 30–60 s the first time; idempotent
  thereafter).
- On Linux CI: Playwright's OS dependencies (`libnss3`, `libatk`, etc.).
  The CI workflow runs `go run github.com/playwright-community/playwright-go/cmd/playwright install --with-deps chromium`
  before the test step.

## Layout

Tests live in a top-level `tests/` folder with three sub-folders —
`integration/`, `e2e/`, and `internal/` (shared fixtures used by both
upper layers). Each sub-folder has its own README with the conventions
and recommendations specific to that layer.

Unit tests live next to the production code they cover; the per-package
conventions are documented in the package itself.

## Build tags

| Tag           | Files                                                                      |
| ------------- | -------------------------------------------------------------------------- |
| _none_        | Unit tests, co-located with production code.                               |
| `integration` | Every file under `tests/integration/...` and shared helpers below.         |
| `e2e`         | Every file under `tests/e2e/...` and shared helpers below.                 |
| `integration \|\| e2e` | Shared fixtures in `tests/internal/` (Postgres, Mailpit, GitHub fixtures, Browser, full-topology App). |

Without the right tag, those files are invisible to the compiler — `make
test` runs unit tests only and does not require Docker.

## Why `-count=1` for integration and e2e

Go caches test results and re-runs only when source files change.
Integration and e2e tests depend on Docker, network, and container state
that changes outside the source tree, which can produce false positives
from the cache. `-count=1` disables the cache.

## Debugging e2e failures

- **Headed mode** — `PLAYWRIGHT_HEADED=1 make test-e2e` opens a visible
  Chromium so you can watch the run.
- **Screenshots on failure** — every failed test writes a PNG to
  `tests/e2e/_artifacts/<TestName>.png` (gitignored).
- **Browser console / failed requests** — surfaced through the test
  logger (see `tests/internal/browser.go`).

## Philosophy

We follow the **Testing Trophy** ([ADR-0015](adr/0015-testing-trophy.md)).
Four layers, in increasing weight and decreasing count:

- **Static** — catches what doesn't need execution.
- **Unit** — pins down complex or non-obvious logic inside a single component.
- **Integration** — boots the real infrastructure and verifies the system as a whole.
- **E2E** — drives the user-facing surface to confirm the main flows work.

### Static

We run `golangci-lint` over the whole module. It catches the bug classes
that need no execution — unused symbols, shadowed errors, missing error
wraps, dead code. This is the cheapest layer and runs on every push.

### Unit

Unit tests cover logic that is dense or non-obvious enough that a future
change could silently break it. Component dependencies are mocked through
their consumer-side interfaces. We do **not** chase 100% coverage —
trivial getters, glue code, and straight-through delegations are not
worth a test.

### Integration

Integration tests are the most important layer. They boot real
infrastructure (Postgres, Mailpit, …) via testcontainers and run the
system as a whole — HTTP handlers, services, repositories, cron workers.
This is where SQL bugs, migration mistakes, transaction issues, and
cross-component contracts are caught. Most of the test budget goes here.

### E2E

E2E tests cover the main application flows through the user-facing
surface — a real browser, real emails. They assert only what the user
actually observes (pages rendered, emails received, links working) and
deliberately do **not** look at internal state such as database rows or
metric counters; that belongs to lower layers.
