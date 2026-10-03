#!/usr/bin/env bash
# Bootstraps Elasticsearch index template, ILM policy, and initial rollover
# alias for reposeetory logs. Idempotent: re-running is safe.

set -euo pipefail

ES_URL="${ES_URL:-http://localhost:9200}"
ES_AUTH="${ES_AUTH:-elastic:changeme}"

echo "-> Creating ILM policy reposeetory-logs-policy"
curl -fsS -u "$ES_AUTH" -X PUT "$ES_URL/_ilm/policy/reposeetory-logs-policy" \
  -H 'Content-Type: application/json' -d '{
  "policy": {
    "phases": {
      "hot":    { "actions": { "rollover": { "max_age": "1d", "max_primary_shard_size": "1gb" } } },
      "delete": { "min_age": "7d", "actions": { "delete": {} } }
    }
  }
}' >/dev/null
echo "  ok"

echo "-> Creating index template reposeetory-logs"
curl -fsS -u "$ES_AUTH" -X PUT "$ES_URL/_index_template/reposeetory-logs" \
  -H 'Content-Type: application/json' -d '{
  "index_patterns": ["reposeetory-logs-*"],
  "template": {
    "settings": {
      "index.lifecycle.name": "reposeetory-logs-policy",
      "index.lifecycle.rollover_alias": "reposeetory-logs",
      "number_of_shards": 1,
      "number_of_replicas": 0
    },
    "mappings": {
      "dynamic": true,
      "properties": {
        "@timestamp":  { "type": "date" },
        "level":       { "type": "keyword" },
        "service":     { "type": "keyword" },
        "env":         { "type": "keyword" },
        "version":     { "type": "keyword" },
        "host":        { "type": "keyword" },
        "request_id":  { "type": "keyword" },
        "method":      { "type": "keyword" },
        "path":        { "type": "keyword" },
        "status":      { "type": "integer" },
        "duration_ms": { "type": "long" },
        "message":     { "type": "text", "fields": { "keyword": { "type": "keyword", "ignore_above": 1024 } } }
      }
    }
  }
}' >/dev/null
echo "  ok"

echo "-> Creating initial rollover index reposeetory-logs-000001"
if curl -fsS -u "$ES_AUTH" -X HEAD "$ES_URL/reposeetory-logs-000001" >/dev/null 2>&1; then
  echo "  already exists"
else
  curl -fsS -u "$ES_AUTH" -X PUT "$ES_URL/reposeetory-logs-000001" \
    -H 'Content-Type: application/json' -d '{
    "aliases": { "reposeetory-logs": { "is_write_index": true } }
  }' >/dev/null
  echo "  ok"
fi

echo "Done."
