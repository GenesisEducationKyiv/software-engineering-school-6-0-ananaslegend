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

### Everything (unit + integration, Docker required)

```sh
make test-all
# equivalent:
go test -tags=integration -count=1 ./...
```

## Prerequisites for integration tests

- A running Docker daemon — `testcontainers-go` boots `postgres:17-alpine` as a test fixture.
- First run pulls the image from Docker Hub (~80 MB); subsequent runs use the local cache.

## Why the `integration` build tag

Every file under `tests/integration/...` starts with `//go:build integration`. Without the tag, those files are invisible to the compiler, so `make test` (and `go test ./...`) runs unit tests only and does not require Docker.

## Why `-count=1` for integration tests

Go caches test results and re-runs only when source files change. Integration tests depend on Postgres/Docker state that changes outside the source tree, which can produce false positives from the cache. `-count=1` disables the cache entirely.

## Running a single test

```sh
# A specific suite method
go test -tags=integration -count=1 -run 'TestSubscriptionSuite/TestSubscribe_HappyPath' -v ./tests/integration/subscription/...

# A specific unit test
go test -run 'TestNew_AppliesDefaults' -v ./internal/github/
```

## Layout

- **Unit tests** live next to production code (`internal/<feature>/*_test.go`) and rely on mocks generated into `internal/<feature>/mocks/` via `go generate`.
- **Integration tests** live in `tests/integration/<feature>/` and share fixtures from `tests/integration/internal/` (Postgres testcontainer, GitHub REST fixture, composition root).

See [ADR-0011](adr/0011-testing-strategy.md) for the full testing strategy.
