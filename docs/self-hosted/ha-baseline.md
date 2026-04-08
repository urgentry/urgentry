# Self-Hosted HA Baseline

This is the minimum public high-availability posture for self-hosted Urgentry.

## Required shared services

- PostgreSQL for the control and telemetry stores
- MinIO or another S3-compatible blob store
- Valkey for shared cache and guard state
- NATS with JetStream for async work

## Application topology

- at least one `api` process
- at least one `ingest` process
- at least one `worker` process
- at least one `scheduler` process

Run those roles behind durable shared services instead of one local Tiny-mode data directory.

## Before calling an install HA-ready

- the stack boots cleanly through `deploy/compose` or the Kubernetes path
- backup and restore are documented and exercised for your environment
- maintenance mode is wired into the operator runbook
- `make selfhosted-sentry-baseline` passes against the deployed topology
