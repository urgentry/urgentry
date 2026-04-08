# Urgentry serious self-hosted next-step roadmap

Status: complete historical roadmap
Last updated: 2026-03-30
Root epic: `urgentry-hzp`

## Purpose

This roadmap captured the work needed after the first serious self-hosted preview shipped.

The goal was not to add more Tiny-mode surface area. The goal was to make the split-role path concrete enough to operate, reason about, and extend without hand-waving:

- clearer operator SLOs and alerting
- a real query execution contract
- repair and recovery tooling boundaries
- scale and packaging gates
- explicit upgrade, cluster, and blob-lifecycle rules
- a written bridge from the current Postgres-first bridge to a future telemetry engine

## Shipped tasks

- `urgentry-0bn`
  - serious self-hosted SLO and alert pack
  - doc: [slo-and-alert-pack](slo-and-alert-pack.md)
- `urgentry-25r`
  - query execution contract
  - doc: [query-execution-contract](query-execution-contract.md)
- `urgentry-m3w`
  - repair tooling contract
  - doc: [repair-tools](repair-tools.md)
- `urgentry-rok`
  - scale gate contract
  - doc: [scale-gate](scale-gate.md)
- `urgentry-4yv`
  - Kubernetes and Helm distribution contract
  - doc: [kubernetes-and-helm](kubernetes-and-helm.md)
- `urgentry-ck6`
  - telemetry engine ADR
  - doc: [telemetry-engine-adr](telemetry-engine-adr.md)
- `urgentry-gpf`
  - fanout contract
  - doc: [fanout-contract](fanout-contract.md)
- `urgentry-jj2`
  - upgrade contract
  - doc: [upgrade-contract](upgrade-contract.md)
- `urgentry-pif`
  - cluster contract
  - doc: [cluster-contract](cluster-contract.md)
- `urgentry-zke`
  - large-scale blob and archive lifecycle contract
  - doc: [blob-lifecycle](blob-lifecycle.md)

## Outcome

The serious self-hosted preview now has a complete operator contract pack for the next layer of depth:

- how operators measure health
- how queries are allowed to execute
- how repairs, upgrades, and cluster changes are supposed to work
- how larger-scale blobs and archives move through retention tiers
- when the current telemetry bridge stops being enough

That makes the next round of work narrower and more honest. Future epics can now build on explicit contracts instead of mixing product work with unresolved operator assumptions.

## Follow-on work

The next open serious self-hosted programs build on this completed roadmap:

- `urgentry-k98` recovery and upgrade depth
- `urgentry-j2k` long-running multi-node correctness
- `urgentry-0z2` supportability and fleet diagnostics
- `urgentry-54o` control-plane lifecycle controls
- `urgentry-4np` Kubernetes operator reality
- `urgentry-9eb` telemetry backend graduation

## Exit note

This roadmap is complete. It should stay in the repo as the historical handoff between the first serious self-hosted preview and the deeper operator programs that follow it.
