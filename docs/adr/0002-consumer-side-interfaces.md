# ADR-0002: Consumer-side interfaces

Status: accepted · 2026-05-09 · @ananaslegend · architecture

## Context

A common Go anti-pattern (carried over from Java/C#) is to define an
interface in the same package as its concrete implementation —
"`repository.Repository` interface plus `repository.Pgx` struct" —
and have consumers import the producer package to use the interface.

This is the wrong direction in Go. The consumer is the one who knows
which methods it needs; the producer cannot anticipate every consumer.
Provider-side interfaces couple consumers to the producer's package,
bloat that package with declarations it might never satisfy itself,
and risk import cycles when one feature's interface references another
feature's types — a real problem under the per-feature layout from
[ADR-0001](0001-screaming-architecture.md).

## Decision

Interfaces live in the package that **uses** them, never where they
are implemented. Implementation packages export only concrete types
and structs.

Concrete examples in the codebase:

- `service.Repository`, `service.MailSender`, `service.RemoteRepositoryProvider`
  in `internal/subscription/service/`.
- `http.SubscriptionService` in `internal/subscription/http/`.
- `notifier.{Repository,MailSender}` in `internal/notifier/`.
- `confirmer.{Repository,MailSender}` in `internal/confirmer/`.
- `internal/github/` and `internal/notifier/emailer/` export concrete
  types only — zero interfaces.

Wiring happens in `internal/app/`: each consumer is constructed with
the concrete implementations it needs, satisfying its own interfaces
by structural typing.

## Consequences

### Positive
- No import cycles between features — each feature stands alone, the
  composition root in `internal/app/` pulls them together.
- Interface segregation by default: each consumer's interface contains
  only the methods that consumer calls, not the full producer surface.
- Adding a new consumer of an existing implementation means defining a
  new (smaller) interface in the consumer; the producer is untouched.
- Mocks live next to the consumer (`internal/<feature>/mocks/`) and
  are generated from the consumer-side interface — a producer change
  that doesn't affect the consumer's surface never invalidates a mock.

### Negative
- Onboarding cost: contributors familiar with Java/C# expect
  interfaces in the producer package and will instinctively put them
  there. Caught in code review and documented here.

## Links

- [ADR-0001](0001-screaming-architecture.md) — required this pattern
  to keep features from importing each other.
- `internal/subscription/service/service.go` — canonical example
  (interfaces declared at the top, struct below).
- `internal/notifier/notifier.go`, `internal/confirmer/confirmer.go` —
  same pattern at module scope.
