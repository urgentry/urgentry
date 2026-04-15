#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_TEMPLATE="$SCRIPT_DIR/.env.example"
SMOKE_SCRIPT="$SCRIPT_DIR/smoke.sh"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
LIB_SH="$SCRIPT_DIR/lib.sh"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-urgentry-selfhosted-role-drill}"
KEEP_STACK="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"

# shellcheck disable=SC1090
source "$LIB_SH"

ENV_FILE=""
API_URL=""
INGEST_URL=""

usage() {
  cat <<'EOF'
usage: role-restart-drill.sh

Run the serious self-hosted role restart drill against a temporary Compose stack.
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
  ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/urgentry-selfhosted-role.XXXXXX")"
  cp -f "$ENV_TEMPLATE" "$ENV_FILE"
  local bootstrap_password bootstrap_pat metrics_token postgres_password minio_password
  bootstrap_password="$(generate_secret "" 28)"
  bootstrap_pat="$(generate_secret "gpat_" 28)"
  metrics_token="$(generate_secret "metrics_" 28)"
  postgres_password="$(generate_secret "" 28)"
  minio_password="$(generate_secret "" 28)"
  {
    echo "COMPOSE_PROJECT_NAME=$PROJECT_NAME"
    echo "POSTGRES_PASSWORD=${postgres_password}"
    echo "MINIO_ROOT_PASSWORD=${minio_password}"
    echo "URGENTRY_CONTROL_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:${postgres_password}@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_TELEMETRY_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:${postgres_password}@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_BOOTSTRAP_PAT=${bootstrap_pat}"
    echo "URGENTRY_BOOTSTRAP_PASSWORD=${bootstrap_password}"
    echo "URGENTRY_METRICS_TOKEN=${metrics_token}"
  } >>"$ENV_FILE"
  append_random_port_overrides "$ENV_FILE"
}

boot_stack() {
  local attempt
  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
    URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
    "$SMOKE_SCRIPT" check >/dev/null
    return 0
  fi
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
  echo "failed to boot role-restart stack after retries" >&2
  return 1
}

cleanup() {
  if [[ -n "$ENV_FILE" && -f "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" && "$KEEP_STACK" != "true" ]]; then
    docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ -n "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    rm -f "$ENV_FILE"
  fi
}

fetch_public_key() {
  local keys_json
  keys_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/keys/")"
  printf '%s' "$keys_json" | json_value '0.public'
}

post_store_event() {
  local public_key="$1"
  local event_id="$2"
  local message="$3"
  curl -fsS -X POST "$INGEST_URL/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"message\":\"${message}\",\"level\":\"error\",\"platform\":\"go\"}" >/dev/null
}

wait_for_event() {
  local event_id="$1"
  local timeout="${2:-90}"
  local deadline=$((SECONDS + timeout))
  local events_json=""
  while (( SECONDS < deadline )); do
    events_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/events/")"
    if printf '%s' "$events_json" | grep -q "$event_id"; then
      return 0
    fi
    sleep 2
  done
  echo "timed out waiting for event $event_id" >&2
  return 1
}

assert_event_absent() {
  local event_id="$1"
  local events_json
  events_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/events/")"
  if printf '%s' "$events_json" | grep -q "$event_id"; then
    echo "event $event_id unexpectedly appeared while worker was stopped" >&2
    return 1
  fi
}

wait_ready() {
  local url="$1"
  local deadline=$((SECONDS + 90))
  until curl -fsS "$url/readyz" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for $url/readyz" >&2
      return 1
    fi
    sleep 2
  done
}

wait_container_ready() {
  local service="$1"
  local timeout="${2:-120}"
  local deadline=$((SECONDS + timeout))
  local container_id status
  while (( SECONDS < deadline )); do
    container_id="$(compose ps -q "$service")"
    if [[ -z "$container_id" ]]; then
      sleep 2
      continue
    fi
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_id" 2>/dev/null || true)"
    if [[ "$status" == "healthy" || "$status" == "running" ]]; then
      return 0
    fi
    sleep 2
  done
  echo "timed out waiting for container ${service}" >&2
  return 1
}

create_backfill() {
  curl -fsS -X POST -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" -H "Content-Type: application/json" \
    "$API_URL/api/0/organizations/urgentry-org/backfills/" \
    -d '{"kind":"telemetry_rebuild","projectSlug":"default"}'
}

backfill_status() {
  local run_id="$1"
  curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
    "$API_URL/api/0/organizations/urgentry-org/backfills/${run_id}/" | json_value 'status'
}

wait_for_backfill() {
  local run_id="$1"
  local timeout="${2:-90}"
  local deadline=$((SECONDS + timeout))
  local status=""
  while (( SECONDS < deadline )); do
    status="$(backfill_status "$run_id")"
    if [[ "$status" == "completed" ]]; then
      return 0
    fi
    if [[ "$status" == "failed" || "$status" == "cancelled" ]]; then
      echo "backfill ${run_id} ended in status ${status}" >&2
      return 1
    fi
    sleep 2
  done
  echo "timed out waiting for backfill ${run_id}" >&2
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
  load_env

  boot_stack
  discover_ports
  local public_key worker_event run_json run_id scheduler_url worker_url
  public_key="$(fetch_public_key)"
  worker_url="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-worker-1" 8082)"
  scheduler_url="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-scheduler-1" 8083)"

  compose stop urgentry-worker >/dev/null
  worker_event="rolerestart$(date +%s)"
  post_store_event "$public_key" "$worker_event" "worker stopped backlog test"
  sleep 5
  assert_event_absent "$worker_event"
  compose start urgentry-worker >/dev/null
  wait_container_ready urgentry-worker 180
  wait_for_event "$worker_event" 90

  compose stop urgentry-scheduler >/dev/null
  sleep 15
  run_json="$(create_backfill)"
  run_id="$(printf '%s' "$run_json" | json_value 'id')"
  local conflict_json conflict_status
  conflict_json="$(mktemp "${TMPDIR:-/tmp}/urgentry-role-restart-conflict.XXXXXX")"
  conflict_status="$(curl -sS -o "$conflict_json" -w '%{http_code}' -X POST \
    -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" \
    -H "Content-Type: application/json" \
    "$API_URL/api/0/organizations/urgentry-org/backfills/" \
    -d '{"kind":"telemetry_rebuild"}')"
  if [[ "$conflict_status" != "409" ]]; then
    echo "expected overlapping rebuild request to return 409, got ${conflict_status}" >&2
    cat "$conflict_json" >&2
    rm -f "$conflict_json"
    exit 1
  fi
  rm -f "$conflict_json"
  sleep 4
  if [[ "$(backfill_status "$run_id")" != "pending" ]]; then
    echo "backfill ${run_id} should stay pending while scheduler is stopped" >&2
    exit 1
  fi
  compose start urgentry-scheduler >/dev/null
  wait_container_ready urgentry-scheduler 180
  wait_for_backfill "$run_id" 90

  cat <<EOF
role restart drill passed
project=$PROJECT_NAME
worker_event=$worker_event
backfill_run=$run_id
EOF
}

main "$@"
