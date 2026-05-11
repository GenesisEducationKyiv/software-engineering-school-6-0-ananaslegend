🔥 Критичні (справжні баги, а не лише стиль)

1. Nil-pointer ланцюг навколо Redis-клієнта — LSP, DIP

internal/app/app.go:41-44, 78 + internal/app/github.go + internal/github/caching_client.go:61

// app.go:41 — на помилці rdb залишається nil, але виконання продовжується
rdb, err := NewRedisClient(cfg.RedisURL)
if err != nil {
log.Warn().Err(err).Msg("redis unavailable, github caching disabled")
}
// ...
rdb.Close() //nolint:errcheck   // app.go:78 — паніка на nil

Далі newReleaseProvider завжди обгортає клієнт у CachingReleaseProvider (навіть з rdb == nil), а caching_client.go:61 робить c.rdb.MGet(...) без перевірки на nil. Лог пише "github release cache: redis" навіть коли Redis
недоступний. LSP: декоратор зобовʼязаний бути взаємозамінним із базовим ReleaseProvider, але з nil RDB він катастрофічно ламається замість деградації.
Фікс: if rdb == nil { return githubClient } у newReleaseProvider; if rdb != nil { _ = rdb.Close() } у завершенні Run.

2. Зовнішній GraphQL-виклик усередині відкритої pgx-транзакції з FOR UPDATE — SRP, Low Coupling, операційний ризик

internal/scanner/scanner.go:81-125

err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
repos, err := s.repo.GetRepositoriesWithLock(ctx, scanLimit)   // (1) lock batch
tags, err := s.github.GetLatestReleases(...)                    // (2) HTTP виклик усередині відкритої tx
for _, repo := range repos { s.repo.UpsertLastSeen(...); ... }  // (3) writes
})

Лок-рядки тримаються весь час GitHub round-trip (вкл. retry/timeout). Це і SRP-проблема (три відповідальності в одному closure), і латентнісний баг.
Фікс: loadBatch (tx) → fetchReleases (без tx) → persist (tx) — три короткі функції, дві короткі транзакції.

3. Subscription Repository ігнорує проєктовий transactor — DIP, узгодженість архітектури

internal/subscription/repository/subscriptions.go:16-53

func (r *Repository) CreateSubscription(...) (*domain.Subscription, error) {
tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})   // власна транзакція
defer tx.Rollback(ctx)
// INSERT subscription ... INSERT confirmation_notifications ... tx.Commit
}

Усі інші репозиторії (scanner, notifier, confirmer) йдуть через transactor.ConnFromContext(ctx, r.pool) — це задокументований проєктовий патерн. subscription/repository єдиний жорстко прибиває r.pool.* і навіть володіє власною
BeginTx/Commit, через що сервіс не може скомпонувати цей виклик в WithinTransaction з іншими.
Фікс: додати r.conn(ctx) helper як скрізь; винести композицію двох INSERT в Service.Subscribe через tx.WithinTransaction.

  ---
⚙️ SRP / High Cohesion

4. Service.Subscribe — god-метод

internal/subscription/service/service.go:74-122

10 відповідальностей у 50 рядках: parse → format-validation → split → remote check → upsert repo → token gen × 2 → outbox insert → log → metric. Регулярка repoNameRe і функція normalizeRepo живуть у сервісі, але оперують виключно
доменними даними — Information Expert каже, що це належить domain.ParseRepoRef.

5. Дублювання Notifier / Confirmer — OCP, Polymorphism

internal/notifier/notifier.go:88-130 vs internal/confirmer/confirmer.go:87-123

Два майже ідентичних Flush (FOR UPDATE SKIP LOCKED → send → MarkSent → commit). Уже видно дрейф: у Notifier є гістограма flushDuration, у Confirmer її забули. Третій outbox-канал = третя копія цього коду.
Фікс: абстракція OutboxDrainer або інтерфейс outboxItem { Send(ctx) error; ID() int64 }.

6. URL-будівництво у воркері — Information Expert

internal/notifier/notifier.go:103-110, internal/confirmer/confirmer.go:101

ReleaseURL: fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", p.RepoOwner, p.RepoName, p.ReleaseTag),
UnsubscribeURL: fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, p.UnsubscribeToken),

Worker не повинен знати маршрутів HTTP-шару. Експерт — або domain.PendingNotification.UnsubscribeURL(baseURL), або окремий urls пакет.

7. internal/app/postgres.go змішує bootstrap і Prometheus-collector

Один файл містить newPostgresDatabase (рядки 13-30) і PoolCollector (32-75) — дві незалежні відповідальності, які CLAUDE.md правильно розводить у різні файли, але код — ні.

8. httpapi/router.go напряму імпортує feature-handler — OCP, Low Coupling

internal/httpapi/router.go:24-47 знає про subscription/http, swagger, prometheus і монтує конкретні маршрути. Cross-cutting пакет залежить від downstream-feature.
Фікс: type Mounter interface { Mount(r chi.Router) } — кожна фіча реєструє свої маршрути сама.

9. Глобальна мутація у конструкторі логера — Principle of least surprise / SRP

internal/app/logger.go:29 — функція з назвою New мутує zerolog.DefaultContextLogger. Перейменувати на Init або прибрати глобальний side effect.

  ---
🔌 DIP / ISP

10. pages.Renderer зашитий конкретним типом у Handler — DIP

internal/subscription/http/handler.go:25-32 — конструктор NewHandler(svc SubscriptionService) мовчки покладається на zero-value pages.Renderer{}. Прихована залежність, неможливо застабити в тестах HTML-гілок.

11. github.StubClient має іншу поверхню, ніж github.Client — LSP / naming

stub.go реалізує лише RepoExists, а Client — і RepoExists, і GetLatestReleases. Не повноцінна заміна. Або перейменувати у RepoExistsStub, або винести RepoExistsChecker як інтерфейс.

12. Дублювання конструкції github.Client — DRY, DIP

internal/app/server.go:21 та internal/app/github.go:16 обидва викликають githubclient.NewClient(cfg.GitHubToken). Сервіс отримує неприкритий нем-кешований клієнт; сканер — кешований. Різна поведінка та метрики для одного
зовнішнього API.

  ---
📧 Mailer / Templates

13. Дублювання рендеру шаблонів та проігнорований ctx — DRY, Pure Fabrication

internal/notifier/emailer/smtp.go:60-104 і resend.go:25-67 обидва вручну рендерять confirmationHTMLTmpl + confirmationTXTTmpl. Resend має SendWithContext, але код викликає звичайний Send — ctx cancel/deadline втрачаються на
границі.

  ---
🧹 Дрібниці, які теж кричать

- workers.go:39, 49, 59 — лишилися ; no-op перед scan.Run, notify.Run, confirm.Run (wg.Go(func() { ; scan.Run(ctx) })). Машинна правка, ніким не переглянута.
- redis.go:10 — мертвий тип Config{ URL string }, ніким не використовуваний (NewRedisClient приймає raw string).
- subscription/service/service.go — половина wrap-ів у короткій формі ("check repo existence: %w"), хоча проєктова конвенція (вже в memory) вимагає subscription.Service.Method: Callee: %w.

  ---
✅ Що чисто (не вигадую проблем)

- pkg/transactor — мінімальний Conn інтерфейс, правильний ISP.
- domain/ — без behavioral creep, чиста модель.
- Інтерфейси MailSender, ReleaseProvider, service.Repository, RemoteRepositoryProvider — consumer-side, один-два методи, як треба.
- scanner/repository, notifier/repository, confirmer/repository — еталонні адаптери поверх transactor.Conn.

  ---
Що чинити першим (за впливом)

1. #1 — nil-RDB ланцюг (реальний panic у проді без Redis).
2. #2 — винести GitHub-виклик зі сканерної транзакції (тримає FOR UPDATE під час HTTP).
3. #3 — subscription/repository під transactor (єдина точка непослідовності).
4. #5 — спільний OutboxDrainer для Notifier/Confirmer (дрейф уже видно).
5. Оновити CLAUDE.md під реальну розкладку (або винести internal/app/*.go назад у storage/postgres, storage/redis, mailer, transactor, як декларує док).