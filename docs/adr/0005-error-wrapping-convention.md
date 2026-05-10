# ADR-0004: Wrap every error with package and method context

Status: accepted · 2026-05-09 · @ananaslegend · observability

## Context

When an error bubbles up through three or four layers (handler →
service → repository → driver), the bare error string (e.g. `"no rows
in result"`) says nothing about *where* it came from. Go has no
default stack traces, and pulling in a stack-trace library
(`pkg/errors`, `cockroachdb/errors`) is heavyweight. We need a
"breadcrumb trail" of the call path baked into the error message
itself — without losing the ability to match sentinel errors via
`errors.Is`.

## Decision

**Every** error returned across a function boundary is wrapped — even
errors from other packages of this module. Format:

```go
return fmt.Errorf("pkg.Struct.Method: %w", err)  // method
return fmt.Errorf("pkg.funcName: %w", err)       // free function
```

`%w` preserves the chain, so `errors.Is(err, domain.ErrTokenNotFound)`
still works at the HTTP layer for status mapping.

`wrapcheck` enforces this in CI. It is **deliberately** configured to
**not** exempt this module's own packages — internal calls must wrap
too. Exceptions are kept narrow:

- `fmt.Errorf`, `errors.{New,Unwrap,Join}` — already produce errors;
  re-wrapping is noise.
- `transactor.Transactor.WithinTransaction` — its callback already
  wraps; a second wrap would double-prefix.

## Consequences

### Positive
- Logs read like a breadcrumb trail; no stack traces needed to find
  the call path.
- Sentinel-based HTTP mapping in `httpapi/errors.go` keeps working —
  `%w` lets `errors.Is` traverse the wrapped chain.

### Negative
- Verbose, especially in short forwarding methods. Accepted as the
  cost of not pulling in a stack-trace library.

### Constraints
- New contributors must learn the format; `wrapcheck` flags violations
  in CI before review.
- `wrapcheck.ignore-sigs` and `ignore-interface-regexps` must stay in
  sync with new "already-wrapping" call sites — otherwise we silently
  produce double prefixes.

## Links

- `.golangci.yml` — `wrapcheck` rules (`ignore-sigs`,
  `ignore-interface-regexps`; **no** `ignore-package-globs` for own
  module).
