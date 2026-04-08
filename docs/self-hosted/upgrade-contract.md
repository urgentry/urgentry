# Urgentry serious self-hosted upgrade contract

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-jj2`

## Why this exists

The repo already has upgrade scripts and rollback plans. Operators still needed one clear answer about version skew: which components can lag, how far, and when rollback is still safe?

This document defines that answer. The typed source of truth lives in [upgrade_contract.go](../../internal/selfhostedops/upgrade_contract.go).

## Components covered

- app bundle
- control schema
- telemetry schema
- worker
- scheduler

## Default rules

- schemas can move one version ahead of the app only in the expand phase
- the app, workers, and scheduler may lag one release during canary
- contract cleanup waits until rollback proof exists

## Canary flow

1. expand schemas
2. roll one canary node
3. verify queue, bridge, and query health
4. roll the rest of the region
5. prove rollback before contract cleanup

## Rollback safeguards

- capture rollback artifacts before the first schema change
- keep app rollback safe only while schemas remain backward compatible
- require explicit restore proof before any schema restore

## Validation

[upgrade_contract_test.go](../../internal/selfhostedops/upgrade_contract_test.go) covers:

- complete component coverage
- non-empty canary and rollback safeguards
- explicit app-bundle skew rules
