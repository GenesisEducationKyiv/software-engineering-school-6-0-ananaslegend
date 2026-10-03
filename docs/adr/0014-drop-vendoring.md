# ADR-0014: Drop vendoring — rely on Go modules with the proxy cache

Status: accepted · 2026-05-16 · @ananaslegend · infra

## Context

[ADR-0007](0007-vendor-and-dependency-updates.md) committed `vendor/` and
built everything with `-mod=vendor`. Two of the three forces that
motivated it no longer hold for this project:

1. **No corporate SSL proxy.** Builds run on GitHub-hosted Linux
   runners and on developer laptops with normal egress to
   `proxy.golang.org`. The "offline-build" requirement is hypothetical.
2. **`go.sum` already pins integrity.** Module fetches go through the
   Go checksum database; a tampered module is rejected at download
   time. Vendoring duplicates that guarantee in-tree.

The cost of vendoring is concrete and felt every week:

- ~3 250 files and tens of MB tracked in git. Every dep bump produces
  a PR with thousands of mechanical lines, drowning the actual review
  signal.
- A dedicated CI step (`Verify vendor is in sync`) and a Dependabot
  ritual workflow (`dependabot-comment.yml`) exist only to nag humans
  when `vendor/` and `go.mod` drift apart.
- `make tidy` has to atomically run `go mod tidy && go mod vendor`;
  forgetting the second half breaks CI.
- Adding a single dep means writing a `vendor/` tree the reviewer
  cannot reasonably read.

The supply-chain argument from ADR-0007 — "vendored code is at least
7 days old, malicious yanks have already passed" — is preserved by
the Dependabot cooldown (`.github/dependabot.yml`), not by
vendoring. Dropping `vendor/` does not change that timeline.

## Decision

Stop tracking `vendor/`. Build, test, and lint everywhere via the
standard module resolution:

1. `vendor/` is removed from the repository and not re-created.
2. `make build` and `make tidy` drop the `-mod=vendor` / `go mod
   vendor` clauses.
3. `Dockerfile` swaps `COPY vendor/` + `-mod=vendor` for `RUN go mod
   download` before the source-copy layer. Cache invalidation moves
   from `vendor/` to `go.sum`.
4. CI:
   - "Verify vendor is in sync" → "Verify `go.mod` is tidy" (no
     `vendor/` diff check).
   - `actions/setup-go` runs with `cache: true` so module downloads
     are cached across runs.
   - `GOFLAGS: -mod=vendor` removed from the Lint step.
   - `.github/workflows/dependabot-comment.yml` is deleted — its sole
     purpose was to remind humans to re-sync `vendor/`.
5. Dependabot itself stays (`.github/dependabot.yml`): same weekly
   cadence, same grouping, same 7-day / 30-day cooldown. Updates land
   as `go.mod` / `go.sum` edits without any vendor follow-up.

## Consequences

### Positive

- Dep-bump PRs become two-line diffs (`go.mod` + `go.sum`),
  reviewable in seconds.
- One less CI step and one less workflow to maintain.
- `make tidy` is a single atomic command (`go mod tidy`) — no
  forgotten second half.
- Repo size shrinks by the full `vendor/` tree.

### Negative

- Builds need network access to `proxy.golang.org`. If GitHub's
  module proxy is down, CI is blocked; same for Docker builds on
  developer machines.
- First-time CI and Docker builds pay the download cost (~150 MB) once
  before the cache warms up.
- Anyone behind a strict corporate firewall that blocks
  `proxy.golang.org` cannot build the project without setting
  `GOPROXY` to a mirror they can reach.

### Constraints

- If the corporate-SSL-proxy environment ever becomes relevant again,
  revisit this decision before bringing `vendor/` back — the cooldown
  + tidy workflow still works without vendoring, so vendoring should
  be argued on its own merits, not as a bundle.

## Links

- Supersedes [ADR-0007](0007-vendor-and-dependency-updates.md).
- `.github/dependabot.yml` — unchanged; still the source of routine
  updates.
- `Makefile` — `build`, `tidy` targets.
- `Dockerfile` — `RUN go mod download` layer.
- `.github/workflows/ci.yml` — `Verify go.mod is tidy`, no
  `-mod=vendor`.
