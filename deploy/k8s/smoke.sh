#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
KUSTOMIZE_DIR="${URGENTRY_SELF_HOSTED_KUSTOMIZE_DIR:-$SCRIPT_DIR}"
NAMESPACE="${URGENTRY_SELF_HOSTED_NAMESPACE:-urgentry-system}"
KEEP_RESOURCES="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"
ROTATE_SECRETS="$APP_DIR/deploy/rotate-secrets.sh"
SMOKE_ATTEMPTS="${URGENTRY_SELF_HOSTED_SMOKE_ATTEMPTS:-3}"

API_URL=""
INGEST_URL=""
WORKER_URL=""
SCHEDULER_URL=""
BOOTSTRAP_PAT=""
API_PF_PID=""
INGEST_PF_PID=""
WORKER_PF_PID=""
SCHEDULER_PF_PID=""
APPLIED_BUNDLE="false"
PF_LOG_DIR=""
APPLY_DIR=""
KIND_HOSTPATH=""

usage() {
  cat <<'EOF'
usage: smoke.sh [up|check]

Commands:
  up     Apply the kustomize bundle, wait for readiness, run the smoke flow, then delete it unless URGENTRY_SELF_HOSTED_KEEP_STACK=true.
  check  Run the smoke flow against an already applied bundle in the target namespace.

Optional environment:
  URGENTRY_SELF_HOSTED_NAMESPACE   namespace for the bundle (default: urgentry-system)
  URGENTRY_SELF_HOSTED_KEEP_STACK  keep resources after smoke (true|false)

Notes:
  The applied urgentry-secret must already contain real secret values. Placeholder values fail closed.
EOF
}

current_context() {
  kubectl config current-context 2>/dev/null || true
}

kind_cluster_name() {
  local context
  context="$(current_context)"
  if [[ "$context" == kind-* ]]; then
    printf '%s\n' "${context#kind-}"
  fi
}

is_kind_context() {
  [[ -n "$(kind_cluster_name)" ]]
}

safe_slug() {
  python3 - "$1" <<'PY'
import re
import sys

slug = re.sub(r"[^a-z0-9-]+", "-", sys.argv[1].lower()).strip("-")
print(slug or "smoke")
PY
}

build_local_image() {
  local build_log
  build_log="$(mktemp "${TMPDIR:-/tmp}/urgentry-k8s-build.XXXXXX")"
  if docker build \
    --build-arg "URGENTRY_BUILD_TAGS=${URGENTRY_BUILD_TAGS:-netgo,osusergo}" \
    -t urgentry:latest \
    -f "$APP_DIR/Dockerfile" \
    "$APP_DIR" >"$build_log" 2>&1; then
    rm -f "$build_log"
    return 0
  fi
  cat "$build_log" >&2
  rm -f "$build_log"
  return 1
}

ensure_kind_image() {
  local cluster
  cluster="$(kind_cluster_name)"
  if [[ -z "$cluster" ]]; then
    return 0
  fi
  build_local_image
  kind load docker-image urgentry:latest --name "$cluster" >/dev/null
}

rewrite_namespace() {
  python3 - "$1" "$2" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
namespace = sys.argv[2]
kustom = root / "kustomization.yaml"
text = kustom.read_text(encoding="utf-8")
lines = []
replaced = False
for line in text.splitlines():
    if line.startswith("namespace:"):
        lines.append(f"namespace: {namespace}")
        replaced = True
    else:
        lines.append(line)
if not replaced:
    lines.insert(0, f"namespace: {namespace}")
kustom.write_text("\n".join(lines) + "\n", encoding="utf-8")
PY
}

prepare_apply_dir() {
  APPLY_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-k8s-smoke.XXXXXX")"
  cp -R "$KUSTOMIZE_DIR/." "$APPLY_DIR/"
  rewrite_namespace "$APPLY_DIR" "$NAMESPACE"

  if grep -q 'REPLACE_ME_' "$APPLY_DIR/secret.yaml"; then
    "$ROTATE_SECRETS" k8s \
      --secret-file "$APPLY_DIR/secret.yaml" \
      --bootstrap-password "K8sSmokeBootstrap!123" \
      --bootstrap-pat "gpat_k8s_smoke" \
      --metrics-token "metrics_k8s_smoke" \
      --postgres-password "k8s-smoke-postgres" \
      --minio-password "k8s-smoke-minio" >/dev/null
  fi

  if is_kind_context; then
    python3 - "$APPLY_DIR" "$NAMESPACE" <<'PY'
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])
namespace = sys.argv[2]
slug = re.sub(r"[^a-z0-9-]+", "-", namespace.lower()).strip("-") or "smoke"
pv_name = f"urgentry-k8s-data-{slug}"
host_path = f"/var/local/{pv_name}"
pvc = root / "urgentry-data-pvc.yaml"
text = pvc.read_text(encoding="utf-8")
needle = "  resources:\n"
replacement = f'  storageClassName: ""\n  volumeName: {pv_name}\n  resources:\n'
if needle not in text:
    raise SystemExit("urgentry-data pvc shape changed")
pvc.write_text(text.replace(needle, replacement, 1), encoding="utf-8")
(root / "urgentry-data-pv.yaml").write_text(
    "apiVersion: v1\n"
    "kind: PersistentVolume\n"
    f"metadata:\n  name: {pv_name}\n"
    "spec:\n"
    "  capacity:\n"
    "    storage: 5Gi\n"
    "  accessModes:\n"
    "    - ReadWriteMany\n"
    "  persistentVolumeReclaimPolicy: Delete\n"
    '  storageClassName: ""\n'
    "  hostPath:\n"
    f"    path: {host_path}\n"
    "    type: DirectoryOrCreate\n",
    encoding="utf-8",
)
kustom = root / "kustomization.yaml"
ktext = kustom.read_text(encoding="utf-8")
if "urgentry-data-pv.yaml" not in ktext:
    ktext = ktext.replace("resources:\n", "resources:\n  - urgentry-data-pv.yaml\n", 1)
kustom.write_text(ktext, encoding="utf-8")
PY
    KIND_HOSTPATH="$(python3 - "$NAMESPACE" <<'PY'
import re
import sys
slug = re.sub(r"[^a-z0-9-]+", "-", sys.argv[1].lower()).strip("-") or "smoke"
print(f"/var/local/urgentry-k8s-data-{slug}")
PY
)"
  fi
}

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

json_value() {
  local key="$1"
  python3 -c '
import json
import sys
key = sys.argv[1]
value = json.load(sys.stdin)
for part in key.split("."):
    if isinstance(value, list):
        value = value[int(part)]
    else:
        value = value[part]
    if isinstance(value, (dict, list)):
        continue
if isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
' "$key"
}

secret_value() {
  local key="$1"
  kubectl -n "$NAMESPACE" get secret urgentry-secret -o json | python3 -c '
import base64
import json
import sys

key = sys.argv[1]
payload = json.load(sys.stdin)
data = payload.get("data", {})
value = data.get(key, "")
if value:
    print(base64.b64decode(value).decode("utf-8"))
' "$key"
}

wait_http() {
  local url="$1"
  local timeout="${2:-120}"
  local deadline=$((SECONDS + timeout))
  until curl -fsS "$url" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for $url" >&2
      return 1
    fi
    sleep 2
  done
}

wait_job_complete() {
  local job_name="$1"
  local timeout="${2:-300}"
  local deadline=$((SECONDS + timeout))
  local payload

  while (( SECONDS < deadline )); do
    if ! payload="$(kubectl -n "$NAMESPACE" get job "$job_name" -o json 2>/dev/null)"; then
      sleep 2
      continue
    fi
    set +e
    printf '%s' "$payload" | python3 -c '
import json
import sys

job = json.load(sys.stdin)
status = job.get("status", {})
if status.get("succeeded", 0) >= 1:
    raise SystemExit(0)
failed = status.get("failed", 0)
active = status.get("active", 0)
if failed > 0 and active == 0:
    raise SystemExit(2)
raise SystemExit(1)
'
    local status=$?
    set -e
    if [[ $status -eq 0 ]]; then
      return 0
    fi
    if [[ $status -eq 2 ]]; then
      echo "job ${job_name} failed" >&2
      kubectl -n "$NAMESPACE" get job "$job_name" >&2 || true
      kubectl -n "$NAMESPACE" logs "job/${job_name}" --tail=200 >&2 || true
      return 1
    fi
    sleep 2
  done

  echo "timed out waiting for job/${job_name} to complete" >&2
  kubectl -n "$NAMESPACE" get job "$job_name" >&2 || true
  kubectl -n "$NAMESPACE" logs "job/${job_name}" --tail=200 >&2 || true
  return 1
}

wait_pods_ready() {
  local selector="$1"
  local timeout="${2:-300}"
  local deadline=$((SECONDS + timeout))
  local payload

  while (( SECONDS < deadline )); do
    if ! payload="$(kubectl -n "$NAMESPACE" get pods -l "$selector" -o json 2>/dev/null)"; then
      sleep 2
      continue
    fi
    if printf '%s' "$payload" | python3 -c '
import json
import sys

pods = json.load(sys.stdin).get("items", [])
if not pods:
    raise SystemExit(1)
for pod in pods:
    conditions = {c.get("type"): c.get("status") for c in pod.get("status", {}).get("conditions", [])}
    if conditions.get("Ready") != "True":
        raise SystemExit(1)
raise SystemExit(0)
'
    then
      return 0
    fi
    sleep 2
  done

  echo "timed out waiting for pods matching ${selector} to become ready" >&2
  kubectl -n "$NAMESPACE" get pods -l "$selector" >&2 || true
  return 1
}

port_forward() {
  local service="$1"
  local remote_port="$2"
  local local_port="$3"
  local pid_var="$4"

  kubectl -n "$NAMESPACE" port-forward "service/${service}" "${local_port}:${remote_port}" >"$PF_LOG_DIR/${service}.port-forward.log" 2>&1 &
  printf -v "$pid_var" '%s' "$!"
}

cleanup() {
  for pid in "$API_PF_PID" "$INGEST_PF_PID" "$WORKER_PF_PID" "$SCHEDULER_PF_PID"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  if [[ -n "$PF_LOG_DIR" ]]; then
    rm -rf "$PF_LOG_DIR"
  fi
  if [[ "$APPLIED_BUNDLE" == "true" && "$KEEP_RESOURCES" != "true" ]]; then
    kubectl delete -k "${APPLY_DIR:-$KUSTOMIZE_DIR}" --ignore-not-found >/dev/null 2>&1 || true
  fi
  if [[ -n "$APPLY_DIR" ]]; then
    rm -rf "$APPLY_DIR"
  fi
  if [[ -n "$KIND_HOSTPATH" && "$KEEP_RESOURCES" != "true" ]]; then
    local cluster
    cluster="$(kind_cluster_name)"
    if [[ -n "$cluster" ]]; then
      docker exec "${cluster}-control-plane" rm -rf "$KIND_HOSTPATH" >/dev/null 2>&1 || true
    fi
  fi
}

start_port_forwards() {
  local api_port ingest_port worker_port scheduler_port
  PF_LOG_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-k8s-port-forward.XXXXXX")"
  api_port="$(free_port)"
  ingest_port="$(free_port)"
  worker_port="$(free_port)"
  scheduler_port="$(free_port)"
  port_forward urgentry-api 8080 "$api_port" API_PF_PID
  port_forward urgentry-ingest 8081 "$ingest_port" INGEST_PF_PID
  port_forward urgentry-worker 8082 "$worker_port" WORKER_PF_PID
  port_forward urgentry-scheduler 8083 "$scheduler_port" SCHEDULER_PF_PID
  API_URL="http://127.0.0.1:${api_port}"
  INGEST_URL="http://127.0.0.1:${ingest_port}"
  WORKER_URL="http://127.0.0.1:${worker_port}"
  SCHEDULER_URL="http://127.0.0.1:${scheduler_port}"
}

wait_stack() {
  wait_pods_ready app=postgres 300
  wait_pods_ready app=minio 300
  wait_pods_ready app=valkey 300
  wait_pods_ready app=nats 300
  wait_job_complete minio-bootstrap 300
  wait_job_complete urgentry-bootstrap 300
  wait_pods_ready app=urgentry,role=api 300
  wait_pods_ready app=urgentry,role=ingest 300
  wait_pods_ready app=urgentry,role=worker 300
  wait_pods_ready app=urgentry,role=scheduler 300
  assert_role_topology
  start_port_forwards
  wait_http "$API_URL/readyz"
  wait_http "$INGEST_URL/readyz"
  wait_http "$WORKER_URL/readyz"
  wait_http "$SCHEDULER_URL/readyz"
}

assert_role_topology() {
  local role count total=0
  for role in api ingest worker scheduler; do
    count="$(kubectl -n "$NAMESPACE" get pods -l "app=urgentry,role=${role}" --no-headers 2>/dev/null | grep -c . || true)"
    if (( count < 1 )); then
      echo "k8s smoke expected at least one ready pod for role ${role}" >&2
      exit 1
    fi
    total=$((total + count))
  done
  if (( total < 4 )); then
    echo "k8s smoke expected split topology with at least four urgentry pods, got ${total}" >&2
    exit 1
  fi
}

assert_runtime_backends() {
  local response async_backend cache_backend expected_async expected_cache
  response="$(curl -fsS "$API_URL/healthz")"
  async_backend="$(printf '%s' "$response" | json_value 'async_backend')"
  cache_backend="$(printf '%s' "$response" | json_value 'cache_backend')"
  expected_async="${URGENTRY_ASYNC_BACKEND:-jetstream}"
  expected_cache="${URGENTRY_CACHE_BACKEND:-valkey}"
  if [[ "$async_backend" != "$expected_async" ]]; then
    echo "unexpected async backend: got $async_backend want $expected_async" >&2
    return 1
  fi
  if [[ "$cache_backend" != "$expected_cache" ]]; then
    echo "unexpected cache backend: got $cache_backend want $expected_cache" >&2
    return 1
  fi
}

smoke_event_flow() {
  local keys_json public_key event_id events_json upload_json attachments_json attachment_id attachment_body tmpfile
  keys_json="$(curl -fsS -H "Authorization: Bearer ${BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/keys/")"
  public_key="$(printf '%s' "$keys_json" | json_value '0.public')"

  event_id="k8ssmoke$(date +%s)"
  curl -fsS -X POST "$INGEST_URL/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"message\":\"self-hosted k8s smoke\",\"level\":\"error\",\"platform\":\"go\"}" >/dev/null

  local deadline=$((SECONDS + 90))
  while (( SECONDS < deadline )); do
    events_json="$(curl -fsS -H "Authorization: Bearer ${BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/events/")"
    if printf '%s' "$events_json" | grep -q "$event_id"; then
      break
    fi
    sleep 2
  done
  if ! printf '%s' "$events_json" | grep -q "$event_id"; then
    echo "event $event_id did not appear in API list" >&2
    return 1
  fi

  tmpfile="$(mktemp "${TMPDIR:-/tmp}/urgentry-k8s-attachment.XXXXXX")"
  printf 'blob-smoke-%s' "$event_id" >"$tmpfile"
  upload_json="$(curl -fsS -H "Authorization: Bearer ${BOOTSTRAP_PAT}" -F "event_id=${event_id}" -F "file=@${tmpfile};filename=smoke.txt" "$API_URL/api/0/projects/urgentry-org/default/attachments/")"
  rm -f "$tmpfile"

  attachment_id="$(printf '%s' "$upload_json" | json_value 'id')"
  attachments_json="$(curl -fsS -H "Authorization: Bearer ${BOOTSTRAP_PAT}" "$API_URL/api/0/events/${event_id}/attachments/")"
  if ! printf '%s' "$attachments_json" | grep -q "$attachment_id"; then
    echo "attachment $attachment_id did not appear in event attachment list" >&2
    return 1
  fi

  attachment_body="$(curl -fsS -H "Authorization: Bearer ${BOOTSTRAP_PAT}" "$API_URL/api/0/events/${event_id}/attachments/${attachment_id}/")"
  if [[ "$attachment_body" != "blob-smoke-${event_id}" ]]; then
    echo "attachment body mismatch" >&2
    return 1
  fi
}

main() {
  local command="${1:-up}"
  case "$command" in
    up|check) ;;
    -h|--help|help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac

  if [[ "$command" == "up" ]]; then
    trap cleanup EXIT
    prepare_apply_dir
    ensure_kind_image
    kubectl apply -k "${APPLY_DIR:-$KUSTOMIZE_DIR}" >/dev/null
    APPLIED_BUNDLE="true"
  else
    trap cleanup EXIT
  fi

  wait_stack
  BOOTSTRAP_PAT="$(secret_value URGENTRY_BOOTSTRAP_PAT)"
  if [[ -z "$BOOTSTRAP_PAT" || "$BOOTSTRAP_PAT" == "change-me-in-production" ]]; then
    echo "k8s smoke requires a real URGENTRY_BOOTSTRAP_PAT in secret/urgentry-secret" >&2
    exit 1
  fi
  local attempt
  for attempt in $(seq 1 "$SMOKE_ATTEMPTS"); do
    if assert_runtime_backends && smoke_event_flow; then
      break
    fi
    if [[ "$attempt" == "$SMOKE_ATTEMPTS" ]]; then
      exit 1
    fi
    sleep 2
  done

  cat <<EOF
k8s smoke passed
namespace=$NAMESPACE
api=$API_URL
ingest=$INGEST_URL
worker=$WORKER_URL
scheduler=$SCHEDULER_URL
EOF
}

main "$@"
