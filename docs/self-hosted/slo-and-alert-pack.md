# Urgentry serious self-hosted SLO and alert pack

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-0bn`

## Why this exists

The serious self-hosted bundle already has health checks, operator views, and recovery drills. Operators still needed a simpler answer to a harder question: what does Urgentry promise to watch by default once the install matters?

This document defines that answer. The typed source of truth lives in [slo.go](../../internal/selfhostedops/slo.go).

The operator-facing health and alert rollup now uses [slo_status.go](../../internal/selfhostedops/slo_status.go) and the `/ops/` overview.

## Planes covered

The built-in pack covers five planes:

- control
- async
- cache
- blob
- telemetry

Each plane gets three things:

- a small SLO set
- built-in alert definitions
- a default dashboard pack

## Plane summary

### Control

- focus: auth and control-plane mutation latency plus control-plane error rate
- page when: control API 5xx spikes or control writes slow down badly
- dashboard: auth health, mutation latency, Postgres control saturation

### Async

- focus: backlog age and scheduler lease health
- page when: queue age blows past budget
- dashboard: queue depth, retry rate, lease timeline

### Cache

- focus: quota and lease correctness plus Valkey latency
- page when: quota or lease operations fail
- dashboard: quota decisions, lease operations, Valkey latency

### Blob

- focus: artifact read success and blob write latency
- page when: blob reads start failing in volume
- dashboard: blob latency, restore backlog, blob failures by surface

### Telemetry

- focus: query latency and bridge freshness
- page when: bridge lag breaches budget
- dashboard: query latency, bridge lag, telemetry saturation

## What this bead does not do

This bead does not wire the dashboards into the web UI yet. It does not send alert traffic. It defines the pack that later operator surfaces and eval lanes should expose.

## Validation

[slo_test.go](../../internal/selfhostedops/slo_test.go) covers:

- complete plane coverage
- non-empty objectives, alerts, and dashboard widgets
- validation of alert severity and required fields
