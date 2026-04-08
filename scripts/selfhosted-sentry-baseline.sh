#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_DIR="$ROOT_DIR/deploy/compose"
ENV_TEMPLATE="$COMPOSE_DIR/.env.example"
LIB_SH="$COMPOSE_DIR/lib.sh"
SMOKE_SH="$COMPOSE_DIR/smoke.sh"

# shellcheck disable=SC1091
source "$ROOT_DIR/scripts/lib-paths.sh"
resolve_urgentry_paths "$0"
# shellcheck disable=SC1090
source "$LIB_SH"

TMP_DIR="$(mktemp -d)"
ENV_FILE="$TMP_DIR/selfhosted.env"
PROJECT_SUFFIX="$(basename "$TMP_DIR" | tr '[:upper:]' '[:lower:]' | tr -c '[:alnum:]_-' '-' | sed 's/^-*//; s/-*$//')"
PROJECT_NAME="urgentry-selfhosted-baseline-${PROJECT_SUFFIX}"
SMOKE_OUT="$TMP_DIR/smoke.txt"
API_URL=""
INGEST_URL=""
WORKER_URL=""
SCHEDULER_URL=""
BOOTSTRAP_PAT="${URGENTRY_SENTRY_BASELINE_SELF_HOSTED_PAT:-gpat_self_hosted_baseline_token}"
BOOTSTRAP_PASSWORD="${URGENTRY_SENTRY_BASELINE_SELF_HOSTED_PASSWORD:-SeriousSelfHosted!123}"

cleanup() {
  if [[ -n "${PROJECT_NAME:-}" ]]; then
    docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_DIR/docker-compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "selfhosted-sentry-baseline failed: missing required tool '$1'" >&2
    exit 1
  fi
}

require_tool curl
require_tool jq
require_tool python3
require_tool docker

cp -f "$ENV_TEMPLATE" "$ENV_FILE"
cat >>"$ENV_FILE" <<EOF
COMPOSE_PROJECT_NAME=$PROJECT_NAME
POSTGRES_PASSWORD=serious-selfhosted-postgres
MINIO_ROOT_PASSWORD=serious-selfhosted-minio
URGENTRY_CONTROL_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable
URGENTRY_TELEMETRY_DATABASE_URL=postgres://\${POSTGRES_USER:-urgentry}:serious-selfhosted-postgres@postgres:5432/\${POSTGRES_DB:-urgentry}?sslmode=disable
URGENTRY_BOOTSTRAP_PAT=$BOOTSTRAP_PAT
URGENTRY_BOOTSTRAP_PASSWORD=$BOOTSTRAP_PASSWORD
URGENTRY_METRICS_TOKEN=metrics-self-hosted-baseline
URGENTRY_INGEST_RATE_LIMIT=10000
EOF
append_random_port_overrides "$ENV_FILE"

echo "booting serious self-hosted baseline stack"
for attempt in 1 2 3 4 5; do
  if URGENTRY_SELF_HOSTED_KEEP_STACK=true \
    URGENTRY_SELF_HOSTED_ENV_FILE="$ENV_FILE" \
    bash "$SMOKE_SH" up >"$SMOKE_OUT"; then
    break
  fi
  docker compose --project-name "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_DIR/docker-compose.yml" down -v --remove-orphans >/dev/null 2>&1 || true
  if [[ "$attempt" == "5" ]]; then
    echo "selfhosted-sentry-baseline failed: could not boot compose stack" >&2
    exit 1
  fi
  sleep 1
done

API_URL="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-api-1" 8080)"
INGEST_URL="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-ingest-1" 8081)"
WORKER_URL="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-worker-1" 8082)"
SCHEDULER_URL="http://127.0.0.1:$(docker_host_port "${PROJECT_NAME}-urgentry-scheduler-1" 8083)"

PROJECTS_JSON="$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$API_URL/api/0/projects/")"
ORG_SLUG="$(jq -r '.[0].organization // empty' <<<"$PROJECTS_JSON")"
PROJECT_SLUG="$(jq -r '.[0].slug // empty' <<<"$PROJECTS_JSON")"
if [[ -z "$ORG_SLUG" || -z "$PROJECT_SLUG" ]]; then
  echo "selfhosted-sentry-baseline failed: could not resolve bootstrap org/project" >&2
  echo "$PROJECTS_JSON" >&2
  exit 1
fi

KEYS_JSON="$(curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$API_URL/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/keys/")"
PUBLIC_KEY="$(jq -r '.[0].public // empty' <<<"$KEYS_JSON")"
PROJECT_DSN="$(jq -r '.[0].dsn.public // empty' <<<"$KEYS_JSON")"
PROJECT_ID="${PROJECT_DSN##*/}"
if [[ -z "$PUBLIC_KEY" || -z "$PROJECT_ID" ]]; then
  echo "selfhosted-sentry-baseline failed: could not resolve bootstrap project key" >&2
  echo "$KEYS_JSON" >&2
  exit 1
fi

AUTH_HEADER="Sentry sentry_key=${PUBLIC_KEY},sentry_version=7,sentry_client=sentry-baseline/1.0"
STORE_URL="$INGEST_URL/api/$PROJECT_ID/store/"
ENVELOPE_URL="$INGEST_URL/api/$PROJECT_ID/envelope/"

post_ingest() {
  local label="$1"
  local content_type="$2"
  local body_file="$3"
  local target_url="$4"
  local status
  status="$(curl -sS -o "$TMP_DIR/${label}.out" -w '%{http_code}' \
    -H "X-Sentry-Auth: $AUTH_HEADER" \
    -H "Content-Type: $content_type" \
    --data-binary "@${body_file}" \
    "$target_url")"
  if [[ "$status" != "200" ]]; then
    echo "selfhosted-sentry-baseline failed: $label returned $status" >&2
    cat "$TMP_DIR/${label}.out" >&2
    exit 1
  fi
  echo "  ok: $label"
}

api_get() {
  local path="$1"
  curl -fsS -H "Authorization: Bearer $BOOTSTRAP_PAT" "$API_URL$path"
}

api_get_retry() {
  local path="$1"
  local out=""
  for _ in $(seq 1 15); do
    if out="$(api_get "$path" 2>/dev/null)"; then
      printf '%s' "$out"
      return 0
    fi
    sleep 1
  done
  api_get "$path"
}

echo "replaying upstream-baselined ingest corpus against serious self-hosted"
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

echo "verifying self-hosted event, issue, monitor, attachment, and feedback readback"
api_get_retry "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/events/01010101010101010101010101010101/" | jq -e '.title == "Synthetic store payload"' >/dev/null
api_get_retry "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/events/02020202020202020202020202020202/" | jq -e '.title == "Synthetic envelope event"' >/dev/null
api_get_retry "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/monitors/synthetic-cron/checkins/" | jq -e 'length > 0 and .[0].monitorSlug == "synthetic-cron" and .[0].duration == 4.2' >/dev/null
api_get_retry "/api/0/events/07070707070707070707070707070707/attachments/" | jq -e 'length > 0 and .[0].name == "test.txt"' >/dev/null

issues_json="[]"
for _ in $(seq 1 15); do
  issues_json="$(api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/issues/")"
  if jq -e 'map(.title) | index("Synthetic artifact envelope") != null and index("Synthetic store payload") != null and index("Synthetic envelope event") != null and index("Synthetic fresh feedback anchor urgentry") != null' >/dev/null <<<"$issues_json"; then
    break
  fi
  sleep 1
done
jq -e 'map(.title) | index("Synthetic artifact envelope") != null and index("Synthetic store payload") != null and index("Synthetic envelope event") != null and index("Synthetic fresh feedback anchor urgentry") != null' >/dev/null <<<"$issues_json"

feedback_json="[]"
for _ in $(seq 1 15); do
  feedback_json="$(api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/user-feedback/")"
  if jq -e 'map(.name) | index("Urgentry Fresh Reporter") != null' >/dev/null <<<"$feedback_json"; then
    break
  fi
  sleep 1
done
jq -e 'map(.name) | index("Urgentry Fresh Reporter") != null' >/dev/null <<<"$feedback_json"

fresh_event_json="{}"
for _ in $(seq 1 15); do
  fresh_event_json="$(api_get "/api/0/projects/$ORG_SLUG/$PROJECT_SLUG/events/0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a/")"
  if jq -e '.userReport.name == "Urgentry Fresh Reporter"' >/dev/null <<<"$fresh_event_json"; then
    break
  fi
  sleep 1
done
jq -e '.userReport.name == "Urgentry Fresh Reporter"' >/dev/null <<<"$fresh_event_json"

echo "self-hosted sentry baseline passed"
echo "api=$API_URL"
echo "ingest=$INGEST_URL"
echo "worker=$WORKER_URL"
echo "scheduler=$SCHEDULER_URL"
