# Urgentry serious self-hosted Kubernetes and Helm path

Status: current operator contract
Last updated: 2026-03-30
Bead: `urgentry-4yv`

## Why this exists

The repo already ships a Compose bundle and a cluster-oriented manifest set. Operators still need one clear answer about the cluster path: what counts as the supported Kubernetes distribution surface, and what does the future Helm path have to include?

This document defines that answer. The typed source of truth lives in [distribution.go](../../internal/selfhostedops/distribution.go).

## Bundles covered

- Compose
- raw Kubernetes manifests
- Helm

Each bundle defines:

- the install guide operators should follow
- the secret source model
- the smoke command
- the upgrade command
- the minimum artifact list

## Current posture

- Compose stays the easiest serious self-hosted install path.
- Raw Kubernetes manifests stay the direct cluster path today.
- Helm is the packaged cluster path the repo has to grow into, not a hand-waved future label.

## Secret model

- Compose: env-file driven secrets
- raw Kubernetes: secret manifests
- Helm: external-secret or secret-manager driven inputs

That split keeps the operator story grounded in the real deployment environments people use.

## What this bead does not do

This bead does not finish a Helm chart. It defines the distribution contract the future chart and cluster docs must satisfy.

## Validation

[distribution_test.go](../../internal/selfhostedops/distribution_test.go) covers:

- complete bundle coverage
- required Helm path coverage
- non-empty install, smoke, upgrade, and artifact fields
