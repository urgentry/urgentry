# Urgentry Kubernetes Deployment

This directory contains kustomize manifests for deploying Urgentry on Kubernetes.

## Quick Start

```bash
# 1. Edit secret.yaml — replace all REPLACE_ME_* values with real secrets
# 2. Apply
kubectl apply -k .
# 3. Verify
kubectl -n urgentry-system get pods
```

## Manifests

| File | Purpose |
|------|---------|
| `kustomization.yaml` | Kustomize entry point |
| `namespace.yaml` | `urgentry-system` namespace |
| `secret.yaml` | Secrets (edit before applying!) |
| `configmap.yaml` | Shared configuration |
| `postgres.yaml` | PostgreSQL StatefulSet |
| `minio.yaml` | MinIO deployment |
| `valkey.yaml` | Valkey deployment |
| `nats.yaml` | NATS deployment |
| `urgentry-*-deployment.yaml` | App role deployments |
| `urgentry-services.yaml` | ClusterIP services |
| `urgentry-data-pvc.yaml` | Shared data volume |

## Smoke Test

```bash
bash smoke.sh up
```

## Full Guide

See [docs/self-hosted/kubernetes-and-helm.md](../../docs/self-hosted/kubernetes-and-helm.md).
