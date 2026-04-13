#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_DIR="$APP_DIR/deploy/compose"
K8S_DIR="$APP_DIR/deploy/k8s"
COMPOSE_FILE="$COMPOSE_DIR/docker-compose.yml"
DEFAULT_ENV_FILE="$COMPOSE_DIR/.env"
DEFAULT_SECRET_FILE="$K8S_DIR/secret.yaml"

generate_secret() {
  local prefix="$1"
  local length="$2"
  python3 - "$prefix" "$length" <<'PY'
import secrets
import string
import sys

prefix, length = sys.argv[1], int(sys.argv[2])
alphabet = string.ascii_letters + string.digits
value = "".join(secrets.choice(alphabet) for _ in range(length))
print(prefix + value)
PY
}

make_temp_file() {
  local stem="$1"
  local suffix="${2:-}"
  local path
  path="$(mktemp "${TMPDIR:-/tmp}/${stem}.XXXXXX")"
  if [[ -n "$suffix" ]]; then
    mv -f "$path" "${path}${suffix}"
    path="${path}${suffix}"
  fi
  printf '%s\n' "$path"
}

write_summary() {
  local path="$1"
  local mode="$2"
  local target="$3"
  local backup="$4"
  local applied="$5"
  local restarted="$6"
  local bootstrap_password="$7"
  local bootstrap_pat="$8"
  local metrics_token="$9"
  local postgres_password="${10}"
  local minio_password="${11}"
  python3 - "$path" "$mode" "$target" "$backup" "$applied" "$restarted" "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_password" <<'PY'
import json
import sys

(
    path,
    mode,
    target,
    backup,
    applied,
    restarted,
    bootstrap_password,
    bootstrap_pat,
    metrics_token,
    postgres_password,
    minio_password,
) = sys.argv[1:]

def mask(secret):
    if len(secret) <= 4:
        return "***"
    return "***" + secret[-4:]

summary = {
    "mode": mode,
    "target": target,
    "backupFile": backup,
    "appliedToRuntime": applied == "true",
    "restartedWorkloads": restarted == "true",
    "rotated": {
        "URGENTRY_BOOTSTRAP_PASSWORD": mask(bootstrap_password),
        "URGENTRY_BOOTSTRAP_PAT": mask(bootstrap_pat),
        "URGENTRY_METRICS_TOKEN": mask(metrics_token),
        "POSTGRES_PASSWORD": mask(postgres_password),
        "MINIO_ROOT_PASSWORD": mask(minio_password),
    },
}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(summary, fh, indent=2)
    fh.write("\n")
PY
}

read_env_value() {
  local path="$1"
  local key="$2"
  python3 - "$path" "$key" <<'PY'
import sys

path, key = sys.argv[1:]
value = ""
with open(path, "r", encoding="utf-8") as fh:
    for line in fh:
        if line.startswith(key + "="):
            value = line.strip().split("=", 1)[1]
print(value)
PY
}

read_secret_file_value() {
  local path="$1"
  local key="$2"
  python3 - "$path" "$key" <<'PY'
import json
import sys

path, key = sys.argv[1:]
value = ""
with open(path, "r", encoding="utf-8") as fh:
    for line in fh:
        if line.startswith(f"  {key}:"):
            value = line.strip().split(":", 1)[1].strip()
if value.startswith('"') and value.endswith('"'):
    value = json.loads(value)
print(value)
PY
}

rewrite_env_file() {
  local path="$1"
  local backup="$2"
  local postgres_user="$3"
  local postgres_db="$4"
  local bootstrap_password="$5"
  local bootstrap_pat="$6"
  local metrics_token="$7"
  local postgres_password="$8"
  local minio_password="$9"
  python3 - "$path" "$backup" "$postgres_user" "$postgres_db" "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_password" <<'PY'
import shutil
import sys

(
    path,
    backup,
    postgres_user,
    postgres_db,
    bootstrap_password,
    bootstrap_pat,
    metrics_token,
    postgres_password,
    minio_password,
) = sys.argv[1:]
shutil.copy2(path, backup)
updates = {
    "URGENTRY_BOOTSTRAP_PASSWORD": bootstrap_password,
    "URGENTRY_BOOTSTRAP_PAT": bootstrap_pat,
    "URGENTRY_METRICS_TOKEN": metrics_token,
    "POSTGRES_PASSWORD": postgres_password,
    "URGENTRY_CONTROL_DATABASE_URL": f"postgres://{postgres_user}:{postgres_password}@postgres:5432/{postgres_db}?sslmode=disable",
    "URGENTRY_TELEMETRY_DATABASE_URL": f"postgres://{postgres_user}:{postgres_password}@postgres:5432/{postgres_db}?sslmode=disable",
    "MINIO_ROOT_PASSWORD": minio_password,
}
optional = {"URGENTRY_S3_SECRET_KEY": minio_password}
seen = set()
out = []
with open(path, "r", encoding="utf-8") as fh:
    for raw in fh:
        line = raw.rstrip("\n")
        replaced = False
        for key, value in {**updates, **optional}.items():
            prefix = key + "="
            if line.startswith(prefix):
                out.append(prefix + value)
                seen.add(key)
                replaced = True
                break
        if not replaced:
            out.append(line)
for key, value in updates.items():
    if key not in seen:
        out.append(key + "=" + value)
with open(path, "w", encoding="utf-8") as fh:
    fh.write("\n".join(out) + "\n")
PY
}

rewrite_secret_file() {
  local path="$1"
  local backup="$2"
  local postgres_user="$3"
  local postgres_db="$4"
  local bootstrap_password="$5"
  local bootstrap_pat="$6"
  local metrics_token="$7"
  local postgres_password="$8"
  local minio_user="$9"
  local minio_password="${10}"
  python3 - "$path" "$backup" "$postgres_user" "$postgres_db" "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_user" "$minio_password" <<'PY'
import json
import shutil
import sys

(
    path,
    backup,
    postgres_user,
    postgres_db,
    bootstrap_password,
    bootstrap_pat,
    metrics_token,
    postgres_password,
    minio_user,
    minio_password,
) = sys.argv[1:]
shutil.copy2(path, backup)
updates = {
    "POSTGRES_USER": postgres_user,
    "POSTGRES_PASSWORD": postgres_password,
    "POSTGRES_DB": postgres_db,
    "URGENTRY_CONTROL_DATABASE_URL": f"postgres://{postgres_user}:{postgres_password}@postgres:5432/{postgres_db}?sslmode=disable",
    "URGENTRY_TELEMETRY_DATABASE_URL": f"postgres://{postgres_user}:{postgres_password}@postgres:5432/{postgres_db}?sslmode=disable",
    "MINIO_ROOT_USER": minio_user,
    "MINIO_ROOT_PASSWORD": minio_password,
    "URGENTRY_S3_ACCESS_KEY": minio_user,
    "URGENTRY_S3_SECRET_KEY": minio_password,
    "URGENTRY_BOOTSTRAP_PASSWORD": bootstrap_password,
    "URGENTRY_BOOTSTRAP_PAT": bootstrap_pat,
    "URGENTRY_METRICS_TOKEN": metrics_token,
}
seen = set()
out = []
indent = "  "
with open(path, "r", encoding="utf-8") as fh:
    for raw in fh:
        line = raw.rstrip("\n")
        replaced = False
        for key, value in updates.items():
            prefix = f"  {key}:"
            if line.startswith(prefix):
                out.append(f"  {key}: {json.dumps(value)}")
                seen.add(key)
                replaced = True
                break
        if not replaced:
            out.append(line)
for key, value in updates.items():
    if key not in seen:
        out.append(f"{indent}{key}: {json.dumps(value)}")
with open(path, "w", encoding="utf-8") as fh:
    fh.write("\n".join(out) + "\n")
PY
}

usage() {
  cat <<'EOF'
usage: rotate-secrets.sh <compose|k8s> [options]

Compose options:
  --env-file <path>           env file to rewrite (default: deploy/compose/.env)
  --project-name <name>       docker compose project name (defaults to URGENTRY_SELF_HOSTED_PROJECT or COMPOSE_PROJECT_NAME)
  --summary-file <path>       where to write the rotated-secret summary JSON
  --no-restart                rewrite files only; do not apply the new secrets to a live runtime
  --no-verify                 skip post-rotation security-report and smoke checks

Kubernetes options:
  --secret-file <path>        secret manifest to rewrite (default: deploy/k8s/secret.yaml)
  --namespace <name>          namespace for live apply steps (default: urgentry-system)
  --apply                     apply the rewritten secret and restart live workloads
  --no-restart                with no --apply, rewrite the manifest only

Shared options:
  --bootstrap-password <val>
  --bootstrap-pat <val>
  --metrics-token <val>
  --postgres-password <val>
  --minio-password <val>

Notes:
  - Generated values are written to the summary JSON instead of stdout.
  - Compose live rotation rewrites the env file, updates Postgres credentials in place,
    recreates MinIO and the four Urgentry roles, and reruns security-report plus smoke.sh check.
  - Compose live rotation uses the explicit --project-name, URGENTRY_SELF_HOSTED_PROJECT,
    or COMPOSE_PROJECT_NAME from the env file so it hits the active stack instead of the
    directory-default compose project name.
  - Kubernetes live rotation rewrites the secret manifest; with --apply it also updates the
    live secret, alters the Postgres user password, and restarts MinIO plus the four Urgentry deployments.
EOF
}

compose_cmd() {
  docker compose --project-name "$COMPOSE_PROJECT_NAME" --env-file "$COMPOSE_ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

compose_stack_running() {
  [[ -n "$(compose_cmd ps -q urgentry-api 2>/dev/null)" ]]
}

resolve_compose_project_name() {
  if [[ -n "${COMPOSE_PROJECT_NAME:-}" ]]; then
    return 0
  fi
  COMPOSE_PROJECT_NAME="$(read_env_value "$COMPOSE_ENV_FILE" COMPOSE_PROJECT_NAME)"
  if [[ -z "$COMPOSE_PROJECT_NAME" ]]; then
    COMPOSE_PROJECT_NAME="urgentry-selfhosted"
  fi
}

rotate_compose() {
  COMPOSE_ENV_FILE="$DEFAULT_ENV_FILE"
  COMPOSE_PROJECT_NAME="${URGENTRY_SELF_HOSTED_PROJECT:-}"
  local summary_file=""
  local no_restart="false"
  local no_verify="false"
  local bootstrap_password=""
  local bootstrap_pat=""
  local metrics_token=""
  local postgres_password=""
  local minio_password=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --env-file) COMPOSE_ENV_FILE="$2"; shift 2 ;;
      --project-name) COMPOSE_PROJECT_NAME="$2"; shift 2 ;;
      --summary-file) summary_file="$2"; shift 2 ;;
      --no-restart) no_restart="true"; shift ;;
      --no-verify) no_verify="true"; shift ;;
      --bootstrap-password) bootstrap_password="$2"; shift 2 ;;
      --bootstrap-pat) bootstrap_pat="$2"; shift 2 ;;
      --metrics-token) metrics_token="$2"; shift 2 ;;
      --postgres-password) postgres_password="$2"; shift 2 ;;
      --minio-password) minio_password="$2"; shift 2 ;;
      *) usage >&2; exit 2 ;;
    esac
  done
  if [[ ! -f "$COMPOSE_ENV_FILE" ]]; then
    echo "compose env file not found: $COMPOSE_ENV_FILE" >&2
    exit 1
  fi
  resolve_compose_project_name
  bootstrap_password="${bootstrap_password:-$(generate_secret "" 28)}"
  bootstrap_pat="${bootstrap_pat:-$(generate_secret "gpat_" 28)}"
  metrics_token="${metrics_token:-$(generate_secret "metrics_" 28)}"
  postgres_password="${postgres_password:-$(generate_secret "" 28)}"
  minio_password="${minio_password:-$(generate_secret "" 28)}"
  summary_file="${summary_file:-$(make_temp_file urgentry-compose-rotation .json)}"
  local backup_file="${COMPOSE_ENV_FILE}.bak.$(date +%Y%m%d%H%M%S)"
  local postgres_user postgres_db stack_running restarted applied
  postgres_user="$(read_env_value "$COMPOSE_ENV_FILE" POSTGRES_USER)"
  postgres_db="$(read_env_value "$COMPOSE_ENV_FILE" POSTGRES_DB)"
  stack_running="false"
  restarted="false"
  applied="false"
  if compose_stack_running; then
    stack_running="true"
  fi
  rewrite_env_file "$COMPOSE_ENV_FILE" "$backup_file" "$postgres_user" "$postgres_db" \
    "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_password"
  if [[ "$stack_running" == "true" && "$no_restart" != "true" ]]; then
    compose_cmd stop minio urgentry-api urgentry-ingest urgentry-worker urgentry-scheduler >/dev/null || true
    compose_cmd exec -T postgres psql -U "$postgres_user" -d "$postgres_db" \
      -c "ALTER USER \"$postgres_user\" WITH PASSWORD '${postgres_password}';" >/dev/null
    URGENTRY_SELF_HOSTED_ENV_FILE="$COMPOSE_ENV_FILE" bash "$COMPOSE_DIR/ops.sh" rotate-bootstrap >/dev/null
    compose_cmd rm -f minio urgentry-api urgentry-ingest urgentry-worker urgentry-scheduler >/dev/null || true
    compose_cmd up -d minio urgentry-api urgentry-ingest urgentry-worker urgentry-scheduler >/dev/null
    restarted="true"
    applied="true"
    if [[ "$no_verify" != "true" ]]; then
      URGENTRY_SELF_HOSTED_ENV_FILE="$COMPOSE_ENV_FILE" bash "$COMPOSE_DIR/ops.sh" security-report >/dev/null
      URGENTRY_SELF_HOSTED_ENV_FILE="$COMPOSE_ENV_FILE" URGENTRY_SELF_HOSTED_PROJECT="$COMPOSE_PROJECT_NAME" URGENTRY_SELF_HOSTED_KEEP_STACK=true bash "$COMPOSE_DIR/smoke.sh" check >/dev/null
      URGENTRY_SELF_HOSTED_ENV_FILE="$COMPOSE_ENV_FILE" bash "$COMPOSE_DIR/ops.sh" record-action secret.rotate "rotated compose secrets via rotate-secrets.sh" >/dev/null
    fi
  fi
  write_summary "$summary_file" "compose" "$COMPOSE_ENV_FILE" "$backup_file" "$applied" "$restarted" \
    "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_password"
  printf 'compose secret rotation summary written to %s\n' "$summary_file"
}

rotate_k8s() {
  local secret_file="$DEFAULT_SECRET_FILE"
  local namespace="${URGENTRY_SELF_HOSTED_NAMESPACE:-urgentry-system}"
  local summary_file=""
  local apply_live="false"
  local no_restart="false"
  local bootstrap_password=""
  local bootstrap_pat=""
  local metrics_token=""
  local postgres_password=""
  local minio_password=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --secret-file) secret_file="$2"; shift 2 ;;
      --namespace) namespace="$2"; shift 2 ;;
      --summary-file) summary_file="$2"; shift 2 ;;
      --apply) apply_live="true"; shift ;;
      --no-restart) no_restart="true"; shift ;;
      --bootstrap-password) bootstrap_password="$2"; shift 2 ;;
      --bootstrap-pat) bootstrap_pat="$2"; shift 2 ;;
      --metrics-token) metrics_token="$2"; shift 2 ;;
      --postgres-password) postgres_password="$2"; shift 2 ;;
      --minio-password) minio_password="$2"; shift 2 ;;
      *) usage >&2; exit 2 ;;
    esac
  done
  if [[ ! -f "$secret_file" ]]; then
    echo "k8s secret file not found: $secret_file" >&2
    exit 1
  fi
  if [[ "$apply_live" == "true" && "$no_restart" == "true" ]]; then
    echo "k8s live secret rotation requires rollout restarts; rerun without --no-restart" >&2
    exit 1
  fi
  bootstrap_password="${bootstrap_password:-$(generate_secret "" 28)}"
  bootstrap_pat="${bootstrap_pat:-$(generate_secret "gpat_" 28)}"
  metrics_token="${metrics_token:-$(generate_secret "metrics_" 28)}"
  postgres_password="${postgres_password:-$(generate_secret "" 28)}"
  minio_password="${minio_password:-$(generate_secret "" 28)}"
  summary_file="${summary_file:-$(make_temp_file urgentry-k8s-rotation .json)}"
  local backup_file="${secret_file}.bak.$(date +%Y%m%d%H%M%S)"
  local postgres_user postgres_db minio_user applied restarted postgres_pod api_pod
  postgres_user="$(read_secret_file_value "$secret_file" POSTGRES_USER)"
  postgres_db="$(read_secret_file_value "$secret_file" POSTGRES_DB)"
  minio_user="$(read_secret_file_value "$secret_file" MINIO_ROOT_USER)"
  applied="false"
  restarted="false"
  rewrite_secret_file "$secret_file" "$backup_file" "$postgres_user" "$postgres_db" \
    "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_user" "$minio_password"
  if [[ "$apply_live" == "true" ]]; then
    postgres_pod="$(kubectl -n "$namespace" get pods -l app=postgres -o jsonpath='{.items[0].metadata.name}')"
    kubectl -n "$namespace" exec "$postgres_pod" -- psql -U "$postgres_user" -d "$postgres_db" \
      -c "ALTER USER \"$postgres_user\" WITH PASSWORD '${postgres_password}';" >/dev/null
    kubectl apply -f "$secret_file" >/dev/null
    applied="true"
    if [[ "$no_restart" != "true" ]]; then
      kubectl -n "$namespace" rollout restart deployment/urgentry-api deployment/urgentry-ingest deployment/urgentry-worker deployment/urgentry-scheduler >/dev/null
      kubectl -n "$namespace" rollout restart statefulset/minio >/dev/null
      kubectl -n "$namespace" rollout status deployment/urgentry-api --timeout=300s >/dev/null
      kubectl -n "$namespace" rollout status deployment/urgentry-ingest --timeout=300s >/dev/null
      kubectl -n "$namespace" rollout status deployment/urgentry-worker --timeout=300s >/dev/null
      kubectl -n "$namespace" rollout status deployment/urgentry-scheduler --timeout=300s >/dev/null
      kubectl -n "$namespace" rollout status statefulset/minio --timeout=300s >/dev/null
      api_pod="$(kubectl -n "$namespace" get pods -l app=urgentry-api -o jsonpath='{.items[0].metadata.name}')"
      kubectl -n "$namespace" exec "$api_pod" -- sh -lc 'urgentry self-hosted rotate-bootstrap --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --email "$URGENTRY_BOOTSTRAP_EMAIL" --password "$URGENTRY_BOOTSTRAP_PASSWORD" --pat "$URGENTRY_BOOTSTRAP_PAT"' >/dev/null
      restarted="true"
    fi
  fi
  write_summary "$summary_file" "k8s" "$secret_file" "$backup_file" "$applied" "$restarted" \
    "$bootstrap_password" "$bootstrap_pat" "$metrics_token" "$postgres_password" "$minio_password"
  printf 'k8s secret rotation summary written to %s\n' "$summary_file"
}

main() {
  if [[ $# -lt 1 ]]; then
    usage >&2
    exit 2
  fi
  case "$1" in
    compose)
      shift
      rotate_compose "$@"
      ;;
    k8s)
      shift
      rotate_k8s "$@"
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

main "$@"
