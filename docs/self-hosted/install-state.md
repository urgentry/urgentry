# Urgentry serious self-hosted install state

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-6q6`

## Why this exists

Operators already had health checks, backup plans, and upgrade contracts. They still lacked one persisted install record that survives restarts and says what box they are looking at right now.

This document defines that persisted record. The typed source of truth lives in [lifecycle.go](../../internal/store/lifecycle.go), with SQLite and Postgres implementations in [lifecycle_store.go](../../internal/sqlite/lifecycle_store.go) and [lifecycle_store.go](../../internal/postgrescontrol/lifecycle_store.go).

## Stored fields

The install state now persists:

- install id
- region from `URGENTRY_REGION`
- environment
- deployed version
- whether bootstrap access has completed
- when bootstrap completed
- whether maintenance mode is enabled
- maintenance reason
- when maintenance mode started

Tiny mode stores the record in SQLite. Serious self-hosted stores it in the Postgres control plane.

## Runtime behavior

At startup, Urgentry now syncs the current region, environment, version, and bootstrap-complete state into the lifecycle store. The operator overview reads that state back and shows it on `/ops/`, and the diagnostics bundle includes the same install metadata.

This gives operators one stable install identity across upgrades, restores, and role restarts instead of inferring state from container tags or env files.

## Validation

- [lifecycle_store_test.go](../../internal/sqlite/lifecycle_store_test.go) covers SQLite sync and maintenance-state persistence.
- [lifecycle_store_test.go](../../internal/postgrescontrol/lifecycle_store_test.go) covers the Postgres control-plane implementation.
- [operator_store_test.go](../../internal/sqlite/operator_store_test.go), [operator_test.go](../../internal/api/operator_test.go), and [web_test.go](../../internal/web/web_test.go) cover operator overview and diagnostics export exposure.
