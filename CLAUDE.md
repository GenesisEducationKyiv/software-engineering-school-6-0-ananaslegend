# CLAUDE.md — reposeetory

GitHub Release Notification API. Користувачі підписуються на GitHub-репозиторій, отримують email-підтвердження, і потім — сповіщення про нові релізи.

## Стек

| Шар | Технологія |
|---|---|
| HTTP router | `go-chi/chi/v5` |
| PostgreSQL | `jackc/pgx/v5` (без ORM) |
| Config | `joho/godotenv` + `kelseyhightower/envconfig` |
| Logger | `rs/zerolog` |
| Mocks | `go.uber.org/mock` + `mockgen` |
| Migrations | `golang-migrate` CLI (окремо, не на старті бінарника) |
| Email | `github.com/wneessen/go-mail` (multipart HTML+text, STARTTLS/SSL/none) |
| Redis cache | `github.com/redis/go-redis/v9` (кешування GitHub releases) |
| Redis test | `github.com/alicebob/miniredis/v2` (in-process Redis для unit-тестів) |

## Архітектура папок

```
cmd/api/main.go                          # entrypoint
internal/
  config/config.go                       # Config struct + Load()
  logger/logger.go                       # zerolog init
  httpapi/                               # cross-cutting HTTP: router, middlewares, error writer
  storage/postgres/pool.go               # NewPool() → *pgxpool.Pool
  storage/redis/client.go                # NewClient() → *redis.Client (ping on start; pkg alias goredis)
  github/
    client.go                            # реальний GitHub GraphQL клієнт (batch release fetching)
    client_test.go                       # тести з httptest.Server
    stub.go                              # StubClient (для subscription service)
    caching_client.go                    # CachingReleaseProvider декоратор (Redis MGET + pipeline SET)
  transactor/
    transactor.go                        # Transactor інтерфейс, PgxTransactor, Conn інтерфейс, ConnFromContext
    mocks/mock_transactor.go             # mockgen
  scanner/                               # feature-модуль: перевірка нових релізів
    scanner.go                           # Scanner.Run/Tick; інтерфейси Repository, ReleaseProvider
    scanner_test.go
    mocks/                               # mockgen
    repository/db.go                     # pgx: SELECT FOR UPDATE SKIP LOCKED; conn() → ConnFromContext
  notifier/                              # feature-модуль: розсилка email із outbox
    notifier.go                          # Notifier.Run/Flush; інтерфейси Repository, MailSender
    notifier_test.go
    mocks/                               # mockgen
    repository/db.go                     # pgx: один запис за транзакцію; conn() → ConnFromContext
    emailer/                             # SMTPMailer + StubMailer (перенесено з mailer/)
  confirmer/                             # feature-модуль: розсилка confirm emails із outbox
    confirmer.go                         # Confirmer.Run/Flush; інтерфейси Repository, MailSender
    confirmer_test.go
    mocks/                               # mockgen
    repository/db.go                     # pgx: один запис за транзакцію; conn() → ConnFromContext
  mailer/
    stub.go                              # StubMailer (використовується якщо SMTP_HOST не задано)
    smtp.go                              # SMTPMailer (go-mail; активується через SMTP_HOST env)
    templates/
      confirmation.html / .txt           # шаблони підтвердження підписки
      release.html / .txt                # шаблони сповіщення про реліз
  subscription/                          # feature-модуль (vertical slice)
    domain/                              # моделі, помилки, токени — нуль залежностей від проекту
    http/                                # HTTP handlers (package http, аліас subhttp при імпорті)
      pages/                             # HTML-сторінки для браузерних GET-ендпоінтів
        templates/
          base.html                      # dark hero base (для confirmed/unsubscribed/unavailable/oops)
          landing.html                   # standalone: GET / — форма підписки
          subscribed.html                # standalone: GET /subscribed — success page
    service/                             # бізнес-логіка; тут живуть інтерфейси Repository/MailSender/RemoteRepositoryProvider
    repository/                          # pgx-реалізація персистенсу
migrations/
  000001_...                             # subscriptions, repositories
  000002_release_notifications.up.sql   # outbox-таблиця release_notifications
  000003_confirmation_notifications.up.sql # outbox-таблиця confirmation_notifications
Dockerfile                               # multi-stage: golang:1.26-alpine → alpine:3.21; -mod=vendor
docker-compose.yml                       # postgres, mailpit, migrate, api
```

**Правило:** кожна нова фіча — нова папка `internal/<feature>/` з тим самим шаблоном. `internal/httpapi/` залишається мінімальним (тільки cross-cutting HTTP).

## Ключові архітектурні рішення

### Інтерфейси по місцю використання
Інтерфейси визначаються в пакеті-споживачі, не в пакеті-продюсері.
- `service.Repository`, `service.RemoteRepositoryProvider` — у `internal/subscription/service/service.go`
- `http.SubscriptionService` — у `internal/subscription/http/handler.go`
- `notifier.Repository`, `notifier.MailSender` — у `internal/notifier/notifier.go`
- `confirmer.Repository`, `confirmer.MailSender` — у `internal/confirmer/confirmer.go`
- Пакети `github/` і `notifier/emailer/` містять тільки конкретні реалізації, без інтерфейсів.

### Param objects
Будь-яка exported функція, що викликається з іншого пакету і приймає більше 2 параметрів, замість цього приймає один struct.
Всі param-структури живуть у `internal/subscription/domain/model.go`.

```go
// правильно
func (r *Repository) CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)

// неправильно
func (r *Repository) CreateSubscription(ctx context.Context, email string, repoID int64, ...) (*domain.Subscription, error)
```

### Логування
Логер передається через context. У хендлерах і сервісах — тільки `zerolog.Ctx(ctx)`.

```go
zerolog.Ctx(ctx).Info().Str("email", p.Email).Int64("repo_id", repoID).Msg("subscription created")
```

`RequestLogger` middleware в `httpapi/middleware.go` інжектує request-scoped logger з полями `request_id`, `method`, `path` у кожен запит.

### Error wrapping

**Кожна помилка обгортається — включно з помилками з internal-пакетів.** Wrap message має містити: назву пакету, назву структури/receiver-типу (якщо це метод), назву функції/методу, де помилка виникла. Це дає повний шлях у логах без stack trace.

```go
// функція в пакеті
func runMigrations(...) error {
    if err != nil {
        return fmt.Errorf("app.runMigrations: iofs.New: %w", err)
    }
}

// метод на структурі
func (r *Repository) GetByConfirmToken(ctx context.Context, token string) (*domain.Subscription, error) {
    if err != nil {
        return nil, fmt.Errorf("subscription.Repository.GetByConfirmToken: %w", err)
    }
}

// service шар викликає repository — wrap-ить теж
func (s *Service) Confirm(ctx context.Context, token string) error {
    sub, err := s.repo.GetByConfirmToken(ctx, token)
    if err != nil {
        return fmt.Errorf("subscription.Service.Confirm: %w", err)
    }
}
```

**Винятки** (налаштовані у `.golangci.yml` через `wrapcheck.ignore-sigs` + `ignore-interface-regexps`):
- `fmt.Errorf`, `errors.New/Unwrap/Join` — не wrap-ити wrap.
- `transactor.Transactor.WithinTransaction` — callback вже wrap-ить, повторний wrap створить double prefix.

**Sentinel errors** (`domain.ErrTokenNotFound`, `ErrAlreadyExists` тощо) проходять наскрізь через `%w` — `errors.Is` у `httpapi/errors.go` бачить їх для error mapping → HTTP status.

`wrapcheck` налаштований так, щоб ловити **всі** unwrapped помилки, навіть з internal-пакетів — `ignore-package-globs` свідомо НЕ містить власний модуль.

### Мoki
Генеруються через `uber-go/mock`. Директива в `service.go`:
```go
//go:generate mockgen -source=service.go -destination=mocks/mock_interfaces.go -package=mocks
```

Після зміни будь-якого інтерфейсу в `service.go` — регенерувати:
```sh
go generate ./internal/subscription/service/...
```

### Колізія пакету `http`
`internal/subscription/http` — це Go package з іменем `http`, що тіньує `net/http`. При імпорті завжди використовувати аліас:
```go
subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
```

### Візуальна айдентика (dark hero)

Всі HTML-сторінки і email-шаблони дотримуються єдиного dark hero стилю:

| Токен | Значення | Використання |
|---|---|---|
| Background | `#0f172a` | Фон всіх сторінок і листів |
| Surface | `#1e293b` | Input поля, chips |
| Border | `#334155` | Межі input, subtle dividers |
| Text primary | `#ffffff` | Заголовки |
| Text secondary | `#94a3b8` | Підзаголовки, описи |
| Accent blue | `#60a5fa` | "see" у wordmark, посилання |
| CTA blue | `#2563eb` | Основна кнопка |

**Wordmark:** `reposeetory` — літери `see` виділені кольором `#60a5fa` з dotted underline (`text-decoration-style: dotted`, `text-underline-offset: 5px`, `thickness: 2px`).

**Слоган:** `Don't monitor GitHub. Just see the updates.`

**Footer** на кожній сторінці: wordmark + іконка GitHub + посилання `ananaslegend/reposeetory`.

**Іконки:** використовуються **Noto Color Emoji SVG** (Google, Apache 2.0) — вставляються **inline** прямо в HTML, без окремих файлів і без зовнішніх запитів. Брати SVG з `https://raw.githubusercontent.com/googlefonts/noto-emoji/main/svg/emoji_u{codepoint}.svg`.

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

**Правила підготовки SVG для inline:**
1. Видалити `<?xml version="1.0" encoding="utf-8"?>` declaration
2. Видалити `<!-- Generator: Adobe Illustrator... -->` коментар
3. Спростити `<svg>` tag — залишити лише `xmlns`, `xmlns:xlink` (якщо є `xlink:href`), `width`, `height`, `viewBox`, `aria-hidden="true"`
4. Видалити атрибути `version`, `x`, `y`, `style="enable-background:..."`, `xml:space`
5. Перейменувати gradient/clipPath IDs на унікальні (`noto-warn-a`, `noto-wave-b` тощо) — щоб уникнути колізій при кількох SVG на одній сторінці
6. Не використовувати emoji як текст — тільки SVG

**Правило:** будь-яка нова сторінка — темний фон, без білих карток, без роздільних ліній між секціями, без зовнішніх CSS-залежностей.

### HTML-сторінки для браузерних ендпоінтів
Рендерер — `internal/subscription/http/pages` (пакет `pages`), `Renderer{}` zero-value usable.
Шаблони вбудовані через `//go:embed templates/*`, парсяться при ініціалізації пакету.

Нові standalone шаблони (`landing.html`, `subscribed.html`) НЕ використовують `base.html` — парсяться як `template.ParseFS(templateFS, "templates/landing.html")` і виконуються через `.Execute`, не `.ExecuteTemplate`.

Шаблони на основі `base.html` (confirmed, unsubscribed, unavailable, oops) парсяться разом з ним і виконуються через `.ExecuteTemplate(w, "base", data)`.

| Метод | Тригер | HTTP статус |
|---|---|---|
| `Landing(w)` | `GET /` | 200 |
| `Subscribed(w)` | `GET /subscribed` | 200 |
| `Confirmed(w)` | успішний confirm | 200 |
| `Unsubscribed(w)` | успішний unsubscribe | 200 |
| `Unavailable(w, status)` | `ErrTokenNotFound` або `ErrTokenExpired` | 404 / 410 |
| `Oops(w, requestID)` | будь-яка інша помилка | 500 |

`Subscribe` (POST) залишається JSON — `writeError` / `WriteError` незмінні.

### Scanner та Notifier

**Scanner** (`internal/scanner/`) перевіряє нові релізи на GitHub батчем:
- `Scanner.Tick(ctx)` → `tx.WithinTransaction` → `GetRepositoriesWithLock(ctx, 100)` → один GraphQL запит на всі репо
- Для кожного репо: `UpsertLastSeen` викликається **завжди** (оновлює `last_seen_tag` і `last_checked_at = NOW()`), після чого `shouldNotify(repo, latestTag)` → якщо `repo.LastSeenTag != nil && latestTag != *repo.LastSeenTag` — вставляє рядки в `release_notifications`
- `shouldNotify` повертає `false` при першому скані (`LastSeenTag == nil`) — підписники не отримують нотифікацію за перший побачений тег
- `Repository` інтерфейс: `GetRepositoriesWithLock`, `UpsertLastSeen`, `InsertNotifications` — без `BumpCheckedAt`
- **Без `GITHUB_TOKEN` сканер не запускається** (GraphQL API повертає 403 без токена)
- `Scanner.GitHub` — `ReleaseProvider` інтерфейс; в `main.go` загортається в `CachingReleaseProvider` якщо `REDIS_URL` задано

**Notifier** (`internal/notifier/`) дренує outbox `release_notifications`:
- `Notifier.Flush(ctx)` — цикл: `ProcessNext` → один pending запис FOR UPDATE SKIP LOCKED → надсилає email → `sent_at = NOW()` → commit
- Один запис за транзакцію (без батч-обробки — trade-off не вартий)
- На помилці mailer → rollback, логує, повертає (retry на наступному тіку)
- `Config.BaseURL` (`APP_BASE_URL`) — використовується для побудови `UnsubscribeURL` у release email
- `PendingNotification` містить `UnsubscribeToken` (вибирається з `s.unsubscribe_token` в SQL)
- `domain.SendReleaseParams` містить `UnsubscribeURL` — передається в email шаблон

**Confirmer** (`internal/confirmer/`) дренує outbox `confirmation_notifications`:
- Ідентичний патерн до Notifier: `Confirmer.Flush(ctx)` → `ProcessNext` → FOR UPDATE SKIP LOCKED → `SendConfirmation` → `sent_at = NOW()` → commit
- `Subscribe()` більше не надсилає email синхронно — `CreateSubscription` вставляє рядок у `confirmation_notifications` в одній транзакції зі subscription row
- SQL фільтрує `WHERE s.confirm_token IS NOT NULL` — якщо підписка вже підтверджена (токен анульований), рядок пропускається
- `Config.BaseURL` → будує `ConfirmURL = baseURL + "/api/confirm/" + confirm_token`
- `PendingConfirmation` містить ID, Email, ConfirmToken, RepoOwner, RepoName

**Outbox таблиця** `release_notifications`:
```sql
id BIGSERIAL, subscription_id, repository_id, release_tag TEXT, created_at TIMESTAMPTZ, sent_at TIMESTAMPTZ
-- partial index: WHERE sent_at IS NULL
```

**Outbox таблиця** `confirmation_notifications`:
```sql
id BIGSERIAL, subscription_id REFERENCES subscriptions ON DELETE CASCADE, created_at TIMESTAMPTZ, sent_at TIMESTAMPTZ
-- partial index: WHERE sent_at IS NULL
```

**Транзакції через context:** керується пакетом `internal/transactor`. `PgxTransactor.WithinTransaction` записує `pgx.Tx` у контекст через приватний `dbKey{}`. Репозиторій отримує з'єднання через `transactor.ConnFromContext(ctx, pool)` — повертає tx з контексту або pool, якщо транзакції нема. Кожен репозиторій має однорядковий хелпер `conn(ctx) transactor.Conn`.

### Transactor — управління транзакціями

`internal/transactor/transactor.go` — єдиний пакет для транзакцій у всьому проєкті.

**Інтерфейси:**
```go
// Conn — спільний інтерфейс для *pgxpool.Pool і pgx.Tx
type Conn interface {
    Begin(ctx context.Context) (pgx.Tx, error)
    Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
    CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
    SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// Transactor — використовується в Config усіх feature-модулів
type Transactor interface {
    WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
```

**Патерн у репозиторіях** — кожен pgx-репозиторій має однорядковий хелпер:
```go
func (r *Repository) conn(ctx context.Context) transactor.Conn {
    return transactor.ConnFromContext(ctx, r.pool)
}
// Використання:
r.conn(ctx).Exec(ctx, `UPDATE ...`, args...)
r.conn(ctx).Query(ctx, `SELECT ...`, args...)
```

`ConnFromContext` повертає tx з контексту або `pool` якщо транзакції нема — репозиторій не знає, чи виконується в транзакції.

**Wiring:** єдиний `txr := transactor.New(pool)` у `main.go` передається у `scanner.Config`, `notifier.Config`, `confirmer.Config`.

### Subscription service — без MailSender
`internal/subscription/service/service.go` не має `MailSender` інтерфейсу і не надсилає email. Підтвердження тепер повністю асинхронне через outbox. Сервіс відповідає лише за валідацію, upsert репозиторію і `CreateSubscription` (яка атомарно створює і subscription, і confirmation outbox row).

### CachingReleaseProvider — Redis-кеш GitHub releases

`internal/github/caching_client.go` — декоратор над `ReleaseProvider`:
- Ключ: `github:release:{repoID}`, TTL: 10 хвилин
- Читання: `MGET` (один round-trip на всі репо)
- Запис: pipeline `SET` тільки для репо з тегом (без релізу — не кешуємо)
- Помилка Redis → `log.Warn` + silent fallback на wrapped provider, без помилки назовні
- Тип assertion з `vals[i].(string)` — через safe two-value form; при unexpected type → treat as miss
- Тести у `caching_client_test.go` через `miniredis` (in-process): cache miss, hit, partial, no-release, Redis error

Wiring у `main.go`: `var releaseProvider scanner.ReleaseProvider = githubClient` → якщо `REDIS_URL` задано → обгортається у `CachingReleaseProvider`. При помилці підключення → `log.Warn`, fallback на голий клієнт. `defer rdb.Close()` — тільки якщо підключенн�� успішне.

`internal/storage/redis/client.go`: package `redis`, але import аліасується як `goredis` (щоб уникнути collision з назвою пакету). В `main.go` — `redisstorage "github.com/ananaslegend/reposeetory/internal/storage/redis"`.

### main.go — fullMailer
У `cmd/api/main.go` оголошений локальний unexported інтерфейс:
```go
type fullMailer interface {
    notifier.MailSender
    confirmer.MailSender
}
```
Змінна `mailSender fullMailer` передається і в `notifier.Config`, і в `confirmer.Config`. Конкретні реалізації (SMTPMailer, ResendMailer, StubMailer) реалізують обидва методи.

### SMTP Mailer
`internal/mailer/smtp.go` — `SMTPMailer`, використовує `github.com/wneessen/go-mail`.
Надсилає multipart/alternative (HTML + plain-text) через `SetBodyHTMLTemplate` + `AddAlternativeTextTemplate`.
TLS-політика конфігурується через `SMTP_TLS_POLICY`: `starttls` (default) → `TLSOpportunistic`, `ssl` → `TLSMandatory`, `none` → `NoTLS`.
Якщо `SMTP_HOST` порожній — `main.go` використовує `StubMailer`.

### Swagger документація

Використовуємо `swaggo/swag` для генерації OpenAPI 2.0 специфікації.

**Інструменти:**
| Пакет | Роль |
|---|---|
| `github.com/swaggo/swag` | CLI + анотації для генерації |
| `github.com/swaggo/http-swagger/v2` | Middleware для Swagger UI (`GET /swagger/*`) |
| `docs/` | Згенерований пакет — **не редагувати вручну** |

**Загальні анотації** — у `cmd/api/main.go` (рядок 1, `//go:generate`):
```go
//go:generate swag init -g cmd/api/main.go -o docs --parseDependency

// @title        Reposeetory API
// @version      1.0
// @description  GitHub Release Notification API.
// @host         reposeetory.com
// @BasePath     /
```

**Анотації хендлерів** — безпосередньо перед методом, у файлі хендлера (`internal/<feature>/http/handler.go`):
```go
// Subscribe godoc
//
//	@Summary     Subscribe to a repository
//	@Description Subscribe to GitHub repository release notifications.
//	@Tags        subscriptions
//	@Accept      json
//	@Produce     json
//	@Param       body  body      SubscribeRequest  true  "Subscription request"
//	@Success     202   {object}  StatusResponse
//	@Failure     400   {object}  ErrorResponse
//	@Router      /api/subscribe [post]
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
```

**DTO** — у `internal/<feature>/http/dto.go`; теги `example:` обов'язкові для полів запиту/відповіді:
```go
type SubscribeRequest struct {
    Email      string `json:"email"      example:"user@example.com"`
    Repository string `json:"repository" example:"ananaslegend/reposeetory"`
}
```

**Генерація:**
```sh
make swagger   # swag init -g cmd/api/main.go -o docs --parseDependency
# або
go generate ./cmd/api/...
```

**Підключення UI** — в `internal/httpapi/router.go`:
```go
import (
    _ "github.com/ananaslegend/reposeetory/docs"   // side-effect: реєструє специфікацію
    httpswagger "github.com/swaggo/http-swagger/v2"
)

r.Get("/swagger/*", httpswagger.Handler())
```

**Правила:**
- `docs/` — лише згенерований код, не редагувати вручну; додано в git.
- Після зміни будь-якого хендлера або DTO — перегенерувати: `make swagger`.
- Для HTML-ендпоінтів (`GET /`, confirm, unsubscribe): `@Produce html`, без `{object}` у `@Success`.
- Теги (`@Tags`) групують ендпоінти в UI: `pages` для браузерних сторінок, `subscriptions` для API.

### Docker / локальне оточення
`make docker-up` піднімає повний стек: postgres → migrate → api + mailpit (:8025) + redis (:6379).
Залежності вендоруються (`go mod vendor`) і білд у Docker використовує `-mod=vendor` — обхід корпоративного SSL-проксі, який перехоплює TLS всередині контейнера.
Не використовувати `apk add` у runtime-стейджі з тієї ж причини; CA-сертифікати копіюються з builder-стейджу.

## HTTP API

| Метод | Шлях | Опис |
|---|---|---|
| GET | `/` | Landing page (форма підписки) |
| GET | `/subscribed` | Success page ("Check your inbox") |
| POST | `/api/subscribe` | Підписатись; повертає 202 + надсилає email-підтвердження |
| GET | `/api/confirm/{token}` | Підтвердити підписку (токен одноразовий, TTL 24h) |
| GET | `/api/unsubscribe/{token}` | Відписатись (hard delete, GDPR) |

### Error mapping (`httpapi/errors.go`)
| Помилка | HTTP status |
|---|---|
| `ErrInvalidRepoFormat`, bad email/JSON | 400 |
| `ErrRepoNotFound`, `ErrTokenNotFound` | 404 |
| `ErrAlreadyExists` | 409 |
| `ErrTokenExpired` | 410 |
| default | 500 |

## Конфігурація (env vars)

| Змінна | Default | Опис |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Адреса HTTP сервера |
| `HTTP_READ_TIMEOUT` | `10s` | |
| `HTTP_WRITE_TIMEOUT` | `10s` | |
| `HTTP_SHUTDOWN_TIMEOUT` | `15s` | |
| `DATABASE_URL` | (required) | pgx connection string |
| `DB_MAX_CONNS` | `10` | pgxpool max connections |
| `APP_BASE_URL` | `http://localhost:8080` | Для побудови confirm/unsubscribe URLs |
| `CONFIRM_TOKEN_TTL` | `24h` | TTL підтверджувального токена |
| `LOG_LEVEL` | `info` | trace/debug/info/warn/error |
| `LOG_PRETTY` | `true` | true=console, false=JSON |
| `SMTP_HOST` | `` | Якщо порожній — StubMailer; інакше — SMTPMailer |
| `SMTP_PORT` | `587` | |
| `SMTP_USER` | `` | |
| `SMTP_PASS` | `` | |
| `SMTP_FROM` | `` | From-адреса листа (обов'язковий якщо SMTP_HOST задано) |
| `SMTP_TLS_POLICY` | `starttls` | `starttls` / `ssl` / `none` |
| `GITHUB_TOKEN` | `` | **Обов'язковий для сканера** — GraphQL API повертає 403 без токена; якщо порожній — сканер не запускається (лог `WARN`) |
| `SCANNER_INTERVAL` | `5m` | Інтервал між тіками сканера |
| `NOTIFIER_INTERVAL` | `30s` | Інтервал між тіками нотифікатора |
| `CONFIRMER_INTERVAL` | `30s` | Інтервал між тіками confirmer'а |
| `REDIS_URL` | `` | Redis connection URL; якщо порожній — GitHub кеш вимкнено (log `INFO`) |

Скопіювати `.env.example` → `.env` перед першим запуском.

## Команди

```sh
make build              # go build -o bin/api ./cmd/api
make run                # go run ./cmd/api
make test               # go test ./...
make vet                # go vet ./...
make generate           # go generate ./...
make lint               # golangci-lint run ./...
make lint-install       # install pinned golangci-lint version
make lint-fix           # go fix + golangci-lint run --fix
make tidy               # go mod tidy + go mod vendor (атомарно)
make mod-update         # go get -u ./... + tidy + vendor
make mod-update-patch   # go get -u=patch ./... + tidy + vendor
make migrate-up         # apply all migrations
make migrate-down       # rollback last migration
make clean              # rm -rf bin/
make docker-up          # docker compose up --build -d
make docker-down        # docker compose down
make docker-clean       # docker compose down -v (видаляє volumes)
```

`DB_URL` в Makefile можна перевизначити: `make migrate-up DB_URL=postgres://...`

## Dependency updates

Dependabot (`.github/dependabot.yml`) створює PR щотижня (понеділок 06:00 Kyiv) для двох ekosystem-ів:
- `gomod` — групує всі minor/patch у один PR (`go-minor-patch`); major-релізи окремо.
- `github-actions` — оновлення версій actions у workflow.

**Cooldown:** 7 днів для всіх релізів, 30 днів для semver-major. Захист від supply-chain compromises (malicious release зазвичай yanked-ять за 24-48h).

**Vendor sync на Dependabot PR:** Dependabot оновлює `go.mod`/`go.sum`, але **не** перегенерує `vendor/`. CI має step `Verify vendor is in sync` — він впаде на Dependabot PR. Workflow:

```sh
gh pr checkout <PR-number>   # checkout локально
make tidy                    # go mod tidy + go mod vendor
git add go.mod go.sum vendor/
git commit -m "deps: sync vendor"
git push
```

Після цього CI пройде. Альтернативно — auto-revendor workflow (не реалізовано — security trade-off з push-permissions на Dependabot branches).

## Тести

Unit-тести на service layer з mock-залежностями (`uber-go/mock`). Integration-тести з реальною БД — **не mock-ати postgres**.

```sh
go test ./internal/subscription/service/...
```

### Prometheus metrics

`internal/storage/postgres/collector.go` — `PoolCollector` реалізує `prometheus.Collector` поверх `pgxpool.Stat()`.

Єдиний `*prometheus.Registry` створюється в `main.go` і передається через `Config`-структури в усі компоненти. Nil-safe патерн: якщо `Registry == nil` — метрика створюється, але не реєструється (існуючі тести не ламаються).

```go
// main.go
reg := prometheus.NewRegistry()
reg.MustRegister(
    prometheus.NewGoCollector(),
    prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
    postgres.NewPoolCollector(pool),
)
// далі Registry: reg передається в scanner.Config, notifier.Config, confirmer.Config, service.Config, RouterConfig
```

**Метрики по компонентах:**

| Компонент | Метрика | Тип | Labels |
|---|---|---|---|
| HTTP | `http_requests_total` | counter | `method`, `path`, `status` |
| HTTP | `http_request_duration_seconds` | histogram | `method`, `path` |
| Scanner | `scanner_ticks_total` | counter | `result` |
| Scanner | `scanner_repos_scanned_total` | counter | — |
| Notifier | `notifier_emails_sent_total` | counter | `result` |
| Notifier | `notifier_flush_duration_seconds` | histogram | — |
| Confirmer | `confirmer_emails_sent_total` | counter | `result` |
| GitHub cache | `github_cache_hits_total` | counter | — |
| GitHub cache | `github_cache_misses_total` | counter | — |
| GitHub cache | `github_cache_errors_total` | counter | — |
| Service | `subscriptions_created_total` | counter | — |
| Service | `subscriptions_confirmed_total` | counter | — |
| Service | `subscriptions_deleted_total` | counter | — |
| DB pool | `db_pool_acquired_conns` | gauge | — |
| DB pool | `db_pool_idle_conns` | gauge | — |
| DB pool | `db_pool_total_conns` | gauge | — |

**HTTP middleware** (`internal/httpapi/metrics.go`): `/metrics` шлях не трекається; chi route pattern (`chi.RouteContext(r.Context()).RoutePattern()`) використовується для `path` label — низька кардинальність.

**Ендпоінт:** `GET /metrics` — `promhttp.HandlerFor(reg, promhttp.HandlerOpts{})`, підключено в `RouterConfig`.

**Тести:** `testutil.GatherAndCompare` з `prometheus/testutil` — у `scanner_test.go`, `notifier_test.go`, `confirmer_test.go`, `service_test.go`, `caching_client_test.go`.

**Пакет `collectors`** не вендорується окремо — `NewGoCollector` і `NewProcessCollector` беруться напряму з пакету `prometheus` (deprecated warning, але функціонально коректно).

## Поза обсягом (майбутні таски)

- `GET /api/subscriptions?email=` — список підписок
- CI (GitHub Actions)
- Rate limiting на `/subscribe`
- API key auth
