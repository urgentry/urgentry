# Urgentry serious self-hosted support bundle

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-820`

## Why this exists

The operator page already shows local health. Support still needed one portable bundle that can be captured, attached to a ticket, and reviewed without shell access to a live box.

This document defines that bundle. The typed source of truth now lives in [operator_bundle.go](../../internal/store/operator_bundle.go), with the serious self-hosted compatibility wrapper in [support_bundle.go](../../internal/selfhostedops/support_bundle.go).

## Bundle contents

The support bundle captures:

- organization slug
- capture time
- persisted install state
- operator-safe runtime metadata
- service checks
- queue depth
- backfill state
- retention outcomes
- recent audit activity

## Redaction rule

The bundle is redacted by default.

It carries operator-safe runtime metadata, but it does not include secrets, raw credentials, or hidden connection strings.

The redacted bundle is now exposed through `GET /api/0/organizations/{org_slug}/ops/diagnostics/` and linked from `/ops/`.

## Validation

[support_bundle_test.go](../../internal/selfhostedops/support_bundle_test.go) covers:

- copying operator overview data into a portable bundle
- copying persisted install state into the portable bundle
- preserving the capture timestamp
- preserving explicit redaction notes
