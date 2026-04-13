#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
K8S_DIR="$APP_DIR/deploy/k8s"
ROTATE_SECRETS="$APP_DIR/deploy/rotate-secrets.sh"

RELEASE="${URGENTRY_HELM_RELEASE:-urgentry}"
NAMESPACE="${URGENTRY_SELF_HOSTED_NAMESPACE:-urgentry-system}"
KEEP_RESOURCES="${URGENTRY_SELF_HOSTED_KEEP_STACK:-false}"
FULLNAME="${URGENTRY_HELM_FULLNAME:-}"
DEPENDENCY_DIR=""
APPLIED_DEPS="false"
INSTALLED_HELM="false"
PF_LOG_DIR=""
API_PF_PID=""
INGEST_PF_PID=""
WORKER_PF_PID=""
SCHEDULER_PF_PID=""
API_URL=""
INGEST_URL=""
WORKER_URL=""
SCHEDULER_URL=""
BOOTSTRAP_PAT=""

usage() {
  cat <<'EOF'
usage: smoke.sh [up|check]

Commands:
  up     Apply temporary dependency manifests, install the Helm chart, run smoke, then uninstall unless URGENTRY_SELF_HOSTED_KEEP_STACK=true.
  check  Run smoke against an already installed Helm release in the target namespace.

Environment:
  URGENTRY_HELM_RELEASE           Helm release name (default: urgentry)
  URGENTRY_HELM_FULLNAME          rendered chart fullname override, if not derived from the release name
  URGENTRY_SELF_HOSTED_NAMESPACE  namespace for the smoke (default: urgentry-system)
  URGENTRY_SELF_HOSTED_KEEP_STACK keep Helm release and temporary dependencies after smoke (true|false)

Notes:
  This smoke validates the chart-owned Urgentry roles against a temporary
  Postgres, MinIO, Valkey, and NATS substrate. It does not claim a custom
  Kubernetes controller or a no-shared-disk HA data plane.
EOF
}

chart_fullname() {
  if [[ -n "$FULLNAME" ]]; then
    printf '%s\n' "$FULLNAME"
  elif [[ "$RELEASE" == *urgentry* ]]; then
    printf '%s\n' "$RELEASE"
  else
    printf '%s-urgentry\n' "$RELEASE"
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
value = json.load(sys.stdin)
for part in sys.argv[1].split("."):
    value = value[int(part)] if isinstance(value, list) else value[part]
print(json.dumps(value) if isinstance(value, (dict, list)) else value)
' "$key"
}

secret_value() {
  local key="$1"
  kubectl -n "$NAMESPACE" get secret urgentry-secret -o json | python3 -c '
import base64
import json
import sys
key = sys.argv[1]
data = json.load(sys.stdin).get("data", {})
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
  while (( SECONDS < deadline )); do
    if kubectl -n "$NAMESPACE" wait --for=condition=complete "job/${job_name}" --timeout=10s >/dev/null 2>&1; then
      return 0
    fi
    if kubectl -n "$NAMESPACE" get job "$job_name" -o jsonpath='{.status.failed}' 2>/dev/null | grep -Eq '^[1-9]'; then
      echo "job ${job_name} failed" >&2
      kubectl -n "$NAMESPACE" logs "job/${job_name}" --tail=200 >&2 || true
      return 1
    fi
  done
  echo "timed out waiting for job/${job_name}" >&2
  kubectl -n "$NAMESPACE" get job "$job_name" >&2 || true
  kubectl -n "$NAMESPACE" logs "job/${job_name}" --tail=200 >&2 || true
  return 1
}

wait_pods_ready() {
  local selector="$1"
  local timeout="${2:-300}"
  if ! kubectl -n "$NAMESPACE" wait --for=condition=Ready pod -l "$selector" --timeout="${timeout}s" >/dev/null; then
    echo "timed out waiting for pods matching ${selector}" >&2
    kubectl -n "$NAMESPACE" get pods -l "$selector" >&2 || true
    return 1
  fi
}

prepare_dependency_bundle() {
  DEPENDENCY_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-helm-deps.XXXXXX")"
  for file in \
    namespace.yaml \
    configmap.yaml \
    secret.yaml \
    postgres.yaml \
    minio.yaml \
    valkey.yaml \
    nats.yaml \
    minio-bootstrap-job.yaml \
    selfhosted-bootstrap-job.yaml \
    urgentry-data-pvc.yaml; do
    cp -f "$K8S_DIR/$file" "$DEPENDENCY_DIR/$file"
  done
  cat >"$DEPENDENCY_DIR/kustomization.yaml" <<'EOF'
namespace: urgentry-system
resources:
  - urgentry-data-pv.yaml
  - namespace.yaml
  - configmap.yaml
  - secret.yaml
  - postgres.yaml
  - minio.yaml
  - valkey.yaml
  - nats.yaml
  - minio-bootstrap-job.yaml
  - selfhosted-bootstrap-job.yaml
  - urgentry-data-pvc.yaml
EOF
  "$ROTATE_SECRETS" k8s \
    --secret-file "$DEPENDENCY_DIR/secret.yaml" \
    --bootstrap-password "HelmSmokeBootstrap!123" \
    --bootstrap-pat "gpat_helm_smoke" \
    --metrics-token "metrics_helm_smoke" \
    --postgres-password "helm-smoke-postgres" \
    --minio-password "helm-smoke-minio" >/dev/null
  python3 - "$DEPENDENCY_DIR" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
pvc = root / "urgentry-data-pvc.yaml"
text = pvc.read_text(encoding="utf-8")
needle = "  resources:\n"
replacement = '  storageClassName: ""\n  volumeName: urgentry-helm-data-kind\n  resources:\n'
if needle not in text:
    raise SystemExit("urgentry-data pvc shape changed")
pvc.write_text(text.replace(needle, replacement, 1), encoding="utf-8")
(root / "urgentry-data-pv.yaml").write_text(
    """apiVersion: v1
kind: PersistentVolume
metadata:
  name: urgentry-helm-data-kind
spec:
  capacity:
    storage: 5Gi
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Delete
  storageClassName: ""
  hostPath:
    path: /var/local/urgentry-helm-data
    type: DirectoryOrCreate
""",
    encoding="utf-8",
)
PY
}

apply_dependencies() {
  kubectl apply -k "$DEPENDENCY_DIR" >/dev/null
  APPLIED_DEPS="true"
  wait_pods_ready app=postgres 300
  wait_pods_ready app=minio 300
  wait_pods_ready app=valkey 300
  wait_pods_ready app=nats 300
  wait_job_complete minio-bootstrap 300
  wait_job_complete urgentry-bootstrap 300
}

install_chart() {
  helm upgrade --install "$RELEASE" "$SCRIPT_DIR" \
    --namespace "$NAMESPACE" \
    --create-namespace \
    --wait \
    --timeout 5m \
    --set image.repository=urgentry \
    --set image.tag=latest \
    --set image.pullPolicy=IfNotPresent \
    --set bootstrap.existingSecret=urgentry-secret \
    --set externalPostgres.existingSecret=urgentry-secret \
    --set-string externalNats.url=nats://nats:4222 \
    --set-string externalValkey.url=redis://valkey:6379/0 \
    --set-string externalMinio.endpoint=http://minio:9000 \
    --set externalMinio.bucket=urgentry-artifacts \
    --set externalMinio.existingSecret=urgentry-secret \
    --set persistence.existingClaim=urgentry-data >/dev/null
  INSTALLED_HELM="true"
}

port_forward() {
  local service="$1"
  local remote_port="$2"
  local local_port="$3"
  local pid_var="$4"
  kubectl -n "$NAMESPACE" port-forward "service/${service}" "${local_port}:${remote_port}" >"$PF_LOG_DIR/${service}.port-forward.log" 2>&1 &
  printf -v "$pid_var" '%s' "$!"
}

start_port_forwards() {
  local fullname api_port ingest_port worker_port scheduler_port
  fullname="$(chart_fullname)"
  PF_LOG_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-helm-port-forward.XXXXXX")"
  api_port="$(free_port)"
  ingest_port="$(free_port)"
  worker_port="$(free_port)"
  scheduler_port="$(free_port)"
  port_forward "${fullname}-api" 8080 "$api_port" API_PF_PID
  port_forward "${fullname}-ingest" 8081 "$ingest_port" INGEST_PF_PID
  port_forward "${fullname}-worker" 8082 "$worker_port" WORKER_PF_PID
  port_forward "${fullname}-scheduler" 8083 "$scheduler_port" SCHEDULER_PF_PID
  API_URL="http://127.0.0.1:${api_port}"
  INGEST_URL="http://127.0.0.1:${ingest_port}"
  WORKER_URL="http://127.0.0.1:${worker_port}"
  SCHEDULER_URL="http://127.0.0.1:${scheduler_port}"
}

wait_chart_stack() {
  local name role
  name="$(chart_fullname)"
  for role in api ingest worker scheduler; do
    wait_pods_ready "app.kubernetes.io/name=urgentry,app.kubernetes.io/instance=${RELEASE},app.kubernetes.io/component=${role}" 300
  done
  start_port_forwards
  wait_http "$API_URL/readyz"
  wait_http "$INGEST_URL/readyz"
  wait_http "$WORKER_URL/readyz"
  wait_http "$SCHEDULER_URL/readyz"
}

assert_runtime_backends() {
  local response async_backend cache_backend
  response="$(curl -fsS "$API_URL/healthz")"
  async_backend="$(printf '%s' "$response" | json_value 'async_backend')"
  cache_backend="$(printf '%s' "$response" | json_value 'cache_backend')"
  if [[ "$async_backend" != "jetstream" ]]; then
    echo "unexpected async backend: got $async_backend want jetstream" >&2
    return 1
  fi
  if [[ "$cache_backend" != "valkey" ]]; then
    echo "unexpected cache backend: got $cache_backend want valkey" >&2
    return 1
  fi
}

smoke_event_flow() {
  local keys_json public_key event_id events_json upload_json attachments_json attachment_id attachment_body tmpfile
  keys_json="$(curl -fsS -H "Authorization: Bearer ${BOOTSTRAP_PAT}" "$API_URL/api/0/projects/urgentry-org/default/keys/")"
  public_key="$(printf '%s' "$keys_json" | json_value '0.public')"
  event_id="helmsmoke$(date +%s)"
  curl -fsS -X POST "$INGEST_URL/api/default-project/store/?sentry_key=$public_key" \
    -H "Content-Type: application/json" \
    -d "{\"event_id\":\"${event_id}\",\"message\":\"self-hosted helm smoke\",\"level\":\"error\",\"platform\":\"go\"}" >/dev/null

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

  tmpfile="$(mktemp "${TMPDIR:-/tmp}/urgentry-helm-attachment.XXXXXX")"
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
  if [[ "$KEEP_RESOURCES" != "true" ]]; then
    if [[ "$INSTALLED_HELM" == "true" ]]; then
      helm uninstall "$RELEASE" --namespace "$NAMESPACE" >/dev/null 2>&1 || true
    fi
    if [[ "$APPLIED_DEPS" == "true" && -n "$DEPENDENCY_DIR" ]]; then
      kubectl delete -k "$DEPENDENCY_DIR" --ignore-not-found >/dev/null 2>&1 || true
    fi
  fi
  if [[ -n "$DEPENDENCY_DIR" ]]; then
    rm -rf "$DEPENDENCY_DIR"
  fi
}

main() {
  local command="${1:-up}"
  case "$command" in
    up|check) ;;
    -h|--help|help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac

  trap cleanup EXIT
  if [[ "$command" == "up" ]]; then
    prepare_dependency_bundle
    apply_dependencies
    install_chart
  fi
  wait_chart_stack
  BOOTSTRAP_PAT="$(secret_value URGENTRY_BOOTSTRAP_PAT)"
  if [[ -z "$BOOTSTRAP_PAT" || "$BOOTSTRAP_PAT" == "change-me-in-production" ]]; then
    echo "helm smoke requires a real URGENTRY_BOOTSTRAP_PAT in secret/urgentry-secret" >&2
    exit 1
  fi
  assert_runtime_backends
  smoke_event_flow

  cat <<EOF
helm smoke passed
namespace=$NAMESPACE
release=$RELEASE
api=$API_URL
ingest=$INGEST_URL
worker=$WORKER_URL
scheduler=$SCHEDULER_URL
EOF
}

main "$@"
