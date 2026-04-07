#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
ENV_TEMPLATE="$SCRIPT_DIR/.env.example"
LIB_SH="$SCRIPT_DIR/lib.sh"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-}"
KEEP_STACK="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"
UP_ATTEMPTS="${URGENTRY_SELF_HOSTED_SMOKE_ATTEMPTS:-3}"

# shellcheck disable=SC1090
source "$LIB_SH"

API_URL=""
INGEST_URL=""
WORKER_URL=""
SCHEDULER_URL=""
ENV_FILE=""
GENERATED_ENV_FILE="false"

usage() {
  cat <<'EOF'
usage: smoke.sh [up|check]

Commands:
  up     Boot the Compose stack, run the smoke flow, then tear it down unless URGENTRY_SELF_HOSTED_KEEP_STACK=true.
  check  Run the smoke flow against an already running stack using the provided env file or defaults.

Optional environment:
  URGENTRY_SELF_HOSTED_PROJECT     docker compose project name
  URGENTRY_SELF_HOSTED_ENV_FILE    existing env file to use instead of a generated temp file
  URGENTRY_SELF_HOSTED_KEEP_STACK  keep the stack running after smoke (true|false)
EOF
}

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
        continue
if isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
' "$key"
}

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
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

wait_service_success() {
  local service="$1"
  local timeout="${2:-180}"
  local deadline=$((SECONDS + timeout))
  local container_id
  local status
  local exit_code

  while (( SECONDS < deadline )); do
    container_id="$(compose ps --all -q "$service")"
    if [[ -z "$container_id" ]]; then
      sleep 2
      continue
    fi
    status="$(docker inspect -f '{{.State.Status}}' "$container_id")"
    exit_code="$(docker inspect -f '{{.State.ExitCode}}' "$container_id")"
    if [[ "$status" == "exited" && "$exit_code" == "0" ]]; then
      return 0
    fi
    if [[ "$status" == "exited" && "$exit_code" != "0" ]]; then
      docker logs "$container_id" >&2 || true
      echo "service $service exited with code $exit_code" >&2
      return 1
    fi
    sleep 2
  done

  container_id="$(compose ps --all -q "$service")"
  if [[ -n "$container_id" ]]; then
    docker logs "$container_id" >&2 || true
  fi
  echo "timed out waiting for $service to complete successfully" >&2
  return 1
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

render_urls() {
  local api_port ingest_port worker_port scheduler_port
  api_port="$(grep '^URGENTRY_API_PORT=' "$ENV_FILE" | tail -1 | cut -d= -f2)"
  ingest_port="$(grep '^URGENTRY_INGEST_PORT=' "$ENV_FILE" | tail -1 | cut -d= -f2)"
  worker_port="$(grep '^URGENTRY_WORKER_PORT=' "$ENV_FILE" | tail -1 | cut -d= -f2)"
  scheduler_port="$(grep '^URGENTRY_SCHEDULER_PORT=' "$ENV_FILE" | tail -1 | cut -d= -f2)"
  if [[ -z "$api_port" || "$api_port" == "0" ]]; then
    api_port="$(docker_host_port "${PROJECT_NAME}-urgentry-api-1" 8080)"
  fi
  if [[ -z "$ingest_port" || "$ingest_port" == "0" ]]; then
    ingest_port="$(docker_host_port "${PROJECT_NAME}-urgentry-ingest-1" 8081)"
  fi
  if [[ -z "$worker_port" || "$worker_port" == "0" ]]; then
    worker_port="$(docker_host_port "${PROJECT_NAME}-urgentry-worker-1" 8082)"
  fi
  if [[ -z "$scheduler_port" || "$scheduler_port" == "0" ]]; then
    scheduler_port="$(docker_host_port "${PROJECT_NAME}-urgentry-scheduler-1" 8083)"
  fi
  API_URL="http://127.0.0.1:${api_port}"
  INGEST_URL="http://127.0.0.1:${ingest_port}"
  WORKER_URL="http://127.0.0.1:${worker_port}"
  SCHEDULER_URL="http://127.0.0.1:${scheduler_port}"
}

ensure_env_file() {
  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    ENV_FILE="$URGENTRY_SELF_HOSTED_ENV_FILE"
    return 0
  fi

  if [[ -z "$PROJECT_NAME" ]]; then
    PROJECT_NAME="urgentry-selfhosted-smoke-$$"
  fi

  ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/urgentry-selfhosted-env.XXXXXX")"
  GENERATED_ENV_FILE="true"
  cp -f "$ENV_TEMPLATE" "$ENV_FILE"
  {
    echo "COMPOSE_PROJECT_NAME=${PROJECT_NAME}"
    echo "POSTGRES_PASSWORD=serious-selfhosted-postgres"
    echo "MINIO_ROOT_PASSWORD=serious-selfhosted-minio"
    echo "URGENTRY_CONTROL_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_TELEMETRY_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_BOOTSTRAP_PAT=gpat_serious_self_hosted_smoke"
    echo "URGENTRY_BOOTSTRAP_PASSWORD=SeriousSelfHosted!123"
    echo "URGENTRY_METRICS_TOKEN=metrics-self-hosted-smoke"
  } >>"$ENV_FILE"
  append_random_port_overrides "$ENV_FILE"
}

resolve_project_name() {
  if [[ -n "$PROJECT_NAME" ]]; then
    return 0
  fi
  PROJECT_NAME="$(grep '^COMPOSE_PROJECT_NAME=' "$ENV_FILE" | tail -1 | cut -d= -f2)"
  if [[ -z "$PROJECT_NAME" ]]; then
    PROJECT_NAME="urgentry-selfhosted-smoke"
  fi
}

cleanup() {
  if [[ "$KEEP_STACK" != "true" && -n "$ENV_FILE" ]]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ -n "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    rm -f "$ENV_FILE"
  fi
}

load_env() {
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
}

wait_stack() {
  wait_service_success minio-bootstrap
  wait_service_success urgentry-bootstrap
  wait_service_ready urgentry-api
  wait_service_ready urgentry-ingest
  wait_service_ready urgentry-worker
  wait_service_ready urgentry-scheduler
  wait_http "$API_URL/readyz"
  wait_http "$INGEST_URL/readyz"
}

check_storage_services() {
  local minio_port="${MINIO_API_PORT:-}"
  local nats_monitor_port="${NATS_MONITOR_PORT:-}"
  if [[ -z "$minio_port" || "$minio_port" == "0" ]]; then
    minio_port="$(docker_host_port "${PROJECT_NAME}-minio-1" 9000)"
  fi
  if [[ -z "$nats_monitor_port" || "$nats_monitor_port" == "0" ]]; then
    nats_monitor_port="$(docker_host_port "${PROJECT_NAME}-nats-1" 8222)"
  fi
  wait_http "http://127.0.0.1:${minio_port}/minio/health/live"
  wait_http "http://127.0.0.1:${nats_monitor_port}/healthz"
  compose exec -T postgres pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null
  [[ "$(compose exec -T valkey valkey-cli ping | tr -d '\r')" == "PONG" ]]
}

reset_generated_env_file() {
  if [[ -n "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    rm -f "$ENV_FILE"
  fi
  ENV_FILE=""
  GENERATED_ENV_FILE="false"
}

boot_stack() {
  local attempt output

  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    compose up -d --build
    return 0
  fi

  for attempt in 1 2 3 4 5; do
    output="$(mktemp "${TMPDIR:-/tmp}/urgentry-compose-up.XXXXXX")"
    if compose up -d --build >"$output" 2>&1; then
      cat "$output"
      rm -f "$output"
      return 0
    fi
    if ! command_hit_port_conflict "$output"; then
      cat "$output" >&2
      rm -f "$output"
      return 1
    fi
    compose down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f "$output"
    reset_generated_env_file
    ensure_env_file
    resolve_project_name
    load_env
    render_urls
  done

  echo "failed to boot compose smoke stack after port-conflict retries" >&2
  return 1
}

prepare_retry_attempt() {
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  if [[ -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    reset_generated_env_file
    ensure_env_file
    resolve_project_name
    load_env
  fi
}

assert_runtime_backends() {
  local response async_backend cache_backend
  response="$(curl -fsS "$API_URL/healthz")"
  async_backend="$(printf '%s' "$response" | json_value 'async_backend')"
  cache_backend="$(printf '%s' "$response" | json_value 'cache_backend')"
  if [[ "$async_backend" != "${URGENTRY_ASYNC_BACKEND}" ]]; then
    echo "unexpected async backend: got $async_backend want ${URGENTRY_ASYNC_BACKEND}" >&2
    return 1
  fi
  if [[ "$cache_backend" != "${URGENTRY_CACHE_BACKEND}" ]]; then
    echo "unexpected cache backend: got $cache_backend want ${URGENTRY_CACHE_BACKEND}" >&2
    return 1
  fi
}

smoke_event_flow() {
  local keys_json public_key event_id events_json upload_json attachments_json attachment_id attachment_body tmpfile
  keys_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/keys/")"
  public_key="$(printf '%s' "$keys_json" | json_value '0.public')"

  event_id="smoke$(date +%s)"
  curl -fsS -X POST "$INGEST_URL/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"message\":\"self-hosted smoke\",\"level\":\"error\",\"platform\":\"go\"}" >/dev/null

  local deadline=$((SECONDS + 90))
  while (( SECONDS < deadline )); do
    events_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/events/")"
    if printf '%s' "$events_json" | grep -q "$event_id"; then
      break
    fi
    sleep 2
  done
  if ! printf '%s' "$events_json" | grep -q "$event_id"; then
    echo "event $event_id did not appear in API list" >&2
    return 1
  fi

  tmpfile="$(mktemp "${TMPDIR:-/tmp}/urgentry-attachment.XXXXXX")"
  printf 'blob-smoke-%s' "$event_id" >"$tmpfile"
  upload_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" -F "event_id=${event_id}" -F "file=@${tmpfile};filename=smoke.txt" "$API_URL/api/0/projects/urgentry-org/default/attachments/")"
  rm -f "$tmpfile"

  attachment_id="$(printf '%s' "$upload_json" | json_value 'id')"
  attachments_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/events/${event_id}/attachments/")"
  if ! printf '%s' "$attachments_json" | grep -q "$attachment_id"; then
    echo "attachment $attachment_id did not appear in event attachment list" >&2
    return 1
  fi

  attachment_body="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/events/${event_id}/attachments/${attachment_id}/")"
  if [[ "$attachment_body" != "blob-smoke-${event_id}" ]]; then
    echo "attachment body mismatch" >&2
    return 1
  fi
}

run_smoke_flow() {
  local command="$1"
  if [[ "$command" == "up" ]]; then
    boot_stack
  fi
  render_urls
  wait_stack
  check_storage_services
  assert_runtime_backends
  smoke_event_flow
}

main() {
  local command="${1:-up}"
  case "$command" in
    up|check) ;;
    -h|--help|help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac

  ensure_env_file
  resolve_project_name
  load_env

  if [[ "$command" == "up" ]]; then
    trap cleanup EXIT
    local attempt smoke_log status
    for attempt in $(seq 1 "$UP_ATTEMPTS"); do
      smoke_log="$(mktemp "${TMPDIR:-/tmp}/urgentry-selfhosted-smoke.XXXXXX")"
      set +e
      run_smoke_flow "$command" >"$smoke_log" 2>&1
      status=$?
      set -e
      if [[ "$status" == "0" ]]; then
        cat "$smoke_log"
        rm -f "$smoke_log"
        break
      fi
      if [[ "$attempt" == "$UP_ATTEMPTS" ]]; then
        cat "$smoke_log" >&2
        rm -f "$smoke_log"
        exit "$status"
      fi
      prepare_retry_attempt
      rm -f "$smoke_log"
      sleep 2
    done
  else
    run_smoke_flow "$command"
  fi

  cat <<EOF
compose smoke passed
api=$API_URL
ingest=$INGEST_URL
worker=$WORKER_URL
scheduler=$SCHEDULER_URL
project=$PROJECT_NAME
EOF
}

main "$@"
