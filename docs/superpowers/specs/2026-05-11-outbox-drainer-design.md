# Outbox Drainer — Design

**Status:** approved
**Created:** 2026-05-11
**Branch:** HW#4-SOLID-&-GRASP

## Problem

`internal/notifier/notifier.go:88-130` і `internal/confirmer/confirmer.go:87-123` мають структурно ідентичний `Flush`:

```
for {
  WithinTransaction {
    items := GetXWithLock(ctx, 1)
    if empty: return
    err := mailer.SendX(ctx, params)
    if err: emailsSent("error").Inc; return err
    emailsSent("ok").Inc
    MarkSent(ctx, items[0].ID)
    processed = true
  }
  if err: log; return
  if !processed: return
}
```

Різниця тільки в типах і назвах методів — fetch/send/markSent. Уже видно **drift**: Notifier має `notifier_flush_duration_seconds` histogram, Confirmer її забули. Третій outbox-канал (наприклад, digest/marketing) = третя копія цього коду.

## Goals

1. **OCP / extensibility:** додавання нового outbox-каналу = новий пакет із fetch+send+mark логікою, без копіювання loop/transaction/timing коду.
2. **DRY / drift closure:** один source-of-truth для top-level loop і `flush_duration_seconds` histogram; Confirmer автоматично отримує метрику, яка йому зараз бракує.

## Non-goals

- Винесення counter `emails_sent_total` у спільний код (свідоме рішення — counter живе в feature, бо знає про item-level success/error семантику).
- Зміна `Notifier.Flush` / `Confirmer.Flush` signature або сигнатур `Config`.
- Зміна `internal/app/workers.go`.
- Зміна mock interfaces / `go generate` repository інтерфейсів.
- Усунення дублювання тест-helper'ів між `notifier_test.go` і `confirmer_test.go` — це окремий refactor, не пов'язаний з Drainer.
- Виправлення інваріанти "counter інкрементується до MarkSent" (потенційний double-count на retry після MarkSent error) — зберігаємо поточну поведінку.
- Integration tests з реальним Postgres.

## Constraints

- Тести `notifier_test.go` / `confirmer_test.go` мають проходити **без модифікацій** після рефакторингу.
- `Notifier.Flush(ctx)` / `Confirmer.Flush(ctx)` signatures зберігаються.
- Метрика `notifier_flush_duration_seconds` зберігається 1:1 (name, help, buckets), щоб не зламати потенційні існуючі дашборди/алерти.
- Метрики `notifier_emails_sent_total`, `confirmer_emails_sent_total` зберігаються 1:1.
- Метрика `confirmer_flush_duration_seconds` **з'являється** як side-effect (закриття drift) — це бажано.
- `Config` структури `notifier.Config` / `confirmer.Config` залишаються без змін.

## Architecture

Новий пакет `internal/outbox/`, який містить **лише** top-level loop і `flush_duration_seconds` histogram registration. Знає тільки про `prometheus.Registry` і `context.Context`; **не знає** про `transactor.Transactor`, mailer, repo, Pending типи, baseURL.

Feature (`notifier`, `confirmer`) тримає:
- свій `emailsSent` counter у `metrics.go` (як зараз),
- свій `tx transactor.Transactor` field (як зараз),
- приватний метод `processOne(ctx) (bool, error)`, що сам відкриває транзакцію через `n.tx.WithinTransaction` і робить fetch+send+counter.Inc+mark всередині,
- `*outbox.Drainer` як field,
- `Flush(ctx)` делегує: `n.drainer.Flush(ctx, n.processOne)`.

### Контракт Drainer

```go
package outbox

type ProcessOne func(ctx context.Context) (processed bool, err error)

type Config struct {
    ChannelName string                // "notifier" | "confirmer" — префікс histogram-метрики
    Registry    *prometheus.Registry  // nil-safe
}

type Drainer struct {
    channelName   string
    flushDuration prometheus.Histogram
}

func New(cfg Config) *Drainer
func (d *Drainer) Flush(ctx context.Context, process ProcessOne)
```

`Flush` крутить:

```go
start := time.Now()
defer func() { d.flushDuration.Observe(time.Since(start).Seconds()) }()
for {
    processed, err := process(ctx)
    if err != nil {
        zerolog.Ctx(ctx).Error().Err(err).Str("channel", d.channelName).Msg("outbox: process next failed")
        return
    }
    if !processed {
        return
    }
}
```

### Feature: processOne (приклад — notifier)

```go
func (n *Notifier) processOne(ctx context.Context) (bool, error) {
    var processed bool
    err := n.tx.WithinTransaction(ctx, func(ctx context.Context) error {
        items, err := n.repo.GetNotificationsWithLock(ctx, notifyLimit)
        if err != nil {
            return fmt.Errorf("notifier.Notifier.processOne: Repository.GetNotificationsWithLock: %w", err)
        }
        if len(items) == 0 {
            return nil
        }
        p := items[0]
        if err := n.mailer.SendRelease(ctx, domain.SendReleaseParams{
            To:             p.Email,
            RepoFullName:   p.RepoOwner + "/" + p.RepoName,
            ReleaseTag:     p.ReleaseTag,
            ReleaseURL:     fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", p.RepoOwner, p.RepoName, p.ReleaseTag),
            UnsubscribeURL: fmt.Sprintf("%s/api/unsubscribe/%s", n.baseURL, p.UnsubscribeToken),
        }); err != nil {
            n.m.emailsSent.WithLabelValues("error").Inc()
            return fmt.Errorf("notifier.Notifier.processOne: MailSender.SendRelease: %w", err)
        }
        n.m.emailsSent.WithLabelValues("ok").Inc()
        if err := n.repo.MarkSent(ctx, p.ID); err != nil {
            return fmt.Errorf("notifier.Notifier.processOne: Repository.MarkSent: %w", err)
        }
        processed = true
        return nil
    })
    return processed, err
}
```

### Wiring (notifier.New)

```go
func New(cfg Config) *Notifier {
    return &Notifier{
        drainer:  outbox.New(outbox.Config{ChannelName: "notifier", Registry: cfg.Registry}),
        tx:       cfg.Tx,
        repo:     cfg.Repo,
        mailer:   cfg.Mailer,
        interval: cfg.Interval,
        baseURL:  cfg.BaseURL,
        m:        newNotifierMetrics(cfg.Registry),
    }
}
```

Дзеркально для `confirmer.New`. `internal/app/workers.go` не змінюється.

## Data flow

```
ticker.C
  → Notifier.Flush(ctx)
    → Drainer.Flush(ctx, n.processOne)
      ├ start := time.Now()
      └ for loop:
          processed, err := n.processOne(ctx)
            └ n.tx.WithinTransaction(ctx, fn):       ← БД-транзакція межа
                └ fn(ctx):
                    ├ repo.GetNotificationsWithLock(ctx, 1)    [SELECT FOR UPDATE SKIP LOCKED]
                    ├ len(items)==0 → return nil (processed stays false)
                    ├ mailer.SendRelease(ctx, params)          [SMTP / Stub]
                    │   error → emailsSent("error").Inc; return wrap
                    ├ emailsSent("ok").Inc
                    ├ repo.MarkSent(ctx, p.ID)                 [UPDATE sent_at=NOW()]
                    │   error → return wrap
                    └ processed = true; return nil
            (tx commit / rollback)
          err != nil       → log; return       ← наступний tick спробує знову
          processed == false → return          ← outbox порожній
          else continue                        ← наступна ітерація
  defer: flushDuration.Observe(elapsed)
```

## Error handling

| Клас | Звідки | Поведінка | Стан БД |
|---|---|---|---|
| Empty outbox | `processOne` повертає `(false, nil)` | Drainer виходить з циклу нормально | tx commit (no-op) |
| Mailer error | `SendRelease`/`SendConfirmation` повернув error | `processOne` робить `emailsSent("error").Inc` + повертає `(false, wrap)` → Drainer логує + return | tx rollback (sent_at не оновлено, рядок лишається pending) |
| Repository error | `GetWithLock` або `MarkSent` повернув error | `processOne` повертає `(false, wrap)` → Drainer логує + return | tx rollback |

### Error wrapping

- `notifier.Notifier.processOne: Repository.GetNotificationsWithLock: %w`
- `notifier.Notifier.processOne: MailSender.SendRelease: %w`
- `notifier.Notifier.processOne: Repository.MarkSent: %w`
- Дзеркально для confirmer.
- Drainer **не wrap'ить** error (одинарний префікс у логу — чисто).
- `transactor.Transactor.WithinTransaction` — у винятках wrapcheck (callback уже wrap'ить).

## Metrics

| Метрика | Тип | Реєстрація | До рефакторингу | Після |
|---|---|---|---|---|
| `notifier_emails_sent_total{result}` | Counter | `internal/notifier/metrics.go` (без змін) | exists | exists (без змін) |
| `notifier_flush_duration_seconds` | Histogram | `internal/outbox/drainer.go` через `outbox.New(ChannelName="notifier")` | exists (у notifier) | exists (переїхала в outbox, 1:1 формат) |
| `confirmer_emails_sent_total{result}` | Counter | `internal/confirmer/metrics.go` (без змін) | exists | exists (без змін) |
| `confirmer_flush_duration_seconds` | Histogram | `internal/outbox/drainer.go` через `outbox.New(ChannelName="confirmer")` | **MISSING** | **exists** (drift closed) |

Histogram opts:

```go
prometheus.NewHistogram(prometheus.HistogramOpts{
    Name:    cfg.ChannelName + "_flush_duration_seconds",
    Help:    "Duration of one " + cfg.ChannelName + " Flush() call in seconds.",
    Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
})
```

— збігається бак-в-бак з поточним `notifierMetrics.flushDuration`. Реєстрація nil-safe (`if cfg.Registry != nil`).

### Видаляється з notifier

- `notifierMetrics.flushDuration` поле і його ініціалізація в `metrics.go`.
- `start := time.Now()` + `defer flushDuration.Observe(...)` у `notifier.go:Flush`.

### Не торкаємо

- `confirmerMetrics` (не мав histogram, нічого видаляти).
- Інші метрики проєкту (`scanner_*`, `subscriptions_*`, `db_pool_*`, `http_*`, `github_cache_*`).

## Testing

### Існуючі тести (регресія) — без модифікацій

- `notifier_test.go`: `TestNotifier_FlushEmpty_NoMailer`, `TestNotifier_FlushOneNotification_MailerCalled`, `TestNotifier_FlushMailerError_NoMarkSentAndStops`, `TestNotifier_FlushMultipleNotifications_ProcessedInOrder`, `TestNotifier_Flush_IncrementsEmailSentMetric`.
- `confirmer_test.go`: дзеркальний набір.

Контракт: ті ж `WithinTransaction` виклики, ті ж `Get*WithLock(ctx, 1)`/`Send*`/`MarkSent`, ті ж counter асерти, ті ж error paths.

### Нові тести — `internal/outbox/drainer_test.go`

Тестуємо Drainer ізольовано через fake `ProcessOne` — без транзактора, без mock'ів.

| Тест | Перевіряє |
|---|---|
| `TestDrainer_Flush_EmptyOutbox_CallsProcessOnce` | `(false, nil)` → 1 виклик, вихід |
| `TestDrainer_Flush_OneItem_CallsProcessTwice` | `(true, nil)` → `(false, nil)` → 2 виклики |
| `TestDrainer_Flush_MultipleItems_CallsUntilEmpty` | 3 successful + empty → 4 виклики |
| `TestDrainer_Flush_ProcessError_StopsLoop` | `(false, err)` → 1 виклик, вихід без panic / loop |
| `TestDrainer_Flush_ProcessErrorAfterSuccess_StopsLoop` | `(true, nil)` → `(false, err)` → 2 виклики, вихід |
| `TestDrainer_Flush_RegistersHistogramWithChannelName` | Registry містить `<channel>_flush_duration_seconds` |
| `TestDrainer_Flush_NilRegistry_DoesNotPanic` | `Registry: nil` → New + Flush не панікують |
| `TestDrainer_Flush_ObservesDurationOnce` | один Flush → `testutil.CollectAndCount` повертає 1 для histogram |

Без mocks: fake `ProcessOne` — звичайна closure-функція в test scope.

### Verification commands

```sh
go build ./...
go vet ./...
go test ./internal/notifier/... ./internal/confirmer/... ./internal/outbox/...
golangci-lint run ./internal/notifier/... ./internal/confirmer/... ./internal/outbox/...
```

Усі — без warnings, всі existing tests зелені.

## File changes summary

| Файл | Зміна |
|---|---|
| `internal/outbox/drainer.go` | **new** — `Drainer`, `Config`, `New`, `Flush`, `ProcessOne`, histogram inline (без окремого `metrics.go` — все в одному файлі, ~50 LOC) |
| `internal/outbox/drainer_test.go` | **new** — 8 тестів через fake ProcessOne |
| `internal/notifier/notifier.go` | modified — `drainer` field, `Flush` делегує, `processOne` приватний з тілом транзакції |
| `internal/notifier/metrics.go` | modified — видалити `flushDuration` поле і реєстрацію |
| `internal/confirmer/confirmer.go` | modified — дзеркально notifier |
| `internal/confirmer/metrics.go` | no change |
| `internal/app/workers.go` | no change |
| `internal/notifier/notifier_test.go` | no change |
| `internal/confirmer/confirmer_test.go` | no change |
| mocks (`internal/notifier/mocks/`, `internal/confirmer/mocks/`) | no change |
