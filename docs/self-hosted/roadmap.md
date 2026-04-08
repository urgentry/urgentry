# Urgentry serious self-hosted roadmap

Status: completed and retained as a historical serious self-hosted roadmap
Last updated: 2026-03-29
Root epic: `urgentry-5ew`

Current implementation note:

- the `urgentry-5ew` roadmap is now fully shipped; this document is retained as the execution record for the completed serious self-hosted baseline program

- `urgentry-5ew.1.4` is now shipped: `internal/postgrescontrol/controlplane_harness_test.go` runs the same control-plane API and authenticated web mutation/read suite against SQLite Tiny mode and the Postgres control plane, and the resulting fixes eliminated SQLite-only settings and alert handler assumptions
- `urgentry-5ew.1.5` and `urgentry-5ew.1.6` are now shipped: serious-mode runtime wiring now writes canonical groups into the Postgres control plane, and project issue list, issue detail, org issue search, and the default Discover issue table now hydrate canonical issue rows through backend-neutral control-plane issue-read seams instead of SQLite-only row helpers
- `urgentry-5ew.2.2` is now shipped: manifest-aware blob reads are centralized in `internal/blob/resolver.go`, replay/profile/native routes now resolve through canonical manifest readers instead of storage-layout guesses, and archived blob restore is exercised by targeted API/web regression tests
- `urgentry-5ew.3.2` through `urgentry-5ew.3.4` are now shipped: serious self-hosted roles can switch to JetStream-backed worker plus backfill execution and Valkey-backed quotas plus leases, while `deploy/compose/drills.sh` plus the self-hosted eval lane exercise worker redelivery, scheduler handoff, and Valkey outage behavior
- `urgentry-5ew.4.2` is now shipped: Discover, logs, traces, replay, and profile handlers consume the injected logical telemetry query service from `internal/telemetryquery/`, Tiny mode stays on the SQLite-backed implementation, and serious self-hosted runtime wiring opens plus migrates the telemetry bridge so those query-heavy reads can execute against Postgres or Timescale when configured
- `urgentry-5ew.4.3` is now shipped: org-admin backfill control can now launch `telemetry_rebuild` runs, the worker plane drives resumable bridge rebuilds through `internal/telemetrybridge/projector.go`, and targeted recovery tests prove that missing bridge families can be rebuilt from the authoritative SQLite plus blob planes
- `urgentry-5ew.4.5` is now shipped: serious-mode Discover table, series, and explain execution for the supported logs and transactions datasets now run against telemetry bridge fact tables instead of falling back to the SQLite discover engine, with direct bridge-harness coverage plus a self-hosted rebuild drill that verifies bridge-backed discover transaction reads after recovery
- `urgentry-5ew.4.6` is now shipped: serious-mode profile detail, trace/release profile lookup, and top-down, bottom-up, flamegraph, hot-path, and compare queries now execute through a bridge-backed profile read store that reconstructs canonical profile graphs from bridge manifests plus raw blob payload fallback instead of delegating to `sqlite.ProfileStore`
- `urgentry-5ew.5.1` is now shipped: Compose and cluster-oriented deployment bundles boot the split serious self-hosted stack end to end, smoke `/healthz` backend selection, and verify ingest plus attachment flow against Postgres, MinIO, Valkey, and NATS

## Purpose

Define the next active program after Tiny-mode parity: a serious self-hosted baseline that operators can install, scale, upgrade, back up, restore, and trust.

This roadmap is not another Tiny-surface expansion plan. Tiny mode is already the shipped SQLite-first product wedge. The next work is the self-hosted substrate described in the architecture docs:

- PostgreSQL for the control plane
- S3-compatible object storage for large artifacts
- JetStream for durable async execution
- Valkey for shared quotas and hot caches
- a backend-neutral telemetry query contract that can start on a Postgres or Timescale bridge and later graduate to a richer analytics plane

## Why this is next

Current reality:

- Tiny-mode parity and the deeper post-Tiny parity program are shipped
- the former deep-parity roadmap in [urgentry-deep-parity-roadmap.md](urgentry-deep-parity-roadmap.md) is now historical
- the latest eval rerun produces a real scorecard again, but it still measures Tiny mode and leaves major serious self-hosted concerns unscored

The next bottleneck is no longer product surface breadth. It is deployment credibility:

- multi-node runtime ownership
- external storage and queue contracts
- upgrade and rollback discipline
- backup and restore truth
- operator observability
- serious self-hosted performance budgets

## Scope

This roadmap covers:

- the serious self-hosted control plane
- object-backed artifact custody
- JetStream and Valkey runtime services
- the serious self-hosted telemetry bridge
- packaging, upgrades, backups, restore, and deployment eval

This roadmap does not cover:

- more Tiny-only UX breadth
- another parity-only feature wave
- managed-cloud-only scale work
- enterprise identity breadth beyond what is needed to operate the self-hosted baseline

## Program shape

Root roadmap epic:

- `urgentry-5ew` Serious self-hosted baseline roadmap

Child epics:

- `urgentry-5ew.1` Control-plane cutover and backend-neutral stores
- `urgentry-5ew.2` Object storage and artifact custody
- `urgentry-5ew.3` Async backbone and multi-node runtime
- `urgentry-5ew.4` Telemetry bridge and query execution
- `urgentry-5ew.5` Operator workflows and release engineering

## Critical path

1. `urgentry-5ew.1.1` write the control-plane ADR and cutover checklist
2. `urgentry-5ew.3.1` write the JetStream and Valkey runtime ADR
3. `urgentry-5ew.4.1` define the serious self-hosted telemetry bridge schema
4. `urgentry-5ew.1.2` and `urgentry-5ew.1.3` land Postgres schema plus backend-neutral control-plane stores
5. `urgentry-5ew.3.2` and `urgentry-5ew.3.3` move async execution and quotas to shared runtime services
6. `urgentry-5ew.2.*` and `urgentry-5ew.4.*` align artifact custody and query execution with the new substrate
7. `urgentry-5ew.5.*` productize deployment, upgrades, backup/restore, and eval

## Phase plan

### Phase 0: contracts before code motion

Goal:

- lock the self-hosted control-plane, async-runtime, and telemetry-bridge contracts before implementation creates backend-specific drift

Primary beads:

- `urgentry-5ew.1.1`
- `urgentry-5ew.3.1`
- `urgentry-5ew.4.1`

Locked contracts:

- [control-plane-adr](control-plane-adr.md)
- [runtime-adr](runtime-adr.md)
- [telemetry-bridge-adr](telemetry-bridge-adr.md)

Exit criteria:

- the team can explain what is authoritative in Postgres, object storage, JetStream, Valkey, and the telemetry bridge
- Tiny-only assumptions are named and bounded
- rollback points are documented before migrations begin

Phase 0 contract docs:

- [control-plane-adr](control-plane-adr.md)
- [runtime-adr](runtime-adr.md)
- [telemetry-bridge-adr](telemetry-bridge-adr.md)

### Phase 1: control plane first

Goal:

- move the control plane onto the serious self-hosted baseline without regressing the shipped Tiny workflow

Primary beads:

- `urgentry-5ew.1.2`
- `urgentry-5ew.1.3`
- `urgentry-5ew.1.4`
- `urgentry-5ew.1.5`
- `urgentry-5ew.1.6`

Exit criteria:

- Postgres boots a clean control plane
- shared stores run against SQLite Tiny mode and Postgres serious mode
- the dual-backend harness proves auth, settings, issue workflow, release, alert, and monitor parity

### Phase 2: runtime and artifact substrate

Goal:

- make the serious self-hosted runtime multi-node credible and artifact-safe

Primary beads:

- `urgentry-5ew.2.1`
- `urgentry-5ew.2.2`
- `urgentry-5ew.2.3`
- `urgentry-5ew.2.4`
- `urgentry-5ew.3.2`
- `urgentry-5ew.3.3`
- `urgentry-5ew.3.4`

Exit criteria:

- object-backed artifacts are authoritative above Tiny
- worker, scheduler, and backfill flows no longer depend on SQLite jobs in serious mode
- rate limits and query quotas behave consistently across nodes
- failure drills prove lease handoff, retry, and recovery semantics

### Phase 3: telemetry bridge cutover

Goal:

- move query-heavy surfaces onto logical planners and serious self-hosted bridge executors

Primary beads:

- `urgentry-5ew.4.5`
- `urgentry-5ew.4.6`
- `urgentry-5ew.4.4`

Exit criteria:

- Discover, logs, traces, replay, and profiles no longer depend on SQLite-specific direct query helpers in serious mode
- rebuild and backfill jobs exist for every derived telemetry surface
- performance and cost budgets exist for the self-hosted bridge, not only Tiny mode

Current `urgentry-5ew.4.2` deliverables:
- logical telemetry query contract at [contracts.go](../../internal/telemetryquery/contracts.go)
- SQLite implementation at [sqlite.go](../../internal/telemetryquery/sqlite.go)
- serious self-hosted bridge implementation at [bridge.go](../../internal/telemetryquery/bridge.go)
- runtime wiring in [run.go](../../internal/app/run.go), [server.go](../../internal/http/server.go), and [web.go](../../internal/web/web.go)

Current `urgentry-5ew.4.3` deliverables:
- backfill-kind extension at [backfill_store.go](../../internal/sqlite/backfill_store.go)
- worker execution in [backfill_controller.go](../../internal/pipeline/backfill_controller.go)
- projector executor at [projector.go](../../internal/telemetrybridge/projector.go)

Current `urgentry-5ew.4.6` deliverables:
- bridge-backed profile reader at [profile_store.go](../../internal/telemetrybridge/profile_store.go)
- serious-mode profile cutover in [bridge.go](../../internal/telemetryquery/bridge.go)
- profile-manifest bridge metadata expansion in [migrations.go](../../internal/telemetrybridge/migrations.go) and [projector.go](../../internal/telemetrybridge/projector.go)
- representative bridge profile query coverage in [bridge_test.go](../../internal/telemetryquery/bridge_test.go)
- org-admin API launch plus cancellation audit flow in [backfills.go](../../internal/api/backfills.go)

Current `urgentry-5ew.4.5` deliverables:
- bridge-native discover executor for logs and transactions in [bridge_discover.go](../../internal/telemetryquery/bridge_discover.go)
- serious-mode routing in [bridge.go](../../internal/telemetryquery/bridge.go)
- bridge discover harness coverage in [bridge_test.go](../../internal/telemetryquery/bridge_test.go)

### Phase 4: operator truth

Goal:

- turn the self-hosted stack into a supportable product, not just a pile of services

Primary beads:

- `urgentry-5ew.5.1`
- `urgentry-5ew.5.2`
- `urgentry-5ew.5.3`
- `urgentry-5ew.5.4`
- `urgentry-5ew.5.5`

Exit criteria:

- supported deployment bundles exist
- upgrade and rollback procedures are tested
- backup and restore drills are documented and repeatable
- operator surfaces expose health, queue lag, backfills, retention, and audit state
- eval can score a self-hosted deployment, not only Tiny mode

Current `urgentry-5ew.5.1` deliverables:
- Compose bundle under [deploy/compose/](../../deploy/compose)
- cluster-oriented bundle under [deploy/k8s/](../../deploy/k8s)
- smoke scripts at [deploy/compose/smoke.sh](../../deploy/compose/smoke.sh) and [deploy/k8s/smoke.sh](../../deploy/k8s/smoke.sh)
- eval runner at [eval/dimensions/selfhosted/run.sh](../eval/dimensions/selfhosted/run.sh)

Current `urgentry-5ew.3.2` through `urgentry-5ew.3.4` deliverables:
- JetStream runtime queue at [jetstream.go](../../internal/runtimeasync/jetstream.go)
- Valkey lease store at [valkey.go](../../internal/runtimeasync/valkey.go)
- Valkey-backed ingest limiter at [valkey_rate_limit.go](../../internal/auth/valkey_rate_limit.go)
- Valkey-backed query guard at [query_guard_valkey.go](../../internal/sqlite/query_guard_valkey.go)
- runtime drills at [drills.sh](../../deploy/compose/drills.sh)

## Detailed bead map

### `urgentry-5ew.1` Control-plane cutover and backend-neutral stores

- `urgentry-5ew.1.1` Write serious self-hosted control-plane ADR and cutover checklist
  - deliverable: [control-plane-adr](control-plane-adr.md)
- `urgentry-5ew.1.2` Add Postgres schema and migrations for control-plane state
  - deliverable: [migrations.go](../../internal/postgrescontrol/migrations.go)
- `urgentry-5ew.1.3` Port control-plane stores behind shared backend-neutral contracts
  - deliverables: [catalog.go](../../internal/controlplane/catalog.go), [admin.go](../../internal/controlplane/admin.go), [issues.go](../../internal/controlplane/issues.go), [releases.go](../../internal/controlplane/releases.go), [monitors.go](../../internal/controlplane/monitors.go), [services.go](../../internal/controlplane/services.go), [sqlite.go](../../internal/controlplane/sqlite.go), [router.go](../../internal/api/router.go), [server.go](../../internal/http/server.go), [web.go](../../internal/web/web.go)
- `urgentry-5ew.1.4` Add dual-backend control-plane compatibility harness
  - deliverable: [controlplane_harness_test.go](../../internal/postgrescontrol/controlplane_harness_test.go)
- `urgentry-5ew.1.5` Wire serious runtime to Postgres-backed control-plane services
  - deliverables: [control_runtime.go](../../internal/app/control_runtime.go), [run.go](../../internal/app/run.go)
- `urgentry-5ew.1.6` Move issue query surfaces onto control-plane services
  - deliverables: [issue_read_store.go](../../internal/postgrescontrol/issue_read_store.go), [issues.go](../../internal/api/issues.go), [discover.go](../../internal/api/discover.go), [discover.go](../../internal/web/discover.go), [discover_builder.go](../../internal/web/discover_builder.go)

Control contract:
- [control-plane-adr](control-plane-adr.md)

Why this epic matters:

- if the control plane is not backend-neutral first, every later self-hosted task will reintroduce Tiny-specific assumptions and make rollback harder

### `urgentry-5ew.2` Object storage and artifact custody

- `urgentry-5ew.2.1` Add S3-compatible blob store adapter and config surface
- `urgentry-5ew.2.2` Move artifact and replay/profile/native reads behind manifest-aware blob contracts
- `urgentry-5ew.2.3` Add object-storage retention archive and restore executor
- `urgentry-5ew.2.4` Add serious self-hosted artifact import export verification

Why this epic matters:

- artifact custody becomes the operational truth once the product stops living on one SQLite node with a local blob directory

### `urgentry-5ew.3` Async backbone and multi-node runtime

- `urgentry-5ew.3.1` Write JetStream and Valkey runtime ADR with failure model
  - deliverable: [runtime-adr](runtime-adr.md)
- `urgentry-5ew.3.2` Move worker scheduler and backfill execution onto JetStream
- `urgentry-5ew.3.3` Move rate limits query guard quotas and hot caches onto Valkey
- `urgentry-5ew.3.4` Add multi-node failure recovery drills for async runtime

Runtime contract:
- [runtime-adr](runtime-adr.md)

Why this epic matters:

- serious self-hosted does not exist if queues, quotas, and leases remain single-node behavior dressed up as roles

### `urgentry-5ew.4` Telemetry bridge and query execution

- `urgentry-5ew.4.1` Define Postgres or Timescale telemetry bridge schema
  - deliverable: [telemetry-bridge-adr](telemetry-bridge-adr.md)
- `urgentry-5ew.4.2` Move query-heavy surfaces onto logical planners and serious-mode executors
- `urgentry-5ew.4.3` Add telemetry rebuild and backfill jobs from control and blob planes
- `urgentry-5ew.4.4` Add serious self-hosted latency and cost benchmark gates

Telemetry bridge contract:
- [telemetry-bridge-adr](telemetry-bridge-adr.md)

Why this epic matters:

- self-hosted credibility requires a real bridge between today’s Tiny query surfaces and tomorrow’s larger telemetry substrate

### `urgentry-5ew.5` Operator workflows and release engineering

- `urgentry-5ew.5.1` Ship serious self-hosted deployment bundles
- `urgentry-5ew.5.2` Add upgrade migration and rollback tooling
- `urgentry-5ew.5.3` Add backup restore and disaster recovery drills
- `urgentry-5ew.5.4` Add operator health audit and runtime admin surfaces
- `urgentry-5ew.5.5` Add serious self-hosted eval lane and deployment scorecard

Why this epic matters:

- without operator truth, the self-hosted stack is just architecture theater

## Validation rules

Every bead in this roadmap must update docs and land with the smallest sufficient proof. Minimum expected proof types across the program:

- backend-neutral unit and integration tests
- dual-backend behavioral diff tests where Tiny and serious mode share a surface
- deployment smoke tests
- upgrade and rollback rehearsals
- backup and restore drills
- performance gates for serious self-hosted ingest and query paths
- eval artifacts and scorecards for deployment readiness

## Recommended execution order

Start here:

1. `urgentry-5ew.1.1`
2. `urgentry-5ew.3.1`
3. `urgentry-5ew.4.1`

Then land the first irreversible substrate in this order:

1. `urgentry-5ew.1.2`
2. `urgentry-5ew.1.3`
3. `urgentry-5ew.2.1`
4. `urgentry-5ew.3.2`
5. `urgentry-5ew.3.3`

The first operator-facing milestone should be:

- `urgentry-5ew.5.1` deployment bundles
- `urgentry-5ew.5.2` upgrade/rollback tooling
- `urgentry-5ew.5.5` self-hosted eval lane

## Success condition

This roadmap is complete when Urgentry can honestly claim:

- Tiny mode is the lightweight SKU
- serious self-hosted is the supported multi-service baseline
- operators can install, upgrade, back up, restore, and observe that baseline with published procedures and automated validation

Until then, serious self-hosted remains a direction, not a shipped product.
