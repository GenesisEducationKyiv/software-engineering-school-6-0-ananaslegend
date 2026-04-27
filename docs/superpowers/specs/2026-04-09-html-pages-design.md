# HTML Response Pages — Design Spec

**Date:** 2026-04-09  
**Goal:** Replace plain JSON responses on the browser-facing GET endpoints (`/api/confirm/{token}` and `/api/unsubscribe/{token}`) with styled HTML pages, and add matching HTML error pages for all error cases on those routes.

---

## Context

Confirm and unsubscribe links are clicked directly from email. Currently they return JSON (`{"status":"confirmed"}`), which is fine for API clients but poor UX for end-users. The goal is to show a human-readable, branded page on every outcome: success, link unavailable (404/410 merged — since expired tokens are cleaned from DB and become 404s), and unexpected error (500 with request ID).

---

## Pages (4 total)

| Page | Trigger | HTTP status |
|---|---|---|
| **Confirmed** | `GET /api/confirm/{token}` success | 200 |
| **Unsubscribed** | `GET /api/unsubscribe/{token}` success | 200 |
| **Unavailable** | `ErrTokenNotFound` OR `ErrTokenExpired` | 404 / 410 (actual domain status preserved) |
| **Oops** | Any other error (5xx) | 500 |

---

## Visual Style: Card / Floating

- Background: `#f1f5f9` (light slate)
- Card: white, `border-radius: 16px`, subtle box-shadow, centered, max-width 420px
- Icon: 56×56px square with rounded corners (`border-radius: 14px`), per-page accent colour
- Typography: `system-ui`, dark heading (`#0f172a`), muted body (`#64748b`)
- No external fonts or CDN dependencies — fully self-contained HTML

### Accent colours

| Page | Icon bg | Icon colour | Icon |
|---|---|---|---|
| Confirmed | `#ecfdf5` | `#10b981` (green) | ✓ |
| Unsubscribed | `#eff6ff` | `#3b82f6` (blue) | 👋 |
| Unavailable | `#fef3c7` | `#f59e0b` (amber) | ⏰ |
| Oops | `#fae8ff` | `#a855f7` (purple) | ! |

### Unavailable page CTA
"Subscribe again →" button (blue, `#2563eb`) links to `"/"` (app root). Placeholder until a subscribe form frontend is added.

### Oops page
Shows `Request ID: <id>` in a monospace footer below a horizontal rule. Request ID is retrieved via `middleware.GetReqID(r.Context())` (already available from chi middleware).

---

## Architecture

### New package: `internal/httpapi/pages/`

```
internal/httpapi/pages/
  pages.go            — Renderer struct + render methods
  templates/
    base.html         — shared layout (CSS, card wrapper, {{block "content" .}})
    confirmed.html    — {{define "content"}} block
    unsubscribed.html — {{define "content"}} block
    unavailable.html  — {{define "content"}} block; data: {AppBaseURL string}
    oops.html         — {{define "content"}} block; data: {RequestID string}
```

`pages.go` uses `//go:embed templates/*` and parses templates at init time via `template.Must`. The `Renderer` struct is zero-value usable (no constructor needed).

### Renderer API

```go
type Renderer struct{}

func (r Renderer) Confirmed(w http.ResponseWriter)
func (r Renderer) Unsubscribed(w http.ResponseWriter)
func (r Renderer) Unavailable(w http.ResponseWriter, appBaseURL string)
func (r Renderer) Oops(w http.ResponseWriter, requestID string)
```

Each method sets `Content-Type: text/html; charset=utf-8`, writes the appropriate HTTP status, and executes the template.

### Handler changes (`internal/subscription/http/handler.go`)

`Handler` gains two new fields: `pages pages.Renderer` and `appBaseURL string`. Both set via `NewHandler` options or an updated `HandlerConfig` struct.

**Confirm handler** — switches fully to HTML, no JSON:
```
success              → pages.Confirmed(w)
ErrTokenNotFound     → w.WriteHeader(404); pages.Unavailable(w, appBaseURL)
ErrTokenExpired      → w.WriteHeader(410); pages.Unavailable(w, appBaseURL)
everything else      → w.WriteHeader(500); pages.Oops(w, middleware.GetReqID(r.Context()))
```

**Unsubscribe handler** — same pattern:
```
success              → pages.Unsubscribed(w)
ErrTokenNotFound     → w.WriteHeader(404); pages.Unavailable(w, appBaseURL)
everything else      → w.WriteHeader(500); pages.Oops(w, middleware.GetReqID(r.Context()))
```

**Subscribe handler** — unchanged (JSON, POST from API clients).

`WriteError` in `internal/httpapi/errors.go` — unchanged. Only confirm/unsubscribe go HTML; subscribe stays JSON.

### Wire-up (`cmd/api/main.go`)

Pass `cfg.AppBaseURL` into the handler:
```go
subhttp.NewHandler(svc, httpapi.WriteError, cfg.AppBaseURL)
```

---

## Files to create / modify

| File | Action |
|---|---|
| `internal/httpapi/pages/pages.go` | Create |
| `internal/httpapi/pages/templates/base.html` | Create |
| `internal/httpapi/pages/templates/confirmed.html` | Create |
| `internal/httpapi/pages/templates/unsubscribed.html` | Create |
| `internal/httpapi/pages/templates/unavailable.html` | Create |
| `internal/httpapi/pages/templates/oops.html` | Create |
| `internal/subscription/http/handler.go` | Modify: add pages renderer + appBaseURL, rewrite Confirm/Unsubscribe |
| `cmd/api/main.go` | Modify: pass appBaseURL to NewHandler |

---

## Verification

```sh
go build ./...          # compiles cleanly
go test ./...           # existing service tests still pass

# Docker stack up
make docker-up

# Confirm flow
curl -s -X POST http://localhost:8080/api/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"email":"test@example.com","repository":"golang/go"}'

# Get confirm link from Mailpit http://localhost:8025
# Open confirm link in browser → should see green Confirmed card

# Open unsubscribe link in browser → should see blue Unsubscribed card

# Invalid token → http://localhost:8080/api/confirm/badtoken
# → should see amber Unavailable card with "Subscribe again" button

# 500 page visible in logs via request_id
```
