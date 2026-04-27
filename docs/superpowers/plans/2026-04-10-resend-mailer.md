# Resend Mailer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `ResendMailer` using the Resend Go SDK so the app can send email on Railway (which blocks all SMTP ports).

**Architecture:** New `ResendMailer` in the existing `emailer` package implements the same two-method `service.MailSender` interface. Renders existing HTML/text templates to strings, then posts via Resend SDK. Wired in `main.go` with highest priority: `RESEND_API_KEY` set → ResendMailer, `SMTP_HOST` set → SMTPMailer, else StubMailer.

**Tech Stack:** `github.com/resend/resend-go/v2`, existing `html/template` + `text/template`, `bytes.Buffer`.

---

### Task 1: Add Resend SDK dependency

**Files:**
- Modify: `go.mod`, `go.sum`, `vendor/`

- [ ] **Step 1: Add the SDK**

```bash
go get github.com/resend/resend-go/v2
```

Expected output includes: `go: added github.com/resend/resend-go/v2 vX.Y.Z`

- [ ] **Step 2: Vendor**

```bash
go mod vendor
```

Expected: no errors.

- [ ] **Step 3: Verify it appears in vendor**

```bash
ls vendor/github.com/resend/
```

Expected: `resend-go`

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum vendor/
git commit -m "chore: add resend-go/v2 SDK"
```

---

### Task 2: Add Resend config fields

**Files:**
- Modify: `internal/config/config.go`
- Modify: `.env.example`

- [ ] **Step 1: Add fields to Config struct in `internal/config/config.go`**

Add after the `SMTPTLSPolicy` line:

```go
ResendAPIKey string `envconfig:"RESEND_API_KEY"`
ResendFrom   string `envconfig:"RESEND_FROM"`
```

The full SMTP + Resend block should look like:

```go
SMTPHost      string `envconfig:"SMTP_HOST"`
SMTPPort      int    `envconfig:"SMTP_PORT" default:"587"`
SMTPUser      string `envconfig:"SMTP_USER"`
SMTPPass      string `envconfig:"SMTP_PASS"`
SMTPFrom      string `envconfig:"SMTP_FROM"`
SMTPTLSPolicy string `envconfig:"SMTP_TLS_POLICY" default:"starttls"`

ResendAPIKey string `envconfig:"RESEND_API_KEY"`
ResendFrom   string `envconfig:"RESEND_FROM"`
```

- [ ] **Step 2: Update `.env.example`**

Add after the SMTP block (before the Docker overrides comment):

```
# Resend (alternative to SMTP — works on Railway; set RESEND_API_KEY to activate)
# RESEND_API_KEY=re_...
# RESEND_FROM=noreply@yourdomain.com
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
go build -mod=vendor ./...
```

Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go .env.example
git commit -m "feat: add RESEND_API_KEY and RESEND_FROM config fields"
```

---

### Task 3: Implement ResendMailer

**Files:**
- Create: `internal/notifier/emailer/resend.go`

- [ ] **Step 1: Create `internal/notifier/emailer/resend.go`**

```go
package emailer

import (
	"bytes"
	"context"
	"fmt"

	"github.com/resend/resend-go/v2"

	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
)

type ResendMailer struct {
	client *resend.Client
	from   string
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{
		client: resend.NewClient(apiKey),
		from:   from,
	}
}

func (m *ResendMailer) SendConfirmation(ctx context.Context, p domain.SendConfirmationParams) error {
	var htmlBuf, txtBuf bytes.Buffer
	if err := confirmationHTMLTmpl.Execute(&htmlBuf, p); err != nil {
		return fmt.Errorf("render confirmation html: %w", err)
	}
	if err := confirmationTXTTmpl.Execute(&txtBuf, p); err != nil {
		return fmt.Errorf("render confirmation txt: %w", err)
	}

	_, err := m.client.Emails.Send(&resend.SendEmailRequest{
		From:    m.from,
		To:      []string{p.To},
		Subject: fmt.Sprintf("Confirm your subscription to %s", p.RepoFullName),
		Html:    htmlBuf.String(),
		Text:    txtBuf.String(),
	})
	if err != nil {
		return fmt.Errorf("send confirmation email: %w", err)
	}
	return nil
}

func (m *ResendMailer) SendRelease(ctx context.Context, p domain.SendReleaseParams) error {
	var htmlBuf, txtBuf bytes.Buffer
	if err := releaseHTMLTmpl.Execute(&htmlBuf, p); err != nil {
		return fmt.Errorf("render release html: %w", err)
	}
	if err := releaseTXTTmpl.Execute(&txtBuf, p); err != nil {
		return fmt.Errorf("render release txt: %w", err)
	}

	_, err := m.client.Emails.Send(&resend.SendEmailRequest{
		From:    m.from,
		To:      []string{p.To},
		Subject: fmt.Sprintf("New release %s for %s", p.ReleaseTag, p.RepoFullName),
		Html:    htmlBuf.String(),
		Text:    txtBuf.String(),
	})
	if err != nil {
		return fmt.Errorf("send release email: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Build to verify**

```bash
go build -mod=vendor ./...
```

Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/notifier/emailer/resend.go
git commit -m "feat: add ResendMailer using resend-go/v2 SDK"
```

---

### Task 4: Wire ResendMailer in main.go

**Files:**
- Modify: `cmd/api/main.go`

- [ ] **Step 1: Replace the mailSender selection block in `cmd/api/main.go`**

Find and replace the current block:

```go
var mailSender service.MailSender
if cfg.SMTPHost != "" {
    smtpMailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
        Host:      cfg.SMTPHost,
        Port:      cfg.SMTPPort,
        User:      cfg.SMTPUser,
        Password:  cfg.SMTPPass,
        From:      cfg.SMTPFrom,
        TLSPolicy: cfg.SMTPTLSPolicy,
    })
    if err != nil {
        log.Fatal().Err(err).Msg("create smtp mailer")
    }
    mailSender = smtpMailer
} else {
    mailSender = emailer.NewStubMailer()
}
```

With:

```go
var mailSender service.MailSender
switch {
case cfg.ResendAPIKey != "":
    mailSender = emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom)
    log.Info().Msg("mailer: resend")
case cfg.SMTPHost != "":
    smtpMailer, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
        Host:      cfg.SMTPHost,
        Port:      cfg.SMTPPort,
        User:      cfg.SMTPUser,
        Password:  cfg.SMTPPass,
        From:      cfg.SMTPFrom,
        TLSPolicy: cfg.SMTPTLSPolicy,
    })
    if err != nil {
        log.Fatal().Err(err).Msg("create smtp mailer")
    }
    mailSender = smtpMailer
    log.Info().Msg("mailer: smtp")
default:
    mailSender = emailer.NewStubMailer()
    log.Info().Msg("mailer: stub")
}
```

- [ ] **Step 2: Build to verify**

```bash
go build -mod=vendor ./...
```

Expected: no output (success).

- [ ] **Step 3: Run all tests**

```bash
go test -mod=vendor ./...
```

Expected: all pass, no failures.

- [ ] **Step 4: Commit and push**

```bash
git add cmd/api/main.go
git commit -m "feat: wire ResendMailer in main; RESEND_API_KEY activates it"
git push origin main
```
