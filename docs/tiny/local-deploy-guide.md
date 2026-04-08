# Urgentry Tiny Local Deployment Guide

Use this guide when you want to run Tiny mode on one machine with a persistent data directory and a normal HTTPS front end.

This is the most important production-shaped Tiny deployment:

- one binary
- one SQLite data directory
- one local blob directory by default
- one reverse proxy in front

## Deployment shape

Tiny mode stays simple on purpose:

- run `urgentry serve --role=all`
- keep Urgentry bound to a local address such as `127.0.0.1:8080`
- terminate TLS at a reverse proxy
- back up the full data directory before upgrades

## 1. Create the runtime user and data directory

Example layout:

```bash
sudo mkdir -p /var/lib/urgentry
sudo chown "$USER" /var/lib/urgentry
```

Tiny mode stores SQLite data and default file-backed blobs under `URGENTRY_DATA_DIR`.

## 2. Install the binary

Install Urgentry somewhere predictable:

```bash
sudo install -m 0755 urgentry /usr/local/bin/urgentry
```

If you still need the binary install steps, use [binary-install-guide](binary-install-guide.md).

## 3. Create an env file

Start from the example:

```bash
cp configs/urgentry.example.env /etc/urgentry.env
```

Recommended Tiny-mode settings:

```bash
URGENTRY_ENV=production
URGENTRY_HTTP_ADDR=127.0.0.1:8080
URGENTRY_BASE_URL=https://urgentry.example.com
URGENTRY_DATA_DIR=/var/lib/urgentry
URGENTRY_BLOB_BACKEND=file
URGENTRY_INGEST_RATE_LIMIT=60
```

## 4. First boot

Run Urgentry:

```bash
set -a
. /etc/urgentry.env
set +a
urgentry serve --role=all
```

On first boot, save the bootstrap owner email, password, PAT, and public ingest key from the logs.

## 5. Put a reverse proxy in front

Keep Urgentry off the public internet directly. Bind it to `127.0.0.1:8080` and proxy HTTPS traffic to it.

Minimal Caddy example:

```caddyfile
urgentry.example.com {
  reverse_proxy 127.0.0.1:8080
}
```

That is enough for a basic Tiny deployment.

## 6. Validate the node

Run the shipped smoke first if you still have the repo:

```bash
cd .
make tiny-smoke
```

Then run the public manual checks in [smoke-checklist](smoke-checklist.md).

## 7. Backups

The default Tiny backup rule is simple:

1. stop the process
2. copy the full `URGENTRY_DATA_DIR`
3. start the process again

If you move Tiny mode to external object storage later, back up the SQLite data and blob storage together.

## 8. Upgrades

For a safe Tiny upgrade:

1. back up the data directory
2. stop Urgentry
3. install the new binary
4. start Urgentry
5. run the smoke checklist again

## 9. Basic service wrapper

If you want a small systemd unit:

```ini
[Unit]
Description=Urgentry Tiny
After=network.target

[Service]
EnvironmentFile=/etc/urgentry.env
ExecStart=/usr/local/bin/urgentry serve --role=all
Restart=on-failure
WorkingDirectory=/var/lib/urgentry

[Install]
WantedBy=multi-user.target
```

Tune the service user and paths for your machine.

## Common mistakes

### Urgentry can start, but cannot write data

The process user probably does not own `URGENTRY_DATA_DIR`.

### Login works, but URLs or cookies break behind the proxy

Keep one public origin in front of Urgentry and proxy all requests to the same backend.

### Data disappears after restart

You likely tested on a temporary or throwaway data path.

## Related docs

- [binary-install-guide](binary-install-guide.md)
- [deploy-guide](deploy-guide.md)
- [smoke-checklist](smoke-checklist.md)
- [Operations guide](../architecture/operations-guide.md)
