# Urgentry Tiny Deploy Guide

Use this guide when you want the Tiny-mode deployment overview first, then the exact local or container runbook that matches your install.

Tiny mode is the single-node path:

- one process
- one data directory
- one SQLite database
- one local blob store by default

## Choose your path

- [local-deploy-guide](local-deploy-guide.md) for one machine, one binary, one reverse proxy
- [container-deploy-guide](container-deploy-guide.md) for one container, one volume, one port mapping

## Pick a data directory

Choose a persistent path. Examples:

- `/var/lib/urgentry`
- `/srv/urgentry`
- a named Docker volume

Tiny mode keeps its SQLite database and default file-backed blobs under that path.

## Minimal VM or bare-metal deploy

Build or install the binary first, then run it with an explicit data directory and bind address:

```bash
URGENTRY_DATA_DIR=/var/lib/urgentry \
URGENTRY_HTTP_ADDR=127.0.0.1:8080 \
urgentry serve --role=all
```

That is the simplest production-shaped setup behind a reverse proxy.

## Environment file

The repo ships a starter env file at `configs/urgentry.example.env`.

The settings Tiny mode usually needs are:

```bash
URGENTRY_ENV=production
URGENTRY_HTTP_ADDR=127.0.0.1:8080
URGENTRY_BASE_URL=https://urgentry.example.com
URGENTRY_DATA_DIR=/var/lib/urgentry
URGENTRY_BLOB_BACKEND=file
URGENTRY_INGEST_RATE_LIMIT=60
```

## Reverse proxy and HTTPS

Put Urgentry behind a proxy that terminates TLS and forwards traffic to `127.0.0.1:8080`.

A small Caddy example:

```caddyfile
urgentry.example.com {
  reverse_proxy 127.0.0.1:8080
}
```

## Docker deploy

Build the image:

```bash
cd .
docker build -t urgentry:local .
```

Run it with persistent storage:

```bash
docker run \
  --name urgentry \
  -p 8080:8080 \
  -e URGENTRY_ENV=production \
  -v urgentry-data:/data \
  urgentry:local
```

## Post-deploy validation

Run this after the service starts:

1. Open `/login/`.
2. Sign in with the bootstrap owner account.
3. Open `/issues/`, `/discover/`, and `/ops/`.
4. Send one test event.
5. Confirm the event appears in `/issues/`.

## Backups

For the default Tiny-mode setup, back up the whole data directory.

The safest simple workflow:

1. Stop the process.
2. Copy the full data directory.
3. Start the process again.

If you move Tiny mode onto external blob storage later, back up the SQLite data and the blob bucket together.

## Upgrades

For a small single-node Tiny install:

1. Take a backup.
2. Stop Urgentry.
3. Replace the binary or container image.
4. Start Urgentry.
5. Log in and check `/ops/`, `/issues/`, and `/discover/`.

## Smoke check after deploy

Confirm:

- `/login/` loads
- you can sign in
- `/issues/` loads
- `/discover/` runs a query
- a test event lands

## Common deploy problems

### Urgentry cannot write the data directory

Make sure the process user can read and write the configured `URGENTRY_DATA_DIR`.

Set `URGENTRY_BASE_URL` to the public URL users will open if you want frozen analytics links and scheduled report emails to carry absolute URLs.

### The reverse proxy works, but login or API requests fail

Make sure the proxy forwards requests to the same Urgentry origin and does not strip standard headers or cookies.

### Docker restarts lose data

You probably started the container without a persistent volume. Mount `/data` to a named volume or a host path.

## Next docs

- [QUICKSTART.md](../../QUICKSTART.md)
- [build-guide](build-guide.md)
- [local-deploy-guide](local-deploy-guide.md)
- [container-deploy-guide](container-deploy-guide.md)
- [Operations guide](../architecture/operations-guide.md)
