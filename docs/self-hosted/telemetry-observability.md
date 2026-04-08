# Urgentry serious self-hosted telemetry observability

Status: current serious self-hosted contract
Last updated: 2026-03-30
Bead: `urgentry-1lv`

## Why this exists

The telemetry engine ADR explains when the bridge stops being the right answer. Operators still need a concrete way to see that pressure building in real time.

This document defines that contract. The typed source of truth lives in [observability.go](../../internal/telemetryquery/observability.go).

## What it measures

Bridge observability is tracked per query-facing surface:

- discover
- logs
- traces
- replay
- profile

Each surface carries two budgets:

- lag budgets
- daily cost budgets

## Decision model

Each observation produces one assessment:

- `ok`
- `warn`
- `page`

The assessment pages when lag or projected daily cost crosses the highest threshold for that surface. It warns before that. This keeps the bridge honest as a serious self-hosted baseline instead of treating cost and rebuild lag as hidden operator trivia.

## Relationship to other telemetry contracts

- [telemetry-engine-adr](telemetry-engine-adr.md) defines when the bridge should graduate
- [fanout-contract](fanout-contract.md) defines the lag budget and delivery expectations for projector fanout

## Validation

[observability_test.go](../../internal/telemetryquery/observability_test.go) covers:

- a valid default budget pack
- warning when lag crosses the warning threshold
- paging when projected daily cost crosses the page threshold
