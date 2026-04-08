# Urgentry serious self-hosted fanout contract

Status: current serious self-hosted contract
Last updated: 2026-03-30
Bead: `urgentry-gpf`

## Why this exists

The telemetry bridge is already live, but the repo still needed one direct answer for larger clusters: how do control-plane and telemetry-plane facts move without drifting apart?

This document defines that answer. The typed source of truth lives in [fanout.go](../../internal/telemetrybridge/fanout.go).

## Default posture

- mode: durable outbox
- delivery: at least once
- lag budget: 120 seconds
- idempotency: one stable logical-event key across retries and rebuild handoff

## Events the fanout has to carry

- accepted events, transactions, and logs
- replay indexing
- profile materialization
- release mutations
- retention restore changes that affect query-serving facts

## Rebuild handoff

The contract also fixes the rebuild path:

1. mark the affected family stale
2. enqueue resumable rebuild work
3. keep the idempotency key stable across retries
4. clear stale state only after the rebuild catches up

That keeps the fanout story consistent with the bridge rebuild story instead of letting each surface improvise.

## Validation

[fanout_test.go](../../internal/telemetrybridge/fanout_test.go) covers:

- the default outbox posture
- delivery and lag requirements
- a non-empty idempotency contract
