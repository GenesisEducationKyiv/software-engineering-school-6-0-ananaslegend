# Landing Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dark-themed landing page at `GET /` and a success page at `GET /subscribed` for reposeetory.

**Architecture:** Two standalone HTML templates (no shared base) are embedded alongside existing page templates. The `pages.Renderer` gets two new methods (`Landing`, `Subscribed`). The subscription handler gets two new route handler methods registered at `GET /` and `GET /subscribed`. The form on the landing page uses minimal inline JS to POST JSON to the existing `/api/subscribe` endpoint and redirects to `/subscribed` on success.

**Tech Stack:** Go `html/template`, `//go:embed`, `net/http`, inline CSS + JS (no external dependencies)

---

## File Map

| Action | File | What changes |
|---|---|---|
| Modify | `internal/subscription/http/pages/pages.go` | Add `landingTmpl`, `subscribedTmpl` vars; add `Landing(w)` and `Subscribed(w)` methods |
| Create | `internal/subscription/http/pages/templates/landing.html` | Full dark hero landing page |
| Create | `internal/subscription/http/pages/templates/subscribed.html` | Dark success page |
| Modify | `internal/subscription/http/pages/pages_test.go` | Add tests for `Landing` and `Subscribed` |
| Modify | `internal/subscription/http/handler.go` | Add `Landing` and `Subscribed` handler methods; register `GET /` and `GET /subscribed` |

---

### Task 1: Write failing tests

**Files:**
- Modify: `internal/subscription/http/pages/pages_test.go`

- [ ] **Step 1: Add tests for Landing and Subscribed to pages_test.go**

Append to the end of `internal/subscription/http/pages/pages_test.go`:

```go
func TestRenderer_Landing(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Landing(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type: want text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "reposeetory") {
		t.Error("body: expected 'reposeetory'")
	}
	if !strings.Contains(w.Body.String(), "Watch repository") {
		t.Error("body: expected 'Watch repository'")
	}
}

func TestRenderer_Subscribed(t *testing.T) {
	w := httptest.NewRecorder()
	pages.Renderer{}.Subscribed(w)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type: want text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "Check your inbox") {
		t.Error("body: expected 'Check your inbox'")
	}
	if !strings.Contains(w.Body.String(), "Subscribe more") {
		t.Error("body: expected 'Subscribe more'")
	}
}
```

- [ ] **Step 2: Run tests — expect compile failure**

```bash
go test ./internal/subscription/http/pages/...
```

Expected: compile error — `pages.Renderer{}.Landing undefined`

---

### Task 2: Renderer methods + templates

**Files:**
- Modify: `internal/subscription/http/pages/pages.go`
- Create: `internal/subscription/http/pages/templates/landing.html`
- Create: `internal/subscription/http/pages/templates/subscribed.html`

- [ ] **Step 1: Add template vars and methods to pages.go**

Add to the `var (...)` block in `internal/subscription/http/pages/pages.go`:

```go
landingTmpl    = template.Must(template.ParseFS(templateFS, "templates/landing.html"))
subscribedTmpl = template.Must(template.ParseFS(templateFS, "templates/subscribed.html"))
```

Add methods to `Renderer`:

```go
func (r Renderer) Landing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = landingTmpl.Execute(w, nil)
}

func (r Renderer) Subscribed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = subscribedTmpl.Execute(w, nil)
}
```

- [ ] **Step 2: Create landing.html**

Create `internal/subscription/http/pages/templates/landing.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>reposeetory — watch GitHub releases</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #0f172a;
      min-height: 100vh;
      color: #fff;
      display: flex;
      flex-direction: column;
    }
    .hero {
      flex: 1;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      padding: 64px 24px 48px;
      text-align: center;
      gap: 20px;
    }
    .wordmark {
      font-size: 36px;
      font-weight: 800;
      letter-spacing: -1.5px;
    }
    .wordmark .see {
      color: #60a5fa;
      text-decoration: underline;
      text-decoration-style: dotted;
      text-underline-offset: 5px;
      text-decoration-thickness: 2px;
      text-decoration-color: #60a5fa;
    }
    .tagline {
      font-size: 16px;
      color: #94a3b8;
      line-height: 1.6;
      max-width: 360px;
    }
    .tagline em { color: #60a5fa; font-style: normal; }
    .form {
      display: flex;
      flex-direction: column;
      gap: 10px;
      width: 100%;
      max-width: 360px;
    }
    .form input {
      background: #1e293b;
      border: 1px solid #334155;
      border-radius: 8px;
      height: 44px;
      padding: 0 14px;
      font-size: 14px;
      color: #e2e8f0;
      outline: none;
      width: 100%;
      font-family: inherit;
    }
    .form input::placeholder { color: #475569; }
    .form input:focus { border-color: #60a5fa; }
    .form button {
      background: #2563eb;
      color: #fff;
      border: none;
      border-radius: 8px;
      height: 44px;
      font-size: 14px;
      font-weight: 600;
      cursor: pointer;
      font-family: inherit;
    }
    .form button:disabled { opacity: 0.6; cursor: not-allowed; }
    .error {
      font-size: 13px;
      color: #f87171;
      min-height: 20px;
    }
    .chips {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      justify-content: center;
      max-width: 400px;
    }
    .chip {
      background: #1e293b;
      border-radius: 20px;
      padding: 5px 12px;
      font-size: 12px;
      color: #64748b;
      font-family: ui-monospace, "Cascadia Code", monospace;
    }
    .how { padding: 0 24px 48px; text-align: center; }
    .how-label {
      font-size: 11px;
      font-weight: 600;
      letter-spacing: 0.14em;
      color: #475569;
      text-transform: uppercase;
      margin-bottom: 32px;
    }
    .steps {
      display: flex;
      align-items: flex-start;
      justify-content: center;
      max-width: 480px;
      margin: 0 auto;
    }
    .step { flex: 1; padding: 0 12px; }
    .step-icon { font-size: 28px; margin-bottom: 10px; }
    .step-title { font-size: 13px; font-weight: 600; color: #e2e8f0; margin-bottom: 6px; }
    .step-desc { font-size: 12px; color: #64748b; line-height: 1.5; }
    .step-connector { padding-top: 14px; color: #334155; font-size: 16px; white-space: nowrap; }
    footer {
      padding: 18px 24px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .footer-logo { font-size: 13px; font-weight: 700; color: #334155; }
    .footer-logo .see { color: #1d4ed8; }
    .footer-link {
      display: flex;
      align-items: center;
      gap: 6px;
      font-size: 12px;
      color: #475569;
      text-decoration: none;
    }
    .footer-link:hover { color: #64748b; }
  </style>
</head>
<body>
  <section class="hero">
    <div class="wordmark">repo<span class="see">see</span>tory</div>
    <p class="tagline">Don't monitor GitHub.<br>Just <em>see</em> the updates.</p>
    <form class="form" id="subscribe-form" onsubmit="handleSubmit(event)">
      <input type="text" id="repo" name="repository" placeholder="owner/repository" autocomplete="off" required>
      <input type="email" id="email" name="email" placeholder="your@email.com" autocomplete="email" required>
      <button type="submit" id="submit-btn">Watch repository →</button>
      <p class="error" id="error-msg" aria-live="polite"></p>
    </form>
    <div class="chips">
      <span class="chip">axios/axios</span>
      <span class="chip">golang/go</span>
      <span class="chip">tailwindcss/tailwindcss</span>
      <span class="chip">vercel/next.js</span>
    </div>
  </section>

  <section class="how">
    <p class="how-label">How it works</p>
    <div class="steps">
      <div class="step">
        <div class="step-icon">📝</div>
        <div class="step-title">Subscribe</div>
        <div class="step-desc">Enter a repo and your email</div>
      </div>
      <div class="step-connector">─ ─</div>
      <div class="step">
        <div class="step-icon">✉️</div>
        <div class="step-title">Confirm</div>
        <div class="step-desc">Click the link in your inbox</div>
      </div>
      <div class="step-connector">─ ─</div>
      <div class="step">
        <div class="step-icon">🔔</div>
        <div class="step-title">Get notified</div>
        <div class="step-desc">Email on every new release</div>
      </div>
    </div>
  </section>

  <footer>
    <span class="footer-logo">repo<span class="see">see</span>tory</span>
    <a class="footer-link" href="https://github.com/ananaslegend/reposeetory" target="_blank" rel="noopener">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="#475569"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
      ananaslegend/reposeetory
    </a>
  </footer>

  <script>
    async function handleSubmit(e) {
      e.preventDefault();
      const btn = document.getElementById('submit-btn');
      const errorEl = document.getElementById('error-msg');
      const repo = document.getElementById('repo').value.trim();
      const email = document.getElementById('email').value.trim();

      btn.disabled = true;
      btn.textContent = 'Sending\u2026';
      errorEl.textContent = '';

      try {
        const res = await fetch('/api/subscribe', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ repository: repo, email }),
        });

        if (res.status === 202) {
          window.location.href = '/subscribed';
          return;
        }

        const messages = {
          400: 'Invalid repository or email format.',
          404: 'Repository not found on GitHub.',
          409: 'This email is already subscribed to that repository.',
        };
        errorEl.textContent = messages[res.status] ?? 'Something went wrong. Please try again.';
      } catch (_) {
        errorEl.textContent = 'Something went wrong. Please try again.';
      } finally {
        btn.disabled = false;
        btn.textContent = 'Watch repository \u2192';
      }
    }
  </script>
</body>
</html>
```

- [ ] **Step 3: Create subscribed.html**

Create `internal/subscription/http/pages/templates/subscribed.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Check your inbox — reposeetory</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #0f172a;
      min-height: 100vh;
      color: #fff;
      display: flex;
      flex-direction: column;
    }
    main {
      flex: 1;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      padding: 48px 24px;
      text-align: center;
      gap: 16px;
    }
    .icon { font-size: 52px; margin-bottom: 8px; }
    h1 { font-size: 24px; font-weight: 800; letter-spacing: -0.5px; }
    p { font-size: 14px; color: #94a3b8; line-height: 1.6; max-width: 300px; }
    .back {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      margin-top: 8px;
      font-size: 13px;
      color: #60a5fa;
      text-decoration: none;
      border: 1px solid #1e293b;
      border-radius: 8px;
      padding: 9px 20px;
    }
    .back:hover { border-color: #334155; }
    footer {
      padding: 18px 24px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .footer-logo { font-size: 13px; font-weight: 700; color: #334155; }
    .footer-logo .see { color: #1d4ed8; }
    .footer-link {
      display: flex;
      align-items: center;
      gap: 6px;
      font-size: 12px;
      color: #475569;
      text-decoration: none;
    }
    .footer-link:hover { color: #64748b; }
  </style>
</head>
<body>
  <main>
    <div class="icon">📬</div>
    <h1>Check your inbox</h1>
    <p>We sent a confirmation email. Click the link to activate your subscription.</p>
    <a class="back" href="/">&#8592; Subscribe more</a>
  </main>

  <footer>
    <span class="footer-logo">repo<span class="see">see</span>tory</span>
    <a class="footer-link" href="https://github.com/ananaslegend/reposeetory" target="_blank" rel="noopener">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="#475569"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>
      ananaslegend/reposeetory
    </a>
  </footer>
</body>
</html>
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/subscription/http/pages/...
```

Expected: all tests pass including `TestRenderer_Landing` and `TestRenderer_Subscribed`

- [ ] **Step 5: Commit**

```bash
git add internal/subscription/http/pages/pages.go \
        internal/subscription/http/pages/pages_test.go \
        internal/subscription/http/pages/templates/landing.html \
        internal/subscription/http/pages/templates/subscribed.html
git commit -m "feat: add Landing and Subscribed page renderer methods and templates"
```

---

### Task 3: Register routes in handler

**Files:**
- Modify: `internal/subscription/http/handler.go`

- [ ] **Step 1: Add handler methods and register routes**

In `internal/subscription/http/handler.go`, add two handler methods after the existing `Unsubscribe` method:

```go
func (h *Handler) Landing(w http.ResponseWriter, r *http.Request) {
	h.pages.Landing(w)
}

func (h *Handler) Subscribed(w http.ResponseWriter, r *http.Request) {
	h.pages.Subscribed(w)
}
```

In the `Register` method, add the two new routes **before** the existing `/api/` routes:

```go
func (h *Handler) Register(r chi.Router) {
	r.Get("/", h.Landing)
	r.Get("/subscribed", h.Subscribed)
	r.Post("/api/subscribe", h.Subscribe)
	r.Get("/api/confirm/{token}", h.Confirm)
	r.Get("/api/unsubscribe/{token}", h.Unsubscribe)
}
```

- [ ] **Step 2: Build to verify no compile errors**

```bash
go build ./...
```

Expected: exits with code 0, no output

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/subscription/http/handler.go
git commit -m "feat: register GET / and GET /subscribed routes for landing page"
```
