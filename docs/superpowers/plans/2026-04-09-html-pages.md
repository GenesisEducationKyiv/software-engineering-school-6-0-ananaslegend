# HTML Response Pages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace JSON responses on the browser-facing GET endpoints (`/api/confirm/{token}` and `/api/unsubscribe/{token}`) with styled HTML pages, and add matching HTML error pages for all error cases on those routes.

**Architecture:** New `internal/httpapi/pages` package holds a zero-value `Renderer` struct with embedded `html/template` templates (base layout + 4 page templates). The subscription HTTP handler imports `pages` and switches Confirm/Unsubscribe to render HTML directly, handling errors via switch/errors.Is instead of delegating to `writeError`.

**Tech Stack:** Go `html/template`, `//go:embed`, `chi/v5/middleware.GetReqID`, `zerolog`.

---

### Task 1: pages package — Renderer + templates

**Files:**
- Create: `internal/httpapi/pages/pages.go`
- Create: `internal/httpapi/pages/templates/base.html`
- Create: `internal/httpapi/pages/templates/confirmed.html`
- Create: `internal/httpapi/pages/templates/unsubscribed.html`
- Create: `internal/httpapi/pages/templates/unavailable.html`
- Create: `internal/httpapi/pages/templates/oops.html`
- Create: `internal/httpapi/pages/pages_test.go`

- [ ] **Step 1: Create `internal/httpapi/pages/pages.go`**

```go
package pages

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*
var templateFS embed.FS

var (
	confirmedTmpl    = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/confirmed.html"))
	unsubscribedTmpl = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/unsubscribed.html"))
	unavailableTmpl  = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/unavailable.html"))
	oopsTmpl         = template.Must(template.ParseFS(templateFS, "templates/base.html", "templates/oops.html"))
)

// Renderer renders HTML response pages. Zero-value is ready to use.
type Renderer struct{}

func (r Renderer) Confirmed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = confirmedTmpl.ExecuteTemplate(w, "base", nil)
}

func (r Renderer) Unsubscribed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = unsubscribedTmpl.ExecuteTemplate(w, "base", nil)
}

// Unavailable renders the "link expired or not found" page.
// status should be http.StatusNotFound (404) or http.StatusGone (410).
func (r Renderer) Unavailable(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = unavailableTmpl.ExecuteTemplate(w, "base", nil)
}

type oopsData struct {
	RequestID string
}

func (r Renderer) Oops(w http.ResponseWriter, requestID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = oopsTmpl.ExecuteTemplate(w, "base", oopsData{RequestID: requestID})
}
```

- [ ] **Step 2: Create `internal/httpapi/pages/templates/base.html`**

```html
{{define "base"}}<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{block "title" .}}Reposeetory{{end}}</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f1f5f9;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 24px;
    }
    .card {
      background: #fff;
      max-width: 420px;
      width: 100%;
      padding: 48px 40px;
      border-radius: 16px;
      box-shadow: 0 1px 3px rgba(0,0,0,0.1), 0 1px 2px rgba(0,0,0,0.06);
      text-align: center;
    }
    .icon {
      width: 56px;
      height: 56px;
      border-radius: 14px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      margin-bottom: 20px;
      font-size: 26px;
    }
    h1 { font-size: 20px; font-weight: 700; color: #0f172a; margin-bottom: 10px; }
    p  { font-size: 14px; color: #64748b; line-height: 1.6; }
    .btn {
      display: inline-block;
      margin-top: 24px;
      background: #2563eb;
      color: #fff;
      font-size: 14px;
      font-weight: 600;
      text-decoration: none;
      padding: 10px 22px;
      border-radius: 8px;
    }
    .link {
      display: inline-block;
      margin-top: 20px;
      color: #2563eb;
      font-size: 13px;
      text-decoration: none;
    }
    .req-id {
      margin-top: 20px;
      padding-top: 16px;
      border-top: 1px solid #e2e8f0;
      font-size: 12px;
      color: #94a3b8;
      font-family: ui-monospace, "Cascadia Code", monospace;
    }
  </style>
</head>
<body>
  <div class="card">
    {{block "content" .}}{{end}}
  </div>
</body>
</html>{{end}}
```

- [ ] **Step 3: Create `internal/httpapi/pages/templates/confirmed.html`**

```html
{{define "title"}}Subscription Confirmed{{end}}
{{define "content"}}
<div class="icon" style="background:#ecfdf5;color:#10b981;">✓</div>
<h1>Subscription Confirmed</h1>
<p>You will now receive release notifications directly to your inbox.</p>
<a href="/" class="link">← Back</a>
{{end}}
```

- [ ] **Step 4: Create `internal/httpapi/pages/templates/unsubscribed.html`**

```html
{{define "title"}}Unsubscribed{{end}}
{{define "content"}}
<div class="icon" style="background:#eff6ff;color:#3b82f6;">👋</div>
<h1>You've Been Unsubscribed</h1>
<p>You will no longer receive notifications. You can always re&#8209;subscribe later.</p>
<a href="/" class="link">← Back</a>
{{end}}
```

- [ ] **Step 5: Create `internal/httpapi/pages/templates/unavailable.html`**

```html
{{define "title"}}Link Unavailable{{end}}
{{define "content"}}
<div class="icon" style="background:#fef3c7;color:#f59e0b;">⏰</div>
<h1>Link Unavailable</h1>
<p>This link has expired or has already been used. Please subscribe again to receive a fresh confirmation email.</p>
<a href="/" class="btn">Subscribe again →</a>
{{end}}
```

- [ ] **Step 6: Create `internal/httpapi/pages/templates/oops.html`**

```html
{{define "title"}}Something Went Wrong{{end}}
{{define "content"}}
<div class="icon" style="background:#fae8ff;color:#a855f7;">!</div>
<h1>Something Went Wrong</h1>
<p>An unexpected error occurred. Please try again later.</p>
<div class="req-id">Request ID: {{.RequestID}}</div>
{{end}}
```

- [ ] **Step 7: Create `internal/httpapi/pages/pages_test.go`**

```go
package pages_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananaslegend/reposeetory/internal/httpapi/pages"
)

func TestRenderer_Confirmed(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Confirmed(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type: want text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "Confirmed") {
		t.Error("body: expected 'Confirmed'")
	}
}

func TestRenderer_Unsubscribed(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Unsubscribed(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Unsubscribed") {
		t.Error("body: expected 'Unsubscribed'")
	}
}

func TestRenderer_Unavailable_404(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Unavailable(w, http.StatusNotFound)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Unavailable") {
		t.Error("body: expected 'Unavailable'")
	}
}

func TestRenderer_Unavailable_410(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Unavailable(w, http.StatusGone)

	if w.Code != http.StatusGone {
		t.Errorf("status: want 410, got %d", w.Code)
	}
}

func TestRenderer_Oops(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Oops(w, "abc123/req-001")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "abc123/req-001") {
		t.Error("body: expected request ID in response")
	}
}
```

- [ ] **Step 8: Run tests — expect FAIL (package doesn't compile yet)**

```bash
go test ./internal/httpapi/pages/...
```

Expected: compile error or test failure (files exist but templates may be empty).

- [ ] **Step 9: Run tests — expect PASS (all templates created)**

```bash
go test ./internal/httpapi/pages/...
```

Expected:
```
ok  	github.com/ananaslegend/reposeetory/internal/httpapi/pages
```

- [ ] **Step 10: Commit**

```bash
git add internal/httpapi/pages/
git commit -m "feat: add html pages renderer with 4 templates"
```

---

### Task 2: Update Confirm and Unsubscribe handlers

**Files:**
- Modify: `internal/subscription/http/handler.go`

The `Handler` struct gains a `pages pages.Renderer` field. `NewHandler` initialises it as zero value. Confirm and Unsubscribe are rewritten to render HTML. Subscribe is unchanged.

- [ ] **Step 1: Replace `internal/subscription/http/handler.go` with the updated version**

```go
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"

	"github.com/ananaslegend/reposeetory/internal/httpapi/pages"
	"github.com/ananaslegend/reposeetory/internal/subscription/domain"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

type SubscriptionService interface {
	Subscribe(ctx context.Context, p domain.SubscribeParams) error
	Confirm(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
}

type Handler struct {
	svc        SubscriptionService
	writeError func(w http.ResponseWriter, r *http.Request, err error)
	pages      pages.Renderer
}

func NewHandler(svc SubscriptionService, writeError func(w http.ResponseWriter, r *http.Request, err error)) *Handler {
	return &Handler{svc: svc, writeError: writeError}
}

func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, errBadRequest("invalid JSON body"))
		return
	}

	if _, err := mail.ParseAddress(req.Email); err != nil || req.Email == "" {
		h.writeError(w, r, errBadRequest("invalid email"))
		return
	}

	if err := h.svc.Subscribe(r.Context(), domain.SubscribeParams{
		Email:      req.Email,
		Repository: req.Repository,
	}); err != nil {
		h.writeError(w, r, err)
		return
	}

	writeJSON(w, http.StatusAccepted, statusResponse{Status: "pending_confirmation"})
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := h.svc.Confirm(r.Context(), token); err != nil {
		switch {
		case errors.Is(err, domain.ErrTokenNotFound):
			h.pages.Unavailable(w, http.StatusNotFound)
		case errors.Is(err, domain.ErrTokenExpired):
			h.pages.Unavailable(w, http.StatusGone)
		default:
			zerolog.Ctx(r.Context()).Error().Err(err).Msg("confirm subscription")
			h.pages.Oops(w, middleware.GetReqID(r.Context()))
		}
		return
	}
	h.pages.Confirmed(w)
}

func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := h.svc.Unsubscribe(r.Context(), token); err != nil {
		switch {
		case errors.Is(err, domain.ErrTokenNotFound):
			h.pages.Unavailable(w, http.StatusNotFound)
		default:
			zerolog.Ctx(r.Context()).Error().Err(err).Msg("unsubscribe")
			h.pages.Oops(w, middleware.GetReqID(r.Context()))
		}
		return
	}
	h.pages.Unsubscribed(w)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// BadRequestError is a sentinel for handler-level validation failures.
type BadRequestError struct{ msg string }

func (e *BadRequestError) Error() string { return e.msg }

func errBadRequest(msg string) error { return &BadRequestError{msg: msg} }
```

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected:
```
ok  	github.com/ananaslegend/reposeetory/internal/httpapi/pages
ok  	github.com/ananaslegend/reposeetory/internal/subscription/service
```

- [ ] **Step 4: Commit**

```bash
git add internal/subscription/http/handler.go
git commit -m "feat: confirm and unsubscribe render html pages"
```

---

### Task 3: End-to-end verification

No code changes — manual verification.

- [ ] **Step 1: Start the stack**

```bash
make docker-up
```

Wait for: `api-1 | server listening addr=:8080`

- [ ] **Step 2: Subscribe**

```bash
curl -s -X POST http://localhost:8080/api/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"email":"htmltest@example.com","repository":"golang/go"}' | jq .
```

Expected: `{"status":"pending_confirmation"}`

- [ ] **Step 3: Get confirm and unsubscribe URLs from Mailpit**

```bash
MSG_ID=$(curl -s http://localhost:8025/api/v1/messages | jq -r '.messages[0].ID')
curl -s "http://localhost:8025/api/v1/message/$MSG_ID" | jq -r '.Text' | grep -o 'http://[^ ]*'
```

Copy both URLs from output.

- [ ] **Step 4: Open confirm URL in browser**

Open `http://localhost:8080/api/confirm/<token>` in browser.

Expected: green card — "Subscription Confirmed", ← Back link.

- [ ] **Step 5: Open confirm URL again (already used)**

Open same confirm URL in browser again.

Expected: amber card — "Link Unavailable", "Subscribe again →" button.

- [ ] **Step 6: Open unsubscribe URL in browser**

Open `http://localhost:8080/api/unsubscribe/<token>` in browser.

Expected: blue card — "You've Been Unsubscribed", ← Back link.

- [ ] **Step 7: Test invalid token**

Open `http://localhost:8080/api/confirm/badtoken` in browser.

Expected: amber card — "Link Unavailable".

- [ ] **Step 8: Verify Subscribe still returns JSON**

```bash
curl -s -X POST http://localhost:8080/api/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"email":"bad","repository":"golang/go"}' | jq .
```

Expected: `{"error":"invalid email"}` (JSON, not HTML).

- [ ] **Step 9: Tear down**

```bash
make docker-down
```
