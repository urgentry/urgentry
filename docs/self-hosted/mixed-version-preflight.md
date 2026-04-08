# Urgentry serious self-hosted mixed-version preflight

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-8d6`

## Why this exists

The upgrade contract defined the allowed skew. Operators still needed one preflight that can look at a real cluster and say yes or no before a canary rollout proceeds.

This document defines that preflight. The typed source of truth lives in [compatibility.go](../../internal/selfhostedops/compatibility.go).

## Decision model

The mixed-version preflight checks:

- the app-bundle range across API and ingest nodes
- control schema compatibility with that app-bundle range
- telemetry schema compatibility with that app-bundle range
- worker compatibility with the current app-bundle range
- scheduler compatibility with the current app-bundle range

## Default rules

- API and ingest nodes may differ by one release at most
- control and telemetry schemas may be one release ahead of the oldest app-bundle version, but never behind the newest app-bundle version
- workers and schedulers may lag the newest app-bundle version by one release at most
- workers and schedulers may never run ahead of the newest app-bundle version

## Validation

[compatibility_test.go](../../internal/selfhostedops/compatibility_test.go) covers:

- a valid one-release canary cluster
- rejection when schemas move too far ahead
- rejection when workers move ahead of the live app bundle
