#!/usr/bin/env bash
set -euo pipefail

# shellcheck disable=SC1091
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../../scripts/lib-paths.sh"
resolve_urgentry_paths "$0"
COMPOSE_BACKUP_SCRIPT="$APP_DIR/deploy/compose/backup.sh"

CONTROL_DSN="${URGENTRY_CONTROL_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_DSN="${URGENTRY_TELEMETRY_DATABASE_URL:-${URGENTRY_DATABASE_URL:-}}"
TELEMETRY_BACKEND="${URGENTRY_TELEMETRY_BACKEND:-postgres}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"
BACKUP_DIR="${URGENTRY_SELF_HOSTED_BACKUP_DIR:-}"
UPGRADE_DIR="${URGENTRY_SELF_HOSTED_UPGRADE_DIR:-}"
SKIP_AUTO_BACKUP="${URGENTRY_SELF_HOSTED_SKIP_UPGRADE_BACKUP:-false}"

if [[ -z "${CONTROL_DSN}" || -z "${TELEMETRY_DSN}" ]]; then
  echo "URGENTRY_CONTROL_DATABASE_URL or URGENTRY_DATABASE_URL must be set" >&2
  exit 1
fi

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

CONTROL_DSN="$(rewrite_compose_dsn "$CONTROL_DSN")"
TELEMETRY_DSN="$(rewrite_compose_dsn "$TELEMETRY_DSN")"
OPERATOR_ACTOR="${URGENTRY_OPERATOR_ACTOR:-${USER:-compose}}"

if [[ -z "$UPGRADE_DIR" ]]; then
  UPGRADE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-selfhosted-upgrade.XXXXXX")"
else
  mkdir -p "$UPGRADE_DIR"
fi
if [[ -z "$BACKUP_DIR" ]]; then
  BACKUP_DIR="$UPGRADE_DIR/backup"
fi

pushd "$APP_DIR" >/dev/null
go run ./cmd/urgentry self-hosted preflight \
  --control-dsn "${CONTROL_DSN}" \
  --telemetry-dsn "${TELEMETRY_DSN}" \
  --telemetry-backend "${TELEMETRY_BACKEND}" >"${UPGRADE_DIR}/preflight-before.json"
go run ./cmd/urgentry self-hosted status \
  --control-dsn "${CONTROL_DSN}" \
  --telemetry-dsn "${TELEMETRY_DSN}" \
  --telemetry-backend "${TELEMETRY_BACKEND}" >"${UPGRADE_DIR}/status-before.json"
go run ./cmd/urgentry self-hosted backup-plan \
  --telemetry-backend "${TELEMETRY_BACKEND}" \
  --blob-backend "${URGENTRY_BLOB_BACKEND:-s3}" \
  --async-backend "${URGENTRY_ASYNC_BACKEND:-jetstream}" \
  --cache-backend "${URGENTRY_CACHE_BACKEND:-valkey}" >"${UPGRADE_DIR}/backup-plan.json"
if [[ "${SKIP_AUTO_BACKUP}" != "true" ]]; then
  "${COMPOSE_BACKUP_SCRIPT}" "${BACKUP_DIR}" >/dev/null
  go run ./cmd/urgentry self-hosted verify-backup \
    --dir "${BACKUP_DIR}" \
    --telemetry-backend "${TELEMETRY_BACKEND}" \
    --strict-target-match=false >"${UPGRADE_DIR}/backup-verify.json"
fi
go run ./cmd/urgentry self-hosted migrate-control --dsn "${CONTROL_DSN}" >/dev/null
go run ./cmd/urgentry self-hosted migrate-telemetry --dsn "${TELEMETRY_DSN}" --telemetry-backend "${TELEMETRY_BACKEND}" >/dev/null
go run ./cmd/urgentry self-hosted preflight \
  --control-dsn "${CONTROL_DSN}" \
  --telemetry-dsn "${TELEMETRY_DSN}" \
  --telemetry-backend "${TELEMETRY_BACKEND}" >"${UPGRADE_DIR}/preflight-after.json"
go run ./cmd/urgentry self-hosted status \
  --control-dsn "${CONTROL_DSN}" \
  --telemetry-dsn "${TELEMETRY_DSN}" \
  --telemetry-backend "${TELEMETRY_BACKEND}" >"${UPGRADE_DIR}/status-after.json"
python3 - "${UPGRADE_DIR}/status-before.json" "${UPGRADE_DIR}/status-after.json" "${TELEMETRY_BACKEND}" <<'PY' >"${UPGRADE_DIR}/rollback-plan.json"
import json
import subprocess
import sys

before_path, after_path, backend = sys.argv[1:]
with open(before_path, "r", encoding="utf-8") as fh:
    before = json.load(fh)
with open(after_path, "r", encoding="utf-8") as fh:
    after = json.load(fh)

subprocess.run(
    [
        "go", "run", "./cmd/urgentry", "self-hosted", "rollback-plan",
        "--current-control-version", str(after["controlVersion"]),
        "--target-control-version", str(before["controlVersion"]),
        "--current-telemetry-version", str(after["telemetryVersion"]),
        "--target-telemetry-version", str(before["telemetryVersion"]),
        "--telemetry-backend", backend,
    ],
    check=True,
)
PY
go run ./cmd/urgentry self-hosted record-action \
  --control-dsn "${CONTROL_DSN}" \
  --action upgrade.apply \
  --source compose \
  --actor "${OPERATOR_ACTOR}" \
  --detail "applied serious self-hosted upgrade" >/dev/null
popd >/dev/null

cat <<EOF
upgrade completed
artifacts=${UPGRADE_DIR}
backup=${BACKUP_DIR}
EOF
