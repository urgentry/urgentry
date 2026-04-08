# Urgentry schema starter spec

Status: starter schema spec
Last updated: 2026-03-19

## Purpose

Provide the first-pass data model for the serious self-hosted MVP using:
- PostgreSQL
- MinIO / object storage
- Valkey
- NATS JetStream

This spec is intentionally MVP-first and Postgres-first.

## Scope

This spec covers:
- core Postgres tables
- object-storage buckets
- JetStream streams and subjects
- Valkey key families

It does **not** fully specify:
- ClickHouse tables
- Timescale hypertables
- replay/profile storage
- full native symbolication schema

## Data model principles

1. control-plane truth stays in Postgres
2. raw payloads live in object storage
3. async work flows through JetStream
4. ephemeral coordination belongs in Valkey, not Postgres
5. event tables should preserve a future export/dual-write seam to ClickHouse

## PostgreSQL schema areas

## A. Identity and tenancy

### `organizations`
Fields:
- `id` uuid pk
- `slug` text unique
- `name` text
- `plan` text
- `created_at` timestamptz
- `updated_at` timestamptz

### `teams`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `slug` text
- `name` text
- `created_at` timestamptz
- `updated_at` timestamptz

Constraint ideas:
- unique `(organization_id, slug)`

### `users`
Fields:
- `id` uuid pk
- `email` citext unique
- `display_name` text
- `is_active` boolean
- `created_at` timestamptz
- `updated_at` timestamptz

### `organization_members`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `user_id` uuid fk
- `role` text
- `created_at` timestamptz

Constraint ideas:
- unique `(organization_id, user_id)`

## B. Projects and auth

Detailed auth source of truth:
- `docs/urgentry-auth-schema-and-route-matrix.md`

Use that document for:
- credential separation
- route-to-scope authorization rules
- role-mounted surface rules
- SQLite-first auth table definitions

### `projects`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `team_id` uuid fk nullable
- `slug` text
- `name` text
- `platform` text nullable
- `status` text
- `default_environment` text nullable
- `created_at` timestamptz
- `updated_at` timestamptz

Constraint ideas:
- unique `(organization_id, slug)`

### `project_keys`
Fields:
- `id` uuid pk
- `project_id` uuid fk
- `public_key` text unique
- `status` text
- `label` text
- `rate_limit_per_minute` integer nullable
- `created_at` timestamptz
- `last_used_at` timestamptz nullable

Notes:
- public ingest keys only
- not used for management REST APIs

### `project_automation_tokens`
Fields:
- `id` uuid pk
- `project_id` uuid fk
- `label` text
- `token_prefix` text unique
- `token_hash` text unique
- `scopes` jsonb
- `created_by_user_id` uuid fk nullable
- `created_at` timestamptz
- `last_used_at` timestamptz nullable
- `expires_at` timestamptz nullable
- `revoked_at` timestamptz nullable

### `user_password_credentials`
Fields:
- `user_id` uuid pk fk
- `password_hash` text
- `password_algo` text
- `password_updated_at` timestamptz

### `user_sessions`
Fields:
- `id` uuid pk
- `user_id` uuid fk
- `session_token_hash` text unique
- `csrf_secret` text
- `created_at` timestamptz
- `last_seen_at` timestamptz
- `expires_at` timestamptz
- `revoked_at` timestamptz nullable

### `personal_access_tokens`
Fields:
- `id` uuid pk
- `user_id` uuid fk
- `label` text
- `token_prefix` text unique
- `token_hash` text unique
- `scopes` jsonb
- `created_at` timestamptz
- `last_used_at` timestamptz nullable
- `expires_at` timestamptz nullable
- `revoked_at` timestamptz nullable

## C. Releases and deploys

### `releases`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `version` text
- `ref` text nullable
- `url` text nullable
- `date_released` timestamptz nullable
- `created_at` timestamptz

Constraint ideas:
- unique `(organization_id, version)`

### `release_projects`
Fields:
- `id` uuid pk
- `release_id` uuid fk
- `project_id` uuid fk
- `new_groups` integer default 0
- `resolved_groups` integer default 0

Constraint ideas:
- unique `(release_id, project_id)`

### `deploys`
Fields:
- `id` uuid pk
- `release_id` uuid fk
- `environment` text
- `name` text nullable
- `url` text nullable
- `date_started` timestamptz nullable
- `date_finished` timestamptz nullable
- `created_at` timestamptz

## D. Events and issues

### `events`
Purpose:
- MVP event store in Postgres
- future export seam to ClickHouse

Fields:
- `id` uuid pk
- `project_id` uuid fk
- `event_id` text
- `group_id` uuid nullable
- `release_id` uuid nullable
- `environment` text nullable
- `platform` text nullable
- `level` text nullable
- `transaction_name` text nullable
- `event_type` text
- `occurred_at` timestamptz
- `ingested_at` timestamptz
- `message` text nullable
- `title` text nullable
- `culprit` text nullable
- `fingerprint` jsonb nullable
- `tags` jsonb
- `contexts` jsonb
- `user_data` jsonb
- `request_data` jsonb
- `sdk_data` jsonb
- `search_text` text nullable
- `payload_object_key` text
- `normalized_json` jsonb

Constraint ideas:
- unique `(project_id, event_id)`

Partitioning suggestion:
- range partition by `occurred_at` day or week

Index ideas:
- `(project_id, occurred_at desc)`
- `(group_id, occurred_at desc)`
- `(project_id, release_id, occurred_at desc)`
- `(project_id, environment, occurred_at desc)`
- GIN on `tags`
- GIN/trgm on `search_text` if needed

### `event_attachments`
Fields:
- `id` uuid pk
- `event_id` uuid fk
- `name` text
- `content_type` text nullable
- `size_bytes` bigint
- `object_key` text
- `created_at` timestamptz

### `groups`
Purpose:
- core issue/group entity

Fields:
- `id` uuid pk
- `project_id` uuid fk
- `grouping_version` text
- `grouping_key` text
- `title` text
- `culprit` text nullable
- `level` text nullable
- `status` text
- `substatus` text nullable
- `first_seen` timestamptz
- `last_seen` timestamptz
- `times_seen` bigint
- `last_event_id` uuid nullable
- `assignee_user_id` uuid nullable
- `assignee_team_id` uuid nullable
- `created_at` timestamptz
- `updated_at` timestamptz

Constraint ideas:
- unique `(project_id, grouping_version, grouping_key)`

### `group_occurrences`
Fields:
- `id` uuid pk
- `group_id` uuid fk
- `event_id` uuid fk
- `occurred_at` timestamptz

Constraint ideas:
- unique `(group_id, event_id)`

### `group_states`
Fields:
- `id` uuid pk
- `group_id` uuid fk unique
- `is_resolved` boolean
- `is_ignored` boolean
- `is_muted` boolean
- `resolved_at` timestamptz nullable
- `resolved_by_user_id` uuid nullable
- `updated_at` timestamptz

## E. Feedback and alerts

### `user_feedback`
Fields:
- `id` uuid pk
- `project_id` uuid fk
- `event_id` uuid fk nullable
- `group_id` uuid fk nullable
- `name` text nullable
- `email` text nullable
- `comments` text
- `created_at` timestamptz

### `alert_rules`
Fields:
- `id` uuid pk
- `project_id` uuid fk
- `name` text
- `rule_type` text
- `status` text
- `config_json` jsonb
- `created_at` timestamptz
- `updated_at` timestamptz

### `notification_destinations`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `kind` text
- `name` text
- `config_json` jsonb
- `created_at` timestamptz
- `updated_at` timestamptz

## F. Artifacts and debug files

### `artifacts`
Fields:
- `id` uuid pk
- `project_id` uuid fk
- `release_id` uuid fk nullable
- `kind` text
- `name` text
- `dist` text nullable
- `headers_json` jsonb nullable
- `object_key` text
- `size_bytes` bigint
- `checksum` text nullable
- `created_at` timestamptz

### `debug_files`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `project_id` uuid fk nullable
- `debug_id` text nullable
- `code_id` text nullable
- `kind` text
- `object_key` text
- `size_bytes` bigint
- `checksum` text nullable
- `created_at` timestamptz

Note:
- this generic table is sufficient for Tiny and early artifact upload flows
- post-Tiny native parity should add dedicated native crash, image-catalog, and stackwalk job tables rather than overloading `debug_files` with per-crash resolution state
- see `docs/urgentry-native-artifact-stackwalking-adr.md`

## G. Import/export and audit

### `imports`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `source_kind` text
- `status` text
- `metadata_json` jsonb
- `archive_object_key` text nullable
- `created_at` timestamptz
- `updated_at` timestamptz

### `exports`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `status` text
- `metadata_json` jsonb
- `archive_object_key` text nullable
- `created_at` timestamptz
- `updated_at` timestamptz

### `audit_logs`
Fields:
- `id` uuid pk
- `organization_id` uuid fk
- `actor_user_id` uuid nullable
- `action` text
- `target_type` text
- `target_id` text
- `metadata_json` jsonb
- `created_at` timestamptz

## Object storage layout

Recommended buckets:
- `raw-envelopes`
- `attachments`
- `source-maps`
- `debug-files`
- `imports`
- `exports`

Recommended object key shapes:
- `raw-envelopes/{project_id}/{yyyy}/{mm}/{dd}/{event_id-or-envelope_id}.json.gz`
- `attachments/{project_id}/{event_uuid}/{attachment_id}/{filename}`
- `source-maps/{project_id}/{release_version}/{artifact_id}`
- `debug-files/{organization_id}/{debug_file_id}`
- `imports/{organization_id}/{import_id}.tar.gz`
- `exports/{organization_id}/{export_id}.tar.gz`

## JetStream design

## Streams

### `INGEST`
Purpose:
- raw ingest work after payload persistence

Subjects:
- `ingest.envelope.received`
- `ingest.store.received`
- `ingest.feedback.received`

### `NORMALIZE`
Purpose:
- normalization fanout

Subjects:
- `normalize.error`
- `normalize.attachment`
- `normalize.feedback`

### `ISSUES`
Purpose:
- grouping and issue updates

Subjects:
- `issues.group`
- `issues.update`
- `issues.regression`

### `ALERTS`
Purpose:
- alert evaluation and delivery

Subjects:
- `alerts.evaluate`
- `alerts.notify.email`
- `alerts.notify.webhook`

### `ARTIFACTS`
Purpose:
- source-map and artifact processing

Subjects:
- `artifacts.process.sourcemap`
- `artifacts.process.proguard`

## Consumer guidance

Need durable consumers for:
- normalize worker
- issue worker
- alert worker
- artifact worker

Need DLQ strategy per concern.

Suggested DLQ subjects:
- `dlq.normalize`
- `dlq.issues`
- `dlq.alerts`
- `dlq.artifacts`

## Valkey key families

### Rate limits
- `ratelimit:project:{project_key}:{window}`

### Idempotency
- `idem:event:{project_id}:{event_id}`
- `idem:attachment:{event_uuid}:{attachment_name}`

### Project config cache
- `projectcfg:{project_key}`

### Small query cache
- `query:group-summary:{group_id}`
- `query:release-summary:{project_id}:{release_id}`

### Import/export short-term state
- `import:{import_id}:progress`
- `export:{export_id}:progress`

## Future ClickHouse seam

When ClickHouse is introduced later, the MVP Postgres event model should map cleanly to future tables such as:
- `events_errors`
- `group_occurrences`
- `sessions`
- `transactions`
- `spans`
- `logs`

To preserve that path now:
- keep `event_type` explicit
- keep `occurred_at` and `ingested_at` explicit
- keep raw normalized payloads available
- extract common filter columns early instead of hiding everything in one blob

## Migration priorities

Schema migration order should be:
1. tenancy and projects
2. project keys and tokens
3. releases
4. events partitions
5. groups and occurrences
6. artifacts and feedback
7. alerts and notifications
8. imports/exports/audit

## MVP warning

Do not let the Postgres event schema become an excuse to rebuild a full analytics warehouse in the MVP.

The goal is:
- enough event persistence for serious self-hosted error tracking
- enough extracted fields for common issue workflows
- a clean future export path to ClickHouse

## Related docs

- `docs/urgentry-mvp-cut.md`
- `docs/urgentry-repo-bootstrap-plan.md`
- `docs/urgentry-final-architecture-decision.md`
