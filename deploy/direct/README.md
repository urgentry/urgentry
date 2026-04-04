# Urgentry — Direct Binary Deployment

No Docker, no containers. Just the binary.

## Tiny Mode (single binary + SQLite)

### Install

```bash
# Option 1: install script
curl -fsSL https://urgentry.dev/install.sh | sh

# Option 2: download manually
# macOS Apple Silicon
curl -fsSL -o urgentry https://github.com/ehmo/gentry/releases/latest/download/urgentry-darwin-arm64
chmod +x urgentry

# Linux amd64
curl -fsSL -o urgentry https://github.com/ehmo/gentry/releases/latest/download/urgentry-linux-amd64
chmod +x urgentry
sudo mv urgentry /usr/local/bin/
```

### Run

```bash
urgentry serve
```

That's it. Open http://localhost:8080. Check terminal output for bootstrap credentials.

### Run as a systemd service

```bash
sudo tee /etc/systemd/system/urgentry.service <<EOF
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
Environment=URGENTRY_DATA_DIR=/var/lib/urgentry
Environment=URGENTRY_HTTP_ADDR=:8080
Environment=URGENTRY_BOOTSTRAP_EMAIL=admin@example.com
Environment=URGENTRY_BOOTSTRAP_PASSWORD=changeme

[Install]
WantedBy=multi-user.target
EOF

sudo useradd -r -s /bin/false urgentry
sudo mkdir -p /var/lib/urgentry
sudo chown urgentry:urgentry /var/lib/urgentry
sudo systemctl enable urgentry
sudo systemctl start urgentry
```

### Run behind nginx

```nginx
server {
    listen 443 ssl;
    server_name sentry.example.com;

    ssl_certificate /etc/letsencrypt/live/sentry.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/sentry.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Backup

```bash
# SQLite data is in URGENTRY_DATA_DIR (default: ~/.urgentry/)
cp -r ~/.urgentry/ ~/urgentry-backup-$(date +%Y%m%d)
```

---

## Self-Hosted Mode (multi-role with Postgres)

### Prerequisites

- PostgreSQL 15+
- NATS 2.10+ with JetStream
- S3-compatible storage (MinIO, AWS S3, etc.)
- Valkey/Redis 7+

### Install

Same binary as tiny mode — the `--role` flag switches behavior.

```bash
curl -fsSL https://urgentry.dev/install.sh | sh
```

### Configure

```bash
export URGENTRY_CONTROL_DATABASE_URL="postgres://urgentry:password@localhost:5432/urgentry?sslmode=disable"
export URGENTRY_TELEMETRY_DATABASE_URL="postgres://urgentry:password@localhost:5432/urgentry?sslmode=disable"
export URGENTRY_TELEMETRY_BACKEND=postgres
export URGENTRY_NATS_URL="nats://localhost:4222"
export URGENTRY_VALKEY_URL="redis://localhost:6379/0"
export URGENTRY_S3_ENDPOINT="http://localhost:9000"
export URGENTRY_S3_BUCKET="urgentry-artifacts"
export URGENTRY_S3_ACCESS_KEY="minio"
export URGENTRY_S3_SECRET_KEY="miniosecret"
export URGENTRY_BOOTSTRAP_EMAIL="admin@example.com"
export URGENTRY_BOOTSTRAP_PASSWORD="changeme"
export URGENTRY_BOOTSTRAP_PAT="gpat_your_token"
```

### Run migrations

```bash
urgentry self-hosted migrate-control --dsn "$URGENTRY_CONTROL_DATABASE_URL"
urgentry self-hosted migrate-telemetry --dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend=postgres
```

### Start roles

```bash
# Each role can run on a separate machine or as separate processes.
urgentry serve --role=api --addr=:8080 &
urgentry serve --role=ingest --addr=:8081 &
urgentry serve --role=worker --addr=:8082 &
urgentry serve --role=scheduler --addr=:8083 &
```

### systemd (per role)

Create one service file per role:

```bash
for role in api ingest worker scheduler; do
sudo tee /etc/systemd/system/urgentry-${role}.service <<EOF
[Unit]
Description=Urgentry ${role}
After=network.target postgresql.service

[Service]
Type=simple
User=urgentry
Group=urgentry
ExecStart=/usr/local/bin/urgentry serve --role=${role}
Restart=always
RestartSec=5
EnvironmentFile=/etc/urgentry/env

[Install]
WantedBy=multi-user.target
EOF
done
```

Put all environment variables in `/etc/urgentry/env`.
