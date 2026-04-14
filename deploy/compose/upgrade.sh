#!/usr/bin/env bash
set -euo pipefail

# shellcheck disable=SC1091
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../../scripts/lib-paths.sh"
resolve_urgentry_paths "$0"
COMPOSE_BACKUP_SCRIPT="$APP_DIR/deploy/compose/backup.sh"
COMPOSE_OPS_SCRIPT="$APP_DIR/deploy/compose/ops.sh"
DEFAULT_ENV_FILE="$APP_DIR/deploy/compose/.env"
ENV_FILE="${URGENTRY_SELF_HOSTED_ENV_FILE:-$DEFAULT_ENV_FILE}"
ROLLBACK_PLAN_SCRIPT="$APP_DIR/deploy/compose/rollback-plan.sh"

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

CONTROL_DSN="${URGENTRY_CONTROL_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_DSN="${URGENTRY_TELEMETRY_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_BACKEND="${URGENTRY_TELEMETRY_BACKEND:-postgres}"
PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-${COMPOSE_PROJECT_NAME:-urgentry-selfhosted}}"
BACKUP_DIR="${URGENTRY_SELF_HOSTED_BACKUP_DIR:-}"
UPGRADE_DIR="${URGENTRY_SELF_HOSTED_UPGRADE_DIR:-}"
SKIP_AUTO_BACKUP="${URGENTRY_SELF_HOSTED_SKIP_UPGRADE_BACKUP:-false}"

if [[ -z "${CONTROL_DSN}" || -z "${TELEMETRY_DSN}" ]]; then
  echo "URGENTRY_CONTROL_DATABASE_URL / URGENTRY_TELEMETRY_DATABASE_URL are not set and no compose env file was found at ${ENV_FILE}" >&2
  exit 1
fi

if [[ -z "$UPGRADE_DIR" ]]; then
  UPGRADE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-selfhosted-upgrade.XXXXXX")"
else
  mkdir -p "$UPGRADE_DIR"
fi
if [[ -z "$BACKUP_DIR" ]]; then
  BACKUP_DIR="$UPGRADE_DIR/backup"
fi

URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" preflight >"${UPGRADE_DIR}/preflight-before.json"
URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" status >"${UPGRADE_DIR}/status-before.json"
URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" backup-plan >"${UPGRADE_DIR}/backup-plan.json"
if [[ "${SKIP_AUTO_BACKUP}" != "true" ]]; then
  URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_BACKUP_SCRIPT}" "${BACKUP_DIR}" >/dev/null
  URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" verify-backup "${BACKUP_DIR}" >"${UPGRADE_DIR}/backup-verify.json"
fi
docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$APP_DIR/deploy/compose/docker-compose.yml" run --rm --no-deps -T urgentry-api self-hosted migrate-control --dsn "${CONTROL_DSN}" >/dev/null
docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$APP_DIR/deploy/compose/docker-compose.yml" run --rm --no-deps -T urgentry-api self-hosted migrate-telemetry --dsn "${TELEMETRY_DSN}" --telemetry-backend "${TELEMETRY_BACKEND}" >/dev/null
URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" preflight >"${UPGRADE_DIR}/preflight-after.json"
URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" status >"${UPGRADE_DIR}/status-after.json"
python3 - "${UPGRADE_DIR}/status-before.json" "${UPGRADE_DIR}/status-after.json" "${TELEMETRY_BACKEND}" "${ROLLBACK_PLAN_SCRIPT}" <<'PY' >"${UPGRADE_DIR}/rollback-plan.json"
import json
import os
import subprocess
import sys

before_path, after_path, backend, rollback_script = sys.argv[1:]
with open(before_path, "r", encoding="utf-8") as fh:
    before = json.load(fh)
with open(after_path, "r", encoding="utf-8") as fh:
    after = json.load(fh)

subprocess.run(
    [
        "bash", rollback_script,
    ],
    check=True,
    env={
        **dict(os.environ),
        "CURRENT_CONTROL_VERSION": str(after["controlVersion"]),
        "TARGET_CONTROL_VERSION": str(before["controlVersion"]),
        "CURRENT_TELEMETRY_VERSION": str(after["telemetryVersion"]),
        "TARGET_TELEMETRY_VERSION": str(before["telemetryVersion"]),
        "URGENTRY_TELEMETRY_BACKEND": backend,
    },
)
PY
URGENTRY_SELF_HOSTED_ENV_FILE="${ENV_FILE}" bash "${COMPOSE_OPS_SCRIPT}" record-action upgrade.apply "applied serious self-hosted upgrade" >/dev/null

cat <<EOF
upgrade completed
artifacts=${UPGRADE_DIR}
backup=${BACKUP_DIR}
EOF
