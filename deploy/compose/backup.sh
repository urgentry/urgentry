#!/usr/bin/env bash
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
DEFAULT_ENV_FILE="$SCRIPT_DIR/.env"
ENV_FILE="${URGENTRY_SELF_HOSTED_ENV_FILE:-$DEFAULT_ENV_FILE}"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-}"
OPERATOR_ACTOR="${URGENTRY_OPERATOR_ACTOR:-${USER:-compose}}"

usage() {
  cat <<'EOF'
usage: backup.sh <backup-dir>

Capture a serious self-hosted backup set from the running Compose bundle.

Environment:
  URGENTRY_SELF_HOSTED_ENV_FILE  path to the compose env file (defaults to deploy/compose/.env)
  URGENTRY_SELF_HOSTED_PROJECT   override compose project name; defaults to COMPOSE_PROJECT_NAME from the env file
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

archive_volume() {
  local volume_name="$1"
  local output_file="$2"
  docker run --rm -v "${volume_name}:/data:ro" alpine:3.20 sh -lc 'cd /data && tar -czf - .' >"$output_file"
}

write_manifest() {
  local backup_dir="$1"
  python3 - "$backup_dir" "$PROJECT_NAME" <<'PY'
import hashlib
import json
import os
import sys
from datetime import datetime, timezone

def file_integrity(path):
    digest = hashlib.sha256()
    size = 0
    with open(path, "rb") as fh:
        while True:
            chunk = fh.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
            size += len(chunk)
    return {"name": os.path.basename(path), "bytes": size, "sha256": digest.hexdigest()}

backup_dir, project = sys.argv[1:]
status = json.load(open(os.path.join(backup_dir, "status.json"), "r", encoding="utf-8"))
preflight = json.load(open(os.path.join(backup_dir, "preflight.json"), "r", encoding="utf-8"))
plan = json.load(open(os.path.join(backup_dir, "backup-plan.json"), "r", encoding="utf-8"))
files = sorted(name for name in os.listdir(backup_dir) if name != "manifest.json")
manifest = {
    "schemaVersion": 1,
    "capturedAt": datetime.now(timezone.utc).isoformat(),
    "composeProject": project,
    "files": files,
    "status": status,
    "preflight": preflight,
    "backupPlan": plan,
    "integrity": [file_integrity(os.path.join(backup_dir, name)) for name in files],
}
with open(os.path.join(backup_dir, "manifest.json"), "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2)
    fh.write("\n")
PY
}

main() {
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" || "${1:-}" == "help" ]]; then
    usage
    exit 0
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
  mkdir -p "$backup_dir"
  chmod 700 "$backup_dir"

  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
  if [[ -z "$PROJECT_NAME" ]]; then
    PROJECT_NAME="${COMPOSE_PROJECT_NAME:-urgentry-selfhosted}"
  fi

  wait_service_success minio-bootstrap
  wait_service_success urgentry-bootstrap
  wait_service_healthy postgres
  wait_service_healthy minio
  wait_service_healthy valkey
  wait_service_healthy nats
  wait_service_healthy urgentry-api

  cp -f "$ENV_FILE" "$backup_dir/compose.env"
  chmod 600 "$backup_dir/compose.env"
  compose exec -T urgentry-api sh -lc 'urgentry self-hosted preflight --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend "$URGENTRY_TELEMETRY_BACKEND"' >"$backup_dir/preflight.json"
  compose exec -T urgentry-api sh -lc 'urgentry self-hosted status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend "$URGENTRY_TELEMETRY_BACKEND"' >"$backup_dir/status.json"
  compose exec -T urgentry-api sh -lc 'urgentry self-hosted backup-plan --telemetry-backend "$URGENTRY_TELEMETRY_BACKEND" --blob-backend "$URGENTRY_BLOB_BACKEND" --async-backend "$URGENTRY_ASYNC_BACKEND" --cache-backend "$URGENTRY_CACHE_BACKEND"' >"$backup_dir/backup-plan.json"

  compose exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" | gzip -c >"$backup_dir/postgres.sql.gz"
  archive_volume "${PROJECT_NAME}_urgentry_data" "$backup_dir/urgentry-data.tar.gz"
  archive_volume "${PROJECT_NAME}_minio_data" "$backup_dir/minio-data.tar.gz"
  archive_volume "${PROJECT_NAME}_nats_data" "$backup_dir/nats-data.tar.gz"
  archive_volume "${PROJECT_NAME}_valkey_data" "$backup_dir/valkey-data.tar.gz"
  write_manifest "$backup_dir"
  record_action "backup.capture" "captured backup into $backup_dir"

  cat <<EOF
backup captured
dir=$backup_dir
project=$PROJECT_NAME
manifest=$backup_dir/manifest.json
EOF
}

main "$@"
