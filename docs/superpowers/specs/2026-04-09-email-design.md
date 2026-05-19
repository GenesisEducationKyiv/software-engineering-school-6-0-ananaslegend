# Email Sending — Design Spec

**Date:** 2026-04-09  
**Goal:** Replace `StubMailer` with a real `SMTPMailer` backed by `go-mail`, support Mailpit for dev and any SMTP provider for prod, and enable full subscribe/unsubscribe flow testing via email.

---

## Context

The codebase already has:
- `internal/mailer/smtp.go` — `SMTPMailer` using `net/smtp` (to be replaced)
- `internal/mailer/stub.go` — `StubMailer` (kept for dev when SMTP not configured)
- `internal/mailer/templates/confirmation.html` and `release.html`
- `service.MailSender` interface with `SendConfirmation` and `SendRelease`

Known bugs in current `smtp.go`:
- `SendConfirmationParams` missing `RepoFullName` (template uses it, struct does not)
- No `UnsubscribeURL` in confirmation email (needed for flow testing)
- No multipart MIME (HTML-only, poor spam score)

---

## Architecture

### Library

Use `github.com/wneessen/go-mail` — actively maintained, native TLS, multipart/alternative out of the box.

### `internal/mailer/smtp.go`

Rewrite using `go-mail`:
- `mail.NewClient(host, mail.WithPort(port), mail.WithSMTPAuth(...), mail.WithTLSPolicy(...))`
- Per-email: `mail.NewMsg()` → `msg.SetBodyHTMLTemplate(tmpl, data)` + `msg.AddAlternativeTextTemplate(tmpl, data)`
- TLS policy driven by `SMTP_TLS_POLICY` env var: `starttls` (port 587), `ssl` (port 465), `none` (port 1025, Mailpit)

### Templates

| File | Status |
|---|---|
| `templates/confirmation.html` | Modify: add `{{.UnsubscribeURL}}` |
| `templates/confirmation.txt` | New: plain text version |
| `templates/release.html` | No change |
| `templates/release.txt` | New: plain text version |

### Domain params (`internal/subscription/domain/model.go`)

`SendConfirmationParams` — add two fields:
```go
RepoFullName    string  // was missing, template already uses it
UnsubscribeURL  string  // new, for unsubscribe link in confirmation email
```

### Service (`internal/subscription/service/service.go`)

In `Subscribe(...)`, update `SendConfirmation` call to pass:
- `RepoFullName: p.Repository`
- `UnsubscribeURL: s.appBaseURL + "/api/unsubscribe/" + unsubscribeToken`

### Config (`internal/config/config.go`)

Add SMTP fields:
```
SMTP_HOST         string  (no default — empty = use StubMailer)
SMTP_PORT         int     default: 587
SMTP_USER         string
SMTP_PASS         string
SMTP_FROM         string  (falls back to SMTP_USER)
SMTP_TLS_POLICY   string  default: "starttls"  (starttls | ssl | none)
```

### Wire-up (`cmd/api/main.go`)

```go
var mailSender service.MailSender
if cfg.SMTPHost != "" {
    mailSender = mailer.NewSMTPMailer(mailer.SMTPMailerConfig{...})
} else {
    mailSender = mailer.NewStubMailer()
}
```

---

## Dev Setup (Mailpit)

Install: `brew install mailpit` or `go install github.com/axllent/mailpit@latest`  
Run: `mailpit` → web UI at `http://localhost:8025`, SMTP at `localhost:1025`

`.env` for dev:
```
SMTP_HOST=localhost
SMTP_PORT=1025
SMTP_TLS_POLICY=none
```

---

## Error Handling

- Template render error → return wrapped error (no email sent)
- SMTP dial/send error → return wrapped error, logged by service layer
- No change to service error handling strategy

---

## Testing

- Unit tests: `SMTPMailer` is not mocked — it implements `service.MailSender`; service tests use the existing `MockMailSender`
- Manual E2E: Mailpit web UI at `:8025` for visual verification of HTML + plain text rendering and links
- No new unit tests required for `SMTPMailer` itself (thin wrapper over go-mail)
