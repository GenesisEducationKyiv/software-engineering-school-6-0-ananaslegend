# Observability Stack — Дизайн

**Статус:** Draft (очікує на ревʼю користувача)
**Дата:** 2026-06-03
**Автор:** brainstorming session (limuzin + Claude)

---

## 1. Цілі та скоуп

### Цілі

1. **Структуроване логування + конвеєр до Elasticsearch.** Застосунок продовжує писати JSON у stdout (zerolog, як зараз) і паралельно надсилає кожну подію в HTTP-приймач Vector, який нормалізує і пише у Elasticsearch single-node. Візуалізація — Kibana. Retention 7 днів через ILM rollover.
2. **RED-метрики (rate / errors / duration) на всіх межах застосунку.** HTTP-шар уже інструментовано; додаємо outbound GitHub GraphQL, outbound email, та tick'и фонових воркерів (scanner / notifier / confirmer). Експорт — через `vmagent` (scrape + remote_write) у `vmsingle` (single-node VictoriaMetrics, retention 30 днів).
3. **Grafana з VictoriaMetrics як datasource і одним RED-дашбордом.** Дашборд provisioning'ом завантажується при старті Grafana; містить панелі по 4 шарах: HTTP, outbound, workers, runtime/pool/self-metrics.

### Не-цілі

- **Distributed tracing** (OpenTelemetry traces / Jaeger / Tempo) — out of scope.
- **Alerting rules** (Grafana alerts, alertmanager, PagerDuty) — out of scope; обмежуємось дашбордом.
- **Auth на `/metrics`** — користувач явно відмовився; ендпойнт лишається публічним.
- **Реальний Railway-деплой обсерваційного стека** — out of scope; ця спека покриває код застосунку, локальний docker-compose і документацію готовності до Railway-деплою. Сам деплой виконується окремо.
- **Видалення старих feature-specific метрик** (`notifier_emails_sent_total` тощо) — out of scope; буде окремий cleanup-PR після стабілізації нових RED-метрик.

### Цільові середовища

- **Локально (docker-compose):** усі компоненти стека в overlay-файлі, активуються через профіль / Makefile-таргет.
- **Railway (прод):** app + кожен компонент стека — окремий Railway-сервіс у тому ж project'і; спілкування через `*.railway.internal`. Готовність до деплою — задокументована; виконання — поза спекою.

---

## 2. Архітектура (overview)

```
                  ┌──────────────────────────────────────┐
                  │   reposeetory (Go binary)            │
                  │                                      │
                  │  zerolog (MultiLevelWriter)          │
                  │    ├──→ stdout (Railway UI /         │
                  │    │     docker logs)                │
                  │    └──→ HTTP POST $VECTOR_INGEST_URL │
                  │          (non-blocking,              │
                  │           bounded buffer,            │
                  │           fail-open)                 │
                  │                                      │
                  │  prometheus.Registry → /metrics      │
                  │    (публічний, без auth)             │
                  └────────────┬───────────────────┬─────┘
                               │ HTTP/JSON        │ HTTP scrape
                               ▼                  ▼
                       ┌─────────────┐     ┌────────────┐
                       │  Vector     │     │  vmagent   │
                       │ http_json   │     │ scrape +   │
                       │  source     │     │ remote_write│
                       │   → ES sink │     └─────┬──────┘
                       └──────┬──────┘           │
                              │ bulk index       ▼
                              ▼            ┌──────────┐
                       ┌──────────┐         │ vmsingle │
                       │  ES      │         │ retention│
                       │ single-  │         │   30d    │
                       │ node +   │         └────┬─────┘
                       │ ILM 7d   │              │ PromQL
                       └────┬─────┘              ▼
                            │               ┌──────────┐
                            ▼               │ Grafana  │
                       ┌──────────┐         │ provis.  │
                       │  Kibana  │         │ dashboard│
                       └──────────┘         └──────────┘
```

**Розгортання:**

- **Локально:** усе у docker-compose (overlay-файл `docker-compose.observability.yml`).
- **Railway:** app + кожен компонент стека — окремий Railway-сервіс; спілкування через `*.railway.internal` DNS.

---

## 3. Log pipeline

### 3.1 App-сторона

**Нові env-змінні** (додаємо в `internal/config/config.go` і `.env.example`):

| Var | Default | Призначення |
|---|---|---|
| `VECTOR_INGEST_URL` | `""` | HTTP endpoint Vector (порожнє → шипер вимкнений) |
| `LOG_SERVICE_NAME` | `reposeetory` | Поле `service` у logs |
| `LOG_ENV` | `development` | Поле `env` у logs |
| `LOG_VERSION` | `""` | Build-time `-ldflags -X` (commit SHA / tag) |
| `LOG_SHIPPER_BUFFER_SIZE` | `1024` | Розмір каналу шипера |
| `LOG_SHIPPER_BATCH_SIZE` | `50` | NDJSON batch до Vector |
| `LOG_SHIPPER_FLUSH_INTERVAL` | `2s` | Макс. час між флашами |

**Новий пакет `internal/observability/logshipper`** (consumer-side; zerolog не знає про нього — пакет надає `io.Writer`):

```go
package logshipper

type Config struct {
    URL            string                  // empty → New returns nil, *Writer не створюється
    BufferSize     int
    BatchSize      int
    FlushInterval  time.Duration
    Registry       *prometheus.Registry    // для self-metrics
    Logger         zerolog.Logger          // логер для самого шипера — пише ЛИШЕ у stderr
}

type Writer struct { /* ... */ }              // implements io.Writer
func New(cfg Config) (*Writer, error)
func (w *Writer) Write(p []byte) (int, error)  // non-blocking; full → drop + counter
func (w *Writer) Close(ctx context.Context) error
```

**Семантика `Write`:**

- Кожен виклик = одне JSON-повідомлення zerolog (zerolog пише по одному event на `Write`).
- Кладемо у bounded `chan []byte` (розмір `BufferSize`). Якщо повний — `default:` гілка drop + counter `logshipper_dropped_total++`.
- Завжди повертає `(n=len(p), err=nil)` — fail-open: zerolog не повинен бачити помилок шипера.

**Фонова горутина** (стартує у `New`):

- Збирає події з канала; флашить коли набралось `BatchSize` АБО пройшло `FlushInterval`.
- POST на `URL`; body — NDJSON (одна подія = один рядок); header `Content-Type: application/x-ndjson`.
- Retry з експоненціальним back-off (3 спроби). При повній невдачі — counter `logshipper_failed_total++`, події дропаються (fail-open).

**Self-metrics шипера** (реєструємо в той самий Registry, що й решта; nil-safe):

- `logshipper_dropped_total` — counter, кинуто через переповнений буфер
- `logshipper_failed_total` — counter, POST впав остаточно
- `logshipper_sent_total{result}` — counter; `result=ok|error`
- `logshipper_batch_duration_seconds` — histogram
- `logshipper_buffer_size` — gauge, поточне заповнення буфера

**Інтеграція в `internal/app/logger.go`:**

```go
func New(cfg LoggerConfig, ship *logshipper.Writer) zerolog.Logger {
    var w io.Writer
    if cfg.Pretty {
        w = zerolog.ConsoleWriter{Out: os.Stderr}
    } else {
        w = os.Stderr
    }
    if ship != nil {
        w = zerolog.MultiLevelWriter(w, ship)
    }
    l := zerolog.New(w).With().
        Timestamp().
        Str("service", cfg.ServiceName).
        Str("env", cfg.Env).
        Str("version", cfg.Version).
        Logger()
    // решта (level, DefaultContextLogger) — без змін
}
```

**Обмеження `LOG_PRETTY` у проді:** ConsoleWriter форматує не-JSON; тому HTTP-канал отримує сирий JSON через zerolog ДО форматтера. У проді `LOG_PRETTY=false` → обидва канали JSON. У dev `LOG_PRETTY=true` → stdout pretty, Vector ingest JSON.

### 3.2 Формат log event

Кожна подія від zerolog містить:

```json
{
  "time": "2026-06-03T12:34:56Z",
  "level": "info",
  "service": "reposeetory",
  "env": "production",
  "version": "abc1234",
  "message": "subscription created",
  "request_id": "...", "method": "POST", "path": "/subscribe", "status": 201,
  "duration_ms": 42,
  "subscription_id": "...", "repo_id": 12345
}
```

ECS-mapping не вводимо — це додало б парсингу без помітної користі для academy-scope. Натомість — index template з відомими полями + dynamic mapping для решти.

### 3.3 Vector конфіг

Єдиний `vector.toml`, спільний для локалки і Railway:

```toml
[sources.app_logs]
type = "http_server"
address = "0.0.0.0:8686"
path = "/ingest"
encoding = "ndjson"
method = "POST"

[transforms.normalize]
type = "remap"
inputs = ["app_logs"]
source = '''
  .@timestamp = del(.time)
  .level = downcase(string!(.level))
  .host = get_env_var("HOSTNAME") ?? "unknown"
'''

[sinks.elasticsearch]
type = "elasticsearch"
inputs = ["normalize"]
endpoints = ["${ES_URL}"]
bulk.index = "reposeetory-logs"          # alias, керується ILM rollover
mode = "bulk"
auth.strategy = "basic"
auth.user = "${ES_USER}"
auth.password = "${ES_PASSWORD}"
healthcheck.enabled = true

[sinks.elasticsearch.buffer]
type = "disk"
max_size = 268435456                      # 256MB
when_full = "drop_newest"
```

### 3.4 Elasticsearch: index template + ILM

**ILM policy `reposeetory-logs-policy`:**

- `hot`: rollover на `max_age=1d` АБО `max_primary_shard_size=1GB`
- `delete`: після `min_age=7d`

**Index template `reposeetory-logs`:**

```json
{
  "index_patterns": ["reposeetory-logs-*"],
  "template": {
    "settings": {
      "index.lifecycle.name": "reposeetory-logs-policy",
      "index.lifecycle.rollover_alias": "reposeetory-logs",
      "number_of_shards": 1, "number_of_replicas": 0
    },
    "mappings": {
      "dynamic": true,
      "properties": {
        "@timestamp":   { "type": "date" },
        "level":        { "type": "keyword" },
        "service":      { "type": "keyword" },
        "env":          { "type": "keyword" },
        "version":      { "type": "keyword" },
        "host":         { "type": "keyword" },
        "request_id":   { "type": "keyword" },
        "method":       { "type": "keyword" },
        "path":         { "type": "keyword" },
        "status":       { "type": "integer" },
        "duration_ms":  { "type": "long" },
        "message":      { "type": "text",
                          "fields": { "keyword": { "type": "keyword", "ignore_above": 1024 } } }
      }
    }
  }
}
```

**Bootstrap (одноразово):**

```
PUT reposeetory-logs-000001
POST /reposeetory-logs-000001/_alias/reposeetory-logs
```

Виконується скриптом `scripts/obs-init.sh` (target `make obs-init`).

### 3.5 Kibana

- Data view `reposeetory-logs-*` з time field `@timestamp` — створюється вручну при першому запуску (одноразовий ручний крок, прийнятний для academy scope).
- Saved searches для demo — опціонально.

### 3.6 Кейси-помилки і fail-open поведінка

| Випадок | Поведінка |
|---|---|
| `VECTOR_INGEST_URL` порожнє | Шипер не створюється; лише stdout. |
| Vector down при старті app | Шипер стартує; перші batch'і failing → retry → `failed`. App працює, stdout — норм. |
| Vector повільний (back-pressure) | Buffer повний → drop newest + `dropped`. App не блокується. |
| ES down, Vector up | Vector буферить на диск (256MB), коли ES повертається — флашить. |
| ILM не сконфігурований | Vector все одно пише в alias; без rollover буде один індекс — не критично, але документуємо як обов'язковий init step. |

---

## 4. RED metrics

### 4.1 Спільний helper-пакет

**Новий пакет `internal/observability/redmetrics`:**

```go
package redmetrics

type Config struct {
    Subsystem   string                 // "github_client" | "email" | "scanner" | ...
    ExtraLabels []string               // напр. {"driver"} для email; "result" додається автоматично
    Buckets     []float64              // nil → use defaults
    Registry    *prometheus.Registry   // nil-safe
}

type RED struct {
    requests *prometheus.CounterVec    // <subsystem>_requests_total
    duration *prometheus.HistogramVec  // <subsystem>_request_duration_seconds
}

func New(cfg Config) *RED

// Observe записує одну операцію.
// extraLabels[i] відповідає Config.ExtraLabels[i]; result — "ok" | "error" | (custom: "cached", "empty").
func (m *RED) Observe(result string, dur time.Duration, extraLabels ...string)
```

**Naming convention** (ADR-0018):

- Counter: `<subsystem>_requests_total`
- Histogram: `<subsystem>_request_duration_seconds`
- Mandatory label `result`: `ok|error`; компонент може додавати `cached|empty|timeout` тощо.
- Default histogram buckets: `[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30]`.

**Cardinality discipline:**

- `result` — bounded enum.
- `extraLabels` — фіксований bounded enum.
- Заборонено: user_id, email, repo_full_name, error_message як label.

### 4.2 Інструментаційні точки

#### 4.2.1 HTTP (`internal/httpapi/metrics.go`) — без змін

Існуючі `http_requests_total{method,path,status}` і `http_request_duration_seconds{method,path}` **залишаються як є**. ADR-0018 фіксує виняток: HTTP layer користується власною формою (status code як label замість result enum), оскільки rename зламає наявні дашборди / load-test baselines.

#### 4.2.2 GitHub client (`internal/github`)

Інструментуємо public виклики (`ReleaseProvider.LatestReleases`). CachingDecorator — окрема обгортка:

- `result=cached` — усі repos знайдено в Redis.
- `result=ok` — пішли в GitHub, успіх.
- `result=error` — network / Redis / GitHub помилка.

**Метрики:**

- `github_client_requests_total{result}`
- `github_client_request_duration_seconds`

Існуючі `github_cache_hits_total` / `github_cache_misses_total` — залишаються (business cache hit ratio, не RED).

#### 4.2.3 Email sender (`internal/notifier/emailer/*`)

Декоратор `Wrap(inner, driver, reg)` навколо `MailSender.Send`, додається в composition root:

**Метрики:**

- `email_requests_total{result, driver}` — driver=`resend|smtp|stub`
- `email_request_duration_seconds{driver}`

Декоратор живе в `internal/observability/emailermetrics`.

#### 4.2.4 Scanner / Notifier / Confirmer ticks

Кожен tick = одна RED-операція.

**Метрики:**

- `scanner_ticks_total{result}` — `ok|error|empty`
- `scanner_tick_duration_seconds`
- `notifier_ticks_total{result}` — `ok|error|empty`
- `notifier_tick_duration_seconds`
- `confirmer_ticks_total{result}` — `ok|error|empty`
- `confirmer_tick_duration_seconds`

`empty` для scanner — "немає репо для скану"; для notifier/confirmer — "outbox порожній".

Існуючий `notifier_flush_duration_seconds` стає легасі (тимчасово паралельний; видалення — окремий PR).

### 4.3 Wiring в composition root

`internal/app/observability.go` (новий):

```go
func newRED(reg *prometheus.Registry) reds {
    longBuckets := []float64{0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 120}
    return reds{
        Github:    redmetrics.New(redmetrics.Config{Subsystem: "github_client", Registry: reg}),
        Email:     redmetrics.New(redmetrics.Config{Subsystem: "email", ExtraLabels: []string{"driver"}, Registry: reg}),
        Scanner:   redmetrics.New(redmetrics.Config{Subsystem: "scanner",   Buckets: longBuckets, Registry: reg}),
        Notifier:  redmetrics.New(redmetrics.Config{Subsystem: "notifier",  Buckets: longBuckets, Registry: reg}),
        Confirmer: redmetrics.New(redmetrics.Config{Subsystem: "confirmer", Buckets: longBuckets, Registry: reg}),
    }
}
```

Передача — через існуючі `Config`-структури кожного компонента (узгоджено з ADR-0011 Registry-as-dependency).

### 4.4 Метрики, що залишаються поза RED

- `github_cache_hits_total`, `github_cache_misses_total` (існують)
- `db_pool_*` через `NewPoolCollector` (існують)
- Go runtime / process collectors (існують)

### 4.5 Нові gauge-метрики outbox depth

Outbox depth (скільки рядків чекає у `release_notifications` / `confirmation_notifications` з `sent_at IS NULL`) — критичний сигнал "встигаємо обробляти?", але **наразі не інструментовано**. Додаємо як частину Phase 1.

Реалізація — `prometheus.Collector` interface у новому пакеті `internal/observability/outboxcollector` (аналогічно до існуючого `NewPoolCollector` у `internal/app/postgres.go`):

```go
type Collector struct {
    pool *pgxpool.Pool
    releaseDepth      *prometheus.Desc
    confirmationDepth *prometheus.Desc
}

func New(pool *pgxpool.Pool) *Collector

// Collect виконує два SELECT COUNT(*) запити (timeout 1s) і експонує:
//   - release_notifications_pending      (gauge)
//   - confirmation_notifications_pending (gauge)
```

Scrape-time запит `SELECT COUNT(*) FROM <table> WHERE sent_at IS NULL` на двох таблицях. Counts невеликі (outbox дрейнується кожні 30s), `count(*)` на partial-NULL-індексованій таблиці швидкий. Якщо запит впав за timeout — Collect повертає лише gauge'и, що встигли; помилку рахуємо в `outbox_collector_errors_total` (опційно).

Реєструється у тому ж Registry поряд із `NewPoolCollector` у `newMetricsRegistry`.

---

## 5. VictoriaMetrics + vmagent

### 5.1 vmagent

Окремий контейнер. Конфіг — статичний `vmagent.yml`:

```yaml
global:
  scrape_interval: 15s
  external_labels:
    service: reposeetory
    env: ${ENV}

scrape_configs:
  - job_name: reposeetory
    metrics_path: /metrics
    static_configs:
      - targets: ['${APP_METRICS_TARGET}']
        labels:
          instance: reposeetory-api
```

Запуск:

```
vmagent -promscrape.config=/etc/vmagent.yml \
        -remoteWrite.url=${VM_REMOTE_WRITE_URL} \
        -remoteWrite.tmpDataPath=/tmp/vmagent
```

`-remoteWrite.tmpDataPath` дає disk-buffer на випадок недоступності vmsingle.

### 5.2 vmsingle

```
victoria-metrics-prod \
  -storageDataPath=/storage \
  -retentionPeriod=30d \
  -httpListenAddr=:8428
```

Persistent volume `vmsingle-data` для `/storage`.

---

## 6. Grafana

### 6.1 Provisioning

Структура `infra/grafana/provisioning/`:

```
provisioning/
├── datasources/
│   └── victoria-metrics.yml
└── dashboards/
    ├── dashboards.yml
    └── reposeetory-red.json
```

**`datasources/victoria-metrics.yml`:**

```yaml
apiVersion: 1
datasources:
  - name: VictoriaMetrics
    type: prometheus
    access: proxy
    url: ${VM_QUERY_URL}
    isDefault: true
    jsonData:
      timeInterval: 15s
      httpMethod: POST
```

**`dashboards/dashboards.yml`:**

```yaml
apiVersion: 1
providers:
  - name: reposeetory
    folder: Reposeetory
    type: file
    options:
      path: /etc/grafana/provisioning/dashboards
```

Auth: `GF_AUTH_ANONYMOUS_ENABLED=true` (viewer) локально; у проді — `GF_SECURITY_ADMIN_PASSWORD`.

### 6.2 Dashboard `reposeetory-red.json` — панелі

**Variables (template):**

- `$instance` — `label_values(http_requests_total, instance)` (мульти).
- `$path` — `label_values(http_requests_total{instance=~"$instance"}, path)`.
- `$component` — custom list: `scanner|notifier|confirmer`.
- Time range default: last 1h.

#### Row 1 — HTTP RED

| # | Panel | Тип | Query |
|---|---|---|---|
| 1.1 | Request rate by path | timeseries | `sum by(path) (rate(http_requests_total{instance=~"$instance"}[5m]))` |
| 1.2 | Error ratio (5xx) | stat + sparkline | `sum(rate(http_requests_total{instance=~"$instance",status=~"5.."}[5m])) / sum(rate(http_requests_total{instance=~"$instance"}[5m]))` (threshold red >0.01) |
| 1.3 | Latency p50/p95/p99 | timeseries | `histogram_quantile(0.50/0.95/0.99, sum by(le,path) (rate(http_request_duration_seconds_bucket{instance=~"$instance",path=~"$path"}[5m])))` |
| 1.4 | Status code breakdown | timeseries stacked | `sum by(status) (rate(http_requests_total{instance=~"$instance"}[5m]))` |

#### Row 2 — Outbound RED (GitHub + Email)

| # | Panel | Query |
|---|---|---|
| 2.1 | GitHub rate by result | `sum by(result) (rate(github_client_requests_total[5m]))` |
| 2.2 | GitHub p95 latency | `histogram_quantile(0.95, sum by(le) (rate(github_client_request_duration_seconds_bucket[5m])))` |
| 2.3 | GitHub cache hit ratio | `sum(rate(github_cache_hits_total[5m])) / (sum(rate(github_cache_hits_total[5m])) + sum(rate(github_cache_misses_total[5m])))` |
| 2.4 | Email rate by driver/result | `sum by(driver, result) (rate(email_requests_total[5m]))` |
| 2.5 | Email p95 latency by driver | `histogram_quantile(0.95, sum by(le, driver) (rate(email_request_duration_seconds_bucket[5m])))` |
| 2.6 | Email error% | `sum(rate(email_requests_total{result="error"}[5m])) / sum(rate(email_requests_total[5m]))` |

#### Row 3 — Workers (Scanner / Notifier / Confirmer)

| # | Panel | Query |
|---|---|---|
| 3.1 | Tick rate by component | `sum by(__name__) (rate({__name__=~"(scanner\|notifier\|confirmer)_ticks_total"}[5m]))` |
| 3.2 | Worker error% | `sum by(__name__) (rate({__name__=~"..._ticks_total",result="error"}[5m])) / sum by(__name__) (rate({__name__=~"..._ticks_total"}[5m]))` |
| 3.3 | Scanner tick p95 | `histogram_quantile(0.95, sum by(le) (rate(scanner_tick_duration_seconds_bucket[5m])))` |
| 3.4 | Notifier tick p95 | те ж для `notifier_*` |
| 3.5 | Confirmer tick p95 | те ж для `confirmer_*` |
| 3.6 | Outbox depth (release_notifications) | `release_notifications_pending` (нова gauge, див. 4.5) |
| 3.7 | Outbox depth (confirmation_notifications) | `confirmation_notifications_pending` (нова gauge, див. 4.5) |

#### Row 4 — Runtime / DB pool / Self-metrics

| # | Panel | Query |
|---|---|---|
| 4.1 | Goroutines | `go_goroutines{instance=~"$instance"}` |
| 4.2 | Heap in use | `go_memstats_heap_inuse_bytes` |
| 4.3 | GC pause p99 | `go_gc_pause_seconds{quantile="0.99"}` |
| 4.4 | DB pool in-use / idle | існуючі `db_pool_*` |
| 4.5 | Log shipper status | `rate(logshipper_sent_total[5m])`, `rate(logshipper_dropped_total[5m])`, `rate(logshipper_failed_total[5m])` |
| 4.6 | Log shipper buffer | `logshipper_buffer_size` |

**Чому Row 4.5/4.6:** без них Vector-pipeline ламається тихо — `dropped > 0` означає, що ми втрачаємо логи навіть якщо все інше виглядає здоровим.

### 6.3 Формат дашборду

Перший заїзд — ручний JSON, експортований з Grafana UI після створення панелей. Якщо дашборд зросте → майбутній PR на Grafonnet (Jsonnet) генерацію. Зафіксовано в ADR-0017 як future-work.

---

## 7. Конфігурація, deployment, testing

### 7.1 Env-змінні (зведено)

**App-side:**

| Var | Default | Призначення |
|---|---|---|
| `VECTOR_INGEST_URL` | `""` | HTTP endpoint Vector (порожнє → шипер вимкнений) |
| `LOG_SERVICE_NAME` | `reposeetory` | Поле `service` у logs |
| `LOG_ENV` | `development` | Поле `env` у logs |
| `LOG_VERSION` | `""` | Build-time `-ldflags -X` |
| `LOG_SHIPPER_BUFFER_SIZE` | `1024` | Канал шипера |
| `LOG_SHIPPER_BATCH_SIZE` | `50` | NDJSON batch |
| `LOG_SHIPPER_FLUSH_INTERVAL` | `2s` | Макс. час між флашами |

**Infra-side (compose / Railway):**

| Var | Призначення |
|---|---|
| `VM_REMOTE_WRITE_URL` | `http://vmsingle:8428/api/v1/write` |
| `VM_QUERY_URL` | `http://vmsingle:8428` (для Grafana) |
| `APP_METRICS_TARGET` | `app:8080` локально / `<svc>.railway.internal:8080` у проді |
| `ES_URL`, `ES_USER`, `ES_PASSWORD` | Elasticsearch endpoint і креди |
| `GF_SECURITY_ADMIN_PASSWORD` | Grafana admin (прод) |

### 7.2 Локальний docker-compose

**Overlay-файл** `docker-compose.observability.yml` — не вантажиться default'ом (`make docker-up`), бо стек важкий ~3 GB RAM. Активація:

```sh
make obs-up    # docker compose -f docker-compose.yml -f docker-compose.observability.yml up -d
make obs-down  # ... down
make obs-init  # scripts/obs-init.sh — створює ILM policy, index template, alias
```

**Сервіси в overlay:**

- `vector` — `timberio/vector:0.x-alpine`, volume `vector.toml`
- `elasticsearch` — `docker.elastic.co/elasticsearch/elasticsearch:8.x`, `discovery.type=single-node`, `ES_JAVA_OPTS=-Xms512m -Xmx512m`
- `kibana` — `docker.elastic.co/kibana/kibana:8.x`
- `vmagent` — `victoriametrics/vmagent:latest`
- `vmsingle` — `victoriametrics/victoria-metrics:latest`, volume `/storage`
- `grafana` — `grafana/grafana:latest`, volume + provisioning mount

`api` service у `docker-compose.yml` отримує `VECTOR_INGEST_URL=http://vector:8686/ingest`.

### 7.3 Railway deployment (готовність)

Кожен компонент = окремий Railway-сервіс:

| Сервіс | Образ / Dockerfile | Volume | Private hostname |
|---|---|---|---|
| `app` (існує) | `Dockerfile` | — | `reposeetory.railway.internal` |
| `vector` | `timberio/vector:0.x-alpine` + `vector.toml` | — | `vector.railway.internal:8686` |
| `elasticsearch` | `docker.elastic.co/elasticsearch/elasticsearch:8.x` | persistent | `elasticsearch.railway.internal:9200` |
| `kibana` | `docker.elastic.co/kibana/kibana:8.x` | — | public domain |
| `vmagent` | `victoriametrics/vmagent:latest` + config | small disk | `vmagent.railway.internal` |
| `vmsingle` | `victoriametrics/victoria-metrics:latest` | persistent | `vmsingle.railway.internal:8428` |
| `grafana` | `grafana/grafana:latest` + image-baked provisioning | persistent | public domain |

**Assumptions-to-verify** при деплої (зафіксовано в `docs/operations/observability.md`):

- Railway private networking для нестандартних портів через `*.railway.internal`.
- Persistent volumes на поточному Railway plan для ES/VM/Grafana.
- Сукупна RAM-вимога: ES 1GB + Kibana 1GB + VM 256MB + Grafana 256MB + Vector 100MB + vmagent 100MB ≈ 2.7 GB.

### 7.4 Тестування

**Unit:**

- `internal/observability/redmetrics/redmetrics_test.go` — реєстрація з/без Registry, правильність назв (`testutil.GatherAndCompare`), nil-safe.
- `internal/observability/logshipper/logshipper_test.go` — fake HTTP server як Vector:
  - Successful batch (50 events → один POST з NDJSON body).
  - Buffer full → `Write` повертає `(n, nil)`, counter `dropped` росте.
  - Vector down → 3 retry → counter `failed` росте, app не блокується.
  - Close з deadline → флашить буфер.
- `internal/observability/emailermetrics/wrapper_test.go` — декоратор обчислює `result` правильно для nil/error.
- `internal/observability/outboxcollector/collector_test.go` — testcontainers Postgres; вставити N pending рядків → `Gather` повертає очікувані gauge-значення.
- Existing metrics tests (`scanner/notifier/confirmer`) — додати assertions на нові `_ticks_total` / `_tick_duration_seconds`.

**Integration** (build tag `integration`):

- `tests/integration/observability/logshipper_es_test.go` — ES + Vector через testcontainers; шипер шле подію → перевірити що вона з'явилася в ES через `_search`.
- Cardinality smoke test: після старту app зібрати `/metrics`, parse'ити, assert загальна кількість unique label-value combinations < 200.

**Manual (one-time):**

- `make obs-up` → `make obs-init` → запустити load test (`make load-baseline`) → перевірити Kibana (логи приходять) і Grafana (панелі заповнені).

### 7.5 Документація

| Документ | Зміст |
|---|---|
| **ADR-0017** `0017-observability-stack.md` | Vector → ES + vmagent → vmsingle + Grafana; чому ELK замість Loki; чому VM замість Prometheus; Railway-топологія. |
| **ADR-0018** `0018-red-metrics-conventions.md` | Naming convention RED, cardinality discipline, виняток для HTTP, шар `redmetrics`. |
| `docs/architecture.md` | Observability section + оновлена mermaid-діаграма. |
| `docs/operations/observability.md` (новий) | Як підняти стек локально, як deploy'итись на Railway, як налаштувати ILM, як читати дашборд, troubleshooting. |
| `CLAUDE.md` | Посилання на ADR-0017/0018, оновити "Команди" (`make obs-up/obs-down/obs-init`). |
| `CHANGELOG.md` (якщо є) | Запис про observability stack. |

---

## 8. Фази rollout

Для подальшого `writing-plans`:

1. **Phase 1 — `redmetrics` helper, RED instrumentation, outbox depth collector** (тільки app code, нуль інфри). `/metrics` експонує нові серії. Backward-compatible.
2. **Phase 2 — `logshipper` helper** (з no-op коли `VECTOR_INGEST_URL` порожній). stdout не змінюється. Backward-compatible.
3. **Phase 3 — observability docker-compose overlay** (Vector + ES + Kibana + vmagent + vmsingle + Grafana). Локальний smoke test end-to-end.
4. **Phase 4 — ILM policy, index template, initial alias, dashboard JSON** (provisioning + `obs-init.sh`).
5. **Phase 5 — Railway-deploy ескізи** (Dockerfile'и для не-стандартних компонентів, env templates, `docs/operations/observability.md` з кроками). Реальний Railway-деплой — поза цією спекою.
6. **Phase 6 — ADR-0017/0018, оновлення `architecture.md`, `CLAUDE.md`** (документація як остання фаза, щоб посилання не мутували під час реалізації).

---

## 9. Ризики та мітигація

| Ризик | Мітигація |
|---|---|
| Railway private networking для нестандартних портів не "з коробки" | Перевіряємо у Phase 5; fallback — public domains з firewall rules. |
| ES single-node на Railway хобі-tier — недостатньо RAM | Документуємо мінімальний RAM, рекомендуємо платний tier; fallback — Elastic Cloud free trial. |
| Vector втрачає логи між рестартами | Disk buffer на Vector 256MB; app-сторона fail-open. Trade-off задокументовано. |
| Cardinality explosion при додаванні нового label | Cardinality smoke test у CI; ADR-0018 явно забороняє unbounded labels. |
| Logshipper-метрики → циклічна залежність при ініціалізації | Logger створюємо ПІСЛЯ Registry (existing порядок у `app.Run`); logshipper-логи пишуться ЛИШЕ в stderr, не через сам себе. |
| Дублювання старих `notifier_emails_sent_total` із новими `email_requests_total{driver}` | У Phase 1 не видаляємо; після стабілізації — окремий cleanup-PR. |
| `LOG_PRETTY=true` ламає JSON у HTTP-каналі | `zerolog.MultiLevelWriter` дублює до форматтерів — HTTP отримує JSON завжди; задокументовано в Section 3.1. |
| Outbox-collector запити `count(*)` сповільнюють scrape під час нагрузки | Timeout 1s на запит; outbox дрейнується кожні 30s, тому розмір малий; партіальний індекс `WHERE sent_at IS NULL` робить count швидким. |

---

## 10. Відкриті питання

Жодних. Усі архітектурні розвилки закриті в ході brainstorming-сесії. Reality-checks (Railway networking, plan limits) винесені в `assumptions-to-verify`, що не блокує початок реалізації.
