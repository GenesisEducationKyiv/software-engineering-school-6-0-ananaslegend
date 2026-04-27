# Resend Mailer Design

**Date:** 2026-04-10  
**Goal:** Add `ResendMailer` using the Resend Go SDK as an alternative to `SMTPMailer`. Required because Railway blocks all outbound SMTP ports (25, 465, 587).

---

## Architecture

New file `internal/notifier/emailer/resend.go` in the existing `emailer` package. Implements the same `service.MailSender` interface (`SendConfirmation`, `SendRelease`) as `SMTPMailer`.

Uses the existing `templateFS` embed and HTML/text templates already in the package — no new templates needed.

## Components

### `ResendMailer`

```go
type ResendMailer struct {
    client *resend.Client
    from   string
}
```

- `NewResendMailer(apiKey, from string) *ResendMailer`
- `SendConfirmation(ctx, domain.SendConfirmationParams) error` — renders confirmation HTML+text, sends via Resend SDK
- `SendRelease(ctx, domain.SendReleaseParams) error` — renders release HTML+text, sends via Resend SDK

Template rendering: `bytes.Buffer` + `ExecuteTemplate` → string, same pattern as SMTP path but manual (Resend SDK takes `html string`, `text string`).

### Config

Two new env vars:

| Var | Description |
|---|---|
| `RESEND_API_KEY` | Resend API key (required to activate ResendMailer) |
| `RESEND_FROM` | From address, e.g. `noreply@yourdomain.com` |

### Wiring in `main.go`

Priority order (first match wins):

1. `RESEND_API_KEY` set → `ResendMailer`
2. `SMTP_HOST` set → `SMTPMailer`
3. else → `StubMailer`

### Config struct (`internal/config/config.go`)

Add `ResendAPIKey string` and `ResendFrom string` fields with `envconfig` tags.

## Dependencies

Add `github.com/resend/resend-go/v2` to `go.mod` + `vendor`.

## Error handling

Both send methods wrap errors with `fmt.Errorf("send confirmation/release email: %w", err)` — consistent with existing SMTP error wrapping.

## Testing

No new unit tests required — `ResendMailer` is a thin wrapper over the SDK, same as `SMTPMailer`. Manual verification via Railway deploy.
