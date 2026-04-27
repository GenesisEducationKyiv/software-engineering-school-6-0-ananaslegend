# Scanner + Notifier Design

**Date:** 2026-04-10  
**Status:** Approved

## Overview

Додаємо два нових фонових компоненти до існуючого бінарника:

- **Scanner** — виявляє нові релізи на GitHub, пише в outbox таблицю атомарно з оновленням `last_seen_tag`
- **Notifier** — читає outbox і надсилає email-нотифікації підписникам

Обидва запускаються як горутини в `main.go` поряд з HTTP-сервером.

---

## Нові пакети

```
internal/
  github/
    stub.go          # існуючий
    client.go        # NEW — реальний GraphQL клієнт
  scanner/
    scanner.go       # Run(ctx) loop + ticker
    repository/
      db.go          # DB-queries для сканера
  notifier/
    notifier.go      # Run(ctx) loop + ticker
    repository/
      db.go          # DB-queries для нотифікатора
```

Кожен пакет — вертикальний слайс за тим самим шаблоном що і `subscription`. Інтерфейси (`ScannerRepository`, `ReleaseProvider`, `NotifierRepository`) визначаються у місці використання — в `scanner/scanner.go` і `notifier/notifier.go` відповідно.

---

## GitHub GraphQL Client

**Файл:** `internal/github/client.go`

Метод:
```go
type GetLatestReleasesParams struct {
    Repos []domain.GitHubRepo // id, owner, name
}

// returns: repo_id → latest tag (absent = no releases yet)
func (c *Client) GetLatestReleases(ctx context.Context, p GetLatestReleasesParams) (map[int64]string, error)
```

Динамічно будує GraphQL query з аліасами (`r{id}` = alias для кожного repo):
```graphql
query {
  r1: repository(owner: "foo", name: "bar") { latestRelease { tagName } }
  r2: repository(owner: "baz", name: "qux") { latestRelease { tagName } }
}
```

Парсить JSON відповідь, повертає `map[repoID → tagName]`. Репо без жодного релізу (`latestRelease: null`) — відсутні в map.

**Авторизація:** опціональний `Authorization: Bearer <GITHUB_TOKEN>` header. Без токена — 60 req/h, з токеном — 5000 req/h. Використовує стандартний `net/http` — без нових залежностей.

---

## Міграція 000002

```sql
CREATE TABLE release_notifications (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    repository_id   BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    release_tag     TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ  -- NULL = pending
);

CREATE INDEX idx_release_notifications_pending
    ON release_notifications (created_at)
    WHERE sent_at IS NULL;
```

---

## Scanner

**Файл:** `internal/scanner/scanner.go`

**Інтерфейси (визначені тут):**
```go
type Repository interface {
    LockRepos(ctx context.Context, limit int) ([]domain.GitHubRepo, error)
    UpsertLastSeen(ctx context.Context, p UpsertLastSeenParams) error
    InsertNotifications(ctx context.Context, p InsertNotificationsParams) error
}
```

> **Транзакції через context:** Scanner починає транзакцію через `pool.BeginTx`, кладе `pgx.Tx` у context (helper `withTx(ctx, tx) context.Context`), і передає цей ctx у методи репозиторію. Repository дістає tx з context і виконує SQL на ньому. Scanner відповідає за `Commit`/`Rollback`. Notifier repository використовує той самий підхід.

```go

type ReleaseProvider interface {
    GetLatestReleases(ctx context.Context, p github.GetLatestReleasesParams) (map[int64]string, error)
}
```

**Цикл (кожні `SCANNER_INTERVAL`, default `5m`):**

1. Починає транзакцію
2. `SELECT id, owner, name, last_seen_tag FROM repositories ORDER BY last_checked_at NULLS FIRST LIMIT 100 FOR UPDATE SKIP LOCKED`
3. Один GraphQL запит → `map[repoID]latestTag`
4. Для кожного repo де `latestTag != lastSeenTag`:
   - **Перший скан** (`last_seen_tag IS NULL`): тільки записати поточний тег як `last_seen_tag`, нотифікації не створювати — уникаємо спаму при першій реєстрації
   - **Новий реліз** (`last_seen_tag != nil && latestTag != *last_seen_tag`):
     ```sql
     INSERT INTO release_notifications (subscription_id, repository_id, release_tag)
     SELECT id, $repo_id, $tag FROM subscriptions
     WHERE repository_id = $repo_id AND confirmed_at IS NOT NULL
     ```
     ```sql
     UPDATE repositories SET last_seen_tag = $tag, last_checked_at = now() WHERE id = $repo_id
     ```
5. Commit

Транзакція тримається відкритою під час GitHub API call. Це прийнятно для background worker (не user-facing path). Атомарність: outbox рядки і оновлений `last_seen_tag` комітяться разом — або нічого.

---

## Notifier

**Файл:** `internal/notifier/notifier.go`

**Інтерфейси (визначені тут):**
```go
type Repository interface {
    LockNextPending(ctx context.Context) (*PendingNotification, error)
    MarkSent(ctx context.Context, id int64, now time.Time) error
}

type MailSender interface {
    SendRelease(ctx context.Context, p domain.SendReleaseParams) error
}
```

**Цикл (кожні `NOTIFIER_INTERVAL`, default `30s`):**

Loop поки є pending рядки:
1. Begin tx
2. `SELECT rn.id, s.email, r.owner, r.name, rn.release_tag FROM release_notifications rn JOIN subscriptions s ON rn.subscription_id = s.id JOIN repositories r ON rn.repository_id = r.id WHERE rn.sent_at IS NULL ORDER BY rn.created_at LIMIT 1 FOR UPDATE SKIP LOCKED`
3. Якщо нічого — commit, break
4. `MailSender.SendRelease`
5. `UPDATE release_notifications SET sent_at = now() WHERE id = $id`
6. Commit

Провал SMTP → rollback → тільки цей рядок ретраїться на наступному тіку. Інші нотифікації не блокуються. Немає батч-логіки, немає partial failure.

---

## Wire-up у `main.go`

```go
githubClient := github.NewClient(cfg.GitHubToken)

scanRepo := scannerrepo.New(pool)
scan := scanner.New(scanner.Config{
    Repo:     scanRepo,
    GitHub:   githubClient,
    Interval: cfg.ScannerInterval,
})

notifyRepo := notifierrepo.New(pool)
notify := notifier.New(notifier.Config{
    Repo:     notifyRepo,
    Mailer:   mailer,
    Interval: cfg.NotifierInterval,
})

go scan.Run(ctx)
go notify.Run(ctx)
```

---

## Нові env vars

| Змінна             | Default | Опис                                  |
|--------------------|---------|---------------------------------------|
| `GITHUB_TOKEN`     |         | Опціональний; 5000 req/h замість 60   |
| `SCANNER_INTERVAL` | `5m`    | Як часто запускати scan loop          |
| `NOTIFIER_INTERVAL`| `30s`   | Як часто флашити outbox               |

---

## Тести

- **Scanner service** — unit-тести з mock `Repository` і mock `ReleaseProvider`. Покрити: перший скан (no notifications), новий реліз (notifications created), no change (nothing happens).
- **GitHub client** — unit-тест з `httptest.Server` що повертає mock GraphQL response.
- **Notifier service** — unit-тести з mock `Repository` і mock `MailSender`. Покрити: SMTP success, SMTP failure (no MarkSent call).

---

## Поза скоупом

- `failed_count` / dead-letter для нотифікацій що постійно падають
- Rate limit handling (429 від GitHub) — логуємо помилку, пропускаємо тік
- Prometheus метрики (заплановано в todo.md як окремий таск)
