# TODO / Backlog

Реєстр ідей, оптимізацій і extras, які ми свідомо відклали поза обсяг першого
етапу (схема БД і міграції). Це не roadmap — просто щоб не забути.

## Оптимізації навколо токенів і підписок

### Redis-кеш для confirm-токенів
Винести `confirm_token` + `confirm_token_expires_at` з PG у Redis. Нативний TTL
через `SET key value EX 86400`, одноразове використання через `GETDEL`.
Два варіанти:
- **Token → subscription_id**: у PG залишається `pending`-рядок, Redis тільки
  мапить токен. Потребує cleanup при збої Redis.
- **Token → повний payload (email, repository_id)**: у PG взагалі не існує
  `pending`-стану, запис створюється тільки після підтвердження. Найчистіше,
  але жорстка залежність від Redis у `/subscribe` flow.

Trade-off проти поточного рішення: durability (Redis in-memory) vs простота TTL.
Обов'язково AOF `appendfsync everysec` якщо йдемо цим шляхом.

### Cron cleanup непідтверджених підписок
Раз на N годин (типово 1h):
```sql
DELETE FROM subscriptions
WHERE confirmed_at IS NULL
  AND confirm_token_expires_at < now();
```
Потрібен поки confirm-токени живуть у PG. Якщо переходимо на Redis — не
потрібен (TTL все зробить сам).

### Notification log / idempotency
Окрема таблиця `notification_log (subscription_id, release_tag, sent_at)` для
гарантії exactly-once надсилань при збоях Scanner між "дізнались про реліз" і
"оновили last_seen_tag". Поки Scanner оновлює `last_seen_tag` атомарно після
успішного фанауту — цього достатньо. Актуально при масштабуванні або якщо
Notifier стане окремим воркером.

### Soft delete / audit для відписок
Зараз hard delete (GDPR-friendly, простіше). Якщо знадобиться churn-аналітика
або "відновити відписку" — перейти на поле `unsubscribed_at` + фільтр у запитах.
Простіший компроміс: Prometheus counter `unsubscribes_total` без історії в БД.

## Extras зі специфікації

### Redis-кеш для GitHub API відповідей
TTL ~10 хв на:
- existence check при `POST /subscribe` (`GET /repos/{owner}/{repo}`)
- список релізів при скані (`GET /repos/{owner}/{repo}/releases/latest`)

Знижує тиск на rate-limit (60 req/h без токена, 5000 з токеном). Ключ —
`github:repo:{owner}/{name}` і `github:releases:{owner}/{name}`.

### API key authentication
Хедер `Authorization: Bearer <token>` або `X-API-Key`. Key хешується, зберігається
в окремій таблиці `api_keys` або просто через env-змінну для одного ключа.

### Prometheus `/metrics`
Базові індикатори:
- `http_requests_total{endpoint, status}` (counter)
- `http_request_duration_seconds{endpoint}` (histogram)
- `github_api_requests_total{status}` (counter, включно з 429)
- `github_api_request_duration_seconds` (histogram)
- `active_subscriptions` (gauge, з SQL)
- `pending_subscriptions` (gauge)
- `confirmations_total`, `unsubscribes_total` (counter)
- `releases_detected_total{repository}` (counter)
- `emails_sent_total{status}` (counter)

### gRPC інтерфейс
Альтернатива/доповнення до REST. `.proto` з тими ж операціями, що й REST API.
Один сервіс піднімає обидва транспорти.

### GitHub Actions CI
`.github/workflows/ci.yml` — `go vet`, `golangci-lint`, `go test ./...` на кожен
push і PR. Bonus: build Docker image.

### HTML landing page + deploy
Статична сторінка з формою підписки, що б'є в `POST /api/subscribe`. Деплой
десь на Fly.io / Render / VPS з публічним URL.

## Відхилені варіанти (щоб не повертатись)

### "Один токен на confirm і unsubscribe"
Відхилено. Причини:
1. Різний lifecycle: confirm одноразовий і з TTL, unsubscribe постійний.
2. Email-клієнти і антивіруси роблять автоматичний GET по лінках у листах —
   може випадково підтвердити або відписати користувача.
3. Повторна підписка після відписки стає заплутаною з одним токеном.
4. Економія тільки на схемі (одна колонка), логіка — складніша.

Рішення: два окремих поля `confirm_token` (TTL 24h, одноразовий) і
`unsubscribe_token` (постійний, унікальний).

### "Зберігати confirm_token тільки в Redis з першого дня"
Відхилено на старті заради durability. Redis може перезапуститись без AOF і
втратити всі pending-підтвердження. Повернутись до цієї оптимізації коли
з'явиться Redis у стеку для кешу GitHub API — тоді intra-service reuse
виправданий.
