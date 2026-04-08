# Urgentry serious self-hosted async runtime ADR

Status: accepted
Last updated: 2026-03-29
Bead: `urgentry-5ew.3.1`

## Decision

Serious self-hosted Urgentry uses:

- NATS JetStream for durable async execution
- Valkey for shared rate limits, query guard quotas, idempotency windows, hot config caches, and singleton leases

Tiny mode keeps:

- SQLite durable jobs
- SQLite runtime leases
- in-process configuration reads

Serious mode does not reuse SQLite job or lease semantics under another name.

## Runtime boundaries

### JetStream owns

- ingest fanout after durable control-plane and blob writes
- normalization, grouping, projector, alert, retention, backfill, reprocess, and telemetry rebuild work
- durable retry and redelivery state
- operator-visible queue lag and DLQ state

### Valkey owns

- ingest rate-limit counters
- query guard quota counters
- idempotency windows for ingest and projector stages
- singleton leases for scheduler, retention, and bounded backfill controllers
- short-lived hot config and derived summary caches

### PostgreSQL control plane still owns

- job definitions, backfill definitions, reprocess requests, retention policies, and operator-visible progress rows
- durable workflow state and audit history

The async runtime is allowed to lag. It is not allowed to invent the only durable copy of operator intent.

## Message semantics

JetStream uses at-least-once delivery. All consumers must therefore be idempotent.

Rules:

- every published message has a deterministic `MessageID`
- every work item also carries a product-level `IdempotencyKey`
- consumers must tolerate redelivery, replay, timeout, and duplicate publication
- handlers only acknowledge success after the consumer’s durable side effects are complete

Ordering rules:

- cross-project global ordering is not guaranteed
- per-project causal ordering is best-effort and enforced only where the workflow requires it
- replay, profile, and telemetry projector consumers must be able to rebuild from checkpoints instead of relying on perfect delivery order

## Canonical streams and subjects

The canonical stream and subject contract is defined in [contract.go](../../internal/runtimeasync/contract.go).

The serious-mode baseline uses these stream families:

- `URGENTRY_INGEST`: raw accepted work
- `URGENTRY_PROJECTORS`: normalized event, transaction, replay, profile, and outcome fanout
- `URGENTRY_WORKFLOW`: grouping, issue updates, release rollups, alert evaluation
- `URGENTRY_OPERATIONS`: retention, backfills, reprocessing, imports, exports
- `URGENTRY_DLQ`: dead-letter subjects

## Lease ownership model

Singleton work uses Valkey leases with a fencing token:

- scheduler
- retention executor
- backfill coordinator
- import/export coordinator

Lease rules:

- lease acquisition returns a fencing token and expiry
- every mutating singleton step records the current fencing token in its durable progress row
- a stale holder must fail closed if its token is no longer current
- leases are renewable and intentionally short-lived

This prevents split-brain schedulers from advancing the same run after failover.

Canonical key families:

- `lease:scheduler:{scope}`
- `lease:retention:{scope}`
- `lease:backfill:{run_id}`
- `lease:import:{run_id}`
- `lease:export:{run_id}`

The lease token lives in Valkey. The operator-visible run row that the token fences lives in PostgreSQL.

## Failure model

### JetStream failures

If JetStream is unavailable:

- ingest that requires async durability must fail closed after control-plane/blob durability if publication cannot succeed
- workflow state must not pretend the async work exists
- health surfaces must report publish and consumer lag failures explicitly

### Valkey failures

If Valkey is unavailable:

- ingest rate limits and query guard quotas fail closed for serious mode
- singleton coordinators do not run without a lease service
- read-only control-plane pages may remain available
- audit surfaces must record degraded runtime mode

Loss of Valkey may block new singleton work, but it must not erase the durable progress rows that operators inspect in PostgreSQL.

### Consumer failures

If a consumer repeatedly fails:

- the message is redelivered with bounded attempts
- after the maximum attempt budget, it is routed to DLQ
- a durable control-plane run row tracks the failure and exposes it to operators

## Observability requirements

Serious mode must expose:

- queue depth and consumer lag by stream and subject
- redelivery counts and DLQ counts
- lease holder, lease age, and lease fencing token
- rate-limit and query-guard deny counts
- projector backlog age
- backfill and reprocess progress state

These metrics are product requirements, not “nice to have” admin extras.

## Failure drills

The runtime is not credible until these drills pass:

1. kill a worker during grouping and verify redelivery plus idempotent completion
2. kill the scheduler and verify lease handoff without duplicate retention execution
3. force a consumer into DLQ and verify operator visibility plus replay path
4. drop Valkey and verify serious-mode rate limits and singleton work fail closed
5. restart JetStream and verify backlog replay without workflow corruption

## Tiny versus serious boundaries

Tiny mode:

- uses SQLite jobs and SQLite runtime leases
- keeps existing pipeline worker and scheduler behavior
- remains the shipped default

Serious self-hosted:

- must not open SQLite-backed jobs or leases for normal runtime execution
- must use JetStream and Valkey through explicit interfaces
- may keep Tiny-compatible stubs only in tests

## Non-goals

This bead does not:

- switch the live runtime to JetStream yet
- add production Valkey config parsing yet
- define blob custody or telemetry bridge schemas

Those belong to:

- `urgentry-5ew.3.2`
- `urgentry-5ew.3.3`
- `urgentry-5ew.3.4`
- `urgentry-5ew.2.*`
- `urgentry-5ew.4.*`

## Related docs

- [urgentry-schema-starter-spec.md](urgentry-schema-starter-spec.md)
- [control-plane-adr](control-plane-adr.md)
- [roadmap](roadmap.md)
