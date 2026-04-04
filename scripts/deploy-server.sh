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
#   1. Cross-compiles the binary for linux/amd64
#   2. Uploads it to the server
#   3. Creates a systemd service
#   4. Starts Urgentry
#
# The server needs: SSH access, systemd. That's it.
set -euo pipefail

# --- Parse arguments ---
SSH_TARGET="${1:?Usage: deploy-server.sh <ssh-target> [--base-url URL] [--email EMAIL] [--password PASS] [--port PORT]}"
shift

BASE_URL=""
ADMIN_EMAIL=""
ADMIN_PASSWORD=""
LISTEN_PORT="80"
RATE_LIMIT="10000"

while [ $# -gt 0 ]; do
  case "$1" in
    --base-url)    BASE_URL="$2"; shift 2 ;;
    --email)       ADMIN_EMAIL="$2"; shift 2 ;;
    --password)    ADMIN_PASSWORD="$2"; shift 2 ;;
    --port)        LISTEN_PORT="$2"; shift 2 ;;
    --rate-limit)  RATE_LIMIT="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Urgentry Server Deploy ==="
echo "Target: $SSH_TARGET"
echo ""

# --- 1. Build ---
echo "[1/4] Building linux/amd64 binary..."
cd "$ROOT_DIR"
GOOS=linux GOARCH=amd64 bash scripts/build-urgentry.sh --output /tmp/urgentry-deploy
echo "      Binary: $(ls -lh /tmp/urgentry-deploy | awk '{print $5}')"

# --- 2. Upload ---
echo "[2/4] Uploading to server..."
ssh "$SSH_TARGET" 'systemctl stop urgentry 2>/dev/null || true'
scp /tmp/urgentry-deploy "$SSH_TARGET":/usr/local/bin/urgentry
ssh "$SSH_TARGET" 'chmod +x /usr/local/bin/urgentry'
rm -f /tmp/urgentry-deploy

# --- 3. Configure ---
echo "[3/4] Configuring systemd service..."

# Build environment lines
ENV_LINES="Environment=URGENTRY_DATA_DIR=/var/lib/urgentry"
ENV_LINES="${ENV_LINES}\nEnvironment=URGENTRY_HTTP_ADDR=:${LISTEN_PORT}"
ENV_LINES="${ENV_LINES}\nEnvironment=URGENTRY_INGEST_RATE_LIMIT=${RATE_LIMIT}"

if [ -n "$BASE_URL" ]; then
  ENV_LINES="${ENV_LINES}\nEnvironment=URGENTRY_BASE_URL=${BASE_URL}"
fi
if [ -n "$ADMIN_EMAIL" ]; then
  ENV_LINES="${ENV_LINES}\nEnvironment=URGENTRY_BOOTSTRAP_EMAIL=${ADMIN_EMAIL}"
fi
if [ -n "$ADMIN_PASSWORD" ]; then
  ENV_LINES="${ENV_LINES}\nEnvironment=URGENTRY_BOOTSTRAP_PASSWORD=${ADMIN_PASSWORD}"
fi

ssh "$SSH_TARGET" bash -s "$ENV_LINES" "$LISTEN_PORT" <<'REMOTE'
set -eu
ENV_LINES="$1"
PORT="$2"

# Create user and data dir
id urgentry 2>/dev/null || useradd -r -s /bin/false urgentry
mkdir -p /var/lib/urgentry
chown urgentry:urgentry /var/lib/urgentry

# Allow binding to privileged ports
if [ "$PORT" -lt 1024 ]; then
  setcap cap_net_bind_service=+ep /usr/local/bin/urgentry
fi

# Write systemd service
cat > /etc/systemd/system/urgentry.service <<EOF
[Unit]
Description=Urgentry Error Tracking
After=network.target

[Service]
Type=simple
User=urgentry
Group=urgentry
ExecStart=/usr/local/bin/urgentry serve
Restart=always
RestartSec=5
$(echo -e "$ENV_LINES")

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable urgentry
REMOTE

# --- 4. Start ---
echo "[4/4] Starting Urgentry..."
ssh "$SSH_TARGET" 'systemctl start urgentry && sleep 2'

# Verify
READY=$(ssh "$SSH_TARGET" "curl -sS http://127.0.0.1:${LISTEN_PORT}/readyz 2>/dev/null" || echo "")
if echo "$READY" | grep -q '"ready"'; then
  echo ""
  echo "=== Urgentry is running ==="
  echo ""
  if [ -n "$BASE_URL" ]; then
    echo "  URL:  $BASE_URL"
  else
    echo "  URL:  http://<server-ip>:${LISTEN_PORT}"
  fi
  echo ""
  echo "  Check startup logs for bootstrap credentials:"
  echo "    ssh $SSH_TARGET journalctl -u urgentry -n 15"
  echo ""
  echo "  DSN for SDKs (after login, see Settings → Keys & DSN)"
  echo ""
  echo "  Manage service:"
  echo "    ssh $SSH_TARGET systemctl status urgentry"
  echo "    ssh $SSH_TARGET systemctl restart urgentry"
  echo "    ssh $SSH_TARGET journalctl -u urgentry -f"
else
  echo ""
  echo "WARNING: Urgentry may not have started correctly."
  echo "Check logs: ssh $SSH_TARGET journalctl -u urgentry -n 30"
  exit 1
fi
