#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_TEMPLATE="$SCRIPT_DIR/.env.example"
SMOKE_SCRIPT="$SCRIPT_DIR/smoke.sh"
BACKUP_SCRIPT="$SCRIPT_DIR/backup.sh"
RESTORE_SCRIPT="$SCRIPT_DIR/restore.sh"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
LIB_SH="$SCRIPT_DIR/lib.sh"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-urgentry-selfhosted-backup-drill}"
KEEP_STACK="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"

# shellcheck disable=SC1090
source "$LIB_SH"

ENV_FILE=""
BACKUP_DIR=""
API_URL=""
INGEST_URL=""

usage() {
  cat <<'EOF'
usage: backup-restore-drill.sh

Run the serious self-hosted backup/restore disaster-recovery drill against a temporary Compose stack.
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

ensure_env_file() {
  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    ENV_FILE="$URGENTRY_SELF_HOSTED_ENV_FILE"
    return 0
  fi
  ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/urgentry-selfhosted-drill.XXXXXX")"
  cp -f "$ENV_TEMPLATE" "$ENV_FILE"
  {
    echo "COMPOSE_PROJECT_NAME=$PROJECT_NAME"
    echo "POSTGRES_PASSWORD=serious-selfhosted-postgres"
    echo "MINIO_ROOT_PASSWORD=serious-selfhosted-minio"
    echo "URGENTRY_CONTROL_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_TELEMETRY_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable"
    echo "URGENTRY_BOOTSTRAP_PAT=gpat_serious_self_hosted_drill"
    echo "URGENTRY_BOOTSTRAP_PASSWORD=SeriousSelfHosted!123"
    echo "URGENTRY_METRICS_TOKEN=metrics-self-hosted-drill"
  } >>"$ENV_FILE"
  append_random_port_overrides "$ENV_FILE"
}

reset_bootstrap_env() {
  if [[ -n "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    return 0
  fi
  if [[ -n "$ENV_FILE" && -f "$ENV_FILE" ]]; then
    docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f "$ENV_FILE"
  fi
  ENV_FILE=""
}

cleanup() {
  if [[ -n "$ENV_FILE" && -f "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" && "$KEEP_STACK" != "true" ]]; then
    docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  if [[ -n "$ENV_FILE" && -z "${URGENTRY_SELF_HOSTED_ENV_FILE:-}" ]]; then
    rm -f "$ENV_FILE"
  fi
  if [[ -n "$BACKUP_DIR" && "$KEEP_STACK" != "true" ]]; then
    rm -rf "$BACKUP_DIR"
  fi
}

fetch_public_key() {
  local keys_json
  keys_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/keys/")"
  printf '%s' "$keys_json" | json_value '0.public'
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
    echo "event $event_id should be absent after restore" >&2
    return 1
  fi
}

post_store_event() {
  local public_key="$1"
  local event_id="$2"
  local message="$3"
  curl -fsS -X POST "$INGEST_URL/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"message\":\"${message}\",\"level\":\"error\",\"platform\":\"go\"}" >/dev/null
}

upload_attachment() {
  local event_id="$1"
  local content="$2"
  local tmpfile upload_json
  tmpfile="$(mktemp "${TMPDIR:-/tmp}/urgentry-drill-attachment.XXXXXX")"
  printf '%s' "$content" >"$tmpfile"
  upload_json="$(curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" -F "event_id=${event_id}" -F "file=@${tmpfile};filename=drill.txt" "$API_URL/api/0/projects/urgentry-org/default/attachments/")"
  rm -f "$tmpfile"
  printf '%s' "$upload_json" | json_value 'id'
}

attachment_body() {
  local event_id="$1"
  local attachment_id="$2"
  curl -fsS -H "Authorization: Bearer ${URGENTRY_BOOTSTRAP_PAT}" "$API_URL/api/0/events/${event_id}/attachments/${attachment_id}/"
}

assert_status_matches_backup() {
  python3 - "$BACKUP_DIR/status.json" "$BACKUP_DIR/restore-status.json" <<'PY'
import json
import sys
before = json.load(open(sys.argv[1], "r", encoding="utf-8"))
after = json.load(open(sys.argv[2], "r", encoding="utf-8"))
if before != after:
    raise SystemExit(f"status mismatch: {before!r} != {after!r}")
PY
}

boot_stack() {
  local attempt
  for attempt in 1 2 3 4 5; do
    ensure_env_file
    if URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
      URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
      URGENTRY_SELF_HOSTED_KEEP_STACK=true \
      "$SMOKE_SCRIPT" up >/dev/null; then
      load_env
      return 0
    fi
    reset_bootstrap_env
  done
  echo "failed to boot compose drill stack after retries" >&2
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

  BACKUP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-selfhosted-backup.XXXXXX")"
  trap cleanup EXIT

  boot_stack

  local public_key pre_event queued_event post_backup_event attachment_id
  public_key="$(fetch_public_key)"

  pre_event="drillpre$(date +%s)"
  post_store_event "$public_key" "$pre_event" "backup restore drill baseline"
  wait_for_event "$pre_event" 90
  attachment_id="$(upload_attachment "$pre_event" "backup-drill-${pre_event}")"

  compose stop urgentry-worker >/dev/null
  queued_event="drillqueued$(date +%s)"
  post_store_event "$public_key" "$queued_event" "queued before backup"
  sleep 5
  assert_event_absent "$queued_event"

  URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
  URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
  "$BACKUP_SCRIPT" "$BACKUP_DIR" >/dev/null

  URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
  URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
  "$RESTORE_SCRIPT" --verify-only "$BACKUP_DIR" >/dev/null

  post_backup_event="drillpost$(date +%s)"
  post_store_event "$public_key" "$post_backup_event" "created after backup"

  URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
  URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
  "$RESTORE_SCRIPT" "$BACKUP_DIR" >/dev/null

  load_env
  URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
  URGENTRY_SELF_HOSTED_PROJECT="$PROJECT_NAME" \
  "$SMOKE_SCRIPT" check >/dev/null

  wait_for_event "$pre_event" 90
  wait_for_event "$queued_event" 90
  assert_event_absent "$post_backup_event"
  if [[ "$(attachment_body "$pre_event" "$attachment_id")" != "backup-drill-${pre_event}" ]]; then
    echo "attachment body mismatch after restore" >&2
    exit 1
  fi
  assert_status_matches_backup

  cat <<EOF
backup restore drill passed
project=$PROJECT_NAME
backup_dir=$BACKUP_DIR
pre_event=$pre_event
queued_event=$queued_event
restored_attachment=$attachment_id
EOF
}

main "$@"
