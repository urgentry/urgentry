#!/usr/bin/env bash
# deploy-server.sh — Deploy Urgentry to a Linux server via SSH.
#
# Usage:
#   bash scripts/deploy-server.sh <ssh-target> [options]
#
# Examples:
#   bash scripts/deploy-server.sh root@myserver.com
#   bash scripts/deploy-server.sh root@myserver.com --base-url https://sentry.example.com
#   bash scripts/deploy-server.sh root@myserver.com --email admin@example.com --password changeme
#
# This script:
#   1. Builds a linux/amd64 binary locally
#   2. Uploads it atomically with checksum verification
#   3. Writes a hardened systemd unit + env file
#   4. Starts Urgentry and waits for readiness
#
# The server needs: SSH access, systemd, gzip, sha256sum, curl.
set -euo pipefail

usage() {
  cat <<'EOF'
usage: deploy-server.sh <ssh-target> [options]

Options:
  --base-url URL     public base URL to advertise in DSNs and links
  --email EMAIL      bootstrap owner email (default: admin@urgentry.local)
  --password PASS    bootstrap owner password (generated if omitted)
  --pat TOKEN        bootstrap personal access token (generated if omitted)
  --port PORT        listen port (default: 80)
  --rate-limit N     ingest rate limit (default: 10000)
EOF
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return 0
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return 0
  fi
  echo "missing required checksum tool: sha256sum or shasum" >&2
  exit 1
}

generate_secret() {
  local prefix="$1"
  local length="$2"
  python3 - "$prefix" "$length" <<'PY'
import secrets
import string
import sys

prefix, length = sys.argv[1], int(sys.argv[2])
alphabet = string.ascii_letters + string.digits
print(prefix + "".join(secrets.choice(alphabet) for _ in range(length)))
PY
}

ssh_opts=(
  -o ServerAliveInterval=15
  -o ServerAliveCountMax=4
  -o ConnectTimeout=15
)

ssh_cmd() {
  ssh "${ssh_opts[@]}" "$@"
}

scp_cmd() {
  scp "${ssh_opts[@]}" "$@"
}

remote_sha256() {
  local ssh_target="$1"
  local remote_path="$2"
  ssh_cmd "$ssh_target" "sha256sum '$remote_path' | awk '{print \$1}'"
}

upload_archive() {
  local ssh_target="$1"
  local local_archive="$2"
  local remote_archive="$3"
  local expected_archive_sha="$4"
  local attempt remote_archive_sha

  for attempt in 1 2 3; do
    ssh_cmd "$ssh_target" "rm -f '$remote_archive'"
    if scp_cmd "$local_archive" "${ssh_target}:${remote_archive}"; then
      remote_archive_sha="$(remote_sha256 "$ssh_target" "$remote_archive" 2>/dev/null || true)"
      if [[ "$remote_archive_sha" == "$expected_archive_sha" ]]; then
        return 0
      fi
    fi
    sleep "$attempt"
  done

  echo "failed to upload ${local_archive} to ${ssh_target} with a matching checksum" >&2
  return 1
}

SSH_TARGET="${1:-}"
if [[ -z "$SSH_TARGET" ]]; then
  usage >&2
  exit 2
fi
shift

BASE_URL=""
ADMIN_EMAIL="admin@urgentry.local"
ADMIN_PASSWORD=""
ADMIN_PAT=""
LISTEN_PORT="80"
RATE_LIMIT="10000"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base-url) BASE_URL="$2"; shift 2 ;;
    --email) ADMIN_EMAIL="$2"; shift 2 ;;
    --password) ADMIN_PASSWORD="$2"; shift 2 ;;
    --pat) ADMIN_PAT="$2"; shift 2 ;;
    --port) LISTEN_PORT="$2"; shift 2 ;;
    --rate-limit) RATE_LIMIT="$2"; shift 2 ;;
    -h|--help|help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

require_command ssh
require_command scp
require_command gzip
require_command python3

password_generated="false"
pat_generated="false"
if [[ -z "$ADMIN_PASSWORD" ]]; then
  ADMIN_PASSWORD="$(generate_secret "" 20)"
  password_generated="true"
fi
if [[ -z "$ADMIN_PAT" ]]; then
  ADMIN_PAT="$(generate_secret "gpat_" 28)"
  pat_generated="true"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/urgentry-server-deploy.XXXXXX")"
BIN_PATH="$BUILD_DIR/urgentry"
ARCHIVE_PATH="$BUILD_DIR/urgentry.gz"

cleanup() {
  rm -rf "$BUILD_DIR"
}
trap cleanup EXIT

echo "=== Urgentry Server Deploy ==="
echo "Target: $SSH_TARGET"
echo ""

echo "[1/4] Building linux/amd64 binary..."
(
  cd "$ROOT_DIR"
  GOOS=linux GOARCH=amd64 sh scripts/build-urgentry.sh --output "$BIN_PATH"
)
gzip -1 -c "$BIN_PATH" >"$ARCHIVE_PATH"
bin_size="$(ls -lh "$BIN_PATH" | awk '{print $5}')"
archive_size="$(ls -lh "$ARCHIVE_PATH" | awk '{print $5}')"
local_bin_sha="$(sha256_file "$BIN_PATH")"
local_archive_sha="$(sha256_file "$ARCHIVE_PATH")"
echo "      Binary:  $bin_size"
echo "      Archive: $archive_size"

remote_tmp_dir="$(ssh_cmd "$SSH_TARGET" "mktemp -d /tmp/urgentry-deploy.XXXXXX")"
remote_archive="$remote_tmp_dir/urgentry.gz"
remote_binary="$remote_tmp_dir/urgentry"
remote_env_dir="/etc/urgentry"
remote_env_file="$remote_env_dir/urgentry.env"

echo "[2/4] Uploading to server..."
upload_archive "$SSH_TARGET" "$ARCHIVE_PATH" "$remote_archive" "$local_archive_sha"

echo "[3/4] Installing service..."
ssh_cmd "$SSH_TARGET" bash -s \
  "$remote_archive" \
  "$remote_binary" \
  "$remote_env_dir" \
  "$remote_env_file" \
  "$local_bin_sha" \
  "$LISTEN_PORT" \
  "$RATE_LIMIT" \
  "$BASE_URL" \
  "$ADMIN_EMAIL" \
  "$ADMIN_PASSWORD" \
  "$ADMIN_PAT" <<'REMOTE'
set -euo pipefail

REMOTE_ARCHIVE="$1"
REMOTE_BINARY="$2"
REMOTE_ENV_DIR="$3"
REMOTE_ENV_FILE="$4"
EXPECTED_BINARY_SHA="$5"
PORT="$6"
RATE_LIMIT="$7"
BASE_URL="$8"
ADMIN_EMAIL="$9"
ADMIN_PASSWORD="${10}"
ADMIN_PAT="${11}"

id urgentry >/dev/null 2>&1 || useradd -r -s /bin/false urgentry
mkdir -p /var/lib/urgentry "$REMOTE_ENV_DIR"
chown urgentry:urgentry /var/lib/urgentry
chmod 700 /var/lib/urgentry
chmod 700 "$REMOTE_ENV_DIR"

gunzip -c "$REMOTE_ARCHIVE" > "$REMOTE_BINARY"
REMOTE_BINARY_SHA="$(sha256sum "$REMOTE_BINARY" | awk '{print $1}')"
if [[ "$REMOTE_BINARY_SHA" != "$EXPECTED_BINARY_SHA" ]]; then
  echo "uploaded binary checksum mismatch" >&2
  exit 1
fi

cat >"$REMOTE_ENV_FILE" <<EOF
URGENTRY_DATA_DIR=/var/lib/urgentry
URGENTRY_HTTP_ADDR=:${PORT}
URGENTRY_INGEST_RATE_LIMIT=${RATE_LIMIT}
URGENTRY_BOOTSTRAP_EMAIL=${ADMIN_EMAIL}
URGENTRY_BOOTSTRAP_PASSWORD=${ADMIN_PASSWORD}
URGENTRY_BOOTSTRAP_PAT=${ADMIN_PAT}
EOF
if [[ -n "$BASE_URL" ]]; then
  printf 'URGENTRY_BASE_URL=%s\n' "$BASE_URL" >>"$REMOTE_ENV_FILE"
fi
chmod 600 "$REMOTE_ENV_FILE"

capability_lines=""
if [[ "$PORT" -lt 1024 ]]; then
  capability_lines=$'AmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE'
fi

cat >/etc/systemd/system/urgentry.service <<EOF
[Unit]
Description=Urgentry Error Tracking
After=network.target

[Service]
Type=simple
User=urgentry
Group=urgentry
WorkingDirectory=/var/lib/urgentry
EnvironmentFile=${REMOTE_ENV_FILE}
ExecStart=/usr/local/bin/urgentry serve
Restart=always
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=full
ReadWritePaths=/var/lib/urgentry
UMask=0077
${capability_lines}

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl stop urgentry 2>/dev/null || true
install -m 755 "$REMOTE_BINARY" /usr/local/bin/urgentry
rm -rf "$(dirname "$REMOTE_ARCHIVE")"
systemctl enable urgentry >/dev/null
REMOTE

echo "[4/4] Starting Urgentry..."
ssh_cmd "$SSH_TARGET" "systemctl restart urgentry"

ready="false"
for _ in $(seq 1 60); do
  if ssh_cmd "$SSH_TARGET" "curl -fsS http://127.0.0.1:${LISTEN_PORT}/readyz" >/dev/null 2>&1; then
    ready="true"
    break
  fi
  sleep 2
done

if [[ "$ready" != "true" ]]; then
  echo ""
  echo "deployment failed: Urgentry never became ready" >&2
  ssh_cmd "$SSH_TARGET" "systemctl status urgentry --no-pager || true; journalctl -u urgentry -n 60 --no-pager || true" >&2
  exit 1
fi

echo ""
echo "=== Urgentry is running ==="
echo ""
if [[ -n "$BASE_URL" ]]; then
  echo "  URL:       $BASE_URL"
else
  echo "  URL:       http://<server-ip>:${LISTEN_PORT}"
fi
echo "  Email:     $ADMIN_EMAIL"
echo "  Password:  $ADMIN_PASSWORD"
echo "  PAT:       $ADMIN_PAT"
echo ""
echo "  Check service status:"
echo "    ssh $SSH_TARGET systemctl status urgentry"
echo "    ssh $SSH_TARGET journalctl -u urgentry -f"
echo ""
echo "  Config file:"
echo "    ssh $SSH_TARGET sudo cat $remote_env_file"
