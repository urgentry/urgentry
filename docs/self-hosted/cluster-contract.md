# Urgentry serious self-hosted cluster contract

Status: current serious self-hosted contract
Last updated: 2026-03-30
Bead: `urgentry-pif`

## Why this exists

Serious self-hosted already uses shared runtime primitives. Operators still need one direct answer for cluster correctness: which primitives fail closed, which ones are best effort, and how do you repair them without touching durable truth blindly?

This document defines that answer. The typed source of truth lives in [cluster_contract.go](../../internal/runtimeasync/cluster_contract.go).

## Primitives covered

- ingest quota
- query quota
- leases
- hot caches

## Default posture

- ingest quota, query quota, and leases fail closed
- hot caches are best effort
- Valkey is the shared backend for all four primitives

That keeps the cluster story predictable. Rate limits, query guards, and ownership should not silently split by node. Caches can miss. Quotas and leases cannot.

## Repair rules

- inspect quota windows before clearing them
- inspect holder and TTL before forcing lease handoff
- treat cache repair as key eviction, not truth repair

## Validation

[cluster_contract_test.go](../../internal/runtimeasync/cluster_contract_test.go) covers:

- complete primitive coverage
- fail-closed posture for quotas and leases
- non-empty scope and repair rules
