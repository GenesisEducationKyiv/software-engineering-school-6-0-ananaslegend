# Extract Mailer Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract email rendering + delivery into a standalone, stateless HTTP service (`cmd/mailer`); the monolith's `notifier`/`confirmer` drainers call it over HTTP instead of sending email locally.

**Architecture:** The monolith keeps both outbox tables and the drainer loops. Each drainer builds a request DTO (URLs pre-resolved from `APP_BASE_URL`) and POSTs it to the mailer service via a new `mailerclient`. The mailer service owns Resend/SMTP providers and the email templates, exposing `POST /v1/emails/release` and `POST /v1/emails/confirmation`. Shared wire DTOs and the permanent/transient error sentinel live in a small `internal/mailer/contract` package imported by both sides.

**Tech Stack:** Go 1.26, chi v5, prometheus/client_golang, resend-go/v2, wneessen/go-mail, zerolog, envconfig, testify, gomock.

**PROJECT RULE — NO COMMITS:** Do **not** run `git commit`/`git push`. The user commits manually. Each task ends at a green checkpoint (tests/build pass); stop there.

---

## File Structure

**New files:**
- `internal/mailer/contract/contract.go` — wire DTOs (`SendReleaseRequest`, `SendConfirmationRequest`) + `ErrPermanent` sentinel.
- `internal/mailer/contract/contract_test.go` — JSON tag + template-field compatibility test.
- `internal/mailer/transport/transport.go` — HTTP handlers + delivery metrics.
- `internal/mailer/transport/transport_test.go` — handler tests with mock emailer.
- `internal/mailer/transport/mocks/mock_interfaces.go` — generated mock for the `Emailer` interface (consumer-side in `transport`).
- `internal/mailerclient/client.go` — HTTP client implementing `notifier.MailSender` + `confirmer.MailSender`.
- `internal/mailerclient/client_test.go` — client tests against `httptest.Server`.
- `cmd/mailer/main.go` — service entrypoint: config + emailer selection + router + `/healthz` + `/metrics`.
- `Dockerfile.mailer` — build/run image for the mailer service.
- `docs/adr/0015-extract-mailer-service.md` — ADR.

**Moved files (git mv, package + import retyped):**
- `internal/notifier/emailer/*` → `internal/mailer/emailer/*` (resend.go, smtp.go, stub.go, emailer.go, tmpl.go, templates/).

**Modified files:**
- `internal/mailer/emailer/emailer.go`, `resend.go`, `smtp.go`, `stub.go` — switch `domain.*Params` → `contract.*Request`.
- `internal/notifier/notifier.go` — `MailSender` takes `contract.SendReleaseRequest`; `Flush` builds DTO + permanent/transient handling.
- `internal/notifier/notifier_test.go` — update expectations to `contract.SendReleaseRequest`.
- `internal/notifier/mocks/mock_interfaces.go` — regenerated.
- `internal/confirmer/confirmer.go`, `confirmer_test.go`, `mocks/mock_interfaces.go` — same shape as notifier.
- `internal/subscription/domain/model.go` — delete `SendReleaseParams`, `SendConfirmationParams`.
- `internal/app/workers.go` — accept `*mailerclient.Client` instead of `emailer.Emailer`.
- `internal/app/app.go` — build `mailerclient` instead of `newEmailer`.
- `internal/app/mailer.go` — **delete** (logic moves to `cmd/mailer`).
- `internal/config/config.go` — add `MailerURL`; (SMTP/Resend fields remain — now consumed only by `cmd/mailer`, see Task 8 note).
- `Dockerfile` — no change (still builds `cmd/api`).
- `Makefile` — add `build-mailer`, `run-mailer`.
- `docker-compose.yml` — add `mailer` service; point `api` at it.
- `.env.example` — add `MAILER_URL`, `MAILER_HTTP_ADDR`.
- `docs/architecture.md`, `CLAUDE.md` — update.

---

## Task 1: Contract package (wire DTOs + error sentinel)

**Files:**
- Create: `internal/mailer/contract/contract.go`
- Test: `internal/mailer/contract/contract_test.go`

- [ ] **Step 1: Write the failing test**

```go
package contract_test

import (
	"encoding/json"
	htmltpl "html/template"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
)

func TestSendReleaseRequest_JSONTags(t *testing.T) {
	in := contract.SendReleaseRequest{
		To:             "user@example.com",
		RepoFullName:   "owner/name",
		ReleaseTag:     "v1.2.3",
		ReleaseURL:     "https://github.com/owner/name/releases/tag/v1.2.3",
		UnsubscribeURL: "https://app.example.com/api/unsubscribe/tok",
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)

	var out contract.SendReleaseRequest
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in, out)
	require.Contains(t, string(b), `"repo_full_name"`)
	require.Contains(t, string(b), `"unsubscribe_url"`)
}

// Guards that the template field names email templates rely on
// still exist on the request struct after the move from domain.
func TestSendReleaseRequest_TemplateFieldsResolve(t *testing.T) {
	tmpl := htmltpl.Must(htmltpl.New("t").Parse(
		`{{.RepoFullName}} {{.ReleaseTag}} {{.ReleaseURL}} {{.UnsubscribeURL}}`))
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, contract.SendReleaseRequest{
		RepoFullName: "owner/name", ReleaseTag: "v1", ReleaseURL: "u", UnsubscribeURL: "x",
	}))
	require.Equal(t, "owner/name v1 u x", buf.String())
}

func TestSendConfirmationRequest_TemplateFieldsResolve(t *testing.T) {
	tmpl := htmltpl.Must(htmltpl.New("t").Parse(`{{.RepoFullName}} {{.ConfirmURL}}`))
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, contract.SendConfirmationRequest{
		RepoFullName: "owner/name", ConfirmURL: "c",
	}))
	require.Equal(t, "owner/name c", buf.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailer/contract/...`
Expected: FAIL — package `contract` does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package contract holds the wire DTOs shared between the monolith
// drainers (via mailerclient) and the standalone mailer service.
// It contains data types and a status-classification sentinel only —
// no behaviour.
package contract

import "errors"

// SendReleaseRequest is the body of POST /v1/emails/release.
// Field names match the email template fields; URLs are pre-resolved
// by the caller (the monolith owns its routes and base URL).
type SendReleaseRequest struct {
	To             string `json:"to"`
	RepoFullName   string `json:"repo_full_name"`
	ReleaseTag     string `json:"release_tag"`
	ReleaseURL     string `json:"release_url"`
	UnsubscribeURL string `json:"unsubscribe_url"`
}

// SendConfirmationRequest is the body of POST /v1/emails/confirmation.
type SendConfirmationRequest struct {
	To           string `json:"to"`
	ConfirmURL   string `json:"confirm_url"`
	RepoFullName string `json:"repo_full_name"`
}

// ErrPermanent classifies a mailer failure as non-retryable (HTTP 4xx):
// the request itself is malformed, so re-sending will never succeed.
// Drainers drop the outbox row instead of retrying. Transient failures
// (5xx / network / timeout) are returned without this sentinel.
var ErrPermanent = errors.New("mailer: permanent failure")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailer/contract/...`
Expected: PASS.

- [ ] **Checkpoint:** `go build ./...` succeeds. Stop (no commit).

---

## Task 2: Move emailer package into the mailer service and retype to contract

**Files:**
- Move: `internal/notifier/emailer/` → `internal/mailer/emailer/`
- Modify: `internal/mailer/emailer/emailer.go`, `resend.go`, `smtp.go`, `stub.go`
- Test: `internal/mailer/emailer/emailer_test.go` (new)

- [ ] **Step 1: Move the package directory (no content change yet)**

Run:
```bash
git mv internal/notifier/emailer internal/mailer/emailer
```
(`templates/` and `tmpl.go` move with it; the `//go:embed templates/*` path stays valid because it is relative to the package dir.)

- [ ] **Step 2: Retype `emailer.go` to the contract DTOs**

Replace the whole file `internal/mailer/emailer/emailer.go` with:

```go
package emailer

import (
	"context"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
)

type Emailer interface {
	SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error
	SendRelease(ctx context.Context, p contract.SendReleaseRequest) error
}
```

- [ ] **Step 3: Retype `resend.go`**

In `internal/mailer/emailer/resend.go`:
- change the import `"github.com/ananaslegend/reposeetory/internal/subscription/domain"` → `"github.com/ananaslegend/reposeetory/internal/mailer/contract"`.
- change method signatures: `SendConfirmation(ctx context.Context, p domain.SendConfirmationParams)` → `p contract.SendConfirmationRequest`; `SendRelease(... p domain.SendReleaseParams)` → `p contract.SendReleaseRequest`.
- no body changes (field names are identical).

- [ ] **Step 4: Retype `smtp.go` and `stub.go` the same way**

In `internal/mailer/emailer/smtp.go` and `internal/mailer/emailer/stub.go`: swap the `domain` import for `contract` and replace `domain.SendConfirmationParams`→`contract.SendConfirmationRequest`, `domain.SendReleaseParams`→`contract.SendReleaseRequest`. No body changes.

- [ ] **Step 5: Write a render smoke test (guards templates against the new types)**

Create `internal/mailer/emailer/emailer_test.go`:

```go
package emailer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
	"github.com/ananaslegend/reposeetory/internal/mailer/emailer"
)

// StubMailer must satisfy Emailer and render without touching the network.
func TestStubMailer_SatisfiesEmailerAndRenders(t *testing.T) {
	var e emailer.Emailer = emailer.NewStubMailer()

	require.NoError(t, e.SendRelease(context.Background(), contract.SendReleaseRequest{
		To: "u@example.com", RepoFullName: "owner/name", ReleaseTag: "v1",
		ReleaseURL: "https://x", UnsubscribeURL: "https://y",
	}))
	require.NoError(t, e.SendConfirmation(context.Background(), contract.SendConfirmationRequest{
		To: "u@example.com", ConfirmURL: "https://c", RepoFullName: "owner/name",
	}))
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/mailer/...`
Expected: PASS.

- [ ] **Checkpoint:** `go build ./internal/mailer/...` succeeds. The monolith will NOT build yet (notifier/confirmer still reference the old emailer path + domain types — fixed in Tasks 6–7). Stop.

---

## Task 3: Mailer HTTP transport (handlers + delivery metrics)

**Files:**
- Create: `internal/mailer/transport/transport.go`
- Create: `internal/mailer/transport/mocks/mock_interfaces.go` (generated)
- Test: `internal/mailer/transport/transport_test.go`

- [ ] **Step 1: Write the failing test**

```go
package transport_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
	"github.com/ananaslegend/reposeetory/internal/mailer/transport"
	"github.com/ananaslegend/reposeetory/internal/mailer/transport/mocks"
)

func newHandler(t *testing.T) (*transport.Handler, *mocks.MockEmailer, *prometheus.Registry) {
	ctrl := gomock.NewController(t)
	em := mocks.NewMockEmailer(ctrl)
	reg := prometheus.NewRegistry()
	return transport.NewHandler(transport.Config{Emailer: em, Registry: reg}), em, reg
}

func TestSendRelease_OK(t *testing.T) {
	h, em, reg := newHandler(t)
	em.EXPECT().SendRelease(gomock.Any(), contract.SendReleaseRequest{
		To: "u@example.com", RepoFullName: "owner/name", ReleaseTag: "v1",
		ReleaseURL: "https://r", UnsubscribeURL: "https://u",
	}).Return(nil)

	body := `{"to":"u@example.com","repo_full_name":"owner/name","release_tag":"v1","release_url":"https://r","unsubscribe_url":"https://u"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/emails/release", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mailer_emails_sent_total Total emails attempted by the mailer service.
# TYPE mailer_emails_sent_total counter
mailer_emails_sent_total{result="ok",type="release"} 1
`), "mailer_emails_sent_total"))
}

func TestSendRelease_MalformedJSON_400(t *testing.T) {
	h, _, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/emails/release", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendRelease_MissingTo_400(t *testing.T) {
	h, _, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/emails/release", strings.NewReader(`{"release_tag":"v1"}`))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendRelease_ProviderError_502(t *testing.T) {
	h, em, reg := newHandler(t)
	em.EXPECT().SendRelease(gomock.Any(), gomock.Any()).Return(assertErr())

	body := `{"to":"u@example.com","repo_full_name":"o/n","release_tag":"v1","release_url":"r","unsubscribe_url":"u"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/emails/release", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.SendRelease(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mailer_emails_sent_total Total emails attempted by the mailer service.
# TYPE mailer_emails_sent_total counter
mailer_emails_sent_total{result="error",type="release"} 1
`), "mailer_emails_sent_total"))
}

func TestSendConfirmation_OK(t *testing.T) {
	h, em, _ := newHandler(t)
	em.EXPECT().SendConfirmation(gomock.Any(), contract.SendConfirmationRequest{
		To: "u@example.com", ConfirmURL: "https://c", RepoFullName: "owner/name",
	}).Return(nil)

	body := `{"to":"u@example.com","confirm_url":"https://c","repo_full_name":"owner/name"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/emails/confirmation", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.SendConfirmation(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func assertErr() error { return errString("provider down") }

type errString string

func (e errString) Error() string { return string(e) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailer/transport/...`
Expected: FAIL — package `transport` and its mocks do not exist.

- [ ] **Step 3: Write `transport.go`**

```go
package transport

//go:generate mockgen -source=transport.go -destination=mocks/mock_interfaces.go -package=mocks

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
)

// Emailer renders and delivers transactional emails.
type Emailer interface {
	SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error
	SendRelease(ctx context.Context, p contract.SendReleaseRequest) error
}

// Config holds Handler dependencies.
type Config struct {
	Emailer  Emailer
	Registry *prometheus.Registry
}

// Handler serves the mailer HTTP API.
type Handler struct {
	emailer Emailer
	sent    *prometheus.CounterVec
}

// NewHandler builds a Handler. If Registry is nil the metric is created
// but not registered (test-friendly, matching the project convention).
func NewHandler(cfg Config) *Handler {
	sent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mailer_emails_sent_total",
		Help: "Total emails attempted by the mailer service.",
	}, []string{"type", "result"})
	if cfg.Registry != nil {
		cfg.Registry.MustRegister(sent)
	}
	return &Handler{emailer: cfg.Emailer, sent: sent}
}

// SendRelease handles POST /v1/emails/release.
func (h *Handler) SendRelease(w http.ResponseWriter, r *http.Request) {
	var req contract.SendReleaseRequest
	if err := decode(r, &req); err != nil || req.To == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := h.emailer.SendRelease(r.Context(), req); err != nil {
		h.sent.WithLabelValues("release", "error").Inc()
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("mailer: send release failed")
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}
	h.sent.WithLabelValues("release", "ok").Inc()
	w.WriteHeader(http.StatusNoContent)
}

// SendConfirmation handles POST /v1/emails/confirmation.
func (h *Handler) SendConfirmation(w http.ResponseWriter, r *http.Request) {
	var req contract.SendConfirmationRequest
	if err := decode(r, &req); err != nil || req.To == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := h.emailer.SendConfirmation(r.Context(), req); err != nil {
		h.sent.WithLabelValues("confirmation", "error").Inc()
		zerolog.Ctx(r.Context()).Error().Err(err).Msg("mailer: send confirmation failed")
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}
	h.sent.WithLabelValues("confirmation", "ok").Inc()
	w.WriteHeader(http.StatusNoContent)
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err //nolint:wrapcheck // boundary decode; caller maps to 400
	}
	return nil
}
```

- [ ] **Step 4: Generate the mock**

Run:
```bash
cd internal/mailer/transport && go generate ./... && cd -
```
Expected: creates `internal/mailer/transport/mocks/mock_interfaces.go` with `MockEmailer`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/mailer/transport/...`
Expected: PASS.

- [ ] **Checkpoint:** `go build ./internal/mailer/...` succeeds. Stop.

---

## Task 4: Mailer service entrypoint (`cmd/mailer`)

**Files:**
- Create: `cmd/mailer/main.go`

- [ ] **Step 1: Write the router-assembly test**

Create `cmd/mailer/main_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/mailer/transport"
)

func TestNewRouter_HealthAndMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := transport.NewHandler(transport.Config{Emailer: nil, Registry: reg})
	srv := httptest.NewServer(newRouter(h, reg))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/mailer/...`
Expected: FAIL — `newRouter` undefined.

- [ ] **Step 3: Write `cmd/mailer/main.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	"github.com/ananaslegend/reposeetory/internal/httpapi"
	"github.com/ananaslegend/reposeetory/internal/mailer/emailer"
	"github.com/ananaslegend/reposeetory/internal/mailer/transport"
)

type mailerConfig struct {
	HTTPAddr        string        `envconfig:"MAILER_HTTP_ADDR" default:":8081"`
	ShutdownTimeout time.Duration `envconfig:"HTTP_SHUTDOWN_TIMEOUT" default:"15s"`

	LogLevel  string `envconfig:"LOG_LEVEL" default:"info"`
	LogPretty bool   `envconfig:"LOG_PRETTY" default:"true"`

	SMTPHost      string `envconfig:"SMTP_HOST"`
	SMTPPort      int    `envconfig:"SMTP_PORT" default:"587"`
	SMTPUser      string `envconfig:"SMTP_USER"`
	SMTPPass      string `envconfig:"SMTP_PASS"`
	SMTPFrom      string `envconfig:"SMTP_FROM"`
	SMTPTLSPolicy string `envconfig:"SMTP_TLS_POLICY" default:"starttls"`

	ResendAPIKey string `envconfig:"RESEND_API_KEY"`
	ResendFrom   string `envconfig:"RESEND_FROM"`
}

func main() {
	_ = godotenv.Load()

	var cfg mailerConfig
	if err := envconfig.Process("", &cfg); err != nil {
		zerolog.New(os.Stderr).Fatal().Err(err).Msg("load config")
	}

	log := newLogger(cfg)

	em, err := newEmailer(cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("create emailer")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	h := transport.NewHandler(transport.Config{Emailer: em, Registry: reg})

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      newRouter(h, reg),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("mailer listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("http server error")
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}
	log.Info().Msg("shutdown complete")
}

func newRouter(h *transport.Handler, reg *prometheus.Registry) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(httpapi.RequestLogger(zerolog.Nop()))
	r.Use(httpapi.PrometheusMiddleware(reg))

	r.Post("/v1/emails/release", h.SendRelease)
	r.Post("/v1/emails/confirmation", h.SendConfirmation)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return r
}

func newLogger(cfg mailerConfig) zerolog.Logger {
	var l zerolog.Logger
	if cfg.LogPretty {
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	} else {
		l = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
	lvl, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	return l.Level(lvl)
}

func newEmailer(cfg mailerConfig, log zerolog.Logger) (emailer.Emailer, error) {
	switch {
	case cfg.ResendAPIKey != "":
		log.Info().Msg("mailer: resend")
		return emailer.NewResendMailer(cfg.ResendAPIKey, cfg.ResendFrom), nil
	case cfg.SMTPHost != "":
		m, err := emailer.NewSMTPMailer(emailer.SMTPMailerConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Password: cfg.SMTPPass, From: cfg.SMTPFrom, TLSPolicy: cfg.SMTPTLSPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("main.newEmailer: emailer.NewSMTPMailer: %w", err)
		}
		log.Info().Msg("mailer: smtp")
		return m, nil
	default:
		log.Info().Msg("mailer: stub")
		return emailer.NewStubMailer(), nil
	}
}
```

> Note: the request-scoped logger passed to `RequestLogger` is `zerolog.Nop()` to keep `main.go` minimal; the completed-request line is dropped while the metrics middleware still records. If per-request logs are wanted later, pass `log` instead.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/mailer/...`
Expected: PASS.

- [ ] **Checkpoint:** `go build ./cmd/mailer/...` succeeds. Stop.

---

## Task 5: Mailer HTTP client (`internal/mailerclient`)

**Files:**
- Create: `internal/mailerclient/client.go`
- Test: `internal/mailerclient/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package mailerclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
	"github.com/ananaslegend/reposeetory/internal/mailerclient"
)

func TestSendRelease_2xx_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/emails/release", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := mailerclient.New(srv.URL, srv.Client())
	require.NoError(t, c.SendRelease(context.Background(), contract.SendReleaseRequest{To: "u@example.com"}))
}

func TestSendRelease_4xx_Permanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := mailerclient.New(srv.URL, srv.Client())
	err := c.SendRelease(context.Background(), contract.SendReleaseRequest{To: "u@example.com"})
	require.Error(t, err)
	require.True(t, errors.Is(err, contract.ErrPermanent))
}

func TestSendRelease_5xx_Transient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := mailerclient.New(srv.URL, srv.Client())
	err := c.SendRelease(context.Background(), contract.SendReleaseRequest{To: "u@example.com"})
	require.Error(t, err)
	require.False(t, errors.Is(err, contract.ErrPermanent))
}

func TestSendConfirmation_2xx_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/emails/confirmation", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := mailerclient.New(srv.URL, srv.Client())
	require.NoError(t, c.SendConfirmation(context.Background(), contract.SendConfirmationRequest{To: "u@example.com"}))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mailerclient/...`
Expected: FAIL — package `mailerclient` does not exist.

- [ ] **Step 3: Write `client.go`**

```go
// Package mailerclient is the monolith-side HTTP client for the mailer
// service. It implements notifier.MailSender and confirmer.MailSender.
package mailerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ananaslegend/reposeetory/internal/mailer/contract"
)

// Client posts email requests to the mailer service over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a Client. httpClient may be nil (http.DefaultClient is used).
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, http: httpClient}
}

// SendRelease posts a release notification request.
func (c *Client) SendRelease(ctx context.Context, p contract.SendReleaseRequest) error {
	if err := c.post(ctx, "/v1/emails/release", p); err != nil {
		return fmt.Errorf("mailerclient.Client.SendRelease: %w", err)
	}
	return nil
}

// SendConfirmation posts a confirmation request.
func (c *Client) SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error {
	if err := c.post(ctx, "/v1/emails/confirmation", p); err != nil {
		return fmt.Errorf("mailerclient.Client.SendConfirmation: %w", err)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mailerclient.Client.post: json.Marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mailerclient.Client.post: http.NewRequestWithContext: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mailerclient.Client.post: http.Client.Do: %w", err) // transient
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("mailerclient.Client.post: status %d: %w", resp.StatusCode, contract.ErrPermanent)
	default:
		return fmt.Errorf("mailerclient.Client.post: status %d", resp.StatusCode) // transient
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mailerclient/...`
Expected: PASS.

- [ ] **Checkpoint:** `go build ./internal/mailerclient/...` succeeds. Stop.

---

## Task 6: Retype the drainers (`notifier`, `confirmer`) to contract + permanent/transient handling

**Files:**
- Modify: `internal/notifier/notifier.go`, `internal/notifier/notifier_test.go`, `internal/notifier/mocks/mock_interfaces.go`
- Modify: `internal/confirmer/confirmer.go`, `internal/confirmer/confirmer_test.go`, `internal/confirmer/mocks/mock_interfaces.go`

- [ ] **Step 1: Update `internal/notifier/notifier.go`**

Change the import block: drop `"github.com/ananaslegend/reposeetory/internal/subscription/domain"`, add `"errors"` and `"github.com/ananaslegend/reposeetory/internal/mailer/contract"`.

Change the `MailSender` interface:
```go
// MailSender sends release notification emails.
type MailSender interface {
	SendRelease(ctx context.Context, p contract.SendReleaseRequest) error
}
```

Replace the send block inside `Flush` (the `n.mailer.SendRelease(...)` call and its error handling) with:
```go
			p := items[0]
			sendErr := n.mailer.SendRelease(ctx, contract.SendReleaseRequest{
				To:           p.Email,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
				ReleaseTag:   p.ReleaseTag,
				ReleaseURL: fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s",
					p.RepoOwner, p.RepoName, p.ReleaseTag),
				UnsubscribeURL: fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, p.UnsubscribeToken),
			})
			if sendErr != nil {
				n.m.emailsSent.WithLabelValues("error").Inc()
				if errors.Is(sendErr, contract.ErrPermanent) {
					zerolog.Ctx(ctx).Error().Err(sendErr).Int64("id", p.ID).
						Msg("notifier: permanent failure, dropping notification")
					if err = n.repo.MarkSent(ctx, p.ID); err != nil {
						return fmt.Errorf("notifier.Notifier.Flush: Repository.MarkSent: %w", err)
					}
					processed = true
					return nil
				}
				return fmt.Errorf("notifier.Notifier.Flush: MailSender.SendRelease: %w", sendErr)
			}
			n.m.emailsSent.WithLabelValues("ok").Inc()
			if err = n.repo.MarkSent(ctx, p.ID); err != nil {
				return fmt.Errorf("notifier.Notifier.Flush: Repository.MarkSent: %w", err)
			}
			processed = true
			return nil
```

- [ ] **Step 2: Regenerate the notifier mock**

Run:
```bash
cd internal/notifier && go generate ./... && cd -
```
Expected: `mocks/mock_interfaces.go` now references `contract.SendReleaseRequest`.

- [ ] **Step 3: Update `internal/notifier/notifier_test.go`**

Replace the `domain` import with `"github.com/ananaslegend/reposeetory/internal/mailer/contract"`. Change the explicit expectation (around line 66):
```go
	m.EXPECT().SendRelease(gomock.Any(), contract.SendReleaseRequest{
```
(keep the same field values). Add a new test for the permanent-drop path:
```go
func TestFlush_PermanentError_DropsAndMarksSent(t *testing.T) {
	n, tx, repo, m := newNotifier(t)
	tx.EXPECT().WithinTransaction(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).Times(2)
	repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).
		Return([]notifier.PendingNotification{{ID: 7, Email: "u@example.com", RepoOwner: "o", RepoName: "n", ReleaseTag: "v1"}}, nil)
	repo.EXPECT().GetNotificationsWithLock(gomock.Any(), 1).Return(nil, nil)
	m.EXPECT().SendRelease(gomock.Any(), gomock.Any()).
		Return(fmt.Errorf("bad request: %w", contract.ErrPermanent))
	repo.EXPECT().MarkSent(gomock.Any(), int64(7)).Return(nil)

	n.Flush(context.Background())
}
```
(Adjust imports: ensure `context`, `fmt`, `notifier`, `contract` are imported. The exact mock/var names — `newNotifier`, `tx`, `repo`, `m` — match the existing test helpers in this file.)

- [ ] **Step 4: Apply the identical change shape to `internal/confirmer/confirmer.go`**

Drop the `domain` import; add `"errors"` and `contract`. Change interface:
```go
// MailSender sends confirmation emails.
type MailSender interface {
	SendConfirmation(ctx context.Context, p contract.SendConfirmationRequest) error
}
```
Replace the send block in `Flush`:
```go
			p := items[0]
			sendErr := c.mailer.SendConfirmation(ctx, contract.SendConfirmationRequest{
				To:           p.Email,
				ConfirmURL:   c.baseURL + "/api/confirm/" + p.ConfirmToken,
				RepoFullName: p.RepoOwner + "/" + p.RepoName,
			})
			if sendErr != nil {
				c.m.emailsSent.WithLabelValues("error").Inc()
				if errors.Is(sendErr, contract.ErrPermanent) {
					zerolog.Ctx(ctx).Error().Err(sendErr).Int64("id", p.ID).
						Msg("confirmer: permanent failure, dropping confirmation")
					if err = c.repo.MarkSent(ctx, p.ID); err != nil {
						return fmt.Errorf("confirmer.Confirmer.Flush: Repository.MarkSent: %w", err)
					}
					processed = true
					return nil
				}
				return fmt.Errorf("confirmer.Confirmer.Flush: MailSender.SendConfirmation: %w", sendErr)
			}
			c.m.emailsSent.WithLabelValues("ok").Inc()
			if err = c.repo.MarkSent(ctx, p.ID); err != nil {
				return fmt.Errorf("confirmer.Confirmer.Flush: Repository.MarkSent: %w", err)
			}
			processed = true
			return nil
```

- [ ] **Step 5: Regenerate the confirmer mock + update its test**

Run:
```bash
cd internal/confirmer && go generate ./... && cd -
```
In `internal/confirmer/confirmer_test.go` swap the `domain` import for `contract` and change the explicit expectation (around line 65) to `contract.SendConfirmationRequest{...}` with the same field values.

- [ ] **Step 6: Run the drainer tests**

Run: `go test ./internal/notifier/... ./internal/confirmer/...`
Expected: PASS.

- [ ] **Checkpoint:** `go build ./internal/notifier/... ./internal/confirmer/...` succeeds. Stop.

---

## Task 7: Rewire the monolith composition root + delete dead domain types

**Files:**
- Modify: `internal/app/workers.go`, `internal/app/app.go`, `internal/config/config.go`
- Delete: `internal/app/mailer.go`
- Modify: `internal/subscription/domain/model.go`

- [ ] **Step 1: Add `MailerURL` to config**

In `internal/config/config.go`, add after the `AppBaseURL`/`ConfirmTokenTTL` block:
```go
	MailerURL string `envconfig:"MAILER_URL" default:"http://localhost:8081"`
```
(Leave the SMTP/Resend fields in place — they are now read only by `cmd/mailer`, which loads its own config struct. Removing them from the monolith struct is optional cleanup; keeping them avoids touching `.env` parsing for shared compose files. Documented as such in Task 9.)

- [ ] **Step 2: Update `internal/app/workers.go`**

Change the import: drop `"github.com/ananaslegend/reposeetory/internal/notifier/emailer"`, add `"github.com/ananaslegend/reposeetory/internal/mailerclient"`. Change the `runWorkers` signature param:
```go
	mail *mailerclient.Client,
```
(The `notifier.New`/`confirmer.New` calls are unchanged — `*mailerclient.Client` satisfies both `notifier.MailSender` and `confirmer.MailSender`.)

- [ ] **Step 3: Update `internal/app/app.go`**

Replace the `mailSender, err := newEmailer(...)` block with:
```go
	mailerClient := mailerclient.New(cfg.MailerURL, &http.Client{Timeout: 10 * time.Second})
```
Update the `runWorkers(...)` call to pass `mailerClient` instead of `mailSender`. Add imports `"net/http"`, `"time"`, and `"github.com/ananaslegend/reposeetory/internal/mailerclient"`. Remove the now-unused error check for the mailer.

- [ ] **Step 4: Delete the old monolith mailer factory**

Run:
```bash
rm internal/app/mailer.go
```

- [ ] **Step 5: Delete the moved-out domain types**

In `internal/subscription/domain/model.go`, delete the `SendConfirmationParams` and `SendReleaseParams` struct definitions (they now live in `internal/mailer/contract`). Leave all other types.

- [ ] **Step 6: Verify the whole module builds and tests pass**

Run: `go build ./... && go vet ./...`
Expected: no errors. If `go build` reports any lingering reference to `internal/notifier/emailer` or `domain.Send*Params`, fix that file's import/type.

Run: `go test ./...`
Expected: PASS (integration tests using testcontainers may be skipped without Docker — that is fine).

- [ ] **Checkpoint:** `go build ./...` and `go test ./internal/... ./cmd/...` green. Stop.

---

## Task 8: Infrastructure (Dockerfile, Makefile, compose, env)

**Files:**
- Create: `Dockerfile.mailer`
- Modify: `Makefile`, `docker-compose.yml`, `.env.example`

- [ ] **Step 1: Create `Dockerfile.mailer`**

```dockerfile
FROM golang:1.26-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /mailer ./cmd/mailer

FROM alpine:3.21 AS runtime
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /mailer /mailer
EXPOSE 8081
ENTRYPOINT ["/mailer"]
```

- [ ] **Step 2: Add Makefile targets**

After the existing `build:` target add:
```makefile
build-mailer:
	go build -o bin/mailer ./cmd/mailer

run-mailer:
	go run ./cmd/mailer
```
Add `build-mailer run-mailer` to the `.PHONY` line.

- [ ] **Step 3: Add the `mailer` service to `docker-compose.yml`**

Add a service (mirrors the `api` block; no DB dependency):
```yaml
  mailer:
    build:
      context: .
      dockerfile: Dockerfile.mailer
    environment:
      MAILER_HTTP_ADDR: ":8081"
      SMTP_HOST: mailpit
      SMTP_PORT: "1025"
      SMTP_TLS_POLICY: none
    ports:
      - "8081:8081"
    depends_on:
      - mailpit
```
In the existing `api` service `environment:` block, add:
```yaml
      MAILER_URL: "http://mailer:8081"
```
and add `mailer` to the `api` service's `depends_on:` list. Remove `SMTP_*` from the `api` environment block (the monolith no longer sends email).

- [ ] **Step 4: Update `.env.example`**

Add near the SMTP block:
```sh
# Mailer service
MAILER_URL=http://localhost:8081
MAILER_HTTP_ADDR=:8081
```
Add a comment above the existing `SMTP_*`/`RESEND_*` block: `# Consumed by the mailer service (cmd/mailer), not the monolith.`

- [ ] **Step 5: Verify both images build**

Run:
```bash
docker build -f Dockerfile -t reposeetory-api . && docker build -f Dockerfile.mailer -t reposeetory-mailer .
```
Expected: both succeed.

- [ ] **Checkpoint:** `make build && make build-mailer` succeed. Stop.

---

## Task 9: Documentation (ADR + architecture + CLAUDE.md)

**Files:**
- Create: `docs/adr/0015-extract-mailer-service.md`
- Modify: `docs/architecture.md`, `CLAUDE.md`, `docs/adr/README.md`

- [ ] **Step 1: Write ADR-0015**

Create `docs/adr/0015-extract-mailer-service.md` following the existing ADR format (look at `docs/adr/0013-resend-email-provider.md` for the template). Content must cover:
- **Context:** `subscription/domain` had become a shared kernel; the assignment requires extracting one domain into a microservice.
- **Decision:** extract email rendering + delivery into a stateless `cmd/mailer` HTTP service; drainers stay in the monolith and call it via `mailerclient`. URLs built by the monolith. Shared DTOs + `ErrPermanent` in `internal/mailer/contract`.
- **Consequences:** monolith sheds Resend/SMTP + templates; at-least-once retained; rare double-send on lost-response documented; 4xx = permanent drop, 5xx/network = transient retry; `github → subscription/domain` leak remains as a known follow-up.

- [ ] **Step 2: Update `docs/architecture.md`**

- Component diagram (section 2): move `notifier`/`confirmer` `MAIL` edge to an HTTP call to a new external `mailer` service box; the mailer owns Resend.
- External-deps table (section 5): note the monolith now depends on the mailer service over HTTP; Resend moves behind it.
- Process diagrams 4.2 and 4.3: the drainer step `-> RS` becomes `-> mailer (HTTP) -> RS`.

- [ ] **Step 3: Update `CLAUDE.md`**

- Add an ADR-0015 bullet to the ADR list.
- Update the intro line ("Один Go-бінарник…") to note there are now two binaries: the monolith (`cmd/api`) and the mailer service (`cmd/mailer`).
- Configuration section: add `MAILER_URL` (monolith) and note `MAILER_HTTP_ADDR` + that `RESEND_*`/`SMTP_*` are consumed by `cmd/mailer`.

- [ ] **Step 4: Update `docs/adr/README.md`**

Add the ADR-0015 row to the index table.

- [ ] **Checkpoint:** docs render; links resolve. Stop.

---

## Self-Review Notes (addressed)

- **Spec coverage:** every spec section maps to a task — contract (T1), email service move (T2), HTTP API (T3), service entrypoint (T4), client (T5), drainer rewire + delivery semantics incl. permanent/transient (T6), composition-root rewire + coupling cleanup + config (T7), deploy/infra (T8), docs/ADR (T9).
- **Out of scope (documented, not implemented):** `github → subscription/domain` leak; `Idempotency-Key`.
- **Type consistency:** `contract.SendReleaseRequest` / `contract.SendConfirmationRequest` and `contract.ErrPermanent` are used identically across T1, T2, T3, T5, T6; `transport.Handler.SendRelease/SendConfirmation`, `mailerclient.Client.SendRelease/SendConfirmation`, and `transport.NewHandler(transport.Config{...})` names are consistent across tasks.
