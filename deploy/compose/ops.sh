#!/usr/bin/env bash
set -euo pipefail

# shellcheck disable=SC1091
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../../scripts/lib-paths.sh"
resolve_urgentry_paths "$0"
COMPOSE_DIR="$APP_DIR/deploy/compose"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
DEFAULT_ENV_FILE="$COMPOSE_DIR/.env"
ENV_FILE="${URGENTRY_SELF_HOSTED_ENV_FILE:-$DEFAULT_ENV_FILE}"

usage() {
  cat <<'EOF'
usage: ops.sh <command> [args]

Commands:
  preflight
  status
  maintenance-status
  enter-maintenance <reason>
  leave-maintenance
  record-action <action> [detail]
  backup-plan
  security-report
  rotate-bootstrap
  verify-backup <backup-dir>
  rollback-plan <current-control> <target-control> <current-telemetry> <target-telemetry>

Environment:
  URGENTRY_SELF_HOSTED_ENV_FILE   path to compose env file (defaults to deploy/compose/.env)
  URGENTRY_CONTROL_DATABASE_URL   control-plane DSN (falls back to URGENTRY_DATABASE_URL)
  URGENTRY_TELEMETRY_DATABASE_URL telemetry bridge DSN (falls back to URGENTRY_DATABASE_URL)
  URGENTRY_TELEMETRY_BACKEND      postgres|timescale
EOF
}

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-${COMPOSE_PROJECT_NAME:-urgentry-selfhosted}}"
CONTROL_DSN="${URGENTRY_CONTROL_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_DSN="${URGENTRY_TELEMETRY_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_BACKEND="${URGENTRY_TELEMETRY_BACKEND:-postgres}"

compose() {
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

operator_actor() {
  printf '%s' "${URGENTRY_OPERATOR_ACTOR:-${USER:-compose}}"
}

ensure_compose_env() {
  if [[ ! -f "$ENV_FILE" ]]; then
    echo "compose env file not found: $ENV_FILE" >&2
    exit 1
  fi
}

ensure_control_plane_dsns() {
  if [[ -z "$CONTROL_DSN" || -z "$TELEMETRY_DSN" ]]; then
    echo "URGENTRY_CONTROL_DATABASE_URL / URGENTRY_TELEMETRY_DATABASE_URL are not set in the environment or ${ENV_FILE}" >&2
    exit 1
  fi
}

ensure_local_image() {
  if ! docker image inspect "${URGENTRY_IMAGE:-urgentry:dev}" >/dev/null 2>&1; then
    echo "${URGENTRY_IMAGE:-urgentry:dev} image not found. Boot the compose stack or run deploy/compose/smoke.sh up first." >&2
    exit 1
  fi
}

compose_run_urgentry() {
  compose run --rm --no-deps -T urgentry-api self-hosted "$@"
}

if [[ $# -lt 1 ]]; then
  usage >&2
  exit 2
fi

ensure_compose_env

command="$1"
shift

case "$command" in
  preflight)
    ensure_control_plane_dsns
    compose_run_urgentry preflight \
      --control-dsn "$CONTROL_DSN" \
      --telemetry-dsn "$TELEMETRY_DSN" \
      --telemetry-backend "$TELEMETRY_BACKEND"
    exit $?
    ;;
  status)
    ensure_control_plane_dsns
    compose_run_urgentry status \
      --control-dsn "$CONTROL_DSN" \
      --telemetry-dsn "$TELEMETRY_DSN" \
      --telemetry-backend "$TELEMETRY_BACKEND"
    exit $?
    ;;
  maintenance-status)
    ensure_control_plane_dsns
    compose_run_urgentry maintenance-status \
      --control-dsn "$CONTROL_DSN"
    exit $?
    ;;
  enter-maintenance)
    ensure_control_plane_dsns
    if [[ $# -lt 1 ]]; then
      usage >&2
      exit 2
    fi
    compose_run_urgentry enter-maintenance \
      --control-dsn "$CONTROL_DSN" \
      --actor "$(operator_actor)" \
      --source compose \
      --reason "$*"
    exit $?
    ;;
  leave-maintenance)
    ensure_control_plane_dsns
    compose_run_urgentry leave-maintenance \
      --control-dsn "$CONTROL_DSN" \
      --actor "$(operator_actor)" \
      --source compose
    exit $?
    ;;
  record-action)
    ensure_control_plane_dsns
    if [[ $# -lt 1 ]]; then
      usage >&2
      exit 2
    fi
    action="$1"
    shift
    args=(
      record-action
      --control-dsn "$CONTROL_DSN"
      --action "$action"
      --status "${URGENTRY_OPERATOR_ACTION_STATUS:-succeeded}"
      --source "${URGENTRY_OPERATOR_ACTION_SOURCE:-compose}"
      --actor "$(operator_actor)"
    )
    if [[ -n "${URGENTRY_OPERATOR_ACTION_METADATA:-}" ]]; then
      args+=(--metadata "${URGENTRY_OPERATOR_ACTION_METADATA}")
    fi
    if [[ $# -gt 0 ]]; then
      args+=(--detail "$*")
    fi
    compose_run_urgentry "${args[@]}"
    exit $?
    ;;
  backup-plan)
    compose_run_urgentry backup-plan \
      --telemetry-backend "$TELEMETRY_BACKEND" \
      --blob-backend "${URGENTRY_BLOB_BACKEND:-s3}" \
      --async-backend "${URGENTRY_ASYNC_BACKEND:-jetstream}" \
      --cache-backend "${URGENTRY_CACHE_BACKEND:-valkey}"
    exit $?
    ;;
  security-report)
    ensure_control_plane_dsns
    compose_run_urgentry security-report \
      --env "${URGENTRY_ENV:-production}" \
      --control-dsn "$CONTROL_DSN" \
      --telemetry-dsn "$TELEMETRY_DSN"
    exit $?
    ;;
  rotate-bootstrap)
    ensure_control_plane_dsns
    compose_run_urgentry rotate-bootstrap \
      --control-dsn "$CONTROL_DSN" \
      --email "${URGENTRY_BOOTSTRAP_EMAIL:-admin@urgentry.local}" \
      --password "${URGENTRY_BOOTSTRAP_PASSWORD:-}" \
      --pat "${URGENTRY_BOOTSTRAP_PAT:-}"
    exit $?
    ;;
  verify-backup)
    if [[ $# -ne 1 ]]; then
      usage >&2
      exit 2
    fi
    ensure_local_image
    docker run --rm \
      -v "$1:/backup:ro" \
      "${URGENTRY_IMAGE:-urgentry:dev}" \
      self-hosted \
      verify-backup \
      --dir /backup \
      --telemetry-backend "$TELEMETRY_BACKEND" \
      --strict-target-match="${URGENTRY_SELF_HOSTED_STRICT_TARGET_MATCH:-false}"
    exit $?
    ;;
  rollback-plan)
    if [[ $# -ne 4 ]]; then
      usage >&2
      exit 2
    fi
    output="$(
      ensure_local_image
      docker run --rm \
        "${URGENTRY_IMAGE:-urgentry:dev}" \
        self-hosted \
        rollback-plan \
        --current-control-version "$1" \
        --target-control-version "$2" \
        --current-telemetry-version "$3" \
        --target-telemetry-version "$4" \
        --telemetry-backend "$TELEMETRY_BACKEND"
    )"
    if [[ -n "$CONTROL_DSN" ]]; then
      compose_run_urgentry record-action \
        --control-dsn "$CONTROL_DSN" \
        --action rollback.plan_generated \
        --source compose \
        --actor "$(operator_actor)" \
        --detail "generated rollback plan" >/dev/null
    fi
    printf '%s\n' "$output"
    exit 0
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
