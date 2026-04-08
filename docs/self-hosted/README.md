# Urgentry Serious Self-Hosted

Production deployment with PostgreSQL, MinIO, Valkey, NATS, and split application roles.

## Quick Start

### Docker Compose (fastest)

```bash
cd deploy/compose

# 1. Create your .env from the example
cp .env.example .env
# Edit .env — set real passwords for POSTGRES_PASSWORD, MINIO_ROOT_PASSWORD,
# URGENTRY_BOOTSTRAP_PASSWORD, URGENTRY_BOOTSTRAP_PAT

# 2. Boot the stack
docker compose up -d

# 3. Wait for bootstrap to complete, then open the API port
docker compose logs -f urgentry-api
```

### Kubernetes

See [kubernetes.md](kubernetes-and-helm.md) for the `kustomize` bundle.

## Architecture

```
                    ┌─────────────┐
          ┌────────►│  urgentry-api │◄──── users / browsers
          │         └──────┬──────┘
          │                │
   ┌──────┴──────┐  ┌──────┴──────┐
   │urgentry-ingest│  │urgentry-worker│◄──── NATS JetStream
   └──────┬──────┘  └──────┬──────┘
          │                │
          ▼                ▼
   ┌────────────┐   ┌────────────┐   ┌───────┐   ┌───────┐
   │ PostgreSQL │   │   MinIO    │   │Valkey │   │ NATS  │
   └────────────┘   └────────────┘   └───────┘   └───────┘
```

| Role | Purpose |
|------|---------|
| `api` | Web UI, management API, analytics queries |
| `ingest` | SDK envelope/event/OTLP ingestion |
| `worker` | Async event processing, backfill, projection |
| `scheduler` | Scheduled reports, retention, maintenance |

## Guides

### Setup & Deploy

| Topic | Guide |
|-------|-------|
| Full deployment walkthrough | [deployment-guide.md](deployment-guide.md) |
| Kubernetes / kustomize | [kubernetes-and-helm.md](kubernetes-and-helm.md) |
| HA baseline | [ha-baseline.md](ha-baseline.md) |

### Day-2 Operations

| Topic | Guide |
|-------|-------|
| Upgrade process | [upgrade-contract.md](upgrade-contract.md) |
| Backup & PITR | [pitr-workflow.md](pitr-workflow.md) |
| Maintenance mode | [maintenance-mode.md](maintenance-mode.md) |
| Repair tools | [repair-tools.md](repair-tools.md) |
| Support bundle | [support-bundle.md](support-bundle.md) |
| Operator audit log | [operator-audit-ledger.md](operator-audit-ledger.md) |

### Monitoring & SLOs

| Topic | Guide |
|-------|-------|
| SLO & alert pack | [slo-and-alert-pack.md](slo-and-alert-pack.md) |
| Telemetry observability | [telemetry-observability.md](telemetry-observability.md) |
| Query execution contract | [query-execution-contract.md](query-execution-contract.md) |
| Scale gate | [scale-gate.md](scale-gate.md) |

### Architecture Decisions

| Topic | Guide |
|-------|-------|
| Control plane ADR | [control-plane-adr.md](control-plane-adr.md) |
| Runtime ADR | [runtime-adr.md](runtime-adr.md) |
| Telemetry bridge ADR | [telemetry-bridge-adr.md](telemetry-bridge-adr.md) |
| Telemetry engine ADR | [telemetry-engine-adr.md](telemetry-engine-adr.md) |
| Fanout contract | [fanout-contract.md](fanout-contract.md) |
| Export contract | [telemetry-export-contract.md](telemetry-export-contract.md) |

## Coming from Tiny mode?

Urgentry self-hosted uses the same binary. The difference is configuration:

| | Tiny | Self-Hosted |
|---|---|---|
| Database | SQLite (embedded) | PostgreSQL |
| Blob storage | Local filesystem | MinIO / S3 |
| Async queue | SQLite jobs | NATS JetStream |
| Cache | None | Valkey |
| Roles | `--role=all` | Split `api`, `ingest`, `worker`, `scheduler` |
| Config | `URGENTRY_DATA_DIR` | `URGENTRY_CONTROL_DATABASE_URL` + friends |
