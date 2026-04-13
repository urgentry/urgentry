# Self-Hosted Deployment Guide

Use self-hosted mode when Tiny mode is no longer enough and you need shared infrastructure, split roles, and operator workflows.

## Prerequisites

- Docker and Docker Compose for the packaged bundle, or Kubernetes plus Helm for cluster installs
- PostgreSQL
- MinIO or another S3-compatible object store
- Valkey
- NATS with JetStream

## Docker Compose path

```bash
cd deploy/compose
cp .env.example .env
```

Set real secrets in `.env`, then boot the stack:

```bash
docker compose up -d
docker compose ps
docker compose logs -f urgentry-api
```

Validate the stack:

```bash
bash deploy/compose/smoke.sh up
```

Optional logs-only ClickHouse pilot:

```bash
printf '%s\n' \
  'COMPOSE_PROFILES=columnar' \
  'URGENTRY_BUILD_TAGS=netgo,osusergo,clickhouse' \
  'CLICKHOUSE_PASSWORD=change-me-columnar' \
  'URGENTRY_COLUMNAR_DATABASE_URL=clickhouse://urgentry:change-me-columnar@clickhouse:9000/urgentry' \
  'URGENTRY_COLUMNAR_BACKEND=clickhouse' >> .env
docker compose up -d --build
```

When the pilot is enabled, `bash deploy/compose/smoke.sh up` also verifies that a fresh log lands in ClickHouse and still reads back through the org logs API after the bridge copy is removed.

For the current benchmark tradeoff summary across the lean default, controller-enabled, ClickHouse-enabled, and combined variants, see [../benchmarks.md](../benchmarks.md).

## Operator commands

These commands run against the same self-hosted control plane:

```bash
./urgentry self-hosted preflight --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL"
./urgentry self-hosted status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL"
./urgentry self-hosted maintenance-status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
./urgentry self-hosted enter-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --reason "upgrade window"
./urgentry self-hosted leave-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
```

The Compose wrapper exposes the same operations:

```bash
bash deploy/compose/ops.sh preflight
bash deploy/compose/ops.sh status
bash deploy/compose/ops.sh maintenance-status
```

## Backup, restore, and upgrade

```bash
bash deploy/compose/backup.sh /tmp/urgentry-backup
bash deploy/compose/restore.sh /tmp/urgentry-backup
bash deploy/compose/upgrade.sh
```

## Next docs

- [kubernetes-and-helm.md](kubernetes-and-helm.md)
- [ha-baseline.md](ha-baseline.md)
- [maintenance-mode.md](maintenance-mode.md)
