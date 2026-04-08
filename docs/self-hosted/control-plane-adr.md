# Urgentry serious self-hosted control-plane ADR

Status: accepted
Last updated: 2026-03-31
Bead: `urgentry-5ew.1.1`

## Decision

Serious self-hosted Urgentry uses PostgreSQL as the authoritative control-plane store. Tiny mode remains SQLite-first and keeps its current single-binary workflow, but no serious-mode code may assume:

- a local `URGENTRY_DATA_DIR`
- SQLite-specific DDL or query behavior
- SQLite-backed runtime leases or durable jobs
- direct construction of `sqlite.*Store` values in handlers

SQLite-backed role topologies are Tiny-only. They are not a supported serious self-hosted deployment shape under another name.

The control plane is the transactional truth for:

- organizations, teams, users, membership, invites
- project metadata, ingest keys, automation tokens, PATs, sessions, auth audit logs
- issue workflow state, canonical issue rows, comments, activity, bookmarks, subscriptions, ownership rules
- releases, deploys, release-project bindings, suspect links, release-health rollups
- monitors, alert rules, notification destinations, backfill definitions, retention policies, quotas, dashboards, saved queries, operator audit state

Large immutable bodies stay out of the control plane. Telemetry-serving facts are rebuildable and move to the telemetry bridge or later analytics plane. Tiny mode preserves the shipped SQLite product surface and import/export path.

## Supported topology boundary

Urgentry supports these storage shapes:

| Mode | Supported | Notes |
|---|---|---|
| Tiny | yes | single binary, SQLite-first, local or simple blob backend |
| Serious self-hosted with PostgreSQL control plane and shared backends | yes | the supported serious self-hosted baseline |
| Serious self-hosted roles sharing one SQLite data directory | no | not a supported operator topology |
| Serious self-hosted roles using SQLite as a fallback control plane | no | not a supported operator topology |

This line is deliberate. Tiny and serious self-hosted have different operational promises. The repo keeps both products, but it does not blur their storage contracts.

## Why this ADR exists

Today the runtime still assumes Tiny mode in core seams:

- [run.go](../../internal/app/run.go) always opens SQLite and builds SQLite stores for every role
- [server.go](../../internal/http/server.go) requires `*sql.DB` and constructs SQLite-backed ingest, API, and web dependencies directly
- `internal/api`, `internal/web`, and `internal/auth` still consume concrete SQLite stores in many places even when they already speak higher-level contracts
- import/export and bootstrap flows still treat SQLite as the only authoritative control-plane persistence shape

Without a control-plane ADR, later Postgres, JetStream, and telemetry work would keep growing around SQLite-local assumptions and make rollback harder.

## Authoritative data boundary

### Control-plane tables

These table families are authoritative in serious self-hosted:

- tenancy: `organizations`, `teams`, `users`, `organization_members`, `team_members`, `member_invites`
- auth and credentials: `projects`, `project_keys`, `project_automation_tokens`, `user_password_credentials`, `user_sessions`, `personal_access_tokens`, `auth_audit_logs`
- issue workflow: `groups`, `issue_comments`, `issue_activity`, `issue_bookmarks`, `issue_subscriptions`, `ownership_rules`
- releases and monitors: `releases`, `release_deploys`, `release_commits`, `release_sessions`, `monitors`, `monitor_checkins`
- alerts and operator state: `alert_rules`, `alert_history`, `notification_outbox`, `notification_deliveries`, `backfill_runs`, `telemetry_retention_policies`, `telemetry_archives`, `query_guard_policies`, `saved_searches`, `dashboards`, `dashboard_widgets`
- native control state: `native_symbol_sources`, `native_crashes`, `native_crash_images`
- import and migration state: import runs, export runs, migration checkpoints, restore checkpoints

### Not authoritative here

The control plane does not own:

- attachment bytes, source maps, ProGuard mappings, native debug files, replay assets, raw profile payloads, raw envelopes
- append-heavy telemetry facts used for Discover, logs, transactions, spans, outcomes, replay panes, and profile aggregations
- ephemeral multi-node coordination such as rate-limit counters, idempotency windows, hot caches, or singleton runtime leases

## Shared contracts before code movement

The serious-mode control-plane cutover must target explicit backend-neutral seams:

| Concern | Current Tiny pressure point | Required shared seam |
|---|---|---|
| bootstrap and runtime wiring | [run.go](../../internal/app/run.go) | runtime store bundle selected by mode |
| HTTP composition | [server.go](../../internal/http/server.go) | API, web, and ingest deps expressed via contracts instead of `sqlite.*Store` |
| auth and tokens | `internal/auth`, `internal/api/tokens.go` | backend-neutral auth store + token manager |
| control-plane reads | `internal/web`, `internal/api`, `internal/sqlite/web_store.go` | shared read-store contracts for settings, alerts, issues, releases, dashboards, monitors, admin state |
| control-plane writes | `internal/sqlite/catalog_store.go`, `internal/sqlite/admin_store.go`, `internal/sqlite/auth_store.go`, `internal/sqlite/group_store.go`, `internal/sqlite/release_store.go`, `internal/sqlite/monitor_store.go` | backend-neutral write-store contracts with parity tests |
| import/export | `internal/api/import_export.go`, `internal/sqlite/import_export_store.go` | control-plane snapshot contract independent of SQLite row shape |

The next implementation beads should introduce a serious-mode store bundle instead of adding Postgres calls beside existing SQLite constructors.

The first serious-mode migration scaffold now lives in [migrations.go](../../internal/postgrescontrol/migrations.go).
The shared runtime and handler seam now lives in the split control-plane surface under [catalog.go](../../internal/controlplane/catalog.go), [admin.go](../../internal/controlplane/admin.go), [issues.go](../../internal/controlplane/issues.go), [releases.go](../../internal/controlplane/releases.go), [monitors.go](../../internal/controlplane/monitors.go), [services.go](../../internal/controlplane/services.go), and [sqlite.go](../../internal/controlplane/sqlite.go), with HTTP composition routing Tiny mode through that contract boundary in [server.go](../../internal/http/server.go).

## Tiny assumptions that must be removed

These are the specific Tiny-mode assumptions that later beads must eliminate or confine:

1. SQLite is opened unconditionally in the app runtime.
2. Blob storage is derived from the local data dir.
3. Durable jobs and runtime leases share the primary relational database.
4. API and web handlers can depend on `*sql.DB` and SQLite-specific helpers.
5. Bootstrap org, PAT, and ingest-key creation is tied to SQLite startup.
6. Import/export uses SQLite schema shape as the migration contract instead of a product-level snapshot contract.

## Validation rules this ADR locks

Implementation beads must satisfy all of these:

1. Serious self-hosted preflight rejects role topologies that depend on one shared SQLite data directory. **Enforced**: `ValidateTopology` in [ops.go](../../internal/selfhostedops/ops.go) rejects multi-role or explicit serious-mode configurations that lack `URGENTRY_CONTROL_DATABASE_URL` or `URGENTRY_TELEMETRY_DATABASE_URL`, and rejects `URGENTRY_DATA_DIR` as a control-plane source when no PostgreSQL DSN is configured. SQLite is Tiny-only.
2. Serious self-hosted docs and deploy assets name PostgreSQL as the control-plane requirement and do not advertise shared SQLite as a supported variant.
3. API, ingest, worker, and scheduler roles do not require `URGENTRY_DATA_DIR` as their control-plane source of truth in serious self-hosted mode.
4. Tiny mode keeps the SQLite product path and remains the only supported SQLite deployment mode.
5. Upgrade and migration docs describe the Tiny-to-serious handoff as a mode transition, not a multi-role SQLite expansion.

## Cutover order

The safe control-plane cutover sequence is:

1. Land this ADR plus the async and telemetry bridge ADRs.
2. Add serious-mode configuration without changing Tiny defaults.
3. Add PostgreSQL migrations for authoritative control-plane tables.
4. Port control-plane stores behind shared contracts while keeping SQLite and Postgres implementations side by side.
5. Add a dual-backend control-plane compatibility harness for auth, settings, workflow, releases, alerts, monitors, and dashboards.
6. Move runtime wiring to select Tiny or serious-mode store bundles at startup.
7. Keep Tiny import/export as the migration anchor into the serious control plane.
8. Only after parity is proven, move serious-mode async execution and blob custody off SQLite-local assumptions.

## Rollback points

Every control-plane cutover step must remain reversible:

- after migrations: PostgreSQL schema is additive and not yet traffic-serving
- after shared contracts: SQLite remains the default runtime implementation
- after dual-backend harness: serious mode is still non-default until parity passes
- after runtime selection: Tiny stays the supported default and remains the rollback target

If serious-mode control-plane writes regress:

- freeze serious-mode traffic
- retain PostgreSQL snapshots
- continue serving Tiny deployments unchanged
- keep blob and telemetry rebuild work blocked until control-plane parity is restored

## Original non-goals

This bead originally did not:

- ship Postgres-backed runtime code yet
- change Tiny mode behavior
- move jobs, caches, or blob storage off their Tiny implementations
- define the telemetry bridge query contract in detail

Those belong to:

- `urgentry-5ew.1.2`
- `urgentry-5ew.1.3`
- `urgentry-5ew.3.*`
- `urgentry-5ew.4.*`

## Cutover checklist

Before `urgentry-5ew.1.2` starts:

- authoritative table families are named
- Tiny-only assumptions are explicitly listed
- runtime, HTTP, auth, API, web, and import/export seams are identified
- rollback points are defined

Before `urgentry-5ew.1.3` closes:

- both backends pass the same control-plane characterization suite
- handlers no longer construct SQLite stores directly for control-plane work
- Tiny mode remains the default runtime path

Implementation note:

- Tiny-mode handler wiring now depends on `internal/controlplane.Services` and the split per-surface control-plane interfaces instead of constructing control-plane SQLite stores inline in `internal/api`, `internal/web`, and `internal/http`
- the shipped dual-backend proof now lives in [controlplane_harness_test.go](../../internal/postgrescontrol/controlplane_harness_test.go), which runs one shared API and authenticated web suite against SQLite and PostgreSQL-backed implementations and compares the observable results
- that harness already drove follow-on fixes in [settings.go](../../internal/web/settings.go), [alerts.go](../../internal/web/alerts.go), and [issues.go](../../internal/api/issues.go) so control-plane reads and mutation responses do not silently fall back to stale SQLite-only state
- serious self-hosted runtime selection now lands in [control_runtime.go](../../internal/app/control_runtime.go) and [run.go](../../internal/app/run.go): when `URGENTRY_CONTROL_DATABASE_URL` is configured, API, ingest, worker, and scheduler roles open and migrate the Postgres control plane, bootstrap keys plus owner credentials there, and inject Postgres-backed auth/catalog/admin/issues/ownership/releases/alerts/monitors services into the HTTP and pipeline runtime while Tiny mode keeps the SQLite bundle unchanged
- serious self-hosted issue query surfaces now land through the same contract boundary: [issue_read_store.go](../../internal/postgrescontrol/issue_read_store.go) hydrates canonical issue rows from Postgres for project issue list, issue detail, org issue search, and the web Discover issue table, while SQLite is used only as a candidate-ID filter when issue queries depend on event-derived search tokens like `release:` or `environment:`

Before serious mode is user-visible:

- upgrade and rollback procedures exist
- import/export snapshot format is treated as the migration anchor
- operator docs explain the boundary between Postgres, object storage, Valkey, JetStream, and the telemetry bridge

## Related docs

- [urgentry-final-architecture-decision.md](urgentry-final-architecture-decision.md)
- [urgentry-post-tiny-analytics-adr.md](urgentry-post-tiny-analytics-adr.md)
- [urgentry-validation-and-performance-gates-adr.md](../architecture/validation-and-performance-gates-adr.md)
- [roadmap](roadmap.md)

Current schema scaffold:

- [migrations.go](../../internal/postgrescontrol/migrations.go)
