# Urgentry Tiny Binary Install Guide

Use this guide when you want a Tiny-mode binary on your machine without running `go run` from the repo every time.

This guide covers two install paths:

1. install from a versioned release archive built with `make release`
2. install from a local source build

## Before you start

Tiny mode needs:

- one binary
- one writable data directory
- one open HTTP port, usually `8080`

If you are evaluating Urgentry for the first time, read [QUICKSTART.md](../../QUICKSTART.md) after install.

## Option 1: install from a release archive

Build or download the archive for your platform. The repo’s release artifact layout is documented in [release-artifact-matrix](release-artifact-matrix.md).

Example file names:

- `urgentry-v0.1.0-linux-amd64.tar.gz`
- `urgentry-v0.1.0-linux-arm64.tar.gz`
- `urgentry-v0.1.0-darwin-amd64.tar.gz`
- `urgentry-v0.1.0-darwin-arm64.tar.gz`

### Verify the checksum

If you also have `SHA256SUMS`, verify it before install.

On macOS:

```bash
cd /path/to/release-files
shasum -a 256 -c SHA256SUMS
```

On Linux:

```bash
cd /path/to/release-files
sha256sum -c SHA256SUMS
```

### Extract and install

User-local install:

```bash
tar -xzf urgentry-v0.1.0-linux-amd64.tar.gz
install -m 0755 urgentry ~/.local/bin/urgentry
```

System-wide install:

```bash
tar -xzf urgentry-v0.1.0-linux-amd64.tar.gz
sudo install -m 0755 urgentry /usr/local/bin/urgentry
```

## Option 2: install from a local source build

Build the binary:

```bash
cd .
make build
```

Install it:

```bash
install -m 0755 ./urgentry ~/.local/bin/urgentry
```

If you want the smallest local binary:

```bash
cd .
make build
install -m 0755 ./urgentry ~/.local/bin/urgentry
```

`make build-tiny` is still available, but it now produces the same optimized binary as `make build`.

## Confirm the install

```bash
urgentry version
```

You should see a version string such as:

```text
urgentry v0.1.0
```

Or a development identifier from `git describe --tags --always --dirty`.

## First boot

Start Tiny mode on a fresh data directory:

```bash
URGENTRY_DATA_DIR="$HOME/.local/share/urgentry" \
URGENTRY_HTTP_ADDR=127.0.0.1:8080 \
urgentry serve --role=all
```

On first boot, Urgentry creates:

- a default organization
- a default project
- a bootstrap owner account
- a bootstrap PAT
- a default public ingest key

Those credentials are printed once in the startup logs.

## Put it behind a reverse proxy later

For a real Tiny deployment, keep the app bound to a local address such as `127.0.0.1:8080` and put HTTPS in front of it with Caddy, Nginx, or another reverse proxy.

Use [local-deploy-guide](local-deploy-guide.md) for the full single-node setup.

## Upgrade a binary install

For Tiny mode:

1. take a backup of the data directory
2. stop the old process
3. install the new binary over the old path
4. start Urgentry again
5. run the checks in [smoke-checklist](smoke-checklist.md)

## Quick validation

After install, run:

```bash
cd .
make tiny-smoke
```

If you installed from a release artifact and no longer have the repo checked out locally, do the manual checks in [smoke-checklist](smoke-checklist.md).

## Related docs

- [release-artifact-matrix](release-artifact-matrix.md)
- [build-guide](build-guide.md)
- [local-deploy-guide](local-deploy-guide.md)
