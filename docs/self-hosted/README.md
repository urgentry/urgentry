# Self-Hosted Mode

Self-hosted mode runs split `api`, `ingest`, `worker`, and `scheduler` roles on PostgreSQL, MinIO, Valkey, and NATS.

## Start here

- [deployment-guide.md](deployment-guide.md)
- [kubernetes-and-helm.md](kubernetes-and-helm.md)
- [ha-baseline.md](ha-baseline.md)
- [maintenance-mode.md](maintenance-mode.md)

## Quick start

### Docker Compose

```bash
cd deploy/compose
cp .env.example .env
docker compose up -d
docker compose logs -f urgentry-api
```

### Kubernetes

```bash
kubectl apply -k deploy/k8s
kubectl -n urgentry-system get pods
```

## Roles

| Role | Purpose |
|---|---|
| `api` | Web UI, management API, query endpoints |
| `ingest` | SDK envelope, store, and OTLP ingestion |
| `worker` | Async processing, projection, and backfill work |
| `scheduler` | Maintenance and scheduled jobs |

## Validation

From the repository root:

```bash
bash deploy/compose/smoke.sh up
```
