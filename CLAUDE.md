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

## Архітектура папок

```
cmd/api/main.go                          # entrypoint
internal/
  config/config.go                       # Config struct + Load()
  logger/logger.go                       # zerolog init
  httpapi/                               # cross-cutting HTTP: router, middlewares, error writer
  storage/postgres/pool.go               # NewPool() → *pgxpool.Pool
  github/stub.go                         # StubClient (реальний клієнт — майбутній таск)
  mailer/stub.go                         # StubMailer (реальний SMTP — майбутній таск)
  subscription/                          # feature-модуль (vertical slice)
    domain/                              # моделі, помилки, токени — нуль залежностей від проекту
    http/                                # HTTP handlers (package http, аліас subhttp при імпорті)
    service/                             # бізнес-логіка; тут живуть інтерфейси Repository/MailSender/RemoteRepositoryProvider
    repository/                          # pgx-реалізація персистенсу
migrations/
```

**Правило:** кожна нова фіча — нова папка `internal/<feature>/` з тим самим шаблоном. `internal/httpapi/` залишається мінімальним (тільки cross-cutting HTTP).

## Ключові архітектурні рішення

### Інтерфейси по місцю використання
Інтерфейси визначаються в пакеті-споживачі, не в пакеті-продюсері.
- `service.Repository`, `service.RemoteRepositoryProvider`, `service.MailSender` — у `internal/subscription/service/service.go`
- `http.SubscriptionService` — у `internal/subscription/http/handler.go`
- Пакети `github/` і `mailer/` містять тільки конкретні реалізації (stubs зараз), без інтерфейсів.

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

## HTTP API

| Метод | Шлях | Опис |
|---|---|---|
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

Скопіювати `.env.example` → `.env` перед першим запуском.

## Команди

```sh
make build         # go build -o bin/api ./cmd/api
make run           # go run ./cmd/api
make test          # go test ./...
make vet           # go vet ./...
make generate      # go generate ./...
make lint          # golangci-lint run ./...
make tidy          # go mod tidy
make migrate-up    # apply all migrations
make migrate-down  # rollback last migration
make clean         # rm -rf bin/
```

`DB_URL` в Makefile можна перевизначити: `make migrate-up DB_URL=postgres://...`

## Тести

Unit-тести на service layer з mock-залежностями (`uber-go/mock`). Integration-тести з реальною БД — **не mock-ати postgres**.

```sh
go test ./internal/subscription/service/...
```

## Поза обсягом (майбутні таски)

- `GET /api/subscriptions?email=` — список підписок
- Scanner: перевіряє нові релізи на GitHub (real `github.Client`)
- Notifier: розсилає email про нові релізи (real SMTP mailer)
- docker-compose / Dockerfile / CI
- Prometheus metrics
- Rate limiting на `/subscribe`
- API key auth
