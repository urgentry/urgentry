#!/usr/bin/env bash
set -euo pipefail

CURRENT_CONTROL_VERSION="${CURRENT_CONTROL_VERSION:-0}"
TARGET_CONTROL_VERSION="${TARGET_CONTROL_VERSION:-0}"
CURRENT_TELEMETRY_VERSION="${CURRENT_TELEMETRY_VERSION:-0}"
TARGET_TELEMETRY_VERSION="${TARGET_TELEMETRY_VERSION:-0}"
TELEMETRY_BACKEND="${URGENTRY_TELEMETRY_BACKEND:-postgres}"
URGENTRY_IMAGE="${URGENTRY_IMAGE:-urgentry:dev}"

if ! docker image inspect "$URGENTRY_IMAGE" >/dev/null 2>&1; then
  echo "$URGENTRY_IMAGE image not found. Boot the compose stack or run deploy/compose/smoke.sh up first." >&2
  exit 1
fi

exec docker run --rm "$URGENTRY_IMAGE" self-hosted rollback-plan \
  --current-control-version "${CURRENT_CONTROL_VERSION}" \
  --target-control-version "${TARGET_CONTROL_VERSION}" \
  --current-telemetry-version "${CURRENT_TELEMETRY_VERSION}" \
  --target-telemetry-version "${TARGET_TELEMETRY_VERSION}" \
  --telemetry-backend "${TELEMETRY_BACKEND}"
