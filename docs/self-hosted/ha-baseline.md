# Serious Self-Hosted HA Baseline

Minimum supported high-availability posture for serious self-hosted deployments.

## Status

**Decided** — 2026-03-31. This is the smallest HA contract Urgentry supports. Operators may exceed it but must not undershoot it.

## Minimum HA Posture by Component

| Component | Minimum HA Baseline | Notes |
|-----------|-------------------|-------|
| **PostgreSQL** | Single primary with WAL archiving | Streaming replica recommended but not required |
| **NATS JetStream** | Single server with file-based persistence | Cluster of 3 recommended for queue durability |
| **Valkey** | Single instance | Cache loss degrades query guard; does not cause data loss |
| **MinIO / S3** | Single instance with local persistence | Production should use managed S3 or multi-node MinIO |
| **Urgentry API** | 2 instances behind load balancer | Active-active; healthz/readyz for routing |
| **Urgentry Ingest** | 2 instances behind load balancer | Active-active; envelope dedup prevents double-processing |
| **Urgentry Worker** | 1 instance | JetStream redelivery handles worker failure |
| **Urgentry Scheduler** | 1 instance | Leader election prevents duplicate scheduling |

## Failure Modes and Recovery

| Failure | Impact | Recovery |
|---------|--------|----------|
| PostgreSQL down | Full outage — all reads/writes fail | Restart or failover to replica |
| NATS down | Ingest accepts but queues locally; workers idle | Restart; pending messages redeliver |
| Valkey down | Query guard degrades (allows all); rate limiting off | Auto-recovery on reconnect |
| MinIO down | Attachment/blob reads fail; ingest unaffected | Restart; data on persistent volume |
| Single API instance down | Load balancer routes to surviving instance | readyz returns 503; auto-reroute |
| Single Ingest instance down | Load balancer routes to surviving instance | readyz returns 503; auto-reroute |
| Worker down | Pipeline backlog grows; events process on restart | JetStream redelivers unacked messages |

## Preflight Enforcement

`ValidateTopology` in selfhostedops rejects serious-mode configurations that:
- Use `URGENTRY_DATA_DIR` without PostgreSQL DSNs
- Run multiple distinct roles without a control-plane DSN
- Use `REPLACE_ME` placeholder secrets

## What This Baseline Does NOT Cover

- Automated failover (requires operator tooling: Patroni, pgBouncer, etc.)
- Cross-region replication
- Zero-downtime upgrades (the upgrade path uses rolling restarts)
- Backup automation (documented in ops guide but not enforced)
