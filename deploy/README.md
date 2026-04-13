# Deployment Guide

Urgentry exposes two public deployment paths:

- **Tiny mode** for one binary plus SQLite
- **Self-hosted mode** for split `api`, `ingest`, `worker`, and `scheduler` roles

## Tiny mode

Choose one of these:

| Path | When to use it |
|---|---|
| [direct/README.md](direct/README.md) | You want a binary on a machine you control |
| [docker-tiny/README.md](docker-tiny/README.md) | You want a single container with a mounted data volume |

For packaged containers, use:

```bash
docker pull ghcr.io/urgentry/urgentry:latest
docker run -p 8080:8080 -v urgentry-data:/data ghcr.io/urgentry/urgentry:latest serve
```

## Self-hosted mode

Choose one of these:

| Path | When to use it |
|---|---|
| [compose/README.md](compose/README.md) | Small production install or operator evaluation |
| [k8s/README.md](k8s/README.md) | Kubernetes deployment with raw manifests |
| [helm/urgentry/](helm/urgentry/) | Helm-managed Kubernetes deployment |

## Validate a deployment

Use these from the repository root:

```bash
make tiny-smoke
bash deploy/compose/smoke.sh up
```
