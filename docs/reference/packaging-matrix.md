# Urgentry packaging matrix

Status: living document
Last updated: 2026-03-19

## Purpose

Define how Tiny, serious self-hosted, and cloud variants should differ in infrastructure, features, and positioning.

## Packaging philosophy

Do not pretend every SKU is the same product with the same operational promises.

Instead:
- keep a common compatibility core
- expose different infrastructure defaults
- enable features according to what each tier can support honestly

## SKU summary

| SKU | Target user | Infra default | Main promise |
|---|---|---|---|
| Tiny | solo dev / edge / side project | SQLite + Litestream + local FS | one-binary, low-friction error tracking |
| Self-hosted | startups / teams / design partners | PostgreSQL + MinIO + Valkey + NATS | serious Sentry-compatible deployment without Sentry-class complexity |
| Cloud | managed SaaS / larger teams | PostgreSQL + ClickHouse + object storage + Valkey + NATS | richer telemetry with better scale, retention, and economics |

## Capability matrix

Legend:
- **Yes** = included in the intended SKU
- **Limited** = supported in constrained or smaller-scale form
- **No** = not part of the intended SKU promise yet
- **Later** = planned path, not launch baseline

| Capability | Tiny | Self-hosted | Cloud | Notes |
|---|---|---|---|---|
| DSN/project-key compatibility | Yes | Yes | Yes | common core |
| Legacy store ingest | Yes | Yes | Yes | common core |
| Envelope ingest | Yes | Yes | Yes | common core |
| Errors/events | Yes | Yes | Yes | core wedge |
| Issue grouping/lifecycle | Yes | Yes | Yes | core wedge |
| Releases/environments | Yes | Yes | Yes | core wedge |
| Attachments | Limited | Yes | Yes | Tiny should keep limits strict |
| User feedback | Limited | Yes | Yes | common enough to keep early |
| JS source maps | Limited | Yes | Yes | Tiny may need lower limits |
| ProGuard mappings | Limited | Yes | Yes | same as above |
| dSYM/native debug files | No | Later | Later | avoid in first SKU promises |
| Basic alerts | Yes | Yes | Yes | email/webhook first |
| Slack notifications | No | Later | Later | nice-to-have |
| Project/team/member model | Limited | Yes | Yes | Tiny may simplify team model |
| API tokens | Limited | Yes | Yes | can be reduced in Tiny |
| Audit logs | No | Later | Yes | strong cloud/enterprise value |
| Retention controls | Limited | Yes | Yes | Tiny should keep simple knobs |
| Migration/import tooling | Limited | Yes | Yes | Tiny may only support light import |
| Sessions / release health | No | Limited | Yes | strong cloud path |
| Transactions / spans | No | Limited | Yes | self-hosted only after scope proves out |
| OTLP traces | No | Later | Yes | cloud-first expansion |
| OTLP logs | No | No | Later | avoid early promise |
| Log exploration | No | Limited | Yes | high-cardinality pressure lands here |
| Trace exploration | No | Limited | Yes | same |
| Monitors / check-ins | No | Later | Later | useful, but not day-one |
| Replay | No | No | Later | heavy feature |
| Profiles | No | No | Later | heavy feature |
| SSO/SAML/SCIM | No | Limited | Later | partial SCIM `/Users` and `/Groups` CRUD; SSO and SAML remain later |
| Advanced workflow rules | No | Limited | Yes | cloud likely first |

## Infra matrix

| Infra component | Tiny | Self-hosted | Cloud | Notes |
|---|---|---|---|---|
| SQLite | Yes | No | No | Tiny only |
| Litestream | Yes | No | No | Tiny backup story |
| PostgreSQL | No | Yes | Yes | control-plane anchor |
| Timescale | No | Optional | No | bridge path only |
| ClickHouse | No | No by default | Yes | cloud / rich telemetry path |
| local filesystem blobs | Yes | No by default | No | Tiny only |
| MinIO / S3-compatible blobs | Optional | Yes | Yes | required above Tiny |
| Valkey | No by default | Yes | Yes | can be omitted in Tiny |
| NATS JetStream | No by default | Yes | Yes | async spine above Tiny |
| Redpanda | No | No by default | Later | only after proven need |
| OpenSearch | No | No | Later | only if search-heavy use case forces it |

## Honest SKU boundaries

### Tiny should promise
- minimal setup
- local-first deployment
- useful error tracking
- basic releases and alerts

### Tiny should not promise
- serious HA
- full team/enterprise workflows
- rich telemetry analytics
- deep native support
- replay/profiles

### Self-hosted should promise
- credible migration path from Sentry
- serious team/project support
- strong errors-first workflows
- source maps / ProGuard basics
- object storage and async infrastructure handled simply

### Self-hosted should not promise by default
- broad observability parity
- high-cardinality logs platform semantics
- large-tenant cloud economics
- instant parity with every Sentry feature

### Cloud should promise
- strongest compatibility path
- richer telemetry growth path
- better retention and analytics economics
- less operator burden

### Cloud should still be careful not to promise too early
- replay/profile parity
- full search-language parity
- every deep enterprise/governance feature

## Monetization shape implied by the matrix

### Open core / source-available local core
Shared core across SKUs:
- ingest compatibility
- grouping engine
- issue lifecycle
- releases/environments
- artifacts basics
- alerts basics

### Paid expansion levers
Most likely paid levers:
- managed cloud
- higher-scale telemetry
- enterprise auth/governance
- support/SLA
- advanced migration tooling
- advanced alerting and analytics

## Current recommendation

Launch messaging should be organized around:
1. **Tiny** for low-friction local/self use
2. **Self-hosted** as the serious migration wedge
3. **Cloud** as the scale and observability destination

Not around a claim that all SKUs are perfect substitutes for one another.

## Related docs

- `docs/urgentry-final-architecture-decision.md`
- `docs/urgentry-deployment-matrix.md`
- `docs/urgentry-mvp-cut.md`
- `docs/urgentry-compatibility-matrix.md`
