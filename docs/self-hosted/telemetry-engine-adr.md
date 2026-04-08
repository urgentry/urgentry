# Urgentry serious self-hosted telemetry engine ADR

Status: current serious self-hosted contract
Last updated: 2026-03-30
Beads: `urgentry-ck6`, `urgentry-7u0`

## Why this exists

The serious self-hosted bridge is shipped. That still leaves an uncomfortable question for operators and future maintainers: when does the bridge stop being the right answer?

This document defines that answer. The typed source of truth lives in [engine_plan.go](../../internal/telemetryquery/engine_plan.go).

This ADR also serves as the explicit bridge-to-OLAP graduation contract for `urgentry-7u0`.

## Default posture

- default tier: PostgreSQL bridge
- optional bridge tier: Timescale-backed bridge
- graduation target: a heavier columnar analytics engine

That keeps the product honest. The bridge is the serious self-hosted baseline, not the forever telemetry answer.

## Graduation triggers

The repo should start planning the heavier engine once one or more of these stop being edge cases:

- retention growth keeps stretching past the bridge comfort window
- mixed interactive query workloads stay slow after normal guard and index tuning
- projector lag and rebuild work keep colliding with normal ingest
- operators have to overprovision Postgres only to keep telemetry queries responsive

## What this bead does not do

This bead does not pick the final engine implementation. It does not migrate telemetry data. It locks the graduation rules so later roadmap work has a stable decision point.

## Validation

[engine_plan_test.go](../../internal/telemetryquery/engine_plan_test.go) covers:

- a valid default progression
- an explicit graduation target
- a concrete trigger set instead of a hand-waved future
