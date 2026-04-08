# Urgentry serious self-hosted telemetry bridge ADR

Status: accepted
Last updated: 2026-03-31
Bead: `urgentry-5ew.4.1`

## Decision

Serious self-hosted Urgentry introduces a backend-neutral telemetry bridge contract with a PostgreSQL baseline and an optional Timescale variant.

The bridge is authoritative for query-serving telemetry facts in serious self-hosted until a separate analytics plane is required. It is not authoritative for:

- workflow state
- auth and control metadata
- large immutable blobs

The first migration set for this bridge lives in:
- [migrations.go](../../internal/telemetrybridge/migrations.go)
- [apply.go](../../internal/telemetrybridge/apply.go)

## Purpose

This bridge exists to let serious self-hosted deployments move beyond Tiny-mode SQLite query helpers without forcing ClickHouse immediately.

It must support:

- Discover issue, log, and transaction reads through the logical planner
- trace detail and span exploration
- outcomes and dropped-event accounting
- replay manifest and timeline indexes
- profile manifests and aggregate facts

## Ownership model

### Control plane owns

- org, project, auth, workflow, alert, release, monitor, dashboard, quota, and retention metadata
- projector checkpoints and backfill definitions

### Blob plane owns

- raw payloads
- attachments
- replay assets
- raw profile payloads
- debug artifacts

### Telemetry bridge owns

- append-heavy query-serving facts derived from the control and blob planes
- bounded materialized indexes needed for serious-mode query latency
- projector progress cursors that track what the bridge has already ingested by family and scope

The bridge must always be rebuildable from the control plane plus blob plane.

## Canonical bridge tables

The baseline bridge schema includes these table families:

- `telemetry.event_facts`
- `telemetry.log_facts`
- `telemetry.transaction_facts`
- `telemetry.span_facts`
- `telemetry.outcome_facts`
- `telemetry.replay_manifests`
- `telemetry.replay_timeline_items`
- `telemetry.profile_manifests`
- `telemetry.profile_samples`
- `telemetry.projector_cursors`

These are not 1:1 copies of Tiny SQLite tables. They are serving facts designed for planners and executors.

Baseline schema properties:

- every major fact family carries both `organization_id` and `project_id`
- org-wide reads get explicit org-and-time indexes instead of relying on project fanout alone
- projector cursors store `cursor_family`, `scope_kind`, `scope_id`, `checkpoint`, `last_event_at`, `last_error`, and small `metadata_json` for resumable rebuilds

## Query and rebuild rules

Rules:

- handlers do not construct backend-specific SQL inline
- planners produce logical requests
- bridge executors translate those requests into Postgres or Timescale queries
- every bridge table family defines a rebuild source
- live user reads do not trigger inline projection or repair work
- live projection uses durable async fanout instead of synchronous bridge writes on the ingest request path

Rebuild sources:

- events and logs: normalized events plus blob-backed payloads where needed
- transactions and spans: normalized transaction and span rows
- outcomes: stored client-report outcomes
- replay indexes: replay control metadata plus replay blob manifests
- profile facts: profile manifests plus raw profile payloads

## Live projection policy

The bridge must stay current through durable async projection.

That means:

- Urgentry durably writes control-plane and blob-plane state first
- Urgentry durably enqueues bridge projection work after the source-of-truth write succeeds
- projector consumers update bridge tables out of band
- serious self-hosted reads inspect freshness and follow the published stale and fail-closed budgets

The bridge does not get synchronous per-request projection on user ingest paths.

Why:

- synchronous projection ties ingest latency and availability to the bridge
- async projection gives operators a real backlog, a real replay path, and a cleaner failure model
- the bridge already has explicit freshness contracts, so the product can surface lag instead of hiding it inside request latency

## PostgreSQL baseline

The PostgreSQL baseline is the default serious self-hosted bridge:

- partition-friendly append tables
- JSONB for bounded flexible dimensions
- explicit indexes for common project, org, release, environment, and time-range filters
- projector cursors for resumable bridge fanout
- one migration package and runner that later deployment tooling can call directly instead of copying SQL text into shell scripts

## Timescale variant

The Timescale variant must implement the same logical table contract.

It may add:

- `timescaledb` extension
- hypertables on time-heavy facts
- compression and retention policies

It may not change:

- logical field ownership
- planner contract
- caller-visible semantics

## Latency and cost expectations

The bridge is the serious self-hosted substrate, not the forever analytics answer.

Targets:

- common scoped reads stay interactive for bounded self-hosted deployments
- retention and compression prevent the bridge from becoming an unbounded copy of raw payload history
- expensive multi-project scans fail through planner cost checks rather than saturating the control plane

Detailed benchmark gates belong to `urgentry-5ew.4.4`.

## Freshness rules this ADR locks

Implementation beads must satisfy all of these:

1. Serious self-hosted writes do not depend on synchronous bridge projection before returning success.
2. Serious self-hosted writes do require durable projection enqueue before the write path can claim the event is accepted for bridge-backed reads.
3. Bridge-backed reads never run projector catch-up inline.
4. Every bridge-backed surface reports freshness against the query execution contract in [query-execution-contract](query-execution-contract.md).
5. Projector lag, redelivery, and failure state are operator-visible.

## Non-goals

This bead does not:

- move existing handlers onto a new executor yet
- define the final ClickHouse contract
- make Timescale mandatory

Those belong to:

- `urgentry-5ew.4.2`
- `urgentry-5ew.4.3`
- `urgentry-5ew.4.4`
- the already-accepted [urgentry-post-tiny-analytics-adr.md](urgentry-post-tiny-analytics-adr.md)

## Related docs

- [urgentry-post-tiny-analytics-adr.md](urgentry-post-tiny-analytics-adr.md)
- [urgentry-discover-query-adr.md](urgentry-discover-query-adr.md)
- [control-plane-adr](control-plane-adr.md)
- [runtime-adr](runtime-adr.md)
- [query-execution-contract](query-execution-contract.md)
- [roadmap](roadmap.md)
