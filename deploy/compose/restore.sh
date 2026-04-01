#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
DEFAULT_ENV_FILE="$SCRIPT_DIR/.env"
ENV_FILE="${URGENTRY_SELF_HOSTED_ENV_FILE:-$DEFAULT_ENV_FILE}"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-}"
APP_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
OPERATOR_ACTOR="${URGENTRY_OPERATOR_ACTOR:-${USER:-compose}}"

usage() {
  cat <<'EOF'
usage: restore.sh <backup-dir>
usage: restore.sh --verify-only <backup-dir>

Restore a serious self-hosted Compose bundle from a backup captured by backup.sh.

Environment:
  URGENTRY_SELF_HOSTED_ENV_FILE  path to the compose env file (defaults to deploy/compose/.env)
  URGENTRY_SELF_HOSTED_PROJECT   override compose project name; defaults to COMPOSE_PROJECT_NAME from the env file
EOF
}

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

record_action() {
  local action="$1"
  local detail="$2"
  compose exec -T urgentry-api sh -lc 'urgentry self-hosted record-action --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --action "$1" --source compose --actor "$2" --detail "$3"' sh "$action" "$OPERATOR_ACTOR" "$detail" >/dev/null
}

wait_service_success() {
  local service="$1"
  local timeout="${2:-180}"
  local deadline=$((SECONDS + timeout))
  local container_id status exit_code

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
  echo "timed out waiting for $service to complete successfully" >&2
  return 1
}

wait_service_healthy() {
  local service="$1"
  local timeout="${2:-180}"
  local deadline=$((SECONDS + timeout))
  local container_id status

  while (( SECONDS < deadline )); do
    container_id="$(compose ps -q "$service")"
    if [[ -z "$container_id" ]]; then
      sleep 2
      continue
    fi
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_id")"
    if [[ "$status" == "healthy" || "$status" == "running" ]]; then
      return 0
    fi
    sleep 2
  done
  echo "timed out waiting for $service to become healthy" >&2
  return 1
}

restore_volume() {
  local archive_file="$1"
  local volume_name="$2"
  docker run --rm -i -v "${volume_name}:/data" alpine:3.20 sh -lc 'set -eu; mkdir -p /data; find /data -mindepth 1 -exec rm -rf {} +; tar -xzf - -C /data' <"$archive_file"
}

main() {
  local verify_only=false
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" || "${1:-}" == "help" ]]; then
    usage
    exit 0
  fi
  if [[ "${1:-}" == "--verify-only" ]]; then
    verify_only=true
    shift
  fi
  if [[ $# -ne 1 ]]; then
    usage >&2
    exit 2
  fi
  if [[ ! -f "$ENV_FILE" ]]; then
    echo "compose env file not found: $ENV_FILE" >&2
    exit 1
  fi

  local backup_dir="$1"
  local required=(
    "$backup_dir/manifest.json"
    "$backup_dir/postgres.sql.gz"
    "$backup_dir/urgentry-data.tar.gz"
    "$backup_dir/minio-data.tar.gz"
    "$backup_dir/nats-data.tar.gz"
    "$backup_dir/valkey-data.tar.gz"
  )
  local file
  for file in "${required[@]}"; do
    if [[ ! -f "$file" ]]; then
      echo "backup artifact missing: $file" >&2
      exit 1
    fi
  done

  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
  if [[ -z "$PROJECT_NAME" ]]; then
    PROJECT_NAME="${COMPOSE_PROJECT_NAME:-urgentry-selfhosted}"
  fi

  (
    cd "$APP_DIR"
    go run ./cmd/urgentry self-hosted verify-backup \
      --dir "$backup_dir" \
      --telemetry-backend "${URGENTRY_TELEMETRY_BACKEND:-postgres}" \
      --strict-target-match=false
  ) >"$backup_dir/verify-backup.json"
  if [[ "$verify_only" == "true" ]]; then
    cat <<EOF
backup verification passed
dir=$backup_dir
project=$PROJECT_NAME
verification=$backup_dir/verify-backup.json
EOF
    exit 0
  fi

  compose down -v --remove-orphans

  restore_volume "$backup_dir/urgentry-data.tar.gz" "${PROJECT_NAME}_urgentry_data"
  restore_volume "$backup_dir/minio-data.tar.gz" "${PROJECT_NAME}_minio_data"
  restore_volume "$backup_dir/nats-data.tar.gz" "${PROJECT_NAME}_nats_data"
  restore_volume "$backup_dir/valkey-data.tar.gz" "${PROJECT_NAME}_valkey_data"

  compose up -d postgres minio valkey nats
  wait_service_healthy postgres
  wait_service_healthy minio
  wait_service_healthy valkey
  wait_service_healthy nats

  compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -c "DROP DATABASE IF EXISTS \"$POSTGRES_DB\" WITH (FORCE);"
  compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres -c "CREATE DATABASE \"$POSTGRES_DB\";"
  gunzip -c "$backup_dir/postgres.sql.gz" | compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" >/dev/null

  compose up -d minio-bootstrap urgentry-bootstrap urgentry-api urgentry-ingest urgentry-worker urgentry-scheduler
  wait_service_success minio-bootstrap
  wait_service_success urgentry-bootstrap
  wait_service_healthy urgentry-api
  wait_service_healthy urgentry-ingest
  wait_service_healthy urgentry-worker
  wait_service_healthy urgentry-scheduler

  compose exec -T urgentry-api sh -lc 'urgentry self-hosted preflight --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend "$URGENTRY_TELEMETRY_BACKEND"' >"$backup_dir/restore-preflight.json"
  compose exec -T urgentry-api sh -lc 'urgentry self-hosted status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend "$URGENTRY_TELEMETRY_BACKEND"' >"$backup_dir/restore-status.json"
  record_action "restore.apply" "restored backup from $backup_dir"

  cat <<EOF
restore completed
dir=$backup_dir
project=$PROJECT_NAME
verification=$backup_dir/verify-backup.json
status=$backup_dir/restore-status.json
EOF
}

main "$@"
