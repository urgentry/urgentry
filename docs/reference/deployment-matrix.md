# Urgentry deployment matrix

Status: living document
Last updated: 2026-03-19

## Purpose

Convert the architecture research into a current best recommendation for each deployment tier, plus explicit graduation triggers as product scope and volume increase.

## Executive recommendation

Do **not** force one storage stack across all tiers.

Current best path:
- **Tiny:** SQLite + Litestream + local filesystem
- **Self-hosted default:** PostgreSQL + MinIO + Valkey + NATS JetStream
- **Cloud / rich telemetry:** PostgreSQL + ClickHouse + object storage + Valkey + NATS JetStream
- **Scale-up stream option later:** Redpanda, only if Kafka-class semantics or throughput needs are proven

## Tier 1 — Tiny

### Target user
- solo developer
- side project
- edge / on-prem single node
- teams that primarily want error tracking, not broad observability

### Recommended stack
- Urgentry single binary
- SQLite
- Litestream
- local filesystem for blobs/artifacts
- in-process job runner

### Why
- lowest operational footprint
- strongest one-binary story
- SQLite and Litestream evidence supports local-first plus backup/recovery
- avoids pretending Tiny should solve multi-node HA

### Product scope for this tier
- errors/events
- issue grouping and issue detail
- releases/environments
- basic attachments
- basic source maps / ProGuard in constrained workflows
- email/webhook alerts

### Explicit non-goals
- serious HA
- logs/traces as first-class workloads
- high-cardinality search and analytics
- replay/profiles

### Upgrade trigger out of Tiny
Move off Tiny when **any** of these become true:
- more than one active writer/service instance is required
- availability expectations exceed single-node plus restore
- object/blob volume starts to make local filesystem management painful
- teams need stronger auth/project isolation and operational tooling
- logs/traces/retention needs become central

## Tier 2 — Self-hosted default

### Target user
- startup team
- multi-project org
- design partner / serious production user
- users comparing against Sentry self-hosted complexity

### Recommended stack
- Urgentry binary in split roles if needed
- PostgreSQL
- MinIO or S3-compatible object storage
- Valkey
- NATS JetStream

Current bundle paths:
- Docker Compose bundle under [deploy/compose/](../deploy/compose)
- cluster-oriented Kubernetes bundle under [deploy/k8s/](../deploy/k8s)

### Why
- best balance of simplicity, correctness, and migration credibility
- Postgres remains the strongest control-plane default
- MinIO keeps blob-heavy workflows out of the relational database
- Valkey covers quotas/idempotency/hot config cleanly
- NATS gives a durable async pipeline without Kafka-family operational weight

### Product scope for this tier
- full P0 launch slice
- optional sessions/release-health basics
- light traces if query expectations are constrained
- limited search and dashboards

### Strong recommendation
Support a **narrow Postgres-only evaluation mode** if it helps adoption, but do **not** make it the main long-term recommendation for serious self-hosted installs.

Reason:
- users may love the simpler bootstrap story
- but once artifacts, async work, rate limits, and retention become real, the support burden shifts elsewhere anyway

Current deployment caveat:
- the shipped serious self-hosted bundle already runs async execution on JetStream and shared quotas plus leases on Valkey, but it still runs the split app roles over one shared SQLite data volume while the remaining control-plane and telemetry cutovers continue to land
- MinIO is already active for blob custody in that bundle
- Postgres operator migrations are already active through the bootstrap tooling in the same bundle

## Tier 3 — Cloud / rich telemetry

### Target user
- managed SaaS
- larger teams
- customers expecting broader observability
- higher-ingest or longer-retention tenants

### Recommended stack
- Urgentry API / ingest / worker / scheduler roles
- PostgreSQL for control plane
- ClickHouse for telemetry analytics
- S3 / R2 / GCS-compatible blob storage
- Valkey
- NATS JetStream

### Why
- ClickHouse has the strongest evidence for high-cardinality observability-style analytics
- Postgres stays the clean source of truth for org/project/issue/workflow state
- object storage remains mandatory for artifacts and large payloads
- this stack best matches the future direction if traces/logs/replays expand

### Product scope for this tier
- full P0
- strong P1 path
- traces/logs become realistic
- longer retention and larger tenants
- richer dashboards and query surfaces

## Graduation path for the event plane

Control plane stays on **PostgreSQL**.

The main graduation question is the **event / analytics plane**:
1. Postgres-only
2. Postgres + Timescale bridge
3. Postgres + ClickHouse

## Stage A — Postgres-only event path

### When to use
Use Postgres-only when all of the following are mostly true:
- product is still **errors-first**
- telemetry is mostly events/issues, not broad logs/traces
- retention is modest
- query patterns are mostly issue list/detail, release/environment filters, and a few top tags
- operator simplicity matters more than analytics flexibility

### Practical fit
Good for:
- early self-hosted
- initial design partners
- migration proof
- compatibility harness and issue workflow validation

### Warning signs
You are outgrowing Postgres-only when:
- event tables start dominating total DB size
- autovacuum/partition maintenance becomes a persistent operator complaint
- ad-hoc analytics force too many JSONB scans or large index expansions
- query latency becomes unstable during ingest spikes
- logs/traces or long retention enter the roadmap

## Stage B — Postgres + Timescale bridge

### When to use
Adopt Timescale when:
- you want to stay operationally close to Postgres
- workloads are becoming more append-heavy and time-bucketed
- compression/retention/continuous aggregates would materially help
- sessions, check-ins, metrics-like rollups, and basic traces are growing
- you need more headroom but do not yet need a separate OLAP engine

### Best fit
Timescale is best for:
- self-hosted users who want one main database family
- teams with telemetry-heavy dashboards but not full observability sprawl
- a bridge period where product scope is expanding faster than ops appetite

### Warning signs
You are outgrowing Timescale when:
- logs become first-class
- high-cardinality exploratory filters dominate usage
- trace/span exploration becomes central
- retention and scan cost keep growing despite compression and rollups
- event query workloads start to look more like observability analytics than Postgres-first time-series

## Stage C — Postgres + ClickHouse

### When to use
Adopt ClickHouse when any of these become true:
- traces and logs are becoming first-class product surfaces
- customers expect long retention on large append-only telemetry volumes
- high-cardinality query patterns become normal
- query cost/latency under scale matters more than minimizing component count
- cloud multi-tenant efficiency becomes a major business constraint

### Best fit
ClickHouse is the right destination for:
- managed cloud
- serious observability expansion
- rich aggregate and faceted exploration
- tenants that would otherwise turn Postgres tuning into a full-time product tax

## Hosted fleet note

Hosted Urgentry sits on top of the cloud-oriented stack, but it adds one more layer of rules:

- account and plan ownership
- region and cell placement
- rollout and rollback sequencing
- support and recovery tooling
- hosted launch gates

Those contracts live in:

- [urgentry-hosted-multi-tenant-roadmap.md](urgentry-hosted-multi-tenant-roadmap.md)
- [urgentry-hosted-deploy-and-rollback.md](urgentry-hosted-deploy-and-rollback.md)

## Starting cut lines to validate in benchmarks

These are **working heuristics**, not hard laws.

### Stay Postgres-only if
- workload is mostly errors/issues
- hot retention is short to moderate
- ad-hoc analytics are limited
- expected telemetry volume is still comfortably within one serious Postgres operational envelope
- the team values minimum moving parts over broader analytics power

### Move to Timescale when
- event data clearly becomes time-series-like rather than issue-record-like
- compression/retention and rollups matter before ClickHouse complexity is justified
- you need more ingest/query headroom while keeping the self-hosted story largely Postgres-native

### Move to ClickHouse when
- logs/traces/high-cardinality search become default product behavior
- performance and cost depend on columnar analytics rather than relational tuning
- cloud efficiency or large-tenant isolation becomes strategically important

## Queue graduation: NATS JetStream to Redpanda

Stay on **NATS JetStream** unless proven otherwise.

Only consider **Redpanda** when:
- Kafka ecosystem compatibility becomes commercially important
- replay windows, partition-heavy consumer groups, or throughput targets exceed what the NATS design comfortably supports
- customers or internal pipelines begin to assume Kafka-native tooling

Do **not** jump to Redpanda or Kafka just because they feel more enterprise.

## Current final recommendation

If starting Urgentry now:

### Tiny SKU
- SQLite
- Litestream
- local filesystem
- no external queue/cache by default

### Serious self-hosted SKU
- PostgreSQL
- MinIO
- Valkey
- NATS JetStream
- optional Timescale path for telemetry-heavier installs

### Cloud SKU
- PostgreSQL
- ClickHouse
- object storage
- Valkey
- NATS JetStream
- Redpanda only after a proven need

## Decision note

The current middle path is:
- keep **Postgres** as the durable control-plane anchor
- preserve a **Tiny SQLite** story
- allow **Timescale** as the bridge
- treat **ClickHouse** as the eventual observability destination
- prefer **NATS** first and **Redpanda** later only if justified
