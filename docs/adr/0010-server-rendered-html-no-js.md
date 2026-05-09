# ADR-0010: Server-rendered HTML with embedded templates, no JS framework

Status: accepted · 2026-05-09 · @ananaslegend · http

## Context

The product surface is intentionally small: a landing page, a
"check your inbox" confirmation, and four status pages (`confirmed`,
`unsubscribed`, `unavailable`, `oops`). All are short HTML responses
to GET requests; the only interactive element is the subscribe form,
whose POST is handled by the JSON API.

Three frontend approaches were on the table:

1. **SPA (React / Vue / Svelte)** consuming a JSON API.
2. **Progressive enhancement (HTMX / Alpine.js)** — server HTML with
   small client-side interactions.
3. **Plain server-rendered HTML, no JS** — Go templates rendered
   directly to the response.

For ~6 pages with no interactivity beyond a form submit, options 1 and
2 add a build pipeline, more dependencies, and a runtime that every
visitor must download. Option 3 keeps the project a single Go binary.

## Decision

All HTML is rendered on the server using Go's `html/template`. No JS
framework, no external CSS framework, no client-side routing. The
renderer lives in `internal/subscription/http/pages/`:

- Templates embedded via `//go:embed templates/*` and parsed once at
  package init.
- `Renderer{}` is a zero-value-usable struct exposing one method per
  page: `Landing`, `Subscribed`, `Confirmed`, `Unsubscribed`,
  `Unavailable`, `Oops`.
- **Two parsing patterns** coexist:
  - *Standalone* (`landing.html`, `subscribed.html`) — unique
    branding; parsed via `template.ParseFS` and executed via
    `.Execute`.
  - *Base-extending* (`confirmed`, `unsubscribed`, `unavailable`,
    `oops`) — share the dark-hero `base.html`; parsed together and
    executed via `.ExecuteTemplate(w, "base", data)`.
- Visual identity is inlined: CSS in `<style>` tags, icons as inline
  SVG (Noto Color Emoji, Apache 2.0) with namespaced IDs to avoid
  collisions. No stylesheets, fonts, or CDNs are fetched at runtime.

## Consequences

- One binary ships everything — no separate frontend build, no asset
  pipeline, no CDN dependency.
- Pages load fast: no JS to download or execute, no hydration step.
- Accessibility by default: semantic HTML, no SPA routing weirdness.
- Server roundtrip on every navigation. Acceptable — traffic is low,
  pages are static, and a typical visit touches one or two of them.
- Changing a token (colour, font) requires touching every template
  that inlines CSS. Acceptable at this size; the shared `<style>`
  block in `base.html` already covers four of six pages.
- `POST /api/subscribe` stays JSON — the API layer is still a JSON
  HTTP API; only the GET pages are HTML. Mixing is intentional and
  reflects the two distinct audiences (programmatic clients vs.
  browsers).

## Links

- `internal/subscription/http/pages/` — `Renderer`, embedded templates,
  parsing logic.
- `internal/subscription/http/pages/templates/` — `base.html` plus
  per-page templates and inline SVG icons.