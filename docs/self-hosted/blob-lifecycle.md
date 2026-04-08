# Urgentry serious self-hosted blob lifecycle

Status: current serious self-hosted contract
Last updated: 2026-03-30
Bead: `urgentry-zke`

## Why this exists

Serious self-hosted already archives and restores data. Operators still needed one direct answer for larger installs: which blob surfaces can move to colder storage, which ones must stay hot, and what integrity proof has to exist before data comes back into service?

This document defines that answer. The typed source of truth lives in [lifecycle.go](../../internal/blob/lifecycle.go).

## Surfaces covered

- attachments
- replay assets
- raw profiles
- debug artifacts

## Default posture

- attachments, replay assets, and raw profiles can move through warm and cold storage as long as restore and integrity checks stay explicit
- debug artifacts stay hot by default until cold-archive symbolication is proven safe
- every surface requires an integrity proof before restored data is served again

## Validation

[lifecycle_test.go](../../internal/blob/lifecycle_test.go) covers:

- complete surface coverage
- required restore and integrity fields
- the hot-storage default for debug artifacts
