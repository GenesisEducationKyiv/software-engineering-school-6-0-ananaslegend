# Brand Design System — reposeetory

Єдина візуальна айдентика для всіх HTML-сторінок і email-шаблонів. Стиль — **dark hero**: темний фон без білих карток, без роздільних ліній між секціями, без зовнішніх CSS-залежностей.

## Кольорові токени

| Токен | Значення | Використання |
|---|---|---|
| Background | `#0f172a` | Фон всіх сторінок і листів |
| Surface | `#1e293b` | Input поля, chips |
| Border | `#334155` | Межі input, subtle dividers |
| Text primary | `#ffffff` | Заголовки |
| Text secondary | `#94a3b8` | Підзаголовки, описи |
| Accent blue | `#60a5fa` | "see" у wordmark, посилання |
| CTA blue | `#2563eb` | Основна кнопка |

## Wordmark

`reposeetory` — літери `see` виділені кольором `#60a5fa` з dotted underline:
- `text-decoration-style: dotted`
- `text-underline-offset: 5px`
- `text-decoration-thickness: 2px`

## Слоган

`Don't monitor GitHub. Just see the updates.`

## Footer

На кожній сторінці: wordmark + іконка GitHub + посилання `ananaslegend/reposeetory`.

## Іконки — Noto Color Emoji SVG

Використовуються **Noto Color Emoji SVG** (Google, Apache 2.0) — вставляються **inline** прямо в HTML, без окремих файлів і без зовнішніх запитів.

Джерело: `https://raw.githubusercontent.com/googlefonts/noto-emoji/main/svg/emoji_u{codepoint}.svg`.

| Сторінка | Emoji | Codepoint | Призначення |
|---|---|---|---|
| `landing.html` (step 1) | 📝 | `1f4dd` | Subscribe |
| `landing.html` (step 2) | ✉️ | `2709` | Confirm |
| `landing.html` (step 3) | 🔔 | `1f514` | Get notified |
| `landing.html` (BMC button) | ❤️ | `2764` | Support heart |
| `subscribed.html` | 📬 | `1f4ec` | Check your inbox |
| `confirmed.html` | ✅ | `2705` | Subscription confirmed |
| `unsubscribed.html` | 👋 | `1f44b` | Unsubscribed |
| `unavailable.html` | ⏰ | `23f0` | Link unavailable |
| `oops.html` | ⚠️ | `26a0` | Something went wrong |

### Правила підготовки SVG для inline

1. Видалити `<?xml version="1.0" encoding="utf-8"?>` declaration.
2. Видалити `<!-- Generator: Adobe Illustrator... -->` коментар.
3. Спростити `<svg>` tag — залишити лише `xmlns`, `xmlns:xlink` (якщо є `xlink:href`), `width`, `height`, `viewBox`, `aria-hidden="true"`.
4. Видалити атрибути `version`, `x`, `y`, `style="enable-background:..."`, `xml:space`.
5. Перейменувати gradient/clipPath IDs на унікальні (`noto-warn-a`, `noto-wave-b` тощо) — щоб уникнути колізій при кількох SVG на одній сторінці.
6. Не використовувати emoji як текст — тільки SVG.

## Правило для нових сторінок

Будь-яка нова сторінка — темний фон, без білих карток, без роздільних ліній між секціями, без зовнішніх CSS-залежностей.
