# ADR-0005: Inject the logger through context

Status: accepted · 2026-05-09 · @ananaslegend · observability

## Context

Every log line should carry the request scope (`request_id`, `method`,
`path`) so we can correlate events without grepping by timestamp. The
two common alternatives — threading a `*zerolog.Logger` through every
function signature, or using a single global logger — both fail: the
first pollutes APIs and forces every helper to take a logger arg; the
second loses request scoping unless every call rebuilds context fields
by hand.

## Decision

The logger lives in `context.Context`. Code that needs to log fetches
it via `zerolog.Ctx(ctx)`:

```go
zerolog.Ctx(ctx).Info().
    Str("email", p.Email).
    Int64("repo_id", repoID).
    Msg("subscription created")
```

The logger is attached once, at the **entry point** of each
control-flow lineage:

- **HTTP requests** — `RequestLogger` middleware in
  `internal/httpapi/middleware.go` builds a logger with `request_id`,
  `method`, `path` and stashes it in the request context.
- **Background workers** — each worker (scanner, notifier, confirmer)
  attaches a logger with its component name at tick start, so every
  log line is traceable to the producer.

No function takes a `*zerolog.Logger` parameter.

## Consequences

- Function signatures stay clean — `ctx` is already there.
- Request fields propagate automatically into every nested call.
- Implicit dependency on `ctx` carrying a logger. Mitigated:
  `zerolog.Ctx` returns a disabled no-op logger if none is attached,
  so missing setup never panics — log lines just vanish.
- Tests must attach a test logger to `ctx` to assert log output;
  otherwise lines silently disappear into the no-op.
- Same trade-off as ADR-0003 (`Transactor`): we accept implicit-via-
  context for the API-ergonomics win.

## Links

- `internal/httpapi/middleware.go` — `RequestLogger` middleware.
- `internal/scanner/`, `internal/notifier/`, `internal/confirmer/` —
  worker entrypoints that attach component-scoped loggers.
- [ADR-0003](0003-transactor-via-context.md) — same propagation
  pattern, different concern.
