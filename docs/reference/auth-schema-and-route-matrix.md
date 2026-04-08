# Urgentry auth schema and route matrix

Status: current auth source of truth
Last updated: 2026-03-29

## Purpose

Define the concrete credential model, SQLite-first schema, RBAC rules, role-mounted surfaces, and route-to-scope matrix for Urgentry.

This document assumes:
- Tiny mode is the shipped product truth
- project keys are ingest-only
- all runtime roles are real and fully supported

Use this doc as the implementation source of truth for auth and authorization.

## Design decisions

1. Keep ingest auth separate from management auth.
2. Keep user identity local to Urgentry.
3. Keep authorization resource-scoped, never token-exists scoped.
4. Keep Tiny mode SQLite-first, with names that can map cleanly to future Postgres.
5. Keep `api`, `ingest`, `worker`, and `scheduler` honest by mounting only the surfaces each role owns.

## Credential types

### 1. Project keys

Used for:
- `POST /api/{project_id}/store/`
- `POST /api/{project_id}/envelope/`

Rules:
- DSN/public-key style
- public identifier only
- exact path project match required
- never accepted for management REST APIs
- never accepted for web UI

### 2. Project automation tokens

Used for:
- source map / ProGuard / artifact upload
- future project-scoped release creation from CI
- future deploy and release automation

Rules:
- secret bearer tokens
- scoped to a single project
- hashed at rest
- accepted only on explicitly automation-friendly project routes

### 3. User sessions

Used for:
- web UI

Rules:
- local email/password first
- server-side session record in SQLite
- session cookie in browser
- CSRF enforced on mutating UI routes
- org membership drives authorization

### 4. Personal access tokens

Used for:
- management REST APIs
- CLI automation

Rules:
- secret bearer tokens
- tied to a user
- scoped
- hashed at rest
- org/project authorization still checked after scope check

## Logical auth flow

```text
request
  -> identify route kind
     -> ingest route: require project key
     -> automation route: require automation token or user auth
     -> management API: require PAT or session
     -> web UI: require session
  -> resolve resource
     -> path project_id
     -> or project/org slug
     -> or issue_id/event_id -> load owning project/org first
  -> verify credential scope
  -> verify membership / project binding
  -> run handler
```

## SQLite-first schema

These tables are intended for Tiny mode now. They can map directly to future Postgres tables later.

## Identity and membership

### `users`

Fields:
- `id` text pk
- `email` text unique
- `display_name` text
- `is_active` integer
- `created_at` text
- `updated_at` text

### `user_password_credentials`

Fields:
- `user_id` text pk fk users
- `password_hash` text
- `password_algo` text
- `password_updated_at` text

Notes:
- one local password credential per user for the first pass
- future OIDC identities live in a separate table, not here

### `organization_members`

Fields:
- `id` text pk
- `organization_id` text fk organizations
- `user_id` text fk users
- `role` text
- `created_at` text

Constraints:
- unique `(organization_id, user_id)`

Allowed values:
- `owner`
- `admin`
- `member`
- `viewer`

### `project_members`

Status:
- deferred

Purpose:
- future per-project overrides

Do not implement in the first pass unless org-only RBAC proves insufficient.

## Ingest credentials

### `project_keys`

Fields:
- `id` text pk
- `project_id` text fk projects
- `public_key` text unique
- `status` text
- `label` text
- `rate_limit_per_minute` integer nullable
- `created_at` text
- `last_used_at` text nullable

Rules:
- public ingest key only
- no secret half required
- accepted only on ingest routes

Allowed values:
- `active`
- `disabled`
- `revoked`

## Automation credentials

### `project_automation_tokens`

Fields:
- `id` text pk
- `project_id` text fk projects
- `label` text
- `token_prefix` text unique
- `token_hash` text unique
- `scopes_json` text
- `created_by_user_id` text fk users nullable
- `created_at` text
- `last_used_at` text nullable
- `expires_at` text nullable
- `revoked_at` text nullable

Constraints:
- unique `(project_id, label)` optional

Notes:
- token format should expose a lookup prefix and keep the secret portion unpersisted
- automation tokens are never valid outside their bound project

## User auth

### `user_sessions`

Fields:
- `id` text pk
- `user_id` text fk users
- `session_token_hash` text unique
- `csrf_secret` text
- `ip_address` text nullable
- `user_agent` text nullable
- `created_at` text
- `last_seen_at` text
- `expires_at` text
- `revoked_at` text nullable

Notes:
- cookie stores opaque session token, never raw user id
- `csrf_secret` is rotated with the session

### `personal_access_tokens`

Fields:
- `id` text pk
- `user_id` text fk users
- `label` text
- `token_prefix` text unique
- `token_hash` text unique
- `scopes_json` text
- `created_at` text
- `last_used_at` text nullable
- `expires_at` text nullable
- `revoked_at` text nullable

Notes:
- PATs are user credentials, never org-global anonymous credentials
- access is the intersection of token scopes and the user’s org role

## Audit trail

### `auth_audit_logs`

Fields:
- `id` text pk
- `credential_type` text
- `credential_id` text nullable
- `user_id` text nullable
- `project_id` text nullable
- `organization_id` text nullable
- `action` text
- `request_path` text
- `request_method` text
- `ip_address` text nullable
- `user_agent` text nullable
- `created_at` text

Suggested actions:
- `session.created`
- `session.revoked`
- `pat.created`
- `pat.revoked`
- `automation_token.created`
- `automation_token.revoked`
- `project_key.created`
- `project_key.revoked`
- `query.org_issues.allowed`
- `query.org_issues.denied`
- `query.discover.allowed`
- `query.discover.denied`
- `query.logs.allowed`
- `query.logs.denied`
- `query.transactions.allowed`
- `query.transactions.denied`
- `query.replays.allowed`
- `query.replays.denied`
- `query.profiles.allowed`
- `query.profiles.denied`
- `auth.denied`

### `query_guard_policies`

Fields:
- `organization_id` text fk organizations
- `workload` text
- `max_cost_per_request` integer
- `max_requests_per_window` integer
- `max_cost_per_window` integer
- `window_seconds` integer

Constraints:
- unique `(organization_id, workload)`

Purpose:
- org-scoped guardrail policy for discover, logs, replay, profile, trace, and other telemetry-heavy reads

### `query_guard_usage`

Fields:
- `organization_id` text fk organizations
- `workload` text
- `actor_key` text
- `window_start` text
- `request_count` integer
- `cost_units` integer
- `created_at` text
- `updated_at` text

Constraints:
- unique `(organization_id, workload, actor_key, window_start)`

Purpose:
- per-window usage accounting that powers `429` + `Retry-After` responses for expensive read paths

## Scope catalog

Keep scopes explicit and small.

Notes:
- `org:query:read` is intentionally explicit. `org:read` does not imply access to org-wide discover, logs, or issue-query surfaces.
- `org:query:write` is the mutation counterpart for dashboards and other org-wide saved-query assets. It implies `org:query:read`.

### Org scopes

- `org:admin`
- `org:read`
- `org:members:read`
- `org:members:write`
- `org:query:read`
- `org:query:write`
- `org:import:write`
- `org:export:read`

### Team scopes

- `team:read`
- `team:write`

### Project scopes

- `project:read`
- `project:write`
- `project:keys:read`
- `project:keys:write`
- `project:tokens:read`
- `project:tokens:write`
- `project:settings:read`
- `project:settings:write`

### Issue and event scopes

- `issue:read`
- `issue:write`
- `event:read`
- `search:read`
- `search:write`

### Release and artifact scopes

- `release:read`
- `release:write`
- `project:artifacts:write`

### Alert and feedback scopes

- `alert:read`
- `alert:write`
- `feedback:read`

## Scope implications

```text
org:admin -> all routes in the organization
org:members:write -> org:members:read -> org:read
team:write        -> team:read -> org:read
project:keys:write -> project:keys:read -> project:read
project:tokens:write -> project:tokens:read -> project:read
project:settings:write -> project:settings:read -> project:read
issue:write -> issue:read -> project:read
event:read -> project:read
search:write -> search:read -> project:read
release:write -> release:read -> project:read
project:artifacts:write -> project:read
alert:write -> alert:read -> project:read
feedback:read -> project:read
```

## Org role defaults

These are defaults for user sessions and PAT-backed users.

### `owner`

Gets:
- `org:admin`

Notes:
- `org:admin` implies every narrower org, project, release, issue, event, search, and alert scope
- owner-only control-plane operations such as org import/export, audit logs, and durable backfill control rely on this scope in Tiny mode

### `admin`

Gets:
- all non-owner scopes in the organization

Notes:
- `admin` does not get `org:admin`
- owner-only org-admin routes remain unavailable without an owner-scoped session or PAT that explicitly carries `org:admin`

### `member`

Gets:
- `org:read`
- `org:query:read`
- `team:read`
- `project:read`
- `project:settings:read`
- `issue:read`
- `issue:write`
- `event:read`
- `search:read`
- `search:write`
- `release:read`
- `feedback:read`
- `alert:read`

### `viewer`

Gets:
- `org:read`
- `org:query:read`
- `team:read`
- `project:read`
- `project:settings:read`
- `issue:read`
- `event:read`
- `search:read`
- `release:read`
- `feedback:read`
- `alert:read`

## Authorization resolution rules

### Project-key routes

Algorithm:
1. parse key from `X-Sentry-Auth` or `sentry_key`
2. look up `project_keys.public_key`
3. require `status = active`
4. compare looked-up `project_id` with path `{project_id}`
5. deny on mismatch

### Project automation token routes

Algorithm:
1. parse bearer token
2. use prefix to find `project_automation_tokens`
3. verify hash
4. require not expired or revoked
5. require required scope
6. require route project to equal token `project_id`

### PAT and session routes

Algorithm:
1. authenticate user
2. resolve target org/project from path or resource lookup
3. load user organization membership
4. expand role into default scopes
5. intersect with PAT scopes if using PAT
6. require route scope

Notes:
- owner-scoped PATs must still explicitly include `org:admin` before they can hit owner-only control-plane routes
- project automation tokens are never accepted on org-admin routes, even when those routes target one project or release

### Issue and event resource routes

Routes keyed by `{issue_id}` or `{event_id}` must:
1. load the issue/event first
2. derive owning `project_id`
3. derive owning `organization_id`
4. authorize against that resource

Do not authorize these routes by token existence alone.

## Role-mounted surfaces

This is the required routing truth once roles are real.

| Role | Mounted surfaces |
|---|---|
| `all` | system, ingest, management API, web UI, worker loop, scheduler loop |
| `api` | system, management API, web UI |
| `ingest` | system, ingest |
| `worker` | system, worker loop only |
| `scheduler` | system, scheduler loop only |

System surfaces:
- `GET /healthz`
- `GET /readyz`
- `GET /metrics`

Important:
- `api` must not mount `/api/{project_id}/store/` or `/api/{project_id}/envelope/`
- `ingest` must not mount `/api/0/...` or web pages
- `worker` and `scheduler` should still expose health endpoints, but not user-facing surfaces

## Route and scope matrix

## System

| Route | Methods | Accepted credentials | Required scope | Mounted in roles | Notes |
|---|---|---|---|---|---|
| `/healthz` | `GET` | none | none | all roles | public liveness |
| `/readyz` | `GET` | none | none | all roles | public readiness |
| `/metrics` | `GET` | localhost or metrics bearer token | none | all roles | not part of app RBAC |

## Ingest

| Route | Methods | Accepted credentials | Required scope | Mounted in roles | Notes |
|---|---|---|---|---|---|
| `/api/{project_id}/store/` | `POST` | project key | none beyond exact project match | `all`, `ingest` | reject on project mismatch |
| `/api/{project_id}/envelope/` | `POST` | project key | none beyond exact project match | `all`, `ingest` | reject on project mismatch |

## Management REST API

| Route | Methods | Accepted credentials | Required scope | Mounted in roles | Notes |
|---|---|---|---|---|---|
| `/api/0/organizations/` | `GET` | session, PAT | `org:read` | `all`, `api` | list only orgs visible to caller |
| `/api/0/organizations/{org_slug}/` | `GET` | session, PAT | `org:read` | `all`, `api` | authorize against path org |
| `/api/0/organizations/{org_slug}/projects/` | `GET` | session, PAT | `project:read` | `all`, `api` | org-scoped project list only |
| `/api/0/organizations/{org_slug}/teams/` | `GET` | session, PAT | `team:read` | `all`, `api` | org-scoped team list only |
| `/api/0/organizations/{org_slug}/issues/` | `GET` | session, PAT | `org:query:read` | `all`, `api` | cross-project issue query surface; query-cost and quota guarded; supports `filter`, `query`, `environment`, `project`, `sort`, `limit`, and `statsPeriod` |
| `/api/0/organizations/{org_slug}/discover/` | `GET` | session, PAT | `org:query:read` | `all`, `api` | cross-project discover surface; query-cost and quota guarded |
| `/api/0/organizations/{org_slug}/logs/` | `GET` | session, PAT | `org:query:read` | `all`, `api` | cross-project log query surface; query-cost and quota guarded |
| `/api/0/organizations/{org_slug}/dashboards/` | `GET` | session, PAT | `org:query:read` | `all`, `api` | lists org-shared dashboards plus caller-owned private dashboards; org admins can see all |
| `/api/0/organizations/{org_slug}/dashboards/` | `POST` | session, PAT | `org:query:write` | `all`, `api` | creates a dashboard owned by the caller, including persisted refresh cadence, dashboard-level filters, and annotations; viewers may not write even if the token carries the scope |
| `/api/0/organizations/{org_slug}/dashboards/{dashboard_id}/` | `GET` | session, PAT | `org:query:read` | `all`, `api` | loads one dashboard with widgets; private dashboards stay owner/admin-only |
| `/api/0/organizations/{org_slug}/dashboards/{dashboard_id}/` | `PUT` | session, PAT | `org:query:write` | `all`, `api` | updates one dashboard, including sharing, refresh cadence, dashboard-level filters, and annotations; only owner or org admin may mutate |
| `/api/0/organizations/{org_slug}/dashboards/{dashboard_id}/` | `DELETE` | session, PAT | `org:query:write` | `all`, `api` | deletes one dashboard plus its widget rows |
| `/api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/` | `POST` | session, PAT | `org:query:write` | `all`, `api` | creates a widget backed by Discover AST or a caller-owned saved search, with optional stat-threshold config |
| `/api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/` | `PUT` | session, PAT | `org:query:write` | `all`, `api` | updates one widget, including optional stat-threshold config; stored queries stay AST-backed, never raw SQL |
| `/api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/` | `DELETE` | session, PAT | `org:query:write` | `all`, `api` | deletes one widget row |
| `/api/0/organizations/{org_slug}/releases/` | `GET` | session, PAT | `release:read` | `all`, `api` | org-scoped releases only |
| `/api/0/organizations/{org_slug}/releases/` | `POST` | session, PAT | `release:write` | `all`, `api` | current path is org-scoped, so project automation tokens are intentionally not accepted |
| `/api/0/organizations/{org_slug}/backfills/` | `GET` | session, PAT | `org:admin` | `all`, `api` | owner-only operator view for durable backfill and reprocess runs |
| `/api/0/organizations/{org_slug}/backfills/` | `POST` | session, PAT | `org:admin` | `all`, `api` | owner-only durable backfill launch; request must be bounded by project, release, or time range, and returns `409` when an overlapping active run already owns that scope |
| `/api/0/organizations/{org_slug}/backfills/{run_id}/` | `GET` | session, PAT | `org:admin` | `all`, `api` | owner-only polling surface for status, counters, cursor, and last error |
| `/api/0/organizations/{org_slug}/backfills/{run_id}/cancel/` | `POST` | session, PAT | `org:admin` | `all`, `api` | owner-only cancel for pending runs; running work must finish its current claimed step |
| `/api/0/projects/` | `GET` | session, PAT | `project:read` | `all`, `api` | list only visible projects |
| `/api/0/projects/{org_slug}/{proj_slug}/` | `GET` | session, PAT | `project:read` | `all`, `api` | project-scoped |
| `/api/0/projects/{org_slug}/{proj_slug}/` | `PUT` | session, PAT | `project:write` | `all`, `api` | root project metadata update surface; accepts Sentry-style fields and applies persisted project metadata |
| `/api/0/users/me/personal-access-tokens/` | `GET` | session | `org:read` | `all`, `api` | session only; lists redacted PAT metadata for the current user |
| `/api/0/users/me/personal-access-tokens/` | `POST` | session | `org:read` | `all`, `api` | session only; CSRF required; returns raw PAT once |
| `/api/0/users/me/personal-access-tokens/{token_id}/` | `DELETE` | session | `org:read` | `all`, `api` | session only; CSRF required; revokes caller-owned PAT |
| `/api/0/projects/{org_slug}/{proj_slug}/keys/` | `GET` | session, PAT | `project:keys:read` | `all`, `api` | never project key |
| `/api/0/projects/{org_slug}/{proj_slug}/keys/` | `POST` | session, PAT | `project:keys:write` | `all`, `api` | never automation token |
| `/api/0/projects/{org_slug}/{proj_slug}/automation-tokens/` | `GET` | session | `project:tokens:read` | `all`, `api` | session only; lists redacted token metadata for the bound project |
| `/api/0/projects/{org_slug}/{proj_slug}/automation-tokens/` | `POST` | session | `project:tokens:write` | `all`, `api` | session only; CSRF required; returns raw automation token once |
| `/api/0/projects/{org_slug}/{proj_slug}/automation-tokens/{token_id}/` | `DELETE` | session | `project:tokens:write` | `all`, `api` | session only; CSRF required; revokes a token bound to the path project |
| `/api/0/projects/{org_slug}/{proj_slug}/ownership/` | `GET` | session, PAT | `project:read` | `all`, `api` | project-scoped ownership rules |
| `/api/0/projects/{org_slug}/{proj_slug}/ownership/` | `POST` | session, PAT | `project:write` | `all`, `api` | create ownership rule |
| `/api/0/projects/{org_slug}/{proj_slug}/ownership/` | `PUT` | session, PAT | `project:write` | `all`, `api` | Sentry-compatible alias for ownership rule creation |
| `/api/0/projects/{org_slug}/{proj_slug}/ownership/{rule_id}/` | `DELETE` | session, PAT | `project:write` | `all`, `api` | delete ownership rule |
| `/api/0/projects/{org_slug}/{proj_slug}/issues/` | `GET` | session, PAT | `issue:read` | `all`, `api` | must filter by exact project |
| `/api/0/projects/{org_slug}/{proj_slug}/events/` | `GET` | session, PAT | `event:read` | `all`, `api` | must filter by exact project |
| `/api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/` | `GET` | session, PAT | `event:read` | `all`, `api` | event must belong to exact project |
| `/api/0/projects/{org_slug}/{proj_slug}/transactions/` | `GET` | session, PAT | `project:read` | `all`, `api` | project-scoped telemetry query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/traces/{trace_id}/` | `GET` | session, PAT | `project:read` | `all`, `api` | project-scoped trace detail; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/replays/` | `GET` | session, PAT | `project:read` | `all`, `api` | project-scoped replay query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/` | `GET` | session, PAT | `project:read` | `all`, `api` | replay detail; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/` | `GET` | session, PAT | `project:read` | `all`, `api` | project-scoped profile query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/{profile_id}/` | `GET` | session, PAT | `project:read` | `all`, `api` | profile detail; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/top-down/` | `GET` | session, PAT | `project:read` | `all`, `api` | normalized call-tree query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/bottom-up/` | `GET` | session, PAT | `project:read` | `all`, `api` | normalized hotspot query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/flamegraph/` | `GET` | session, PAT | `project:read` | `all`, `api` | render-oriented profile tree query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/hot-path/` | `GET` | session, PAT | `project:read` | `all`, `api` | dominant-path profile query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/profiles/compare/` | `GET` | session, PAT | `project:read` | `all`, `api` | two-profile comparison query; query-cost and quota guarded |
| `/api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/archives/` | `GET` | session, PAT | `project:read` | `all`, `api` | list recent archive rows for one telemetry surface; operator-visible retention audit surface |
| `/api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/archive/` | `POST` | session, PAT | `project:write` | `all`, `api` | execute one retention archive/delete pass for the requested surface and return recent archive rows |
| `/api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/restore/` | `POST` | session, PAT | `project:write` | `all`, `api` | restore recent archive rows for the requested surface and return updated archive state |
| `/api/0/projects/{org_slug}/{proj_slug}/attachments/` | `POST` | session, PAT, automation token | `project:artifacts:write` | `all`, `api` | multipart standalone event attachment upload; `event_id` must belong to the exact project |
| `/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/` | `GET` | session, PAT | `project:read` | `all`, `api` | list stored debug-file metadata for one release |
| `/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/` | `POST` | session, PAT, automation token | `project:artifacts:write` | `all`, `api` | upload symbol sources only; does not rewrite historical events inline |
| `/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/` | `GET` | session, PAT | `project:read` | `all`, `api` | download one stored debug file |
| `/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/reprocess/` | `POST` | session, PAT | `org:admin` | `all`, `api` | owner-only historical native reprocess trigger; returns `202` with a durable `native_reprocess` run keyed to the exact debug file, or `409` when a broader or matching native reprocess already owns that release scope |
| `/api/0/teams/{org_slug}/{team_slug}/projects/` | `POST` | session, PAT | `project:write` | `all`, `api` | org/team membership required |
| `/api/0/issues/{issue_id}/` | `GET` | session, PAT | `issue:read` | `all`, `api` | load issue -> derive project/org -> authorize |
| `/api/0/issues/{issue_id}/` | `PUT` | session, PAT | `issue:write` | `all`, `api` | load issue -> derive project/org -> authorize |
| `/api/0/issues/{issue_id}/events/` | `GET` | session, PAT | `event:read` | `all`, `api` | load issue first |
| `/api/0/issues/{issue_id}/events/latest/` | `GET` | session, PAT | `event:read` | `all`, `api` | load issue first |
| `/api/0/organizations/{org_slug}/import/` | `POST` | session, PAT | `org:import:write` | `all`, `api` | no project keys, no automation tokens; supports `?dry_run=1` for checksum-aware validation without persistence |
| `/api/0/organizations/{org_slug}/export/` | `GET` | session, PAT | `org:export:read` | `all`, `api` | org-scoped export with blob-backed artifact bodies plus checksum metadata |
| `/api/0/projects/{org_slug}/{proj_slug}/releases/{version}/files/` | `POST` | session, PAT, automation token | `project:artifacts:write` | `all`, `api` | exact project binding required |

## Web UI

Web UI accepts sessions only.

| Route | Methods | Accepted credentials | Required scope | Mounted in roles | Notes |
|---|---|---|---|---|---|
| `/` | `GET` | session | `issue:read` | `all`, `api` | dashboard |
| `/discover/` | `GET` | session | `org:query:read` | `all`, `api` | org-wide discover surface; query-cost and quota guarded, with in-page explain and denial feedback |
| `/discover/starters/{slug}/` | `GET` | session | `org:query:read` | `all`, `api` | opinionated discover starter view for slow endpoints and failing endpoints; query-cost and quota guarded |
| `/discover/save-query` | `POST` | session | `org:query:read` | `all`, `api` | persists a caller-owned saved Discover query in the caller's org with private or org-shared visibility; CSRF required |
| `/discover/queries/{id}/` | `GET` | session | `org:query:read` | `all`, `api` | saved-query detail page with metadata, live preview, explain output, and `?export=csv|json` support |
| `/discover/queries/{id}/favorite` | `POST` | session | `org:query:read` | `all`, `api` | toggles the caller's favorite state for a visible saved query; CSRF required |
| `/discover/queries/{id}/update` | `POST` | session | `search:write` | `all`, `api` | owner-only saved-query metadata and tag update; CSRF required |
| `/discover/queries/{id}/clone` | `POST` | session | `org:query:read` | `all`, `api` | clones a visible saved query into a caller-owned saved query; CSRF required |
| `/discover/queries/{id}/delete` | `POST` | session | `search:write` | `all`, `api` | owner-only saved-query delete; CSRF required |
| `/discover/queries/{id}/reports` | `POST` | session | `org:query:read` | `all`, `api` | creates a caller-owned scheduled report for a visible saved query; CSRF required |
| `/dashboards/` | `GET` | session | `org:query:read` | `all`, `api` | dashboard list for caller-visible org dashboards |
| `/dashboards/` | `POST` | session | `org:query:write` | `all`, `api` | creates a dashboard in the caller's default org; CSRF required |
| `/dashboards/{id}/` | `GET` | session | `org:query:read` | `all`, `api` | dashboard detail with widget execution, saved-query reuse, dashboard-level filters and annotations, auto-refresh, and widget export links |
| `/dashboards/{id}/widgets/{widget_id}/` | `GET` | session | `org:query:read` | `all`, `api` | widget drilldown page with source contract, effective filters, query explain, live result, export links, and snapshot action |
| `/dashboards/{id}/widgets/{widget_id}/export` | `GET` | session | `org:query:read` | `all`, `api` | exports one rendered widget result as CSV or JSON via `?format=csv|json` |
| `/dashboards/{id}/update` | `POST` | session | `org:query:write` | `all`, `api` | updates dashboard metadata, sharing, refresh cadence, dashboard-level filters, and annotations; CSRF required |
| `/dashboards/{id}/duplicate` | `POST` | session | `org:query:write` | `all`, `api` | creates a private copy for the caller; CSRF required |
| `/dashboards/{id}/delete` | `POST` | session | `org:query:write` | `all`, `api` | deletes a dashboard and its widgets; CSRF required |
| `/dashboards/starter/{slug}/create` | `POST` | session | `org:query:write` | `all`, `api` | creates a starter dashboard pack with prebuilt widgets; CSRF required |
| `/dashboards/{id}/widgets` | `POST` | session | `org:query:write` | `all`, `api` | creates a widget from a Discover AST or saved query, with optional stat thresholds; CSRF required |
| `/dashboards/{id}/widgets/{widget_id}/snapshot` | `POST` | session | `org:query:read` | `all`, `api` | freezes the effective widget result, including dashboard-level filters, into a shareable snapshot; CSRF required |
| `/dashboards/{id}/widgets/{widget_id}/reports` | `POST` | session | `org:query:read` | `all`, `api` | creates a caller-owned scheduled report for that widget drilldown source; CSRF required |
| `/dashboards/{id}/widgets/{widget_id}/delete` | `POST` | session | `org:query:write` | `all`, `api` | deletes one widget; CSRF required |
| `/discover/queries/{id}/snapshot` | `POST` | session | `org:query:read` | `all`, `api` | freezes a saved-query result into a shareable snapshot; CSRF required |
| `/discover/queries/{id}/reports/{report_id}/delete` | `POST` | session | `org:query:read` | `all`, `api` | deletes one caller-owned scheduled report for that saved query; CSRF required |
| `/dashboards/{id}/widgets/{widget_id}/reports/{report_id}/delete` | `POST` | session | `org:query:read` | `all`, `api` | deletes one caller-owned scheduled report for that widget; CSRF required |
| `/analytics/snapshots/{token}/` | `GET` | public link | none | `all`, `api` | renders a frozen snapshot without requiring a live session |
| `/logs/` | `GET` | session | `org:query:read` | `all`, `api` | org-wide log surface; query-cost and quota guarded; supports `?export=csv|json` |
| `/logs/starters/{slug}/` | `GET` | session | `org:query:read` | `all`, `api` | opinionated logs starter view for noisy loggers; query-cost and quota guarded |
| `/issues/` | `GET` | session | `issue:read` | `all`, `api` | issue list |
| `/issues/{id}/` | `GET` | session | `issue:read` | `all`, `api` | load issue -> derive project/org |
| `/issues/{id}/status` | `POST` | session | `issue:write` | `all`, `api` | CSRF required |
| `/issues/{id}/assign` | `POST` | session | `issue:write` | `all`, `api` | CSRF required |
| `/issues/{id}/priority` | `POST` | session | `issue:write` | `all`, `api` | CSRF required |
| `/events/{id}/` | `GET` | session | `event:read` | `all`, `api` | load event -> derive project/org |
| `/alerts/` | `GET` | session | `alert:read` | `all`, `api` | alert UI with persisted delivery history; notifications may carry related profile summaries |
| `/feedback/` | `GET` | session | `feedback:read` | `all`, `api` | project/org-scoped only |
| `/releases/` | `GET` | session | `release:read` | `all`, `api` | project/org-scoped only |
| `/replays/` | `GET` | session | `project:read` | `all`, `api` | project-scoped replay query; query-cost and quota guarded |
| `/replays/{id}/` | `GET` | session | `project:read` | `all`, `api` | replay detail; query-cost and quota guarded |
| `/profiles/` | `GET` | session | `project:read` | `all`, `api` | project-scoped profile query; query-cost and quota guarded |
| `/profiles/{id}/` | `GET` | session | `project:read` | `all`, `api` | profile detail; query-cost and quota guarded |
| `/traces/{trace_id}/` | `GET` | session | `project:read` | `all`, `api` | trace detail page with related profiles and stored transactions/spans; query-cost and quota guarded |
| `/settings/` | `GET` | session | `project:settings:read` | `all`, `api` | later split by org/project settings sections |
| `/api/search` | `GET` | session | `search:read` | `all`, `api` | UI helper endpoint |
| `/api/ui/searches` | `POST` | session | `search:write` | `all`, `api` | web saved-search create with private or org-shared visibility in the caller's default org |
| `/api/ui/searches/{id}` | `GET` | session | `search:read` | `all`, `api` | web saved-search detail with reusable open/detail URLs |
| `/api/ui/searches/{id}` | `PUT` | session | `search:write` | `all`, `api` | owner-only saved-search update with metadata, tags, and favorite state |
| `/api/ui/searches/clone/{id}` | `POST` | session | `search:write` | `all`, `api` | clones a visible saved search into a caller-owned saved search |
| `/api/ui/searches/{id}` | `DELETE` | session | `search:write` | `all`, `api` | web saved search delete |

## Current code changes implied by this matrix

1. Replace the current universal REST auth in `internal/api/api.go` with PAT/session auth.
2. Tighten ingest auth in `internal/auth/middleware.go` to enforce exact project match.
3. Make all project-scoped API queries actually scope to the requested project.
4. Make issue- and event-id routes resolve ownership before authorization.
5. Mount routes by runtime role instead of mounting everything on every non-worker role.
6. Remove use of project keys from management REST APIs.
7. Keep automation tokens off org-wide routes.
8. Keep org-wide query scope explicit and guarded by per-request cost limits plus per-window usage quotas.
9. Add a future project-scoped release-write route before allowing CI release creation via automation token.

Operational note:
- serious self-hosted schema rollout is intentionally outside this HTTP route matrix; operators use the binary-native `urgentry self-hosted preflight|status|migrate-control|migrate-telemetry|rollback-plan` commands instead of API routes when advancing or rolling back control-plane and telemetry schemas

## Migration path

### Phase 1

- keep current `project_keys`
- add `users`
- add `user_password_credentials`
- add `organization_members`
- add `user_sessions`
- add `personal_access_tokens`
- add `project_automation_tokens`

### Phase 2

- move REST API from universal bearer token checks to PAT/session checks
- move ingest auth from key-exists to key-and-project-match
- add audit logging for auth-sensitive actions

### Phase 3

- add local login/logout/session endpoints
- add token management UI/API
- add future OIDC identities without changing route auth rules

## Explicit non-goals

- Better Auth or any Node-based auth runtime in the server path
- making project keys valid for read APIs
- project-level role overrides in the first pass
- SSO in the first pass
