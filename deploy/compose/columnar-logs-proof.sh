#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
ENV_TEMPLATE="$SCRIPT_DIR/.env.example"
LIB_SH="$SCRIPT_DIR/lib.sh"
SMOKE_SH="$SCRIPT_DIR/smoke.sh"

# shellcheck disable=SC1090
source "$LIB_SH"

ENV_FILE=""
PROJECT_NAME=""
KEEP_STACK="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"

json_value() {
  local key="$1"
  python3 -c '
import json
import sys

key = sys.argv[1]
value = json.load(sys.stdin)
for part in key.split("."):
    if isinstance(value, list):
        value = value[int(part)]
    else:
        value = value[part]
if isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
' "$key"
}

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

wait_service_ready() {
  local service="$1"
  local timeout="${2:-180}"
  local deadline=$((SECONDS + timeout))
  local container_id
  local status

  while (( SECONDS < deadline )); do
    container_id="$(compose ps --all -q "$service")"
    if [[ -z "$container_id" ]]; then
      sleep 2
      continue
    fi
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_id" 2>/dev/null || true)"
    if [[ "$status" == "healthy" || "$status" == "running" ]]; then
      return 0
    fi
    if [[ "$status" == "exited" || "$status" == "dead" ]]; then
      docker logs "$container_id" >&2 || true
      echo "service $service failed with status $status" >&2
      return 1
    fi
    sleep 2
  done

  container_id="$(compose ps --all -q "$service")"
  if [[ -n "$container_id" ]]; then
    docker logs "$container_id" >&2 || true
  fi
  echo "timed out waiting for $service to become ready" >&2
  return 1
}

cleanup() {
  if [[ "$KEEP_STACK" != "true" && -n "$ENV_FILE" ]]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ -n "$ENV_FILE" ]]; then
    rm -f "$ENV_FILE"
  fi
}

create_env_file() {
  PROJECT_NAME="urgentry-columnar-proof-$$"
  ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/urgentry-columnar-env.XXXXXX")"
  cp -f "$ENV_TEMPLATE" "$ENV_FILE"
  local bootstrap_password bootstrap_pat metrics_token postgres_password minio_password clickhouse_password
  bootstrap_password="$(generate_secret "" 28)"
  bootstrap_pat="$(generate_secret "gpat_" 28)"
  metrics_token="$(generate_secret "metrics_" 28)"
  postgres_password="$(generate_secret "" 28)"
  minio_password="$(generate_secret "" 28)"
  clickhouse_password="$(generate_secret "" 28)"
  {
    echo "COMPOSE_PROJECT_NAME=${PROJECT_NAME}"
    echo "COMPOSE_PROFILES=columnar"
    echo "URGENTRY_BUILD_TAGS=netgo,osusergo,clickhouse"
    echo "POSTGRES_PASSWORD=${postgres_password}"
    echo "MINIO_ROOT_PASSWORD=${minio_password}"
    echo "URGENTRY_CONTROL_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:${postgres_password}@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_TELEMETRY_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:${postgres_password}@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_BOOTSTRAP_PAT=${bootstrap_pat}"
    echo "URGENTRY_BOOTSTRAP_PASSWORD=${bootstrap_password}"
    echo "URGENTRY_METRICS_TOKEN=${metrics_token}"
    echo "CLICKHOUSE_DB=urgentry"
    echo "CLICKHOUSE_USER=urgentry"
    echo "CLICKHOUSE_PASSWORD=${clickhouse_password}"
    echo "URGENTRY_COLUMNAR_DATABASE_URL=clickhouse://urgentry:${clickhouse_password}@clickhouse:9000/urgentry?dial_timeout=30s"
    echo "URGENTRY_COLUMNAR_BACKEND=clickhouse"
  } >>"$ENV_FILE"
  append_random_port_overrides "$ENV_FILE"
}

parse_smoke_value() {
  local key="$1"
  local file="$2"
  awk -F= -v wanted="$key" '$1 == wanted {print $2}' "$file" | tail -1
}

wait_for_backfill() {
  local api_url="$1"
  local run_id="$2"
  local deadline=$((SECONDS + 180))
  local body status

  while (( SECONDS < deadline )); do
    body="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$api_url/api/0/organizations/urgentry-org/backfills/${run_id}/")"
    status="$(printf '%s' "$body" | json_value status)"
    case "$status" in
      completed)
        return 0
        ;;
      failed|cancelled)
        echo "telemetry rebuild ${run_id} ended with status ${status}" >&2
        printf '%s\n' "$body" >&2
        return 1
        ;;
    esac
    sleep 2
  done

  echo "timed out waiting for telemetry rebuild ${run_id}" >&2
  return 1
}

main() {
  trap cleanup EXIT
  create_env_file

  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a

  compose up -d clickhouse
  wait_service_ready clickhouse

  local smoke_log api_url ingest_url keys_json public_key event_id trace_id span_id
  local rebuild_json rebuild_id logs_json row_count query_term

  smoke_log="$(mktemp "${TMPDIR:-/tmp}/urgentry-columnar-smoke.XXXXXX")"
  URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" URGENTRY_SELF_HOSTED_KEEP_STACK=true bash "$SMOKE_SH" up | tee "$smoke_log"

  api_url="$(parse_smoke_value api "$smoke_log")"
  ingest_url="$(parse_smoke_value ingest "$smoke_log")"
  rm -f "$smoke_log"

  keys_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$api_url/api/0/projects/urgentry-org/default/keys/")"
  public_key="$(printf '%s' "$keys_json" | json_value '0.public')"
  event_id="columnar$(date +%s)"
  trace_id="${event_id}trace000000000000000000"
  trace_id="${trace_id:0:32}"
  span_id="${event_id}span0000"
  span_id="${span_id:0:16}"
  query_term="columnar-proof-${event_id}"

  curl -fsS -X POST "$ingest_url/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"type\":\"log\",\"message\":\"${query_term} log payload\",\"logger\":\"${query_term}\",\"level\":\"info\",\"platform\":\"go\",\"release\":\"columnar-proof@1.0.0\",\"environment\":\"benchmark\",\"contexts\":{\"trace\":{\"trace_id\":\"${trace_id}\",\"span_id\":\"${span_id}\"}},\"tags\":{\"proof\":\"columnar\"}}" >/dev/null

  rebuild_json="$(curl -fsS -X POST \
    -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
    -H "Content-Type: application/json" \
    -d '{"kind":"telemetry_rebuild"}' \
    "$api_url/api/0/organizations/urgentry-org/backfills/")"
  rebuild_id="$(printf '%s' "$rebuild_json" | json_value id)"
  wait_for_backfill "$api_url" "$rebuild_id"

  row_count="$(compose exec -T clickhouse clickhouse-client --user "$CLICKHOUSE_USER" --password "$CLICKHOUSE_PASSWORD" --database "$CLICKHOUSE_DB" --query "SELECT count() FROM telemetry_log_facts FINAL WHERE event_id = '${event_id}' FORMAT TSVRaw")"
  if [[ "$row_count" != "1" ]]; then
    echo "unexpected ClickHouse log fact count for ${event_id}: ${row_count}" >&2
    exit 1
  fi

  logs_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$api_url/api/0/organizations/urgentry-org/logs/?query=${query_term}&limit=10")"
  if ! printf '%s' "$logs_json" | grep -q "\"eventId\":\"${event_id}\""; then
    echo "columnar logs proof did not return ${event_id} from the org logs API" >&2
    printf '%s\n' "$logs_json" >&2
    exit 1
  fi

  cat <<EOF
columnar logs proof passed
project=${PROJECT_NAME}
api=${api_url}
ingest=${ingest_url}
event_id=${event_id}
backfill_id=${rebuild_id}
query=${query_term}
EOF
}

main "$@"
