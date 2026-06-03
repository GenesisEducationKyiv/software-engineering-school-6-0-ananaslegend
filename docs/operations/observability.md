# Observability stack — runbook

Covers two environments:

- **Local (docker-compose overlay)** — for development and end-to-end testing.
- **Railway** — production hosting, one Railway service per stack component.

For the *what* (specs, panels, env vars), see [`docs/superpowers/specs/2026-06-03-observability-design.md`](../superpowers/specs/2026-06-03-observability-design.md). For the *why* behind these choices see the ADR set in [`docs/adr/`](../adr/) — observability-stack and RED-conventions ADRs accompany this runbook.

## Local

### Bring up

```
make obs-up         # docker compose with the observability overlay
make obs-init       # bootstrap ES ILM, index template, alias (idempotent)
```

Then:

- App: <http://localhost:8080>
- Kibana: <http://localhost:5601> (create a data view for `reposeetory-logs-*`)
- VictoriaMetrics: <http://localhost:8428>
- Grafana: <http://localhost:3000> — dashboard "Reposeetory RED" auto-loaded under the *Reposeetory* folder.

### Tear down

```
make obs-down
```

Volumes (`es-data`, `vm-data`, `grafana-data`) persist between runs. To wipe:

```
docker compose -f docker-compose.yml -f docker-compose.observability.yml down -v
```

## Railway

Each component runs as its own Railway service in the same project; intra-service communication uses `<service>.railway.internal`.

### Services to create

| Service | Image / build | Env vars (key ones) | Volume |
|---|---|---|---|
| `vector` | `timberio/vector:0.38.0-alpine`, mount `vector.toml` via a thin Dockerfile | `ES_URL=http://elasticsearch.railway.internal:9200`, `ES_USER`, `ES_PASSWORD` | — |
| `elasticsearch` | `docker.elastic.co/elasticsearch/elasticsearch:8.13.0` | `discovery.type=single-node`, `xpack.security.enabled=true`, `ELASTIC_PASSWORD=...` | persistent at `/usr/share/elasticsearch/data` |
| `kibana` | `docker.elastic.co/kibana/kibana:8.13.0` | `ELASTICSEARCH_HOSTS=http://elasticsearch.railway.internal:9200`, `ELASTICSEARCH_USERNAME`, `ELASTICSEARCH_PASSWORD` | — |
| `vmsingle` | `victoriametrics/victoria-metrics:v1.99.0` | command flags as in `docker-compose.observability.yml` | persistent at `/storage` |
| `vmagent` | `victoriametrics/vmagent:v1.99.0`, mount `vmagent.yml` | `APP_METRICS_TARGET=<app-service>.railway.internal:8080`, `ENV=production` | — |
| `grafana` | `grafana/grafana:10.4.2` + image-baked provisioning dir | `GF_SECURITY_ADMIN_PASSWORD=...`, `GF_SERVER_ROOT_URL=...` | persistent at `/var/lib/grafana` |

### App service env vars (add via Railway dashboard)

```
VECTOR_INGEST_URL=http://vector.railway.internal:8686/ingest
LOG_ENV=production
LOG_PRETTY=false
LOG_VERSION=${{RAILWAY_GIT_COMMIT_SHA}}
```

### Bootstrap ILM after first deploy

Run `scripts/obs-init.sh` from a workstation pointed at the production ES (use a temporary port-forward or a one-off Railway shell):

```
ES_URL=https://<es-public-or-tunnel> ES_AUTH=elastic:<password> ./scripts/obs-init.sh
```

### Assumptions to verify before going live

1. **Railway private DNS for non-standard ports.** Each service's `*.railway.internal` hostname resolves from sibling services. Test with `nc -zv vector.railway.internal 8686` from the app's shell once both are deployed.
2. **Persistent volumes available on the chosen plan.** Free/Hobby tier may not expose persistent volumes for arbitrary mount paths — check the current Railway plan.
3. **Memory budget.** Sum: ES 1 GB + Kibana 1 GB + VM 256 MB + Grafana 256 MB + Vector 100 MB + vmagent 100 MB ≈ 2.7 GB. Plan accordingly.
4. **ES authentication.** Production uses `xpack.security.enabled=true`. Generate a strong `ELASTIC_PASSWORD` (Railway secret) and feed it to both Kibana and Vector.

### Kibana data view (one-time)

After first deploy: log into Kibana → *Stack Management* → *Data Views* → create `reposeetory-logs-*` with time field `@timestamp`.

## Hardening checklist (before any non-local deploy)

The local `docker-compose.observability.yml` is intentionally permissive for development convenience. Before exposing the stack on any non-local network, address EACH of these:

1. **Elasticsearch:** enable `xpack.security.enabled: "true"` and set a strong `ELASTIC_PASSWORD`. Remove the `9200:9200` port mapping (or bind to `127.0.0.1:9200:9200`) — Vector and Kibana reach ES over the compose/Railway network, not via the host.
2. **Vector ingest:** bind `8686:8686` to `127.0.0.1` only, or remove it entirely (the app reaches Vector via `http://vector:8686/ingest` over the compose network). For non-trivial deployments, configure authentication on `[sources.app_logs]` (e.g. a shared-secret header check via a `[transforms.gate]` step that filters by `.headers."x-shared-secret"`).
3. **Grafana:** set `GF_AUTH_ANONYMOUS_ENABLED=false` and provide a strong `GF_SECURITY_ADMIN_PASSWORD` via a secret/`.env` file (never `admin`). If anonymous viewer access is genuinely desired (e.g. for a public status page), bind `3000:3000` to `127.0.0.1` and front it with an authenticating reverse proxy.
4. **VictoriaMetrics:** drop the `8428:8428` host port or bind to `127.0.0.1:8428:8428`. Enable VM basic auth (`-httpAuth.username` / `-httpAuth.password`), or front with a proxy.
5. **Credentials:** never commit `changeme`-style defaults. Source `ES_USER`/`ES_PASSWORD` from `.env` (out of tree), and drop default values from `vector.toml` so a missing secret fails-closed.
6. **App `/metrics`:** the spec explicitly chose to leave this endpoint unauthenticated. If you change your mind for prod, see the route registration in `internal/httpapi/router.go` and the rejected basic-auth approach in `docs/superpowers/specs/2026-06-03-observability-design.md` Section 1 Non-goals.

## Troubleshooting

- **No logs appearing in Kibana.** Check `logshipper_dropped_total` and `logshipper_failed_total` in Grafana. `dropped{reason="full"}` → app outpacing Vector — increase `LOG_SHIPPER_BUFFER_SIZE`. `dropped{reason="fail"}` → Vector or ES unreachable.
- **Grafana dashboard "No data".** Check vmagent target health: `curl http://vmagent:8429/targets`. Also try `curl 'http://vmsingle:8428/api/v1/query?query=up'` — should return `1`.
- **ES disk filling up.** Verify ILM policy: `curl -u $ES_AUTH http://elasticsearch:9200/_ilm/policy/reposeetory-logs-policy`. Daily rollover + 7-day delete should keep usage bounded.
- **Shipper not connecting on startup.** Check `VECTOR_INGEST_URL` is set; empty value disables shipper (intentional). Logs are then stderr-only.
