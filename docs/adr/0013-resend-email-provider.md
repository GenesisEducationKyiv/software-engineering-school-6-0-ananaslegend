# ADR-0013: Use Resend as the email provider

Status: accepted · 2026-05-10 · @ananaslegend · integrations

## Context

[ADR-0012](./0012-host-on-railway.md) rules out SMTP — Railway blocks
outbound ports 25/465/587 — so transactional email must go through an
HTTPS-based provider. We need: free-tier sufficient for study /
hobby volume, an officially supported Go SDK, plain HTML+text
delivery, no AWS-style production-access gating. The HTTPS providers
considered:

1. **Resend** — modern; official Go SDK; ~100/day, ~3000/month free.
2. **SendGrid** — mature; larger API surface; weaker free tier.
3. **Mailgun** — solid but typically paid; EU/US region planning.
4. **AWS SES** — cheapest at scale, but production-access approval
   and AWS account overhead are heavy for a study project.
5. **Postmark** — strong reputation, paid only.

## Decision

Use **Resend**. The implementation in
`internal/notifier/emailer/resend.go` is a thin wrapper around the
official `github.com/resend/resend-go/v2` SDK that satisfies the same
`Emailer` interface as `SMTPMailer` and `StubMailer`. Selection in
`internal/app/mailer.go` is gated by `RESEND_API_KEY`; when set, Resend
takes priority over any SMTP config. `RESEND_FROM` controls the
From-address and must be a verified domain on the Resend dashboard.

## Consequences

### Positive
- Free tier covers expected volume at zero cost.
- Official Go SDK — no hand-rolled HTTPS client, well-typed
  request/response shapes.
- Same `Emailer` interface as SMTP and Stub: provider swap is a single
  `case` in `internal/app/mailer.go` thanks to
  [ADR-0002](./0002-consumer-side-interfaces.md). Business code is
  untouched.
- Works on Railway and any other host that allows outbound HTTPS,
  unblocking the deployment chosen in
  [ADR-0012](./0012-host-on-railway.md).

### Negative
- Vendor lock-in to a relatively young company (Resend, founded 2023);
  if the service shuts down or repricing happens, we'd add another
  provider behind the same `Emailer` interface.
- Free-tier rate limits (~100/day) — a real launch needs a paid plan
  or a different provider.
- Per-message API cost replaces the (effectively free) SMTP option.

### Constraints
- The From-address must remain on a verified Resend domain (DKIM, SPF,
  return-path configured in the dashboard); an unverified domain
  fails silently at send time.
- New email types (e.g. weekly digests) must go through the `Emailer`
  interface — do not call the Resend SDK directly from feature
  packages.

## Links

- `internal/notifier/emailer/resend.go` — `ResendMailer` wrapper.
- `internal/app/mailer.go` — selection logic (Resend → SMTP → Stub).
- `internal/config/config.go` — `RESEND_API_KEY`, `RESEND_FROM`.
- [ADR-0012](./0012-host-on-railway.md) — the Railway constraint that
  forced an HTTPS-based provider.
- [ADR-0002](./0002-consumer-side-interfaces.md) — `Emailer` interface
  lives in the consumer (`notifier/`, `confirmer/`), making the swap
  trivial.
- [ADR-0003](./0003-async-email-outbox.md) — outbox pattern that
  decouples the chosen provider from request latency.
