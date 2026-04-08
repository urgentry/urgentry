# Tiny Mode

Tiny mode runs the full product in one process with one SQLite data directory.

## Start here

1. Read [../../QUICKSTART.md](../../QUICKSTART.md)
2. Build and boot the binary:

```bash
make build
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

3. Verify the install:

```bash
make tiny-smoke
make tiny-sentry-baseline
```

## Deployment options

- [../../deploy/direct/README.md](../../deploy/direct/README.md)
- [../../deploy/docker-tiny/README.md](../../deploy/docker-tiny/README.md)

## Operating notes

- Tiny mode stores data under `URGENTRY_DATA_DIR`
- Backup is a copy of that data directory or mounted volume
- `URGENTRY_BASE_URL` should match the external URL when you want stable links in the UI and generated outputs

## When to switch to self-hosted mode

Move to [../self-hosted/README.md](../self-hosted/README.md) when you need shared PostgreSQL-backed infrastructure, split roles, or operator workflows around backup, restore, and maintenance.
