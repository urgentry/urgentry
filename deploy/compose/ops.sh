#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
APP_DIR="$ROOT_DIR/apps/urgentry"
COMPOSE_DIR="$APP_DIR/deploy/compose"
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

rewrite_compose_dsn() {
  python3 - "$1" "$POSTGRES_PORT" <<'PY'
import sys
import urllib.parse

dsn, port = sys.argv[1], sys.argv[2]
parsed = urllib.parse.urlsplit(dsn)
if parsed.hostname not in {"postgres", "postgresql"}:
    print(dsn)
    raise SystemExit(0)

host = "127.0.0.1"
netloc = host
if parsed.username:
    auth = urllib.parse.quote(parsed.username)
    if parsed.password:
        auth += ":" + urllib.parse.quote(parsed.password)
    netloc = auth + "@" + netloc
netloc += ":" + port
rebuilt = urllib.parse.urlunsplit((parsed.scheme, netloc, parsed.path, parsed.query, parsed.fragment))
print(rebuilt)
PY
}

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

CONTROL_DSN="${URGENTRY_CONTROL_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_DSN="${URGENTRY_TELEMETRY_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_BACKEND="${URGENTRY_TELEMETRY_BACKEND:-postgres}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"

CONTROL_DSN="$(rewrite_compose_dsn "$CONTROL_DSN")"
TELEMETRY_DSN="$(rewrite_compose_dsn "$TELEMETRY_DSN")"

operator_actor() {
  printf '%s' "${URGENTRY_OPERATOR_ACTOR:-${USER:-compose}}"
}

if [[ $# -lt 1 ]]; then
  usage >&2
  exit 2
fi

command="$1"
shift

pushd "$APP_DIR" >/dev/null
case "$command" in
  preflight)
    exec go run ./cmd/urgentry self-hosted preflight \
      --control-dsn "$CONTROL_DSN" \
      --telemetry-dsn "$TELEMETRY_DSN" \
      --telemetry-backend "$TELEMETRY_BACKEND"
    ;;
  status)
    exec go run ./cmd/urgentry self-hosted status \
      --control-dsn "$CONTROL_DSN" \
      --telemetry-dsn "$TELEMETRY_DSN" \
      --telemetry-backend "$TELEMETRY_BACKEND"
    ;;
  maintenance-status)
    exec go run ./cmd/urgentry self-hosted maintenance-status \
      --control-dsn "$CONTROL_DSN"
    ;;
  enter-maintenance)
    if [[ $# -lt 1 ]]; then
      usage >&2
      exit 2
    fi
    exec go run ./cmd/urgentry self-hosted enter-maintenance \
      --control-dsn "$CONTROL_DSN" \
      --actor "$(operator_actor)" \
      --source compose \
      --reason "$*"
    ;;
  leave-maintenance)
    exec go run ./cmd/urgentry self-hosted leave-maintenance \
      --control-dsn "$CONTROL_DSN" \
      --actor "$(operator_actor)" \
      --source compose
    ;;
  record-action)
    if [[ $# -lt 1 ]]; then
      usage >&2
      exit 2
    fi
    action="$1"
    shift
    args=(
      go run ./cmd/urgentry self-hosted record-action
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
    exec "${args[@]}"
    ;;
  backup-plan)
    exec go run ./cmd/urgentry self-hosted backup-plan \
      --telemetry-backend "$TELEMETRY_BACKEND" \
      --blob-backend "${URGENTRY_BLOB_BACKEND:-s3}" \
      --async-backend "${URGENTRY_ASYNC_BACKEND:-jetstream}" \
      --cache-backend "${URGENTRY_CACHE_BACKEND:-valkey}"
    ;;
  security-report)
    exec go run ./cmd/urgentry self-hosted security-report \
      --env "${URGENTRY_ENV:-production}" \
      --control-dsn "$CONTROL_DSN" \
      --telemetry-dsn "$TELEMETRY_DSN"
    ;;
  rotate-bootstrap)
    exec go run ./cmd/urgentry self-hosted rotate-bootstrap \
      --control-dsn "$CONTROL_DSN" \
      --email "${URGENTRY_BOOTSTRAP_EMAIL:-admin@urgentry.local}" \
      --password "${URGENTRY_BOOTSTRAP_PASSWORD:-}" \
      --pat "${URGENTRY_BOOTSTRAP_PAT:-}"
    ;;
  verify-backup)
    if [[ $# -ne 1 ]]; then
      usage >&2
      exit 2
    fi
    exec go run ./cmd/urgentry self-hosted verify-backup \
      --dir "$1" \
      --telemetry-backend "$TELEMETRY_BACKEND" \
      --strict-target-match="${URGENTRY_SELF_HOSTED_STRICT_TARGET_MATCH:-false}"
    ;;
  rollback-plan)
    if [[ $# -ne 4 ]]; then
      usage >&2
      exit 2
    fi
    output="$(
      go run ./cmd/urgentry self-hosted rollback-plan \
        --current-control-version "$1" \
        --target-control-version "$2" \
        --current-telemetry-version "$3" \
        --target-telemetry-version "$4" \
        --telemetry-backend "$TELEMETRY_BACKEND"
    )"
    go run ./cmd/urgentry self-hosted record-action \
      --control-dsn "$CONTROL_DSN" \
      --action rollback.plan_generated \
      --source compose \
      --actor "$(operator_actor)" \
      --detail "generated rollback plan" >/dev/null
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
