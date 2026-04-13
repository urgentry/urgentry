# Urgentry Docker Compose Deployment

This directory contains the Docker Compose bundle for serious self-hosted Urgentry.

## Quick Start

```bash
# 1. Create environment file with real secrets
cp .env.example .env
# Edit .env — POSTGRES_PASSWORD, MINIO_ROOT_PASSWORD,
# URGENTRY_BOOTSTRAP_PASSWORD, URGENTRY_BOOTSTRAP_PAT must be set

# 2. Boot the stack
docker compose up -d

# 3. Check status
docker compose ps
docker compose logs -f urgentry-api
```

## Services

| Service | Purpose | Port |
|---------|---------|------|
| `postgres` | Control plane + telemetry | 5432 |
| `minio` | Blob/artifact storage | 9000, 9001 |
| `valkey` | Cache + query guard | 6379 |
| `nats` | Async job queue | 4222 |
| `clickhouse` | Optional logs-only columnar pilot | internal only |
| `urgentry-api` | Web UI + API | 8080 |
| `urgentry-ingest` | SDK ingestion | 8081 |
| `urgentry-worker` | Async processing | 8082 |
| `urgentry-scheduler` | Scheduled tasks | 8083 |

## Operator Scripts

| Script | Purpose |
|--------|---------|
| `ops.sh preflight` | Check infrastructure readiness |
| `ops.sh status` | Runtime status |
| `ops.sh security-report` | Security audit |
| `ops.sh rotate-bootstrap` | Rotate bootstrap credentials |
| `backup.sh <dir>` | Full backup |
| `restore.sh <dir>` | Restore from backup |
| `upgrade.sh` | Rolling upgrade |
| `smoke.sh up` | Boot + smoke test |
| `drills.sh <drill>` | Run operational drills |

## Full Guide

See [docs/self-hosted/](../../docs/self-hosted/) for the complete deployment guide.

Set `COMPOSE_PROFILES=columnar`, `CLICKHOUSE_PASSWORD`, `URGENTRY_COLUMNAR_DATABASE_URL`, and `URGENTRY_COLUMNAR_BACKEND=clickhouse` in the Compose env file to enable the optional logs-only pilot. When those vars are present, `smoke.sh` also verifies that an ingested log lands in ClickHouse and still reads back through the org logs API after the bridge copy is removed, and `columnar-logs-proof.sh` runs the stricter rebuild-backed proof flow.
