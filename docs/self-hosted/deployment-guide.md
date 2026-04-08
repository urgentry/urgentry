# Urgentry Serious Self-Hosted Deployment Guide

Status: current bundle guide
Last updated: 2026-03-30

## Scope

This guide documents the shipped deployment bundles for bead `urgentry-5ew.5.1`.

What exists now:
- a serious self-hosted Docker Compose bundle under [deploy/compose/](../../deploy/compose)
- a cluster-oriented Kubernetes bundle under [deploy/k8s/](../../deploy/k8s)
- a shipped Compose smoke flow plus a Kubernetes smoke script that now runs both manually and from the live eval lane
- Compose backup, restore, and disaster-recovery drill scripts

What does not exist yet:
- a fully Postgres-backed runtime data plane
- separate multi-node app writers over a relational control plane

Current runtime truth:
- the serious self-hosted bundles run split Urgentry roles
- those roles use PostgreSQL for control-plane auth, keys, catalog/admin state, canonical issue workflow writes plus issue row hydration, alerts, monitors, and ownership rules when `URGENTRY_CONTROL_DATABASE_URL` is configured
- those roles use JetStream for durable async execution, Valkey for shared quotas plus scheduler leases, and the serious self-hosted telemetry bridge for Discover/logs/traces/replay/profile reads when `URGENTRY_TELEMETRY_DATABASE_URL` is configured; profile detail and query views reconstruct canonical graphs from bridge manifests with raw-blob fallback instead of delegating back to SQLite profile tables
- those roles still share the SQLite-backed event, replay, profile, outcome, and artifact-adjacent data plane through one mounted data volume
- Postgres, MinIO, Valkey, and NATS are booted and smoked as the serious self-hosted substrate and operator path
- MinIO is already live for blob custody through the shipped S3 backend seam

That means these bundles are honest deployment scaffolding around the current shipped binary: the control plane is now cut over to Postgres, while the broader SQLite-backed data plane and query-surface migration remains in progress.

## Compose bundle

Location:
- [deploy/compose/docker-compose.yml](../../deploy/compose/docker-compose.yml)

Services:
- `postgres`
- `minio`
- `valkey`
- `nats`
- `minio-bootstrap`
- `urgentry-bootstrap`
- `urgentry-api`
- `urgentry-ingest`
- `urgentry-worker`
- `urgentry-scheduler`

Operator flow:
1. `minio-bootstrap` creates the artifact bucket.
2. `urgentry-bootstrap` runs `self-hosted preflight`, `migrate-control`, `migrate-telemetry`, and `status` against Postgres.
3. The four app roles boot only after those one-shot bootstrap services succeed.

Bootstrap secret gate:
- `urgentry self-hosted preflight` now validates bootstrap password, bootstrap PAT, metrics token, Postgres password, and MinIO root password in addition to DSN reachability.
- placeholder values such as `change-me-in-production`, `gpat_self_hosted_bootstrap`, and the shipped MinIO/Postgres defaults are rejected instead of only being documented as unsafe.
- the bundled smoke, drill, and eval env files now override those placeholders automatically so the serious self-hosted operator path stays bootable while production installs fail closed until real secrets are provided.
- the committed Kubernetes secret manifest now also ships fail-closed placeholders instead of insecure literals; operators must replace or overlay those values before `kubectl apply -k` for a real install, while the live eval lane rewrites a disposable bundle copy with generated secrets before it boots the cluster smoke.

App runtime flow:
- all Urgentry roles share the same mounted `/data` volume for the remaining SQLite data plane
- SQLite still lives there for event, replay, profile, outcome, and blob-adjacent metadata
- Postgres now owns control-plane auth, keys, catalog/admin state, issue workflow writes plus canonical issue rows, alerts, monitors, and ownership rules
- serious self-hosted issue list, issue detail, org issue search, and the default Discover issue table hydrate canonical issue rows from Postgres; SQLite is used only as the candidate filter when those issue queries need event-derived tokens such as `release:` or `environment:`
- async jobs run on JetStream through `URGENTRY_ASYNC_BACKEND=jetstream`
- ingest rate limits, query guard windows, and scheduler leases run on Valkey through `URGENTRY_CACHE_BACKEND=valkey`
- blobs go to MinIO through `URGENTRY_BLOB_BACKEND=s3`
- install metadata persists the current environment, version, bootstrap-complete flag, and `URGENTRY_REGION`, then exposes that state on `/ops/` and in diagnostics exports

### Bring it up

```bash
cd deploy/compose
cp -f .env.example .env
docker compose up -d --build
```

### Smoke it

```bash
cd .
bash deploy/compose/smoke.sh up
```

That smoke flow proves:
- Postgres is reachable
- MinIO bucket bootstrap succeeded
- Valkey responds
- NATS monitor endpoint responds
- `/healthz` reports `async_backend=jetstream` and `cache_backend=valkey`
- API, ingest, worker, and scheduler all pass `/readyz`
- an event can be ingested through the ingest role
- the worker processes it into the shared SQLite state and keeps the configured telemetry bridge query substrate fresh for query-heavy reads
- an attachment roundtrip exercises the MinIO blob path through the API role

Once the API role is up, org-admin operators can also inspect the shipped runtime admin surface at:

- `/ops/`
- `GET /api/0/organizations/{org_slug}/ops/overview/`

That overview is backed by the live app state and surfaces backend reachability, queue backlog, recent backfills, recent retention outcomes, recent install-ledger actions, and recent audit history without requiring operators to stitch together several routes by hand.

For the command-line operator surface, use:

```bash
cd .
bash deploy/compose/ops.sh preflight
bash deploy/compose/ops.sh status
bash deploy/compose/ops.sh maintenance-status
bash deploy/compose/ops.sh security-report
bash deploy/compose/ops.sh enter-maintenance "upgrade window"
bash deploy/compose/ops.sh record-action secret.rotate "rotated operator secret"
bash deploy/compose/ops.sh backup-plan
bash deploy/compose/ops.sh rotate-bootstrap
bash deploy/compose/ops.sh verify-backup /tmp/urgentry-backup
bash deploy/compose/ops.sh rollback-plan 3 2 4 3
bash deploy/compose/ops.sh leave-maintenance
```

That wrapper loads the Compose env file, rewrites in-cluster DSNs to localhost when needed, and forwards to the matching `urgentry self-hosted ...` command so operators do not need to hand-assemble those invocations.

### Secret rotation

Generate and rotate the shipped secret set with:

```bash
cd .
bash deploy/rotate-secrets.sh compose --env-file deploy/compose/.env
bash deploy/rotate-secrets.sh k8s --secret-file deploy/k8s/secret.yaml
```

The shared rotation script:
- rewrites the target file in place
- writes a timestamped backup next to the source file
- writes a JSON summary file containing the newly generated values
- rotates bootstrap password, bootstrap PAT, metrics token, Postgres password, and MinIO root password together so the bundle stays internally consistent
- resolves the active Compose stack through `--project-name`, `URGENTRY_SELF_HOSTED_PROJECT`, or `COMPOSE_PROJECT_NAME` from the env file so verification hits the right runtime

For a live Compose install, enter maintenance mode first and then run:

```bash
cd .
bash deploy/rotate-secrets.sh compose --env-file deploy/compose/.env
```

That live path:
- alters the Postgres user password in place
- rotates the live bootstrap password and PAT in the control plane
- recreates MinIO plus the four Urgentry role containers
- reruns `deploy/compose/ops.sh security-report`
- reruns `deploy/compose/smoke.sh check`
- records `secret.rotate` in the operator ledger

If you only want to stage new values on disk before a maintenance window, add `--no-restart`. That rewrites the env file and summary output without touching the running stack.

For a live Kubernetes install, update the manifest first and then apply it during a maintenance window:

```bash
cd .
bash deploy/rotate-secrets.sh k8s --secret-file deploy/k8s/secret.yaml --namespace urgentry-system --apply
```

That live path:
- alters the Postgres user password through the running Postgres pod
- applies the rewritten `urgentry-secret`
- restarts MinIO plus the four Urgentry Deployments
- rotates the live bootstrap password and PAT through the restarted API pod
- waits for the restarted workloads to become ready

For a staged but not yet applied Kubernetes change, omit `--apply`. The script rewrites the manifest and summary output only. `--apply --no-restart` is intentionally rejected because it would leave the live pods on stale credentials.

### Backup and restore

```bash
cd .
bash deploy/compose/backup.sh /tmp/urgentry-backup
bash deploy/compose/restore.sh /tmp/urgentry-backup
URGENTRY_SELF_HOSTED_BACKUP_DIR=/tmp/urgentry-backup bash deploy/compose/upgrade.sh
bash deploy/compose/drills.sh backup-restore
```

What the shipped backup set includes:
- `postgres.sql.gz` for the serious self-hosted Postgres schemas
- `urgentry-data.tar.gz` for the still-shared SQLite data plane state
- `minio-data.tar.gz` for blob custody
- `nats-data.tar.gz` for JetStream backlog state
- `valkey-data.tar.gz` when warm cache state matters; the runtime can cold-start safely without it
- `manifest.json` with SHA-256 and byte-count integrity entries for every captured artifact plus the operator metadata files used during recovery
- operators can also request `kind=telemetry_rebuild` on the org backfill API after a partial telemetry-bridge loss to repopulate derived query facts from the restored SQLite plus object-storage truth; the runtime rejects overlapping active rebuild scopes with `409 Conflict` so two nodes cannot reset the same org or project families at once

Current restore expectations:
- RPO is bounded by the latest completed backup capture.
- RTO is one cold restore plus `preflight`, `status`, and `smoke.sh check`.

Upgrade expectations:
- `enter-maintenance` freezes ingest and mutating API or web actions with `503` responses while reads stay online, giving operators a clean write-drain window before schema or role changes.
- `restore.sh --verify-only <backup-dir>` provides a non-destructive manifest and checksum gate before any running stack is torn down.
- `upgrade.sh` captures and verifies a fresh backup by default before it mutates schemas, unless `URGENTRY_SELF_HOSTED_SKIP_UPGRADE_BACKUP=true` is set deliberately.
- `upgrade.sh` writes a reusable operator artifact directory with pre/post `preflight` and `status` snapshots, the effective `backup-plan`, backup verification output, and a rollback plan from the new schema versions back to the captured pre-upgrade versions.
- `urgentry self-hosted security-report` exposes the same secret-hygiene checks as a standalone operator report when an install needs to be audited without running a full upgrade.
- the install ledger records maintenance transitions, native reprocess plus telemetry rebuild requests, Compose backup plus restore plus upgrade actions, the Compose rollback-plan wrapper, and any manual operator actions sent through `record-action`.

Use [maintenance-mode](./urgentry-serious-self-hosted-maintenance-mode.md) for the exact drain workflow.
Use [operator-audit-ledger](./urgentry-serious-self-hosted-operator-audit-ledger.md) for the exact operator-ledger contract.

The shipped `backup-restore` drill proves the current restore point by bringing back a pre-backup event and attachment, replaying a queued backlog item after restore, and confirming that post-backup divergence is absent.

The shipped `role-restart` drill proves the current split-role HA boundary by stopping the worker long enough to accumulate backlog, restarting it and verifying the queued event drains, then stopping the scheduler long enough to prove a telemetry rebuild stays pending, that a second overlapping rebuild request is rejected with `409 Conflict`, and that the original run resumes once the scheduler returns.

## Cluster-oriented bundle

Location:
- [deploy/k8s/](../../deploy/k8s)

This path now ships as four separate Kubernetes Deployments:
- `urgentry-api`
- `urgentry-ingest`
- `urgentry-worker`
- `urgentry-scheduler`

Reason:
- it matches the serious self-hosted split-role runtime instead of hiding four roles inside one pod
- it keeps PostgreSQL, MinIO, Valkey, and NATS as the shared coordination and storage boundary
- it gives operators one workload per role for scaling, rollout, and debugging
- it keeps the current shared SQLite data plane honest by mounting the same `urgentry-data` PVC into each role pod

The supporting services still run as separate Kubernetes workloads:
- PostgreSQL
- MinIO
- Valkey
- NATS
- MinIO bootstrap job
- Urgentry Postgres bootstrap job

Storage requirement:
- the bundle includes a `urgentry-data` PVC with `ReadWriteMany`
- the backing storage must preserve SQLite file-lock semantics
- if your cluster cannot satisfy a RWX PVC with reliable locks, stay on the Compose bundle for now

### Prerequisite image flow

The manifests use `urgentry:latest`.

For a local cluster such as `kind`:

```bash
cd .
make docker
kind load docker-image urgentry:latest
```

### Apply the bundle

Before applying the bundle, replace the placeholder values in [secret.yaml](../../deploy/k8s/secret.yaml) or overlay a real Kubernetes secret.

```bash
kubectl apply -k deploy/k8s
```

### Smoke it

```bash
bash deploy/k8s/smoke.sh up
```

That script:
- waits for storage pods and bootstrap jobs
- reads the live bootstrap PAT from `secret/urgentry-secret`
- port-forwards the four role services
- runs the same event-plus-attachment smoke flow used by Compose

## Eval hook

The deployment smoke lane is available at:
- [eval/dimensions/selfhosted/run.sh](../eval/dimensions/selfhosted/run.sh)
- [eval/dimensions/selfhostedperf/run.sh](../eval/dimensions/selfhostedperf/run.sh)
- [eval/run-selfhosted.sh](../eval/run-selfhosted.sh)

It currently:
- runs the full Compose smoke flow
- runs a project-scoped telemetry rebuild drill, then verifies bridge-backed log, transaction, and discover-query recovery after seeded trace/log traffic
- boots a second live API node against the same Compose stack and verifies both nodes answer the same fresh event, log, transaction, and Discover reads
- runs the async recovery drills for JetStream redelivery, scheduler lease handoff, and Valkey outage behavior
- runs the split-role restart drill that proves worker backlog recovery plus scheduler restart resumption
- runs the full Compose backup and restore drill
- boots a disposable single-node `kind` cluster, loads `urgentry:latest`, patches the Kubernetes bundle with real eval secrets plus a local-cluster shared data volume, and runs `deploy/k8s/smoke.sh`
- runs the serious self-hosted perf lane for ingest, query, heap, throughput, rebuild, and scored HA churn budgets
- emits a strict readiness scorecard under `eval/reports/selfhosted/`

Local and CI prereqs for that live cluster lane:
- Docker Compose
- `kind`
- `kubectl`
- the workflow builds `urgentry:latest` during the Compose smoke before the cluster smoke loads it

The serious self-hosted scorecard is now a dedicated readiness gate, not a zero-weight appendix to the Tiny scorecard. It is driven by [framework-selfhosted.yaml](../eval/framework-selfhosted.yaml) and fails CI when the weighted readiness score drops below the configured pass threshold.

Current serious self-hosted perf budgets:
- ingest p50 `<= 150 ms`
- ingest p99 `<= 1000 ms`
- throughput `>= 250 eps`
- idle heap `<= 384 MB`
- bridge query p95 `<= 350 ms`
- steady-state soak throughput `>= 200 eps`
- steady-state soak error rate `<= 1%`
- post-soak query p95 `<= 500 ms`
- telemetry rebuild `<= 30 s`
- HA churn soak `PASS` with both API and async roles restarted under live traffic

The soak phase now spins up secondary API, worker, and scheduler replicas, churns both primary and secondary roles under live traffic, and verifies that both API nodes still answer query traffic before the lane passes.

The perf lane also writes `capacity-summary.json` and `capacity-summary.md` under `eval/reports/selfhosted-performance/` so operators can inspect the measured steady envelope, the recorded HA churn actions, and the recommended steady-state ceiling instead of relying only on pass/fail scorecard rows.

## Default credentials and ports

Compose defaults live in:
- [deploy/compose/.env.example](../../deploy/compose/.env.example)

Notable defaults:
- compose project `urgentry-selfhosted`
- bootstrap email `admin@urgentry.local`
- bootstrap PAT `gpat_self_hosted_bootstrap`
- API `8080`
- ingest `8081`
- worker `8082`
- scheduler `8083`
- MinIO bucket `urgentry-artifacts`

The smoke scripts override ports with random free host ports when they create their own temporary env files.

## Current limitations

- The bundle proves the current serious self-hosted operator path and service topology, not a completed Postgres runtime cutover.
- The Kubernetes readiness gate now boots a disposable single-node `kind` cluster. It proves the committed bundle can start, bind storage, pass the live smoke flow, and use the rotated secret material, but it is still not a substitute for a real multi-node storage and networking validation in a serious deployment.
- The Kubernetes bundle still depends on one shared SQLite PVC, so it is not yet a finished no-shared-disk serious self-hosted architecture.
- The control plane is Postgres-backed, but the remaining live event/replay/profile/query data plane is still SQLite-backed, so the deployment truth is split roles with external queue, quota, blob, and control services rather than a fully Postgres-backed data plane.
- Secret material still depends on operator-managed Kubernetes secrets rather than a finished external-secret or secret-manager story.

Those limits remain because the shipped serious self-hosted runtime is still a single-writer SQLite data plane wrapped in operator tooling, not a finished multi-node HA control/data-plane split.
