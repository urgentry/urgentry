# Urgentry serious self-hosted PITR workflow

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-vpw`

## Why this exists

Backup and restore drills are already in the repo. Serious self-hosted still needed one explicit point-in-time recovery contract for the Postgres-backed control and telemetry planes.

This document defines that contract. The typed source of truth lives in [pitr.go](../../internal/selfhostedops/pitr.go).

## Requirements

Both the control and telemetry databases require:

- WAL archiving
- regular base backups
- one operator-approved recovery target before restore starts

The default contract uses a 24-hour base-backup interval with recovery by timestamp or named restore point.

## Workflow

1. capture a fresh backup manifest and current self-hosted status
2. choose the recovery target for both Postgres surfaces
3. restore base backup plus WAL into a staging location first
4. run self-hosted preflight and status against the staged restore
5. promote the staged restore and rerun operator smoke checks

## Boundaries

- control and telemetry recover to one approved point in time
- blob, cache, and async systems still need drift checks after the database restore
- PITR does not replace backup verification or rollback-plan capture

## Validation

[pitr_test.go](../../internal/selfhostedops/pitr_test.go) covers:

- a valid default PITR contract
- WAL archiving remaining mandatory for both Postgres surfaces
