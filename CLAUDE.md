# CLAUDE.md — reposeetory

GitHub Release Notification API. Користувач підписується на репозиторій, отримує email-підтвердження, далі — лист на кожен новий тег. Два Go-бінарники: монолiт `cmd/api` (HTTP API + scanner + outbox-дрейнери, Postgres, опціональний Redis-кеш) і stateless сервіс нотифікацій `cmd/notifications-svc` (Resend/SMTP + шаблони; email — поточний канал доставки) за HTTP-контрактом.

Архітектура й мотивація рішень зафіксовані в [`docs/adr/`](docs/adr/README.md) і [`docs/architecture.md`](docs/architecture.md); візуальна айдентика — у [`docs/brand-design-system.md`](docs/brand-design-system.md). Тут — стисла навігація.

## Архітектурні рішення (ADR)

- **Screaming architecture** — кожна фіча у `internal/<feature>/` (`domain/http/service/repository`). Cross-cutting код — лише в `internal/app/` (composition root) і `internal/httpapi/`. → [ADR-0001](docs/adr/0001-screaming-architecture.md)

- **Consumer-side інтерфейси** — інтерфейси живуть у пакеті, що **використовує** їх; пакети-реалізації експортують лише конкретні типи. → [ADR-0002](docs/adr/0002-consumer-side-interfaces.md)

- **Async email через outbox** — `confirmation_notifications` і `release_notifications` пишуться в одній транзакції з бізнес-подією; драйнери `internal/confirmer/` і `internal/notifier/` беруть рядки через `FOR UPDATE SKIP LOCKED`, надсилають, ставлять `sent_at = NOW()`. → [ADR-0003](docs/adr/0003-async-email-outbox.md)

- **Транзакції через context (`Transactor`)** — `Transactor.WithinTransaction(ctx, fn)` ховає `pgx.Tx` у `ctx` під приватним ключем; репозиторії беруть з'єднання через `transactor.ConnFromContext(ctx, pool)` і не знають, чи всередині транзакції. → [ADR-0004](docs/adr/0004-transactor-via-context.md)

- **Error wrapping convention** — кожна помилка через межу функції обгортається `fmt.Errorf("pkg.Struct.Method: %w", err)` (метод) або `"pkg.funcName: %w"` (free function); sentinel-помилки проходять через `%w` для `errors.Is`. Винятки — у `.golangci.yml` (`wrapcheck.ignore-sigs`). → [ADR-0005](docs/adr/0005-error-wrapping-convention.md)

- **Logger через context** — `zerolog.Ctx(ctx)` усюди; `RequestLogger` middleware інжектує `request_id/method/path` у кожен запит. → [ADR-0006](docs/adr/0006-logger-via-context.md)

- **Без vendoring, Dependabot із cooldown** — `vendor/` не комітимо; CI і Docker білдять через стандартну module resolution із кешем `proxy.golang.org`. Supply-chain захист — Dependabot щотижня з 7-денним cooldown (30 днів для semver-major). `make tidy` — це просто `go mod tidy`. → [ADR-0014](docs/adr/0014-drop-vendoring.md) (раніше [ADR-0007](docs/adr/0007-vendor-and-dependency-updates.md))

- **GitHub GraphQL batch fetch** — один GraphQL-запит на тік із field-aliases по всіх репо. Redis-декоратор `CachingReleaseProvider` (TTL 10 хв, MGET на read, pipeline SET на write, silent fallback при помилці Redis). → [ADR-0008](docs/adr/0008-github-graphql-batch-fetch.md)

- **Swagger з анотацій (`swaggo/swag`)** — анотації в коментарях хендлерів, теги `example:` на полях DTO; `docs/` — згенерований пакет, не редагувати руками; `make swagger` після зміни хендлера або DTO. → [ADR-0009](docs/adr/0009-swagger-from-annotations.md)

- **Server-rendered HTML, без JS** — `html/template` + `//go:embed`; рендерер у `internal/subscription/http/pages`. Жодного JS-білда, жодних зовнішніх CSS-залежностей. → [ADR-0010](docs/adr/0010-server-rendered-html-no-js.md)

- **Стратегія тестів** — мокати consumer-side інтерфейси; реальний Postgres (testcontainers) і `miniredis` для stateful шарів; Prometheus assertions через `testutil.GatherAndCompare`. Не мокати БД. → [ADR-0011](docs/adr/0011-testing-strategy.md)

- **Хостинг на Railway** — git-deploy через `Dockerfile`, Postgres/Redis як managed plugins, `DATABASE_URL` інжектується платформою. Виключає SMTP (порти 25/465/587 заблоковані на мережному рівні). → [ADR-0012](docs/adr/0012-host-on-railway.md)

- **Resend як email-провайдер** — `internal/notifications/email/resend.go` поверх `resend-go/v2`. SMTP-мейлер залишений для локальної розробки через mailpit. → [ADR-0013](docs/adr/0013-resend-email-provider.md)

Брендова система (палітра dark hero, wordmark, Noto Emoji inline) — див. [`docs/brand-design-system.md`](docs/brand-design-system.md).

## Конвенції поза ADR

### Param objects
Будь-яка exported функція, що викликається з іншого пакету і приймає більше 2 параметрів, замість цього приймає один struct. Усі param-структури живуть у `internal/subscription/domain/model.go`.

```go
// правильно
func (r *Repository) CreateSubscription(ctx context.Context, p domain.CreateSubscriptionParams) (*domain.Subscription, error)

// неправильно
func (r *Repository) CreateSubscription(ctx context.Context, email string, repoID int64, ...) (*domain.Subscription, error)
```

### Prometheus Registry-as-dependency (підхід B)
Єдиний `*prometheus.Registry` створюється в `main.go` і передається через `Config`-структури в усі компоненти (`scanner.Config`, `notifier.Config`, `confirmer.Config`, `service.Config`, `RouterConfig`). Без глобального стану, без `promauto`.

**Nil-safe патерн:** якщо `Config.Registry == nil` — метрика створюється, але не реєструється. Тести, що не передають Registry, не ламаються.

### Колізія пакету `http` → аліас `subhttp`
`internal/subscription/http` — Go-пакет з іменем `http`, що тіньовить `net/http`. При імпорті завжди:

```go
subhttp "github.com/ananaslegend/reposeetory/internal/subscription/http"
```

Без аліаса збірка ламається з повідомленнями, які не вказують на причину.

### Одна реалізація на кілька consumer-side інтерфейсів
Коли кілька feature-модулів ділять одну залежність, її конкретна реалізація задовольняє consumer-side інтерфейс кожного з них напряму — без штучного "shared"-пакета чи композитного інтерфейсу. Приклад: `*client.Client` (`internal/notifications/client`) реалізує і `notifier.NotificationsSender`, і `confirmer.NotificationsSender`, і передається в обидва дрейнери у `internal/app/workers.go` ([ADR-0002](docs/adr/0002-consumer-side-interfaces.md)).

### Сервіс нотифікацій (`cmd/notifications-svc`)
Доставка нотифікацій винесена в окремий stateless HTTP-сервіс. Межі:
- **Монолiт** володіє outbox-таблицями і дрейнерами (`notifier`/`confirmer`), будує URL-и з `APP_BASE_URL` і шле запити через клієнтську бібліотеку `internal/notifications/client`.
- **Сервіс** (`cmd/notifications-svc`, пакети `internal/notifications/{contract,transport,email}`) володіє провайдерами (Resend/SMTP) і шаблонами; email — поточний канал (`internal/notifications/email`, інтерфейс `Sender`). Endpoint-и: `POST /v1/notifications/release`, `/v1/notifications/confirmation`.
- **Контракт + клієнт**: спільні DTO + `ErrPermanent` у `internal/notifications/contract`; клієнтська бібліотека для консюмерів — `internal/notifications/client`. Семантика доставки: `2xx`→`sent_at`; `4xx`→permanent (дроп poison-рядка); `5xx`/timeout→transient (ретрай через outbox).

## Команди

```sh
make build / run / test / vet / lint / lint-fix
make tidy            # go mod tidy
make generate        # go generate ./... (мoki, swagger)
make swagger         # перегенерувати docs/ зі swaggo
make migrate-up / migrate-down
make docker-up / docker-down / docker-clean
```

## Конфігурація

Скопіювати `.env.example` → `.env`.

**Монолiт (`cmd/api`):** `DATABASE_URL`, `APP_BASE_URL`, `NOTIFICATIONS_URL` (адреса сервісу нотифікацій), `GITHUB_TOKEN` (обов'язковий для сканера — без нього `WARN` і scanner не стартує), `REDIS_URL` (опціональний кеш).

**Сервіс нотифікацій (`cmd/notifications-svc`):** `NOTIFICATIONS_HTTP_ADDR`, `RESEND_API_KEY` або `SMTP_HOST` + `SMTP_*` (Resend → SMTP → Stub). Ці змінні споживає лише сервіс, не монолiт.