# Urgentry Tiny Container Deployment Guide

Use this guide when you want to run Tiny mode as one container with one persistent volume.

This is still Tiny mode:

- one `urgentry serve --role=all` process
- one `/data` volume
- one SQLite-backed node

## What this guide covers

This guide uses the Tiny-mode Dockerfile at `Dockerfile`.

It does not use the serious self-hosted Compose bundle under `deploy/compose/`.

## 1. Build the image

```bash
cd .
docker build --build-arg VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -t urgentry:local .
```

The image:

- starts `urgentry serve --role=all`
- stores state under `/data`
- listens on port `8080`

## 2. Create persistent storage

Named Docker volume:

```bash
docker volume create urgentry-data
```

Or use a host path if you want direct filesystem access for backups.

## 3. Run the container

Minimal Tiny run:

```bash
docker run \
  --name urgentry \
  --restart unless-stopped \
  -p 8080:8080 \
  -e URGENTRY_ENV=production \
  -v urgentry-data:/data \
  urgentry:local
```

If you want the app bound only on localhost:

```bash
docker run \
  --name urgentry \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e URGENTRY_ENV=production \
  -v urgentry-data:/data \
  urgentry:local
```

## 4. Optional env file

You can keep runtime settings in an env file:

```bash
cat > urgentry-tiny.env <<'EOF'
URGENTRY_ENV=production
URGENTRY_HTTP_ADDR=:8080
URGENTRY_BASE_URL=https://urgentry.example.com
URGENTRY_BLOB_BACKEND=file
URGENTRY_INGEST_RATE_LIMIT=60
EOF
```

Then run:

```bash
docker run \
  --name urgentry \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  --env-file ./urgentry-tiny.env \
  -v urgentry-data:/data \
  urgentry:local
```

## 5. First boot and bootstrap auth

On first boot, the container logs print:

- bootstrap owner email
- bootstrap owner password
- bootstrap PAT
- public ingest key

Save those values before you rotate the container.

View them with:

```bash
docker logs urgentry
```

## 6. Reverse proxy

For a real deployment, prefer:

- `-p 127.0.0.1:8080:8080`
- TLS at Caddy, Nginx, or another reverse proxy

Do not put raw HTTP Tiny mode directly on the public internet if you can avoid it.

## 7. Smoke check the container

After startup:

1. open `/login/`
2. sign in with the bootstrap owner account
3. send one test event
4. confirm `/issues/` and `/discover/` load
5. run the checks in [smoke-checklist](smoke-checklist.md)

## 8. Back up the container deployment

For the default file-backed Tiny container:

1. stop the container
2. copy the data volume or host path
3. start the container again

If you use a host path instead of a named volume, your backup flow is just a filesystem copy of that directory while the container is stopped.

## 9. Upgrade the container

Safe Tiny container upgrade:

1. back up the data volume
2. build or pull the new image
3. stop and remove the old container
4. start a new container against the same volume
5. run the smoke checklist again

Example:

```bash
docker stop urgentry
docker rm urgentry
docker run \
  --name urgentry \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e URGENTRY_ENV=production \
  -v urgentry-data:/data \
  urgentry:local
```

## Common mistakes

### Data disappears after restart

You started the container without a persistent volume.

### The container works locally, but login breaks behind a proxy

Keep one public origin in front of Urgentry and proxy all traffic to the same backend container.

### The serious self-hosted Compose bundle looks similar

It is not the same product path. Tiny mode is one container. The serious self-hosted bundle is split-role and uses Postgres, MinIO, Valkey, and NATS.

## Related docs

- [deploy-guide](deploy-guide.md)
- [local-deploy-guide](local-deploy-guide.md)
- [release-artifact-matrix](release-artifact-matrix.md)
- [smoke-checklist](smoke-checklist.md)
