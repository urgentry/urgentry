#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_TEMPLATE="$SCRIPT_DIR/.env.example"
SMOKE_SCRIPT="$SCRIPT_DIR/smoke.sh"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
LIB_SH="$SCRIPT_DIR/lib.sh"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-urgentry-selfhosted-active-active}"
KEEP_STACK="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"

# shellcheck disable=SC1090
source "$LIB_SH"

ENV_FILE=""
API_URL=""
INGEST_URL=""
ACTIVE_API_URL=""
ACTIVE_API_PORT=""
ACTIVE_API_CONTAINER=""

usage() {
  cat <<'EOF'
usage: active-active-drill.sh

Run the serious self-hosted active-active API and query consistency drill.
EOF
}

json_value() {
  local key="$1"
  python3 -c '
import json
import sys
value = json.load(sys.stdin)
for part in sys.argv[1].split("."):
    value = value[int(part)] if isinstance(value, list) else value[part]
print(json.dumps(value) if isinstance(value, (dict, list)) else value)
' "$key"
}

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

load_env() {
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
  API_URL="http://127.0.0.1:${URGENTRY_API_PORT}"
  INGEST_URL="http://127.0.0.1:${URGENTRY_INGEST_PORT}"
}

discover_ports() {
  API_URL="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-api-1" 8080)"
  INGEST_URL="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-ingest-1" 8081)"
}

ensure_env_file() {
  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    ENV_FILE="$URGENTRY_SELF_HOSTED_ENV_FILE"
    return 0
  fi
  ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/urgentry-selfhosted-active.XXXXXX")"
  cp -f "$ENV_TEMPLATE" "$ENV_FILE"
  {
    echo "COMPOSE_PROJECT_NAME=$PROJECT_NAME"
    echo "POSTGRES_PASSWORD=serious-selfhosted-postgres"
    echo "MINIO_ROOT_PASSWORD=serious-selfhosted-minio"
    echo "URGENTRY_CONTROL_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_TELEMETRY_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_BOOTSTRAP_PAT=gpat_serious_self_hosted_active_active"
    echo "URGENTRY_BOOTSTRAP_PASSWORD=SeriousSelfHosted!123"
    echo "URGENTRY_METRICS_TOKEN=metrics-self-hosted-active-active"
  } >>"$ENV_FILE"
  append_random_port_overrides "$ENV_FILE"
}

cleanup() {
  if [[ -n "$ACTIVE_API_CONTAINER" ]]; then
    docker rm -f "$ACTIVE_API_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [[ -n "$ENV_FILE" && -f "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" && "$KEEP_STACK" != "true" ]]; then
    docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ -n "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    rm -f "$ENV_FILE"
  fi
}

wait_http() {
  local url="$1"
  local timeout="${2:-120}"
  local deadline=$((SECONDS + timeout))
  until curl -fsS "$url" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for $url" >&2
      return 1
    fi
    sleep 2
  done
}

wait_container_ready() {
  local container="$1"
  local timeout="${2:-120}"
  local deadline=$((SECONDS + timeout))
  local status=""
  while (( SECONDS < deadline )); do
    status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || true)"
    if [[ "$status" == "healthy" || "$status" == "running" ]]; then
      return 0
    fi
    sleep 2
  done
  echo "timed out waiting for container $container" >&2
  return 1
}

ensure_stack() {
  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
    URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
    "$SMOKE_SCRIPT" check >/dev/null
    return 0
  fi
  local attempt
  for attempt in 1 2 3 4 5; do
    if URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
      URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
      URGENTRY_SELF_HOSTED_KEEP_STACK=true \
      "$SMOKE_SCRIPT" up >/dev/null; then
      return 0
    fi
    rm -f "$ENV_FILE"
    ENV_FILE=""
    ensure_env_file
    load_env
  done
  echo "failed to boot active-active stack after retries" >&2
  return 1
}

fetch_public_key() {
  local keys_json
  keys_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/keys/")"
  printf '%s' "$keys_json" | json_value '0.public'
}

seed_event() {
  local public_key="$1"
  local event_id="$2"
  curl -fsS -X POST "$INGEST_URL/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"message\":\"${event_id}\",\"level\":\"error\",\"platform\":\"go\"}" >/dev/null
}

seed_trace() {
  local public_key="$1"
  local trace_id="$2"
  local span_id="$3"
  local transaction_name="$4"
  curl -fsS -X POST "$INGEST_URL/api/default-project/otlp/v1/traces/" \
    -H "Content-Type: application/json" \
    -H "X-Sentry-Auth: Sentry sentry_key=${public_key},sentry_version=7,sentry_client=active-active-drill/1.0" \
    -d "{\"resourceSpans\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"checkout\"}}]},\"scopeSpans\":[{\"spans\":[{\"traceId\":\"${trace_id}\",\"spanId\":\"${span_id}\",\"name\":\"${transaction_name}\",\"kind\":2,\"startTimeUnixNano\":\"1743076800000000000\",\"endTimeUnixNano\":\"1743076801000000000\",\"status\":{\"code\":1}}]}]}]}" >/dev/null
}

seed_log() {
  local public_key="$1"
  local log_message="$2"
  curl -fsS -X POST "$INGEST_URL/api/default-project/otlp/v1/logs/" \
    -H "Content-Type: application/json" \
    -H "X-Sentry-Auth: Sentry sentry_key=${public_key},sentry_version=7,sentry_client=active-active-drill/1.0" \
    -d "{\"resourceLogs\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"checkout\"}}]},\"scopeLogs\":[{\"scope\":{\"name\":\"active-active-drill\"},\"logRecords\":[{\"timeUnixNano\":\"1743076800000000000\",\"severityText\":\"INFO\",\"body\":{\"stringValue\":\"${log_message}\"}}]}]}]}" >/dev/null
}

start_second_api() {
  ACTIVE_API_CONTAINER="${PROJECT_NAME}-urgentry-api-active"
  compose run -d --no-deps --name "$ACTIVE_API_CONTAINER" -p "127.0.0.1::8080" urgentry-api >/dev/null
  ACTIVE_API_PORT="$(docker_host_port "$ACTIVE_API_CONTAINER" 8080)"
  ACTIVE_API_URL="http://127.0.0.1:${ACTIVE_API_PORT}"
  wait_container_ready "$ACTIVE_API_CONTAINER" 180
  wait_http "$ACTIVE_API_URL/readyz" 180
}

wait_for_consistency() {
  local event_id="$1"
  local trace_id="$2"
  local transaction_name="$3"
  local log_message="$4"
  local events_primary events_active logs_primary logs_active tx_primary tx_active discover_primary discover_active deadline
  events_primary="$(mktemp "${TMPDIR:-/tmp}/urgentry-events-primary.XXXXXX")"
  events_active="$(mktemp "${TMPDIR:-/tmp}/urgentry-events-active.XXXXXX")"
  logs_primary="$(mktemp "${TMPDIR:-/tmp}/urgentry-logs-primary.XXXXXX")"
  logs_active="$(mktemp "${TMPDIR:-/tmp}/urgentry-logs-active.XXXXXX")"
  tx_primary="$(mktemp "${TMPDIR:-/tmp}/urgentry-tx-primary.XXXXXX")"
  tx_active="$(mktemp "${TMPDIR:-/tmp}/urgentry-tx-active.XXXXXX")"
  discover_primary="$(mktemp "${TMPDIR:-/tmp}/urgentry-discover-primary.XXXXXX")"
  discover_active="$(mktemp "${TMPDIR:-/tmp}/urgentry-discover-active.XXXXXX")"
  deadline=$((SECONDS + 90))
  while (( SECONDS < deadline )); do
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$API_URL/api/0/projects/urgentry-org/default/events/" >"$events_primary"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$ACTIVE_API_URL/api/0/projects/urgentry-org/default/events/" >"$events_active"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$API_URL/api/0/organizations/urgentry-org/logs/?limit=20" >"$logs_primary"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$ACTIVE_API_URL/api/0/organizations/urgentry-org/logs/?limit=20" >"$logs_active"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$API_URL/api/0/projects/urgentry-org/default/transactions/?limit=20" >"$tx_primary"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$ACTIVE_API_URL/api/0/projects/urgentry-org/default/transactions/?limit=20" >"$tx_active"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$API_URL/api/0/organizations/urgentry-org/discover/?scope=transactions&query=${transaction_name}&limit=10" >"$discover_primary"
    curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
      "$ACTIVE_API_URL/api/0/organizations/urgentry-org/discover/?scope=transactions&query=${transaction_name}&limit=10" >"$discover_active"
    if python3 - "$events_primary" "$events_active" "$logs_primary" "$logs_active" "$tx_primary" "$tx_active" "$discover_primary" "$discover_active" "$event_id" "$log_message" "$trace_id" <<'PY'
import json
import sys

event_primary, event_active, logs_primary, logs_active, tx_primary, tx_active, discover_primary, discover_active, event_id, log_message, trace_id = sys.argv[1:]

def load(path):
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)

def same_token_count(label, left, right, token):
    left_count = json.dumps(left, sort_keys=True).count(token)
    right_count = json.dumps(right, sort_keys=True).count(token)
    if left_count == 0 or right_count == 0 or left_count != right_count:
        raise SystemExit(1)

same_token_count("events", load(event_primary), load(event_active), event_id)
same_token_count("logs", load(logs_primary), load(logs_active), log_message)
same_token_count("transactions", load(tx_primary), load(tx_active), trace_id)
same_token_count("discover", load(discover_primary), load(discover_active), trace_id)
PY
    then
      rm -f "$events_primary" "$events_active" "$logs_primary" "$logs_active" "$tx_primary" "$tx_active" "$discover_primary" "$discover_active"
      return 0
    fi
    sleep 2
  done
  rm -f "$events_primary" "$events_active" "$logs_primary" "$logs_active" "$tx_primary" "$tx_active" "$discover_primary" "$discover_active"
  echo "timed out waiting for active-active consistency" >&2
  return 1
}

main() {
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" || "${1:-}" == "help" ]]; then
    usage
    exit 0
  fi
  if [[ $# -ne 0 ]]; then
    usage >&2
    exit 2
  fi

  trap cleanup EXIT
  ensure_env_file
  ensure_stack
  load_env
  discover_ports
  start_second_api

  local public_key event_id trace_id span_id transaction_name log_message
  public_key="$(fetch_public_key)"
  event_id="active-active-event-$(date +%s)"
  trace_id="$(printf '%032x' "$(( ($(date +%s) % 65535) + 1 ))")$(printf '%016x' "$RANDOM")"
  trace_id="${trace_id:0:32}"
  span_id="$(printf '%016x' "$((RANDOM + 1000))")"
  transaction_name="active-active-txn-$(date +%s)"
  log_message="active-active-log-$(date +%s)"

  seed_event "$public_key" "$event_id"
  seed_trace "$public_key" "$trace_id" "$span_id" "$transaction_name"
  seed_log "$public_key" "$log_message"
  wait_for_consistency "$event_id" "$trace_id" "$transaction_name" "$log_message"

  cat <<EOF
active-active drill passed
project=$PROJECT_NAME
primary_api=$API_URL
secondary_api=$ACTIVE_API_URL
event_id=$event_id
trace_id=$trace_id
EOF
}

main "$@"
