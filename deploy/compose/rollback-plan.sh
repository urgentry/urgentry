#!/usr/bin/env bash
set -euo pipefail

# shellcheck disable=SC1091
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../../scripts/lib-paths.sh"
resolve_urgentry_paths "$0"

CURRENT_CONTROL_VERSION="${CURRENT_CONTROL_VERSION:-0}"
TARGET_CONTROL_VERSION="${TARGET_CONTROL_VERSION:-0}"
CURRENT_TELEMETRY_VERSION="${CURRENT_TELEMETRY_VERSION:-0}"
TARGET_TELEMETRY_VERSION="${TARGET_TELEMETRY_VERSION:-0}"
TELEMETRY_BACKEND="${URGENTRY_TELEMETRY_BACKEND:-postgres}"

pushd "$APP_DIR" >/dev/null
go run ./cmd/urgentry self-hosted rollback-plan \
  --current-control-version "${CURRENT_CONTROL_VERSION}" \
  --target-control-version "${TARGET_CONTROL_VERSION}" \
  --current-telemetry-version "${CURRENT_TELEMETRY_VERSION}" \
  --target-telemetry-version "${TARGET_TELEMETRY_VERSION}" \
  --telemetry-backend "${TELEMETRY_BACKEND}"
popd >/dev/null
