# Urgentry serious self-hosted scale gate

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-rok`

## Why this exists

The serious self-hosted bundle already has smoke scripts, drills, performance lanes, and a scorecard. Operators still need one short answer to a release question: which checks have to pass before Urgentry can claim a stronger multi-node self-hosted story?

This document defines that answer. The typed source of truth lives in [scale_gate.go](../../internal/selfhostedops/scale_gate.go).

## Checks in the gate

- backup and restore
- rolling upgrade
- split-role failover
- sustained node-churn soak
- quota isolation
- bridge rebuild recovery
- Kubernetes smoke

Each check fixes three things:

- the command that runs it
- the artifact operators should inspect
- the success bar that has to stay true

## Why this matters

This keeps the self-hosted claim honest. A future release should not be able to say “multi-node is stronger now” unless the backup, failover, soak, quota, rebuild, and cluster smoke checks all still pass together.

## Validation

[scale_gate_test.go](../../internal/selfhostedops/scale_gate_test.go) covers:

- full gate coverage
- required upgrade, failover, and soak checks
- non-empty command, artifact, and success-bar fields
