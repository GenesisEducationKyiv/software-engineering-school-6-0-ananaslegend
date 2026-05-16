# ADR-0015: Adopt the Testing Trophy as the project's testing philosophy

Status: accepted · 2026-05-16 · @ananaslegend · testing

## Context

The project needs a single mental model for what kind of test goes where
and where to spend the test budget. Without one, contributors default to
unit-test maximalism — high coverage numbers, low confidence in real
behaviour — or skip past the cheaper layers entirely. The stack is
infrastructure-heavy (non-trivial SQL, outbox drainers, browser-rendered
flows), so confidence comes from running the real system, not from
counting touched branches.

## Decision

Adopt the **Testing Trophy** — four layers, in order: Static, Unit,
Integration, E2E. Most of the test budget goes into the Integration
layer; the others exist to catch what Integration cannot cheaply prove.

Per-layer rules, layout, and run instructions are intentionally **not**
in this ADR — they live in [`docs/testing.md`](../testing.md), which
evolves. This ADR fixes only the philosophy.

## Consequences

### Positive

- Single mental model new contributors can absorb in one paragraph.
- Reviewers can push back on misplaced tests (e.g. an E2E that asserts
  DB rows, or a unit test that duplicates an integration scenario)
  against a written reference.

### Negative

- CI is slower than a pyramid-aligned project — Integration carries
  Docker startup cost. Accepted as the price of real-system confidence.

## Links

- [`docs/testing.md`](../testing.md) — living per-layer rules and layout.
- [ADR-0011](0011-testing-strategy.md) — finer-grained rule (mock the
  consumer-side interface, run the real DB) that lives inside the Unit
  and Integration layers of this trophy.
