# TODO / Backlog

Реєстр ідей, оптимізацій і extras, які ми свідомо відклали поза обсяг першого
етапу (схема БД і міграції). Це не roadmap — просто щоб не забути.

## Redis-кеш для confirm-токенів
Винести `confirm_token` + `confirm_token_expires_at` з PG у Redis. Нативний TTL
через `SET key value EX 86400`, одноразове використання через `GETDEL`.
Два варіанти:
- **Token → subscription_id**: у PG залишається `pending`-рядок, Redis тільки
  мапить токен. Потребує cleanup при збої Redis.

Trade-off проти поточного рішення: durability (Redis in-memory) vs простота TTL.
Обов'язково AOF `appendfsync everysec` якщо йдемо цим шляхом.

## Перенести конфіми в окрему таблицю

## Cron cleanup непідтверджених підписок
Раз на N годин (типово 1h):
```sql
DELETE FROM subscriptions
WHERE confirmed_at IS NULL
  AND confirm_token_expires_at < now();
```

## gRPC інтерфейс
Альтернатива/доповнення до REST. `.proto` з тими ж операціями, що й REST API.
Один сервіс піднімає обидва транспорти.

## Refactor code 

## Обробка HTTP помилок у відповіді

## Перевірити gracuful shutdown для всіх воркерів (Scanner, Notifier, Confirmer)

## Інтеграційні тести 

## Transactor