# Database Schema Design

Документ описує схему бази даних для GitHub Release Notification API,
мотивацію кожного рішення і компроміси, які ми свідомо прийняли.

## Контекст

Сервіс дозволяє підписатись на email-нотифікації про нові релізи GitHub-репозиторіїв.
Основні операції: підписка (з double opt-in), підтвердження email, відписка, отримання
списку підписок. Фоновий Scanner регулярно опитує GitHub API і через Notifier шле листи.

Стек: Go, PostgreSQL, `golang-migrate/migrate` для міграцій.

## Рішення

### 1. Нормалізована модель (дві таблиці)

Ми обрали окрему таблицю `repositories` замість зберігання `owner/name` безпосередньо
в `subscriptions`.

**Мотивація:** `last_seen_tag` — стан репозиторію, а не підписки. Якщо 1000 людей
підписані на `torvalds/linux`, при денормалізованій схемі Scanner мав би оновлювати
1000 рядків при кожному скані. З нормалізованою — один рядок. Крім того, нормалізація
дає природне місце для `last_checked_at`, яке Scanner використовує для планування
черги перевірок.

### 2. Два токени: `confirm_token` і `unsubscribe_token`

Ми розглянули варіант одного токена на обидві операції і відкинули його.

**Чому не один токен:**
- Різний lifecycle: confirm одноразовий (згоряє після кліку), unsubscribe постійний
  (лінк має працювати через рік з архівного листа).
- Конфлікт TTL: confirm повинен протухати через 24h (безпека double opt-in),
  unsubscribe — ніколи.
- Email-клієнти (Outlook, антивіруси) роблять автоматичний GET по лінках для
  перевірки на фішинг. З одним токеном це може випадково підтвердити або відписати
  користувача без його відома.

**Підсумок:** `confirm_token` (nullable, одноразовий, TTL 24h через
`confirm_token_expires_at`) і `unsubscribe_token` (NOT NULL, постійний, унікальний).

### 3. Обидва токени зберігаються в PostgreSQL

Під час брейнсторму ми розглянули варіант зберігати `confirm_token` виключно в Redis
(нативний TTL через `EXPIRE`, атомарне one-shot через `GETDEL`). Ідея приваблива, але
відхилена на старті з міркувань надійності.

**Мотивація:** Redis тримає дані в пам'яті. Без AOF persistence перезапуск контейнера
знищує всі pending-токени — користувачі отримали листа з лінком, клікають, а сервіс
відповідає 404. Для 24-годинного вікна підтвердження це реальна проблема. PG дає
durability з коробки. Redis-оптимізацію можна додати пізніше, коли Redis все одно
з'явиться в стеку для кешування GitHub API.

### 4. Hard delete при відписці

При `GET /api/unsubscribe/{token}` ми робимо `DELETE FROM subscriptions`, а не
переводимо запис у стан `unsubscribed`.

**Мотивація:** GDPR ("право бути забутим") — email видаляється одразу. Простіша
схема без поля `status` і без ризику забути фільтр `WHERE status = 'active'` у
якомусь запиті. Якщо знадобляться метрики відписок — Prometheus counter
`unsubscribes_total` дає їх без зберігання PII в БД.

### 5. `citext` для email

Email зберігається як `CITEXT` (case-insensitive text), а не звичайний `TEXT`.

**Мотивація:** `vasya@example.com` і `Vasya@Example.com` — одна й та сама адреса,
але `TEXT` + `UNIQUE` constraint пропустить обидва як різні рядки. `CITEXT` робить
усі порівняння і UNIQUE constraint автоматично case-insensitive без додаткових зусиль
у коді застосунку. Альтернатива — `LOWER()` всюди і функціональний індекс — більш
крихка: легко забути в одному місці і отримати дублікат.

## Схема

### `repositories`

```sql
CREATE TABLE repositories (
    id              BIGSERIAL PRIMARY KEY,
    owner           TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    last_seen_tag   TEXT,                         -- NULL до першого скану
    last_checked_at TIMESTAMPTZ,                  -- NULL = ще не перевірявся
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT repositories_owner_name_key UNIQUE (owner, name)
);
```

| Індекс | Колонки | Тип | Для чого |
|---|---|---|---|
| `repositories_owner_name_key` | `(owner, name)` | UNIQUE | lookup при `POST /subscribe` |
| `idx_repositories_last_checked_at` | `last_checked_at NULLS FIRST` | BTREE | Scanner вибирає "найдавніше перевірені" порціями |

### `subscriptions`

```sql
CREATE TABLE subscriptions (
    id                       BIGSERIAL PRIMARY KEY,
    email                    CITEXT      NOT NULL,
    repository_id            BIGINT      NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    confirmed_at             TIMESTAMPTZ,          -- NULL = pending
    confirm_token            TEXT,                 -- NULL після підтвердження
    confirm_token_expires_at TIMESTAMPTZ,          -- TTL 24h
    unsubscribe_token        TEXT        NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT subscriptions_email_repository_key UNIQUE (email, repository_id),
    CONSTRAINT subscriptions_unsubscribe_token_key UNIQUE (unsubscribe_token),
    CONSTRAINT subscriptions_confirm_state_check
        CHECK ((confirm_token IS NULL) = (confirmed_at IS NOT NULL))
);
```

Constraint `subscriptions_confirm_state_check` виражає інваріант:
- `confirm_token IS NOT NULL` ↔ підписка pending (ще не підтверджена)
- `confirm_token IS NULL` ↔ `confirmed_at IS NOT NULL` (підтверджена)

| Індекс | Колонки | Тип | Для чого |
|---|---|---|---|
| `subscriptions_email_repository_key` | `(email, repository_id)` | UNIQUE | запобігає дублікатам |
| `subscriptions_unsubscribe_token_key` | `unsubscribe_token` | UNIQUE | lookup на `/unsubscribe/{token}` |
| `idx_subscriptions_email` | `email` | BTREE | `GET /api/subscriptions?email=` |
| `idx_subscriptions_confirm_token` | `confirm_token WHERE NOT NULL` | PARTIAL UNIQUE | lookup на `/confirm/{token}`; partial щоб не засмічувати індекс NULL-ами |
| `idx_subscriptions_repository_id_confirmed` | `repository_id WHERE confirmed_at IS NOT NULL` | PARTIAL BTREE | Notifier: вибрати всі підтверджені підписники репо |

## Файли міграцій

```
migrations/
├── 000001_init.up.sql    ← створення схеми
└── 000001_init.down.sql  ← DROP TABLE subscriptions, repositories
```

Інструмент: `golang-migrate/migrate`. Кожен файл виконується в транзакції —
у разі помилки вся міграція відкочується.

## Перевірка

```sh
# Запустити PG
docker run --rm -d --name pg-test -e POSTGRES_PASSWORD=pass -p 5432:5432 postgres:16

# Застосувати міграцію
migrate -path ./migrations \
  -database "postgres://postgres:pass@localhost:5432/postgres?sslmode=disable" up

# Переглянути схему
psql "postgres://postgres:pass@localhost:5432/postgres" \
  -c "\d+ repositories" \
  -c "\d+ subscriptions" \
  -c "\di"

# Відкотити
migrate -path ./migrations \
  -database "postgres://postgres:pass@localhost:5432/postgres?sslmode=disable" down 1
```

Sanity-перевірки:
- Вставити репо двічі з тими ж `(owner, name)` → другий має впасти на UNIQUE.
- Вставити підписку з `confirm_token = NULL` і `confirmed_at = NULL` → має впасти на CHECK.
- Вставити підписки з `vasya@example.com` і `Vasya@Example.com` на той самий репо
  → другий має впасти завдяки CITEXT + UNIQUE.
