# Direct Binary Deployment

Direct deployment is the smallest non-container path for Urgentry.

## Tiny mode

Build from source:

```bash
make build
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

Or download a release archive from GitHub Releases:

```bash
curl -L https://github.com/urgentry/urgentry/releases/download/vX.Y.Z/urgentry-vX.Y.Z-linux-amd64.tar.gz | tar xz
./urgentry serve --role=all
```

Other packaged downloads:

- `urgentry-vX.Y.Z-linux-amd64.tar.gz`
- `urgentry-vX.Y.Z-linux-arm64.tar.gz`
- `urgentry-vX.Y.Z-darwin-amd64.tar.gz`
- `urgentry-vX.Y.Z-darwin-arm64.tar.gz`
- `urgentry-vX.Y.Z-windows-amd64.zip`

For a long-running Linux service, use `scripts/deploy-server.sh` or a systemd unit that sets:

- `URGENTRY_BASE_URL`
- `URGENTRY_DATA_DIR`
- `URGENTRY_BOOTSTRAP_EMAIL`
- `URGENTRY_BOOTSTRAP_PASSWORD`
- `URGENTRY_BOOTSTRAP_PAT`

`scripts/deploy-server.sh` now builds a compressed Linux binary locally, uploads it atomically with checksum verification, writes `/etc/urgentry/urgentry.env`, installs a hardened systemd unit, and prints the effective bootstrap email/password/PAT at the end. If you omit `--password` or `--pat`, the script generates secure values for you.

## Self-hosted mode

The same binary can run the split-role self-hosted deployment:

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
export URGENTRY_BOOTSTRAP_PAT="gpat_example_token"
```

Then run the operator commands and split roles:

```bash
./urgentry self-hosted migrate-control --dsn "$URGENTRY_CONTROL_DATABASE_URL"
./urgentry self-hosted migrate-telemetry --dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend=postgres

./urgentry serve --role=api
./urgentry serve --role=ingest
./urgentry serve --role=worker
./urgentry serve --role=scheduler
```

For the packaged operator paths, prefer:

- [../compose/README.md](../compose/README.md)
- [../../docs/self-hosted/README.md](../../docs/self-hosted/README.md)
