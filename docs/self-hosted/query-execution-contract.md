# Urgentry serious self-hosted query execution contract

Status: current serious self-hosted contract
Last updated: 2026-03-31
Bead: `urgentry-25r`

## Why this exists

Serious self-hosted already routes Discover, logs, traces, replay, and profile reads through the telemetry query service. That still left one dangerous gap: the repo had no single contract for freshness, concurrency, cancellation, or rebuild behavior once more than one query node exists.

This document defines that contract. The typed source of truth lives in [execution.go](../../internal/telemetryquery/execution.go).

## Surfaces covered

- Discover logs
- Discover transactions
- traces
- replays
- profiles

## Rules this contract locks

- live user reads never trigger inline bridge rebuild work
- live bridge freshness comes from durable async projection, not synchronous ingest-side bridge writes
- every surface gets a stale-read budget and a fail-closed budget
- every surface gets a per-org concurrency ceiling
- every surface gets a request timeout
- cluster admission uses one shared quota backend instead of per-node drift
- serious self-hosted nodes must support query cancellation

## Default posture

- Discover and traces are the faster interactive reads. They get tighter stale budgets and higher org concurrency.
- Replay and profile reads are heavier. They get smaller org concurrency ceilings and longer timeouts.
- The cluster uses Valkey for shared query admission.
- The query layer exports signals for org concurrency, cluster queue depth, bridge freshness, timeout and cancellation counts, and query-guard deny rates.

Default budgets:

| Surface | Stale budget | Fail-closed budget | Max org concurrency | Timeout |
|---|---:|---:|---:|---:|
| Discover logs | 120s | 600s | 6 | 5s |
| Discover transactions | 120s | 600s | 6 | 5s |
| Traces | 120s | 600s | 4 | 5s |
| Replays | 300s | 900s | 3 | 8s |
| Profiles | 300s | 900s | 2 | 8s |

These are the default service-level budgets for the first serious self-hosted implementation. Later tuning can change the numbers, but it must change the typed contract, this document, and the operator docs together.

## What this bead does not do

This bead does not rewire the bridge executor yet. It does not add the multi-node load harness yet. It defines the contract those later changes have to obey.

## Validation

[execution_test.go](../../internal/telemetryquery/execution_test.go) covers:

- full surface coverage
- validation of stale and fail-closed budgets
- explicit rejection of inline rebuilds on user reads
- tighter concurrency for profile queries than for Discover

Implementation beads must also prove:

- the live projection path enqueues bridge work durably instead of projecting inline on user writes
- stale reads stay inside the published per-surface budgets under steady ingest
- reads fail closed after the published fail-closed budgets are exceeded
- operator surfaces expose bridge freshness and backlog age by family
