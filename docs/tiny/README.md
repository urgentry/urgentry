# Tiny Mode

Tiny mode runs the full product in one process with one SQLite data directory.

## Start here

1. Read [Getting started](../../QUICKSTART.md)
2. Build and boot the binary:

```bash
make build
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

3. Verify the install:

```bash
make tiny-smoke
```

## Pick a deployment shape

- [Direct binary install](../../deploy/direct/README.md)
- [Docker install](../../deploy/docker-tiny/README.md)

## Keep these details in mind

- Tiny mode stores data under `URGENTRY_DATA_DIR`
- Backup is a copy of that data directory or mounted volume
- `URGENTRY_BASE_URL` should match the external URL when you want stable links in the UI and generated outputs

## Move to self-hosted mode when

Move to [self-hosted mode](../self-hosted/README.md) when you need shared PostgreSQL-backed infrastructure, split roles, or operator workflows around backup, restore, and maintenance.
