# GitHub CI Pipeline Design

**Date:** 2026-04-12
**Status:** Approved

## Context

The project has no CI yet. The goal is to catch regressions early on every push and PR to `main`: failed vet, lint issues, broken tests, or compilation errors.

All tests are unit tests (miniredis in-process, uber-go/mock) — no real Postgres or Redis needed. Dependencies are vendored (`-mod=vendor`), so no module download cache is required.

## Design

### File

```
.github/workflows/ci.yml
```

### Triggers

```yaml
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
```

### Job: `ci`

- **runs-on:** `ubuntu-latest`
- **Go version:** `1.26`

| Step | Tool | Notes |
|---|---|---|
| Checkout | `actions/checkout@v4` | standard |
| Setup Go | `actions/setup-go@v5` | `go-version: '1.26'`, `cache: false` (vendor mode) |
| Vet | `go vet -mod=vendor ./...` | catches obvious errors |
| Lint | `golangci-lint-action@v6` | `-mod=vendor`, built-in linter cache |
| Test | `go test -mod=vendor ./...` | all unit tests, no DB required |
| Build | `go build -mod=vendor -o /dev/null ./cmd/api` | compilation check only |

### Key decisions

- **Single linear job** — project is small; linear is simpler to debug and has less overhead than parallel jobs
- **`cache: false` in setup-go** — vendor mode means no module downloads; module cache is useless
- **Build output to `/dev/null`** — CI only needs to verify compilation, not produce an artifact
- **No Docker build** — out of scope; sufficient to verify the binary compiles

## Verification

After implementing:
1. Push a commit or open a PR to `main` — workflow should appear in the "Actions" tab
2. Introduce a lint error locally → CI should fail on the lint step
3. Break a test → CI should fail on the test step
4. Break compilation → CI should fail on the build step
