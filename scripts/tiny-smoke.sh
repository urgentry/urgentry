#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TMP_DIR="$(mktemp -d)"
LOG_FILE="$TMP_DIR/urgentry.log"
COOKIE_JAR="$TMP_DIR/cookies.txt"
HEADERS_FILE="$TMP_DIR/login.headers"
BIN_PATH="$TMP_DIR/urgentry-smoke"
PORT="${URGENTRY_TINY_SMOKE_PORT:-18080}"
BASE_URL="http://127.0.0.1:${PORT}"
BOOTSTRAP_EMAIL="${URGENTRY_TINY_SMOKE_EMAIL:-admin@example.com}"
BOOTSTRAP_PASSWORD="${URGENTRY_TINY_SMOKE_PASSWORD:-tiny-smoke-password}"
BOOTSTRAP_PAT="${URGENTRY_TINY_SMOKE_PAT:-gpat_tiny_smoke_token}"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "building Tiny smoke binary"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}" ./scripts/build-urgentry.sh --output "$BIN_PATH"

echo "starting Tiny smoke server on $BASE_URL"
URGENTRY_ENV=ci \
URGENTRY_HTTP_ADDR="127.0.0.1:${PORT}" \
URGENTRY_DATA_DIR="$TMP_DIR/data" \
URGENTRY_BOOTSTRAP_EMAIL="$BOOTSTRAP_EMAIL" \
URGENTRY_BOOTSTRAP_PASSWORD="$BOOTSTRAP_PASSWORD" \
URGENTRY_BOOTSTRAP_PAT="$BOOTSTRAP_PAT" \
"$BIN_PATH" serve --role=all >"$LOG_FILE" 2>&1 &
SERVER_PID="$!"

for _ in $(seq 1 60); do
  if curl -fsS "$BASE_URL/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS "$BASE_URL/readyz" >/dev/null 2>&1; then
  echo "Tiny smoke failed: server never became ready" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi

echo "checking login page"
login_page="$(curl -fsS "$BASE_URL/login/")"
if [[ "$login_page" != *"Sign in"* ]]; then
  echo "Tiny smoke failed: /login/ did not render the expected page" >&2
  exit 1
fi

echo "checking bootstrap login"
status="$(
  curl -sS -o /dev/null -D "$HEADERS_FILE" -c "$COOKIE_JAR" \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data-urlencode "email=$BOOTSTRAP_EMAIL" \
    --data-urlencode "password=$BOOTSTRAP_PASSWORD" \
    --data-urlencode 'next=/issues/' \
    -w '%{http_code}' \
    "$BASE_URL/login/"
)"
if [[ "$status" != "303" ]]; then
  echo "Tiny smoke failed: bootstrap login returned $status" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi
if ! grep -qi '^Location: /issues/' "$HEADERS_FILE"; then
  echo "Tiny smoke failed: bootstrap login did not redirect to /issues/" >&2
  cat "$HEADERS_FILE" >&2
  exit 1
fi

echo "checking bootstrap PAT API access"
orgs_json="$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$BASE_URL/api/0/organizations/")"
if [[ "$orgs_json" != *"default-org"* && "$orgs_json" != *"urgentry-org"* ]]; then
  echo "Tiny smoke failed: bootstrap PAT could not list organizations" >&2
  echo "$orgs_json" >&2
  exit 1
fi

echo "checking startup log bootstrap output"
if ! grep -q 'bootstrap owner account created' "$LOG_FILE"; then
  echo "Tiny smoke failed: bootstrap credentials were not logged on first boot" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi

# -------------------------------------------------------------------
# Authenticated surface smoke: verify core pages render after login
# -------------------------------------------------------------------
check_page() {
  local path="$1"
  local expected="$2"
  local label="$3"
  local body
  body="$(curl -fsS -b "$COOKIE_JAR" "$BASE_URL$path" 2>/dev/null)" || {
    echo "Tiny smoke failed: $label ($path) returned an error" >&2
    exit 1
  }
  if [[ "$body" != *"$expected"* ]]; then
    echo "Tiny smoke failed: $label ($path) missing expected content '$expected'" >&2
    exit 1
  fi
  echo "  ok: $label"
}

echo "checking authenticated surfaces"
check_page "/" "Analytics" "home/landing"
check_page "/issues/" "Issues" "issues list"
check_page "/settings/" "Settings" "settings"

# API surface smoke via PAT
check_api() {
  local path="$1"
  local label="$2"
  local body
  body="$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$BASE_URL$path" 2>/dev/null)" || {
    echo "Tiny smoke failed: API $label ($path) returned an error" >&2
    exit 1
  }
  if [[ ${#body} -lt 2 ]]; then
    echo "Tiny smoke failed: API $label ($path) returned empty response" >&2
    exit 1
  fi
  echo "  ok: API $label"
}

echo "checking API surfaces"
check_api "/api/0/organizations/" "organizations list"

echo "tiny smoke passed"
