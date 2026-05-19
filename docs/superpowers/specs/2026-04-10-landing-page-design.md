# Landing Page Design

**Date:** 2026-04-10  
**Status:** Approved

## Overview

A dark-themed landing page for reposeetory — a GitHub release notification service. Users enter a repository and their email, subscribe, and receive email notifications on every new release.

The landing page establishes a new visual identity: full dark (`#0f172a`) with no dividers, consistent across all new pages. Existing confirm/unsubscribe pages and email templates will be migrated to this style in a separate task.

---

## Visual Identity

**Color palette:**
| Token | Value | Usage |
|---|---|---|
| Background | `#0f172a` | Page background |
| Surface | `#1e293b` | Input fields, chips |
| Border | `#334155` | Input borders, subtle dividers |
| Text primary | `#ffffff` | Headings |
| Text secondary | `#94a3b8` | Subtext, descriptions |
| Text muted | `#64748b` | Placeholder, step descriptions |
| Accent blue | `#60a5fa` | "see" highlight, links |
| CTA blue | `#2563eb` | Primary button |
| Footer text | `#475569` | Footer links |
| Footer logo | `#334155` / `#1d4ed8` | Logo in footer |

**Typography:** `system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`

**Wordmark branding:** The letters `see` inside `reposeetory` are colored `#60a5fa` with a dotted underline (`text-decoration-style: dotted`, `text-underline-offset: 4px`, `thickness: 2px`).

**Design rules:**
- No horizontal dividers anywhere on the page
- No external CSS dependencies — all styles inline or in `<style>` block
- Minimal inline JS only for form submission

---

## Pages

### 1. Landing page — `GET /`

**Sections (top to bottom, seamlessly connected):**

#### Hero
- Wordmark: `reposeetory` with branded `see`
- Slogan: `Don't monitor GitHub. Just see the updates.` — the word `see` colored `#60a5fa`
- Form:
  - Input 1: placeholder `owner/repository`
  - Input 2: placeholder `your@email.com`
  - Button: `Watch repository →` (background `#2563eb`)
- Social proof chips: hardcoded list of popular repos (e.g. `axios/axios`, `golang/go`, `tailwindcss/tailwindcss`, `vercel/next.js`) displayed as pill tags below the button

#### How it works
- Section label: `HOW IT WORKS` (uppercase, muted, letter-spaced)
- Three steps with emoji icons and dashed connectors between them:
  1. 📝 **Subscribe** — Enter a repo and your email
  2. ✉️ **Confirm** — Click the link in your inbox
  3. 🔔 **Get notified** — Email on every new release

#### Footer
- Left: wordmark `reposeetory` (muted tones)
- Right: GitHub icon + `ananaslegend/reposeetory` link

---

### 2. Success page — `GET /subscribed`

Shown after successful form submission. Same dark background and footer as the landing page.

**Content (centered, full viewport height):**
- Icon: 📬 (large)
- Heading: `Check your inbox`
- Body: `We sent a confirmation email. Click the link to activate your subscription.`
- Link/button: `← Subscribe more` — outlined, returns to `GET /`

**Footer:** identical to landing page footer

---

## Form Behavior

**Submission:** Minimal inline JS performs a `fetch` POST to `/api/subscribe` with `Content-Type: application/json`.

**Success:** `HTTP 202` → JS redirects to `/subscribed`.

**Errors:** Inline error message displayed below the button (no page reload):
| API response | Message shown |
|---|---|
| 400 (bad repo/email format) | `Invalid repository or email format.` |
| 404 (repo not found on GitHub) | `Repository not found on GitHub.` |
| 409 (already subscribed) | `This email is already subscribed to that repository.` |
| 5xx / network error | `Something went wrong. Please try again.` |

---

## Implementation Notes

**File locations (following project conventions):**

- Landing page template: `internal/subscription/http/pages/templates/landing.html`
- Success page template: `internal/subscription/http/pages/templates/subscribed.html`
- Page renderer methods added to `internal/subscription/http/pages/pages.go`
- Routes registered in `internal/subscription/http/handler.go` (or router setup):
  - `GET /` → `pages.Landing(w)`
  - `GET /subscribed` → `pages.Subscribed(w)`

**No new base template:** both pages are standalone full-document templates (dark style differs from existing `base.html` white card style). Existing `base.html` is left untouched for now.

**No new backend endpoint:** the form POSTs to the existing `/api/subscribe` JSON endpoint.
