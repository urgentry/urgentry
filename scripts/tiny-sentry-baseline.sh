#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "$ROOT_DIR/../.." && pwd)"
cd "$ROOT_DIR"

TMP_DIR="$(mktemp -d)"
LOG_FILE="$TMP_DIR/urgentry.log"
BIN_PATH="$TMP_DIR/urgentry-baseline"
PORT="${URGENTRY_SENTRY_BASELINE_TINY_PORT:-18082}"
BASE_URL="http://127.0.0.1:${PORT}"
BOOTSTRAP_EMAIL="${URGENTRY_SENTRY_BASELINE_TINY_EMAIL:-baseline-admin@example.com}"
BOOTSTRAP_PASSWORD="${URGENTRY_SENTRY_BASELINE_TINY_PASSWORD:-baseline-password-123}"
BOOTSTRAP_PAT="${URGENTRY_SENTRY_BASELINE_TINY_PAT:-gpat_baseline_tiny_token}"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "tiny-sentry-baseline failed: missing required tool '$1'" >&2
    exit 1
  fi
}

require_tool curl
require_tool jq
require_tool python3

echo "building Tiny baseline binary"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}" ./scripts/build-urgentry.sh --output "$BIN_PATH" >/dev/null

echo "starting Tiny baseline server on $BASE_URL"
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
  echo "tiny-sentry-baseline failed: server never became ready" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi

PROJECTS_JSON="$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$BASE_URL/api/0/projects/")"
ORG_SLUG="$(jq -r '.[0].organization // empty' <<<"$PROJECTS_JSON")"
PROJECT_SLUG="$(jq -r '.[0].slug // empty' <<<"$PROJECTS_JSON")"
if [[ -z "$ORG_SLUG" || -z "$PROJECT_SLUG" ]]; then
  echo "tiny-sentry-baseline failed: could not resolve bootstrap org/project" >&2
  echo "$PROJECTS_JSON" >&2
  exit 1
fi

KEYS_JSON="$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$BASE_URL/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/keys/")"
PUBLIC_KEY="$(jq -r '.[0].public // empty' <<<"$KEYS_JSON")"
PROJECT_DSN="$(jq -r '.[0].dsn.public // empty' <<<"$KEYS_JSON")"
PROJECT_ID="${PROJECT_DSN##*/}"
if [[ -z "$PUBLIC_KEY" || -z "$PROJECT_ID" ]]; then
  echo "tiny-sentry-baseline failed: could not resolve bootstrap project key" >&2
  echo "$KEYS_JSON" >&2
  exit 1
fi

AUTH_HEADER="Sentry sentry_key=${PUBLIC_KEY},sentry_version=7,sentry_client=sentry-baseline/1.0"
STORE_URL="$BASE_URL/api/$PROJECT_ID/store/"
ENVELOPE_URL="$BASE_URL/api/$PROJECT_ID/envelope/"

post_ingest() {
  local label="$1"
  local content_type="$2"
  local body_file="$3"
  local target_url="$4"
  local status
  status="$(curl -sS -o /tmp/${label}.out -w '%{http_code}' \
    -H "X-Sentry-Auth: $AUTH_HEADER" \
    -H "Content-Type: $content_type" \
    --data-binary "@${body_file}" \
    "$target_url")"
  if [[ "$status" != "200" ]]; then
    echo "tiny-sentry-baseline failed: $label returned $status" >&2
    cat /tmp/${label}.out >&2
    exit 1
  fi
  echo "  ok: $label"
}

api_get() {
  local path="$1"
  curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$BASE_URL$path"
}

echo "replaying upstream-baselined ingest corpus"
post_ingest "store" "application/json" "$REPO_ROOT/.synthetic/generated/structured/store-basic-error.json" "$STORE_URL"
post_ingest "envelope" "application/x-sentry-envelope" "$REPO_ROOT/.synthetic/generated/structured/envelope-event.envelope" "$ENVELOPE_URL"
post_ingest "checkin" "application/x-sentry-envelope" "$REPO_ROOT/.synthetic/generated/structured/envelope-check-in.envelope" "$ENVELOPE_URL"
post_ingest "attachment" "application/x-sentry-envelope" "$REPO_ROOT/.synthetic/generated/artifacts/artifact-envelope_attachment_text/request.envelope" "$ENVELOPE_URL"

python3 - <<'PY' "$TMP_DIR"
from datetime import datetime, timezone
import json, pathlib, sys

tmp_dir = pathlib.Path(sys.argv[1])
now = datetime.now(timezone.utc).replace(microsecond=0)
event_id = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
payload = {
    "event_id": event_id,
    "timestamp": now.isoformat().replace("+00:00", "Z"),
    "platform": "javascript",
    "level": "error",
    "environment": "synthetic",
    "release": "synthetic@1.0.0",
    "message": "Synthetic fresh feedback anchor urgentry",
}
payload_s = json.dumps(payload, separators=(",", ":"))
(tmp_dir / "fresh-event.envelope").write_text(
    json.dumps({"event_id": event_id}, separators=(",", ":"))
    + "\n"
    + json.dumps({"type": "event", "length": len(payload_s.encode())}, separators=(",", ":"))
    + "\n"
    + payload_s
)
feedback = {
    "event_id": event_id,
    "name": "Urgentry Fresh Reporter",
    "email": "fresh@example.com",
    "comments": "Urgentry fresh feedback item",
}
feedback_s = json.dumps(feedback, separators=(",", ":"))
(tmp_dir / "fresh-feedback.envelope").write_text(
    json.dumps({"event_id": event_id}, separators=(",", ":"))
    + "\n"
    + json.dumps({"type": "user_report", "length": len(feedback_s.encode())}, separators=(",", ":"))
    + "\n"
    + feedback_s
)
PY

post_ingest "fresh-event" "application/x-sentry-envelope" "$TMP_DIR/fresh-event.envelope" "$ENVELOPE_URL"
post_ingest "fresh-feedback" "application/x-sentry-envelope" "$TMP_DIR/fresh-feedback.envelope" "$ENVELOPE_URL"

echo "verifying event, issue, monitor, attachment, and feedback readback"
api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/events/01010101010101010101010101010101/" | jq -e '.title == "Synthetic store payload"' >/dev/null
api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/events/02020202020202020202020202020202/" | jq -e '.title == "Synthetic envelope event"' >/dev/null
api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/monitors/synthetic-cron/checkins/" | jq -e 'length > 0 and .[0].monitorSlug == "synthetic-cron" and .[0].duration == 4.2' >/dev/null
api_get "/api/0/events/07070707070707070707070707070707/attachments/" | jq -e 'length > 0 and .[0].name == "test.txt"' >/dev/null
api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/issues/" | jq -e 'map(.title) | index("Synthetic artifact envelope") != null and index("Synthetic store payload") != null and index("Synthetic envelope event") != null and index("Synthetic fresh feedback anchor urgentry") != null' >/dev/null

feedback_json="[]"
for _ in $(seq 1 10); do
  feedback_json="$(api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/user-feedback/")"
  if jq -e 'map(.name) | index("Urgentry Fresh Reporter") != null' >/dev/null <<<"$feedback_json"; then
    break
  fi
  sleep 1
done
jq -e 'map(.name) | index("Urgentry Fresh Reporter") != null' >/dev/null <<<"$feedback_json"

fresh_event_json="{}"
for _ in $(seq 1 10); do
  fresh_event_json="$(api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/events/0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a/")"
  if jq -e '.userReport.name == "Urgentry Fresh Reporter"' >/dev/null <<<"$fresh_event_json"; then
    break
  fi
  sleep 1
done
jq -e '.userReport.name == "Urgentry Fresh Reporter"' >/dev/null <<<"$fresh_event_json"

echo "tiny sentry baseline passed"
