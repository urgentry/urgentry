# Urgentry serious self-hosted migration compatibility gates

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-uua`

## Why this exists

Mixed-version preflight proves a single live cluster shape. Operators still need one explicit statement about the rolling window Urgentry is willing to support.

This document defines that statement. The typed source of truth lives in [migration_gate.go](../../internal/selfhostedops/migration_gate.go).

## Decision

Serious self-hosted aims to support:

- `N-1` rolling compatibility
- `N-2` migration compatibility gates

Anything older than that is outside the default operator contract and should require explicit upgrade staging instead of best-effort hope.

## What the gate checks

The migration gate compares:

- the target binary version
- the target control schema version
- the target telemetry schema version
- the oldest still-running binary and schema versions in the rollout window

It reports:

- whether the window is compatible
- the binary, control-schema, and telemetry-schema lag
- whether the current rollout still fits inside `N-1`
- whether it still fits inside `N-2`

## Required proofs

- prove mixed-version preflight before widening the rollout
- prove rollback safety before contract cleanup
- prove backup verification from the same upgrade window

## Validation

[migration_gate_test.go](../../internal/selfhostedops/migration_gate_test.go) covers:

- a valid `N-2` window
- rejection once the lag moves beyond `N-2`
