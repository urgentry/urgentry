# Urgentry serious self-hosted telemetry export contract

Status: current serious self-hosted contract
Last updated: 2026-03-30
Bead: `urgentry-hs6`

## Why this exists

The telemetry engine ADR now says when the bridge should graduate. Operators still need one stable contract for how telemetry facts leave the bridge on the way to a future analytics backend.

This document defines that contract. The typed source of truth lives in [export_contract.go](../../internal/telemetrybridge/export_contract.go).

## Modes

The export framework supports three modes:

- `snapshot_export`
- `shadow_dual_write`
- `cutover_dual_write`

Snapshot export is the first safe step. Shadow dual-write proves parity without making the new backend authoritative. Cutover dual-write is only allowed on the surfaces where the product can tolerate the extra write path and verification burden.

## Surface rules

- all surfaces (events, logs, traces, replay, profile) support all three modes
- every surface requires cursor checkpoints
- every surface requires idempotency keys
- every shadow path must verify shadow reads before it can become trusted

## Replay and profile graduation

Replay and profile materialization depends on blob-backed reconstruction paths. After verifying shadow dual-write parity through the bridge projection tests and benchmark suite, both surfaces are now graduated to full cutover dual-write support.

## Relationship to other telemetry contracts

- [telemetry-engine-adr](telemetry-engine-adr.md) defines when the bridge should graduate
- [fanout-contract](fanout-contract.md) defines the delivery and lag assumptions underneath export work
- [telemetry-observability](telemetry-observability.md) defines the lag and cost signals that should drive graduation planning

## Validation

[export_contract_test.go](../../internal/telemetrybridge/export_contract_test.go) covers:

- a valid default export contract
- cutover dual-write enabled for all surfaces including replay and profile
