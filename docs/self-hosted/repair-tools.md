# Urgentry serious self-hosted repair tools

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-m3w`

## Why this exists

The serious self-hosted bundle already exposes status, backfills, retention outcomes, and audit logs. Operators still need a sharper answer when something drifts or stalls: what can they inspect, and which repair actions are safe to expose?

This document defines that answer. The typed source of truth lives in [repair.go](../../internal/selfhostedops/repair.go).

## Repair surfaces

The repair pack covers six operator surfaces:

- bridge lag
- backfills
- retention drift
- replay rebuilds
- profile rebuilds
- quota incidents

Each surface defines:

- visibility signals the operator page or API must show
- safe repair actions
- safeguards that have to wrap those actions

## Key actions

- bridge lag: restart rebuild work and reset bridge cursors with scope and audit safeguards
- backfills: restart failed or stalled runs without restarting healthy ones
- retention: restore missing blobs only after the operator can see the affected record
- replay and profile: launch targeted rebuilds with project scope and audit coverage
- quota: clear poisoned or stale quota windows only with explicit org and workload scope

## What this bead does not do

This bead does not wire the buttons into `/ops/` yet. It does not expose new APIs. It defines the repair contract those later operator surfaces should implement.

## Validation

[repair_test.go](../../internal/selfhostedops/repair_test.go) covers:

- complete repair-surface coverage
- non-empty signals, actions, and safeguards
- explicit replay, profile, and quota repair support
