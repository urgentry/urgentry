# Urgentry Operations Guide

Status: current shipped runtime
Last updated: 2026-04-02

## Runtime truth

Urgentry currently ships as a SQLite-first single binary with real runtime roles:

- `all`
- `api`
- `ingest`
- `worker`
- `scheduler`

Tiny mode is the product truth today. External Postgres, Valkey, NATS, and ClickHouse are not required for the default runtime. The repo now also ships a serious self-hosted operator path that boots Postgres, MinIO, Valkey, and NATS, moves async execution onto JetStream, serves Discover/logs/traces/replay/profile reads through the telemetry bridge when `URGENTRY_TELEMETRY_DATABASE_URL` is configured, shares ingest quotas and query guard windows through Valkey, and keeps the remaining live app state on one mounted SQLite volume until the broader control-plane cutover finishes.

Before a public Tiny-mode release or design-partner handoff, run [urgentry-tiny-launch-gate.md](../tiny/launch-gate.md). It combines build, smoke, auth, retention, replay, profile, analytics, and artifact checks into one repeatable launch command.

Serious self-hosted upgrade, backup, and rollback tooling is exposed through the main binary as `urgentry self-hosted ...` plus deployment bundles under [deploy/compose/](../deploy/compose) and [deploy/k8s/](../deploy/k8s).

Current deployment truth for that bundle:

- Postgres, MinIO, Valkey, and NATS boot as the serious self-hosted substrate
- Urgentry boots as split `api`, `ingest`, `worker`, and `scheduler` roles
- those four roles use JetStream for durable worker plus backfill execution, Valkey for quotas and scheduler leases, route query-heavy telemetry reads through the bridge-backed logical query service when the telemetry database is configured, and still share the remaining SQLite runtime state through one mounted data volume
- MinIO is already live for blob custody through `URGENTRY_BLOB_BACKEND=s3`

That bundle is intentionally honest about today’s code: it gives operators a real split-role Compose boot path and smoke coverage, plus a live single-node Kubernetes smoke path for the committed bundle, without claiming the full serious-mode runtime cutover is already finished.

## Build and start

```bash
cd .
make build
./urgentry serve --role=all
```

First boot on an empty database creates:

- organization `urgentry-org`
- project `default-project`
- a public ingest key
- a bootstrap owner user
- a bootstrap personal access token

The server logs the bootstrap email, password, PAT, and public ingest key once.

## Storage

SQLite lives in:

- `~/.urgentry/urgentry.db`

Override with:

- `URGENTRY_DATA_DIR`

Local blobs live under the same data dir in `blobs/` by default.

Optional S3-compatible blob settings:

- `URGENTRY_BLOB_BACKEND=s3`
- `URGENTRY_S3_ENDPOINT`
- `URGENTRY_S3_BUCKET`
- `URGENTRY_S3_REGION`
- `URGENTRY_S3_ACCESS_KEY`
- `URGENTRY_S3_SECRET_KEY`
- `URGENTRY_S3_PREFIX`
- `URGENTRY_S3_USE_TLS=true|false`

That keeps SQLite local for Tiny mode while moving large artifact bodies to MinIO or another S3-compatible service.

Blob backend selection:

- `URGENTRY_BLOB_BACKEND=file` keeps the shipped Tiny-mode filesystem behavior
- `URGENTRY_BLOB_BACKEND=s3` switches blob reads and writes to the configured S3-compatible target
- required S3 settings are `URGENTRY_S3_ENDPOINT`, `URGENTRY_S3_BUCKET`, `URGENTRY_S3_ACCESS_KEY`, and `URGENTRY_S3_SECRET_KEY`
- optional S3 settings are `URGENTRY_S3_REGION`, `URGENTRY_S3_PREFIX`, and `URGENTRY_S3_USE_TLS`

Urgentry enables WAL mode and uses a single SQLite writer connection.

## Serious self-hosted deployment bundles

The full bundle guide is:

- [urgentry-serious-self-hosted-deployment-guide.md](../self-hosted/deployment-guide.md)

### Compose

```bash
cd deploy/compose
cp -f .env.example .env
docker compose up -d --build
```

Smoke it:

```bash
cd .
bash deploy/compose/smoke.sh up
```

That smoke flow verifies the split role bundle, storage services, backend selection reported by `/healthz`, ingest-to-worker event flow, and attachment roundtrip against the MinIO blob path.

Rotate the shipped serious self-hosted secret set with:

```bash
cd .
bash deploy/rotate-secrets.sh compose --env-file deploy/compose/.env
bash deploy/rotate-secrets.sh k8s --secret-file deploy/k8s/secret.yaml
```

Each run rewrites the target file in place, writes a timestamped `.bak` copy beside it, and saves the generated values to a JSON summary file so operators can move them into a real secret store.
The Compose path uses `URGENTRY_SELF_HOSTED_PROJECT` or `COMPOSE_PROJECT_NAME` from the env file so rotation and smoke checks target the same live stack.

### Cluster-oriented path

The Kubernetes bundle now ships as one pod per Urgentry role:

- `urgentry-api`
- `urgentry-ingest`
- `urgentry-worker`
- `urgentry-scheduler`

Those pods share the serious self-hosted backends through PostgreSQL, MinIO, Valkey, and NATS. The bundle no longer hides the role split inside one multi-container pod.
They also mount one shared `urgentry-data` PVC because the shipped serious self-hosted runtime still keeps SQLite-backed event state under `URGENTRY_DATA_DIR`.

That means the cluster path currently requires a storage class that can satisfy a `ReadWriteMany` PVC with working file locks for SQLite.

Render or apply it:

```bash
kubectl kustomize deploy/k8s
kubectl apply -k deploy/k8s
```

Before `kubectl apply -k`, replace the placeholder values in [secret.yaml](../deploy/k8s/secret.yaml) or overlay a real `urgentry-secret`.

Smoke it against a live cluster:

```bash
bash deploy/k8s/smoke.sh up
```

That smoke path is still available as manual operator tooling. The dedicated readiness gate now also boots a disposable `kind` cluster, patches the bundle with real eval secrets plus a local-cluster shared data volume, and runs this live Kubernetes smoke automatically.

The serious self-hosted async and disaster-recovery drills live under `deploy/compose/drills.sh` and cover JetStream redelivery, Valkey-backed scheduler lease handoff, split-role worker and scheduler restart behavior, Valkey outage behavior, and full backup/restore recovery.

The dedicated serious self-hosted eval lanes are:

```bash
cd .
make selfhosted-bench
make selfhosted-eval
make selfhosted-sentry-baseline
```

Those commands require Docker Compose, `kubectl`, and `kind` on the operator host. They boot the split-role Compose bundle, run the operator drills, boot a disposable single-node `kind` cluster for the live Kubernetes smoke, run the serious self-hosted perf lane, and write:

- readiness scorecards under `eval/reports/selfhosted/`
- perf artifacts under `eval/reports/selfhosted-performance/`
- raw result JSON under `eval/reports/results/selfhosted*.json` and `eval/reports/results/selfhostedperf.*.json`

The self-hosted compose smoke and perf lanes now retry transient first-boot failures automatically. The perf lane also reports median steady-ingest and query samples by default so small self-hosted deltas are less affected by one noisy host run.

Current serious self-hosted perf budgets:

- ingest p50 `<= 150 ms`
- ingest p99 `<= 1000 ms`
- throughput `>= 250 eps`
- idle heap `<= 384 MB`
- bridge query p95 `<= 350 ms`
- steady-state soak throughput `>= 200 eps`
- steady-state soak error rate `<= 1%`
- post-soak query p95 `<= 500 ms`
- telemetry rebuild `<= 30 s`
- HA churn soak `PASS` with both API and async roles restarted under live traffic

The soak phase now boots secondary API, worker, and scheduler replicas, churns both primary and secondary roles while traffic stays live, and then probes both API nodes before the lane passes.

The perf lane now also writes a capacity summary under `eval/reports/selfhosted-performance/capacity-summary.{json,md}` with the measured steady and soak envelopes, the recorded HA churn actions, and a conservative recommended steady-state ingest ceiling derived from the observed runs.

For live secret rotation, enter maintenance mode first. Then use:

```bash
cd .
bash deploy/rotate-secrets.sh compose --env-file deploy/compose/.env
bash deploy/rotate-secrets.sh k8s --secret-file deploy/k8s/secret.yaml --namespace urgentry-system --apply
```

The Compose mode updates the env file, alters the Postgres user password in place, rotates the live bootstrap password plus PAT in the control plane, recreates MinIO plus the four Urgentry roles, reruns `security-report`, reruns `smoke.sh check`, and records `secret.rotate` in the operator ledger. The Kubernetes mode rewrites the secret manifest and, with `--apply`, alters the Postgres user password, applies the secret, restarts MinIO plus the four Urgentry Deployments, and then rotates the live bootstrap password plus PAT through the restarted API pod.
Use `--no-restart` only for offline staging. In Compose it rewrites the env file without touching the live stack. In Kubernetes it is only valid without `--apply`, so operators can stage a manifest update before the maintenance window.

## Serious self-hosted upgrade workflow

Use the binary-native operator commands when validating or advancing Postgres-backed serious self-hosted environments.

1. Capture or verify a backup set before changing schemas:

```bash
cd .
bash deploy/compose/backup.sh /tmp/urgentry-backup
go run ./cmd/urgentry self-hosted verify-backup --dir /tmp/urgentry-backup
bash deploy/compose/restore.sh --verify-only /tmp/urgentry-backup
```

2. Run preflight against both databases:

```bash
cd .
bash deploy/compose/ops.sh preflight
bash deploy/compose/ops.sh security-report
go run ./cmd/urgentry self-hosted preflight \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" \
  --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL" \
  --telemetry-backend "${URGENTRY_TELEMETRY_BACKEND:-postgres}"
go run ./cmd/urgentry self-hosted security-report \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" \
  --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL"
go run ./cmd/urgentry self-hosted enter-maintenance \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" \
  --reason "upgrade window"
go run ./cmd/urgentry self-hosted record-action \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" \
  --action secret.rotate \
  --detail "rotated metrics token"
```

3. Apply control-plane migrations:

```bash
cd .
go run ./cmd/urgentry self-hosted migrate-control --dsn "$URGENTRY_CONTROL_DATABASE_URL"
```

4. Apply telemetry-bridge migrations:

```bash
cd .
go run ./cmd/urgentry self-hosted migrate-telemetry \
  --dsn "$URGENTRY_TELEMETRY_DATABASE_URL" \
  --telemetry-backend "${URGENTRY_TELEMETRY_BACKEND:-postgres}"
```

5. Verify applied versus target versions:

```bash
cd .
go run ./cmd/urgentry self-hosted status \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" \
  --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL" \
  --telemetry-backend "${URGENTRY_TELEMETRY_BACKEND:-postgres}"
```

6. Before any downgrade or image rollback, print and follow the rollback plan:

```bash
cd .
go run ./cmd/urgentry self-hosted rollback-plan \
  --current-control-version 3 \
  --target-control-version 2 \
  --current-telemetry-version 4 \
  --target-telemetry-version 3 \
  --telemetry-backend "${URGENTRY_TELEMETRY_BACKEND:-postgres}"
go run ./cmd/urgentry self-hosted maintenance-status \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
go run ./cmd/urgentry self-hosted leave-maintenance \
  --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
```

The Compose helpers mirror the same sequence:

```bash
cd .
bash deploy/compose/ops.sh preflight
bash deploy/compose/ops.sh status
bash deploy/compose/ops.sh security-report
bash deploy/compose/ops.sh enter-maintenance "upgrade window"
bash deploy/compose/ops.sh backup-plan
bash deploy/compose/backup.sh /tmp/urgentry-backup
bash deploy/compose/ops.sh verify-backup /tmp/urgentry-backup
bash deploy/compose/restore.sh /tmp/urgentry-backup
URGENTRY_SELF_HOSTED_BACKUP_DIR=/tmp/urgentry-backup bash deploy/compose/upgrade.sh
URGENTRY_SELF_HOSTED_SKIP_UPGRADE_BACKUP=true bash deploy/compose/upgrade.sh
bash deploy/compose/drills.sh backup-restore
bash deploy/compose/drills.sh active-active
bash deploy/compose/drills.sh role-restart
bash deploy/compose/ops.sh rollback-plan 3 2 4 3
bash deploy/compose/ops.sh maintenance-status
bash deploy/compose/ops.sh leave-maintenance
bash deploy/compose/rollback-plan.sh 3 2 4 3
```

The current serious self-hosted backup contract is:

- `urgentry self-hosted backup-plan` prints the required artifact set for the shipped bundle.
- `deploy/compose/backup.sh` captures the Postgres logical dump plus the shared runtime, MinIO, NATS, and Valkey volumes from the running Compose project, then writes `manifest.json` with SHA-256 and byte-count integrity entries for each captured file.
- `urgentry self-hosted verify-backup --dir <backup-dir>` verifies the captured artifact set against that manifest before an operator upgrades or restores.
- `urgentry self-hosted security-report` validates bootstrap password, bootstrap PAT, metrics token, Postgres password, and MinIO root password so placeholder operator secrets fail closed instead of only being documented as unsafe.
- `urgentry self-hosted enter-maintenance`, `maintenance-status`, and `leave-maintenance` provide the shipped write-freeze and drain workflow before operators touch schemas or restart roles, and the enter plus leave flows now write install-ledger rows.
- `urgentry self-hosted record-action` is the shipped manual path for actions such as secret rotation until every operator workflow has its own dedicated command.
- `deploy/compose/ops.sh` wraps the common preflight, status, maintenance, security, backup-plan, verify-backup, rollback-plan, and manual `record-action` commands so operators can run them against the Compose env without manually rewriting in-cluster DSNs.
- `deploy/compose/restore.sh --verify-only <backup-dir>` runs the same verification gate without dropping volumes or databases.
- `deploy/compose/restore.sh` refuses to continue unless backup verification passes, then restores those artifacts onto a fresh stack, reruns `preflight` and `status`, then leaves the roles ready for smoke validation.
- `deploy/compose/backup.sh`, `deploy/compose/restore.sh`, `deploy/compose/upgrade.sh`, and `deploy/compose/ops.sh rollback-plan` now also write install-ledger entries so `/ops/` exposes the operator trail next to the runtime health view.
- the `/ops/` install ledger now renders attached metadata JSON instead of hiding it from the operator surface.
- `deploy/compose/drills.sh backup-restore` proves the restore point by recovering a pre-backup event and attachment, draining a queued JetStream backlog item after restore, and discarding a post-backup event that was created after the capture.
- `deploy/compose/drills.sh active-active` boots a second live API node against the same serious self-hosted stack, seeds fresh event, log, transaction, and Discover traffic, and verifies both API nodes return the same newly written reads.
- `deploy/compose/drills.sh role-restart` proves the current split-role restart boundary by recovering worker backlog after a stopped worker and resuming a pending telemetry rebuild only after the scheduler returns.

Current expectations:

- RPO is bounded by the latest completed backup capture.
- RTO is one cold restore plus `preflight`, `status`, and `smoke.sh check`.

The rollback plan is intentionally restore-based. Operators should capture a fresh backup set, drain API/ingest/worker/scheduler roles, restore the matching control and telemetry artifacts, then redeploy the image that expects those schema versions before rerunning preflight and smoke checks.

See [urgentry-serious-self-hosted-maintenance-mode.md](./urgentry-serious-self-hosted-maintenance-mode.md) for the full maintenance and drain sequence.

## Auth model

### Project keys

Used only for:

- `POST /api/{project_id}/store/`
- `POST /api/{project_id}/envelope/`

Rules:

- exact project match required
- not valid for management REST APIs
- not valid for web UI

### User sessions

Used for:

- web UI

Rules:

- local email/password login
- opaque session cookie
- CSRF enforced on mutating UI routes

### Personal access tokens

Used for:

- management REST APIs under `/api/0/`

Rules:

- bearer token
- scoped
- tied to a local user
- filtered by org membership role
- org-wide query routes require explicit `org:query:read`; `org:read` alone is not enough
- mint and revoke through `/api/0/users/me/personal-access-tokens/` using a logged-in session plus CSRF token

### Project automation tokens

Used for:

- project-scoped automation routes such as source map, ProGuard, and native debug-file uploads

Rules:

- bearer token
- bound to one project
- never valid for general management APIs
- never valid for org-admin historical reprocess or backfill routes
- mint and revoke through `/api/0/projects/{org_slug}/{proj_slug}/automation-tokens/` using a logged-in session plus CSRF token

## Default routes

### Public system routes

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`

`/metrics` is localhost-only unless `URGENTRY_METRICS_TOKEN` is set and supplied as a bearer token.

When `URGENTRY_PROFILING_ENABLED=true`, Urgentry also mounts:

- `GET /debug/pprof/`
- `GET /debug/pprof/profile`
- `GET /debug/pprof/trace`
- `GET /debug/fgprof`

Profiling routes are localhost-only when `URGENTRY_PROFILING_TOKEN` is unset; when it is set, every request must supply it as a bearer token.

The metrics snapshot includes ingest counters, pipeline queue/drop counts, processing latency buckets, and alert dispatch backpressure counters (`dispatch_queued`, `dispatch_dropped`).

HTTP server timeout knobs:

- `URGENTRY_HTTP_READ_HEADER_TIMEOUT` — default `5s`
- `URGENTRY_HTTP_READ_TIMEOUT` — default `30s`
- `URGENTRY_HTTP_WRITE_TIMEOUT` — default `30s`
- `URGENTRY_HTTP_IDLE_TIMEOUT` — default `60s`

Profiling knobs:

- `URGENTRY_PROFILING_ENABLED` — default `false`
- `URGENTRY_PROFILING_TOKEN` — bearer token for remote profiling-route access

### Web routes

- `GET /login/`
- `POST /login/`
- `POST /logout`
- `GET /`
- `GET /discover/`
- `POST /discover/save-query`
- `GET /dashboards/`
- `GET /dashboards/{id}/`
- `GET /dashboards/{id}/widgets/{widget_id}/`
- `POST /dashboards/`
- `POST /dashboards/starter/{slug}/create`
- `POST /dashboards/{id}/update`
- `POST /dashboards/{id}/duplicate`
- `POST /dashboards/{id}/delete`
- `POST /dashboards/{id}/widgets`
- `POST /dashboards/{id}/widgets/{widget_id}/snapshot`
- `POST /dashboards/{id}/widgets/{widget_id}/delete`
- `POST /discover/queries/{id}/snapshot`
- `GET /analytics/snapshots/{token}/`
- `GET /logs/`
- `GET /issues/`
- `GET /issues/{id}/`
- `GET /events/{id}/`
- `GET /alerts/`
- `GET /monitors/`
- `GET /feedback/`
- `GET /releases/`
- `GET /traces/{trace_id}/`
- `GET /ops/`
- `GET /settings/`
- `POST /settings/project`

The `/issues/{id}/` detail page supports resolve, resolve in next release, ignore, and reopen status actions, plus bookmark, subscription, comment, merge, and unmerge workflows.

The `/alerts/` page supports:

- issue triggers (`every event`, `first seen`, `regression`)
- slow-transaction triggers with a threshold in milliseconds
- monitor-missed triggers
- release crash-free triggers with a percentage threshold
- email, webhook, and Slack incoming-webhook actions
- persisted notification-delivery history in SQLite
- profile-enriched payloads for matching slow-transaction and release-health signals

User-configured outbound alert, hook, forwarding, integration-webhook, and uptime-monitor targets are restricted to public HTTP(S) destinations. Tiny mode rejects localhost, loopback, link-local, and private-network addresses both when the config is saved and again when the runtime client dials, so stale rows cannot bypass the guard.

The `/ops/` page is an org-admin operator surface. It renders:

- persisted install metadata such as install id, region, deployed version, bootstrap state, and maintenance state
- current runtime role, environment, version, and backend selection
- live service checks for SQLite and any configured control-plane, telemetry, JetStream, or Valkey dependencies
- current queue backlog depth
- recent backfill runs
- recent retention outcomes
- recent audit history

Install metadata is synced automatically at startup. Set `URGENTRY_REGION` when the install should advertise a stable region in `/ops/` and the diagnostics bundle.

The landing `/` dashboard still renders a small saved-query widget strip for the current user, but it is now intentionally limited to raw issue filters. The full saved-query and widget workflow lives on `/discover/` and `/dashboards/`, where callers can keep a query private or share it across the org, attach a short description, manage tags, pin per-user favorites, export results as CSV or JSON, create shareable frozen snapshots, then reuse the same query across tables, stats, and time-series widgets. `/discover/`, `/logs/`, `/dashboards/`, `/replays/`, `/profiles/`, and `/traces/{trace_id}/` now also render first-run guidance so a fresh Tiny-mode install explains what each analytics surface is for before it has much data. `/discover/` surfaces starter views for slow endpoints and failing endpoints, while `/logs/` surfaces the noisy-loggers starter, so a fresh install can open opinionated performance and log queries without knowing the search syntax first. `/dashboards/` also ships starter packs for ops triage, release watch, and performance pulse so a new Tiny-mode install can get to a useful analytics board without building every widget from scratch. Dashboard detail now carries its own refresh cadence, org-safe filter layer (`environment`, `release`, `transaction`), operator annotations, stat-threshold styling, and per-widget drilldown links so teams can use one shared board as a living runbook instead of a pile of ad hoc widgets.

The `/monitors/` page supports creating, editing, deleting, and reviewing persisted monitors and their check-ins directly in Tiny mode. Uptime monitors use the same public-target guard as other outbound webhooks and will mark a check as failed instead of dialing a private or local address.

The `/settings/` page supports:

- basic project metadata updates
- ownership-rule management
- per-surface telemetry retention and storage-tier controls
- replay sampling, privacy scrubbing, and max-bytes controls

Project-scoped retention operator routes:

- `GET /api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/archives/`
- `POST /api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/archive/`
- `POST /api/0/projects/{org_slug}/{proj_slug}/retention/{surface}/restore/`

Those routes let operators run one-surface archive or restore passes on demand and inspect the latest archive rows without waiting for the background retention sweep.

The org-wide `/discover/` and `/logs/` pages are guarded by the same query-cost and per-window quota enforcement that protects the matching management APIs. When a request exceeds the current budget, the page returns `429` with `Retry-After` and renders the guard reason plus retry guidance instead of attempting a degraded best-effort read. Dashboard widget execution reuses the same guardrail system, charged against the widget dataset workload.

`POST /discover/save-query`, saved-query management routes under `/discover/queries/{id}/...`, the dashboard mutation routes, and `POST /settings/project` follow the normal web-session mutation rules:

- authenticated session required
- CSRF token required
- route-specific authorization required

Telemetry retention policy rules:

- each surface has `retentionDays`, `storageTier`, and optional `archiveRetentionDays`
- omitted or `0` `archiveRetentionDays` defaults to `2x retentionDays`
- replay assets follow the replay surface, not the generic attachments surface
- attachment and debug-file archive tiers keep metadata rows live and restore archived blobs on demand when those records are read
- the explicit archive/restore routes return the applied policy plus recent archive rows so operators can verify blob presence and restored state after a run

### Ingest routes

- `POST /api/{project_id}/store/`
- `POST /api/{project_id}/envelope/`
- `POST /api/{project_id}/minidump/`
- `POST /api/{project_id}/security/`
- `POST /api/{project_id}/otlp/v1/traces/`
- `POST /api/{project_id}/otlp/v1/logs/`

### Management API routes

REST APIs remain under `/api/0/`.

They require:

- a PAT in `Authorization: Bearer ...`, or
- a valid browser session

Credential-management routes:

- `GET|POST|DELETE /api/0/users/me/personal-access-tokens/`
- `GET|POST|DELETE /api/0/projects/{org_slug}/{proj_slug}/automation-tokens/`
- `GET|POST|PUT|DELETE /api/0/projects/{org_slug}/{proj_slug}/ownership/`

Project management routes:

- `GET|PUT|DELETE /api/0/projects/{org_slug}/{proj_slug}/`

Release and artifact routes:

- `GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/health/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/sessions/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/commits/`
- `GET /api/0/organizations/{org_slug}/releases/{version}/`
- `GET|POST /api/0/organizations/{org_slug}/releases/{version}/deploys/`
- `GET|POST /api/0/organizations/{org_slug}/releases/{version}/commits/`
- `GET /api/0/organizations/{org_slug}/releases/{version}/suspects/`
- `GET /api/0/organizations/{org_slug}/releases/{version}/commitfiles/`
- `GET /api/0/organizations/{org_slug}/ops/overview/`
- `POST /api/0/projects/{org_slug}/{proj_slug}/attachments/`
- `GET|POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/`

Org-scoped release deploy, commit, suspect, and commit-file handlers now validate the control-plane catalog before reading or writing release-adjacent data, so missing org paths fail closed consistently with the rest of the release surface.
The project-scoped release commits route now also requires that the requested project is actually associated with that release before exposing the org release's commit list.
- `GET /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/`
- `POST /api/0/projects/{org_slug}/{proj_slug}/releases/{version}/debug-files/{debug_file_id}/reprocess/`
- `GET /api/0/organizations/{org_slug}/backfills/`
- `POST /api/0/organizations/{org_slug}/backfills/`
- `GET /api/0/organizations/{org_slug}/backfills/{run_id}/`
- `POST /api/0/organizations/{org_slug}/backfills/{run_id}/cancel/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/outcomes/`
- `POST /api/0/projects/{org_slug}/{proj_slug}/monitors/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/`
- `PUT /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/`
- `DELETE /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/monitors/{monitor_slug}/check-ins/`
- `GET|POST /api/0/organizations/{org_slug}/monitors/`
- `GET|PUT|DELETE /api/0/organizations/{org_slug}/monitors/{monitor_slug}/`
- `GET /api/0/organizations/{org_slug}/monitors/{monitor_slug}/checkins/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/transactions/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/traces/{trace_id}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/manifest/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/timeline/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/panes/{pane}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/recording-segments/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/recording-segments/{segment_id}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/viewed-by/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/replays/{replay_id}/assets/{attachment_id}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/{profile_id}/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/top-down/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/bottom-up/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/flamegraph/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/hot-path/`
- `GET /api/0/projects/{org_slug}/{proj_slug}/profiles/compare/`

Prevent repository-management routes:

- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/sync/`
- `POST /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/sync/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repositories/tokens/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/branches/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/test-results/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/test-suites/`
- `GET /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/test-results-aggregates/`
- `POST /api/0/organizations/{org_slug}/prevent/owner/{owner}/repository/{repository}/token/regenerate/`

Token-bearing Prevent reads require `project:tokens:read`, token rotation requires `project:tokens:write`, and the two Prevent POST routes also require a valid CSRF token when the caller is using a browser session instead of a PAT.
The Prevent `test-results` route now honors cursor-based next/previous navigation and computes slow/flaky suite metrics from the filtered interval instead of returning zeroed flaky fields in Tiny mode or serious self-hosted mode.
Project and issue event list pagination now preserves `per_page` and any active query parameters in emitted `Link` headers, and the second page onward marks `rel="previous"` as having results so Tiny and self-hosted cursor navigation stay aligned.

Integration parity routes:

- `GET /api/0/organizations/{org_slug}/sentry-apps/`
- `GET|PUT|DELETE /api/0/sentry-apps/{sentry_app_id_or_slug}/`
- `GET /api/0/organizations/{org_slug}/sentry-app-installations/`
- `POST /api/0/sentry-app-installations/{uuid}/external-issues/`
- `DELETE /api/0/sentry-app-installations/{uuid}/external-issues/{external_issue_id}/`
- `GET /api/0/organizations/{org_slug}/issues/{issue_id}/external-issues/`

Tiny mode persists Sentry app overrides, installation rows, and external-issue links in SQLite. Serious self-hosted mode persists the same control-plane records in Postgres so the API handlers do not fall back to SQLite-only shortcuts. Sentry app mutations require `org:admin` on any active membership. Installation-backed external issue writes require `issue:write`, validate the target issue scope before mutating, and upsert one link per installation plus issue plus display-name key.

Org monitor list responses now aggregate across every project in the org and return the same populated `project` ref block that single-monitor org reads expose, so cross-project monitor dashboards do not need a second lookup to recover project slugs.
Those org monitor list reads now use one org-scoped monitor query instead of a per-project monitor loop, so Tiny and self-hosted control-plane reads keep the same response body without paying an avoidable N+1 penalty as projects grow.

## Preprod artifact parity routes

The preprod artifact parity slice now ships on both Tiny and serious self-hosted mode:

- `GET /api/0/organizations/{org}/preprodartifacts/{artifact_id}/install-details/`
- `GET /api/0/organizations/{org}/preprodartifacts/{artifact_id}/size-analysis/`
- `GET /api/0/projects/{org}/{project}/preprodartifacts/build-distribution/latest/`

These reads use a shared SQLite query-plane store in both modes. Tiny mode writes and reads the artifact metadata from the default SQLite runtime. Serious self-hosted mode still authenticates and resolves org and project membership through the Postgres control plane, but the preprod artifact responses come from the shared SQLite query-plane read model so Tiny and self-hosted stay on one response path.

Auth and validation rules:

- org artifact reads require `org:read`
- project latest-build reads require `project:read`
- `build-distribution/latest` requires `appId` and `platform`
- when `buildVersion` is present, the request must also include `buildNumber` or `mainBinaryIdentifier`
- no-match latest-build requests return `200` with nullable `latestArtifact` or `currentArtifact`, while malformed query contracts return `400`

## Issue autofix parity routes

Urgentry now ships the experimental org issue autofix surface on both Tiny and serious self-hosted mode:

- `GET /api/0/organizations/{org}/issues/{issue_id}/autofix/`
- `POST /api/0/organizations/{org}/issues/{issue_id}/autofix/`

Like the preprod artifact slice, autofix parity uses a shared SQLite query-plane store in both modes. Tiny mode writes the run log directly in the local runtime database. Serious self-hosted mode still authenticates and resolves issue membership through the Postgres control plane, but the autofix read and write payloads persist in the shared SQLite query-plane state so the response contract stays identical across modes.

Auth and behavior rules:

- `GET` requires the org issue read path and returns `{"autofix": null}` when no run exists
- `POST` requires issue-write scope on the org issue path
- `POST` returns `202` plus a numeric `run_id`
- supported `stopping_point` values are `root_cause`, `solution`, `code_changes`, and `open_pr`
- `event_id`, when provided, must belong to the target issue or the API returns `400`

Issue workflow routes:

- `GET /api/0/issues/{issue_id}/comments/`
- `POST /api/0/issues/{issue_id}/comments/`
- `GET /api/0/issues/{issue_id}/activity/`
- `POST /api/0/issues/{issue_id}/merge/`
- `POST /api/0/issues/{issue_id}/unmerge/`

Organization admin routes:

- `GET /api/0/organizations/{org_slug}/issues/`
- `GET /api/0/organizations/{org_slug}/discover/`
- `GET /api/0/organizations/{org_slug}/logs/`
- `GET|POST /api/0/organizations/{org_slug}/dashboards/`
- `GET|PUT|DELETE /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/`
- `POST /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/`
- `PUT|DELETE /api/0/organizations/{org_slug}/dashboards/{dashboard_id}/widgets/{widget_id}/`
- `GET|POST|DELETE /api/0/organizations/{org_slug}/members/`
- `GET|POST|DELETE /api/0/organizations/{org_slug}/invites/`
- `POST /api/0/organizations/{org_slug}/teams/`
- `GET|POST|DELETE /api/0/teams/{org_slug}/{team_slug}/members/`
- `POST /api/0/invites/{invite_token}/accept/`

Query guardrail rules for telemetry-heavy reads:

- `/api/0/organizations/{org_slug}/issues/`, `/discover/`, and `/logs/` require `org:query:read`
- org issue queries accept `filter`, `query`, `environment`, `project`, `sort`, `limit`, and `statsPeriod`; `sort` supports `priority`, `date`, `freq`, and `new`
- `/api/0/organizations/{org_slug}/ops/overview/` requires `org:admin`
- dashboard reads require `org:query:read`; dashboard and widget mutations require `org:query:write`
- replay, profile, transaction, and trace project APIs are cost-scored before execution; the replay manifest, timeline, pane, and asset routes reuse the same scoped auth and replay query-guard workload
- denied requests return `429` with `Retry-After`
- allowed and denied org-wide query decisions are written to `auth_audit_logs`
- profile query responses use normalized snake_case JSON; single-profile routes select the latest query-ready profile that matches the provided transaction, release, environment, and time-window filters when `profile_id` is omitted
- profile query routes return `409 Conflict` when the selected profile exists but normalization did not reach a query-ready state

Import/export routes:

- `GET /api/0/organizations/{org_slug}/export/`
- `POST /api/0/organizations/{org_slug}/import/`

Import/export operator rules:

- org export includes object-backed attachment, replay-asset, raw-profile, source-map, ProGuard, and native-debug-file bodies plus SHA-1 checksum metadata
- `POST /api/0/organizations/{org_slug}/import/?dry_run=1` validates the full payload, including artifact size/checksum integrity, but rolls back all database and blob writes before returning
- a non-dry-run import stays all-or-nothing; any validation failure aborts the transaction and cleans up staged blobs

Exports include:

- projects and releases
- issues and events
- project keys
- alert rules
- organization members
- attachments, raw profile blobs, source maps, ProGuard mappings, and generic native debug-file kinds with artifact bodies

Imports are strict all-or-nothing transactions. A failed organization import is rolled back instead of leaving partial state behind. Import requests reject unknown JSON fields and are capped at 128 MiB.

## Search query subset

The current Tiny-mode issue search supports this typed-query subset across the web issue list, org/project issue APIs, and command palette issue search:

- `is:unresolved`, `is:resolved`, `is:ignored`
- `release:<version>`
- `environment:<name>` or `env:<name>`
- `level:<level>`
- `event.type:<type>` or `type:<type>`
- free-text terms matched against issue title, culprit, and recent event title/message fields

`/discover/` and `/logs/` now reuse the same AST-backed SQLite query surface for org-wide issue, log, and transaction exploration. The web builder accepts comma-separated `columns`, `aggregate`, `group_by`, and `order_by` values on top of dataset, free-text query, environment, visualization, time range, and rollup. That lets callers move from raw tables to grouped multi-aggregate tables without leaving the form. The page also renders a lightweight explain block so callers can see the normalized query document, planner cost class, and validation details when a field or aggregation is unsupported. Saved queries persist the canonical Discover AST, not route-local SQL or ad hoc widget config, and Tiny mode now scopes those saved queries to an organization with either private or org-shared visibility. Each saved query can also carry a short description, normalized tags, a per-user favorite state, a dedicated detail page under `/discover/queries/{id}/`, owner-only metadata update and delete controls, a matching update API under `/api/ui/searches/{id}`, CSV or JSON export links for the live result set, a frozen snapshot action, and per-user scheduled reports so callers can reuse shared analytics assets without rebuilding the query by hand.

Dashboard widgets now expose live CSV or JSON exports on both the dashboard detail page and the dedicated widget drilldown page, so a caller can download the current widget result without scraping HTML. The drilldown route also shows the widget source, the effective dashboard-level filters, and the normalized query explain block, which makes the widget contract readable even when the widget started from a saved query. The separate widget snapshot flow freezes the effective widget result after dashboard-level filters have been applied and stores that contract alongside the shared snapshot. The same drilldown page now owns per-user scheduled reports: Tiny mode stores the report definition in SQLite, the singleton scheduler materializes a fresh frozen snapshot on the due tick, and the email outbox records a queued `tiny-report` delivery that points at that share link.

Set `URGENTRY_BASE_URL` when you want those snapshot and scheduled-report links to be absolute outside the current browser session. If it is unset, Tiny mode falls back to relative `/analytics/snapshots/{token}/` paths in queued report bodies.

Org dashboards now persist separately from the fixed landing page. Tiny mode stores dashboard ownership, visibility, dashboard presentation config, and widget definitions in SQLite; widget queries are persisted as canonical Discover AST documents rather than raw SQL. Private dashboards remain owner-only except for org admins, while organization-visible dashboards are readable across the org but still only editable by the owner or an org admin. The dashboard editor supports create, starter-pack creation, update, duplicate, delete, share, add-widget, remove-widget, widget drilldown, and widget snapshot flows under `/dashboards/`, along with dashboard-level refresh cadence, annotations, filter chips, and stat-threshold configuration for metric widgets.

Replay and profile envelope items are persisted as project-scoped receipts. Tiny mode also materializes canonical `replay_manifests`, `replay_assets`, and `replay_timeline_items` rows in SQLite at ingest time, while replay recording bodies stay authoritative in blob-backed attachment storage. The project replay APIs therefore expose a playback-oriented manifest route, bounded timeline windows, pane filters, and replay-asset streaming without requiring clients to understand attachment object-key layout. The HTTP server assembles those replay services once and injects them into the web surface, so `/replays/{id}/` renders from the loaded canonical replay record, pane-focused timeline rows, linked issue/trace context, and replay-asset downloads instead of triggering a second unconditional timeline fetch or falling back to attachment-only inspection. Replay ingest is governed by project settings for `sampleRate`, `maxBytes`, `scrubFields`, and `scrubSelectors`: sampled-out replays are dropped before any canonical replay rows are written, oversize replay assets are skipped with an explicit replay outcome plus manifest ingest error, configured field names/selectors are scrubbed before the receipt payload, replay blobs, and timeline indexes are stored, and replay policy normalization/defaults are owned by the SQLite replay-config boundary so API and web writes follow the same persisted rules. Replay retention continues to follow the replay telemetry policy, but archive restore rebuilds replay manifests, asset refs, and timeline rows from the restored receipt and replay assets so `/replays/{id}/` and the replay APIs come back with the same canonical surface instead of only raw event/attachment rows. `/profiles/` offers filter-driven profile selection, `/profiles/{id}/` reads canonical SQLite profile manifests, threads, frames, stacks, and samples to render top-down, bottom-up, flamegraph, hot-path, and comparison sections with visible links to related issues, releases, and trace detail, `/traces/{trace_id}/` renders related profiles alongside stored transactions and spans, and discover transaction results link directly into those trace/profile surfaces.

Minidump ingest preserves native frame hints from multipart fields such as `debug_id`, `code_id`, `instruction_addr`, `module`, `function`, `filename`, and `lineno` so uploaded native debug files can resolve symbol names and source locations during issue processing. Tiny mode now persists two native lookup tables in SQLite: a release-scoped symbol-source catalog populated on debug-file upload/import, and a per-crash image/module catalog populated from parsed minidump module/exception data plus any matching event metadata before async stackwalking starts. Native symbol resolution currently supports Breakpad text symbol files plus ELF symbol-table lookups; debug-file list/detail/upload responses surface `symbolicationStatus` as `ready`, `malformed`, or `unsupported` for those native artifact bodies. A minidump request is acknowledged only after Tiny mode durably stores the raw dump, creates a `native_crashes` receipt row, creates a placeholder event row in `pending` processing state, and enqueues a `native_stackwalk` worker job. The worker parses the stored dump, derives the crash frame set from the dump/image catalog, transitions the event through `processing` to `completed` or `failed`, and overwrites the placeholder row with the final normalized native event once stackwalking succeeds. Malformed or unreadable dumps fail permanently without infinite retries, while transient blob or processing failures are requeued safely. Duplicate deliveries for a failed native crash restage the same receipt and requeue stackwalking instead of creating a second crash row. Uploading a native debug file still does not mutate historical events inline. Instead, the explicit reprocess routes return `202 Accepted` with a durable `native_reprocess` run, and operators can list and inspect those runs on the org backfill routes. Release detail pages now summarize native event volume, resolved vs unresolved native frame counts, recent release/debug-file reprocess runs, and the latest per-file reprocess status. Issue and event detail views also surface native processing state, unresolved-frame counts, and the latest ingest error so operators can tell whether a crash is still waiting on symbolication or failed during stackwalking. Historical native reprocessing is owner-scoped in Tiny mode and is not available to project automation tokens. Generic backfill launches must be bounded by project, release, or time range, overlapping native scopes now fail closed with `409 Conflict`, and cancellation is limited to pending runs that have not yet been claimed by a worker. Serious self-hosted operators can also create `telemetry_rebuild` runs on the same org backfill surface to reset and repopulate bridge-backed Discover, log, trace, replay, profile, and outcome facts from the authoritative SQLite plus blob planes; those rebuilds are limited to organization or project scope, reject release/time filters, and fail with `409 Conflict` when another active rebuild already owns the same organization or project scope. Reprocess requests, telemetry rebuild requests, and cancellations are written into auth audit logs, and completed native regrouping records `native_reprocess` issue activity on the affected issue timeline.

`/settings/` now includes project ownership rules. Tiny mode matches them against issue title, culprit, and tags to auto-assign new issues without a separate workflow engine.

`/releases/{version}/` now acts as the Tiny-mode release workflow page. It shows release health, deploy markers, associated commits, heuristic suspect issues derived from release-linked events plus commit file lists, release-scoped profile highlights, native processing summaries, recent native reprocess activity, release-over-release regression cards, environment error movement, transaction p95/count movement, and a latest-deploy impact summary that compares the 24-hour window before and after the newest deploy marker. Owner-only controls to launch release-wide or per-debug-file native reprocessing stay on the same page.
The org release list now batches native release-summary lookups across the listed versions instead of issuing one native-summary scan per row, so Tiny and self-hosted control-plane reads keep one response shape without paying an avoidable N+1 cost.
The organization member list now batches team-slug lookups for the whole org before assembling the response, so Tiny and self-hosted control-plane reads keep the same `teams` payload without issuing one team query per listed member.
Issue list/detail response extras now batch comment counts through the control-plane issue store when that seam is available, so Tiny and self-hosted `numComments` reads no longer fetch full comment bodies once per listed issue just to compute counts.
Invite acceptance remains token-based, but the unauthenticated accept route is now rate-limited per client IP and per invite token, and short garbage tokens fail closed before any store lookup.
Request logging now redacts the invite token segment on `/api/0/invites/{token}/accept/` before emitting the structured `path` field, so leaked invite secrets do not persist in application logs.
The `/api/0/seer/models/` stub stays public for Sentry parity, but it now returns the canonical `{"models":[]}` payload and is IP-rate-limited instead of remaining anonymously unbounded.
Successful ingest responses now emit the empty `X-Sentry-Rate-Limits` header only when an ingest limiter is actually configured, while real 429 responses keep the populated retry metadata.

Issues marked as "resolve in next release" become concretely release-aware when the next release record is created for that organization. Tiny mode binds the pending marker to that release version instead of leaving it as an open-ended status flag.

## Runtime roles

| Role | Mounted surfaces | Background work |
|---|---|---|
| `all` | system, ingest, management API, web UI | worker + scheduler |
| `api` | system, management API, web UI | none |
| `ingest` | system, ingest | none |
| `worker` | system only | durable event worker + durable backfill/reprocess worker |
| `scheduler` | system only | queue lease maintenance, retention sweep, monitor upkeep |

The shipped runtime is SQLite-backed only. Management REST endpoints and the HTML web UI both assume SQLite is available.

### Example split deployment

```bash
./urgentry serve --role=ingest --addr=:8081
./urgentry serve --role=api --addr=:8080
./urgentry serve --role=worker
./urgentry serve --role=scheduler
```

## Queue behavior

Ingest writes durable jobs into SQLite.

Worker processes claim jobs from SQLite, then:

1. normalize the event
2. route transactions and spans into SQLite trace tables
3. compute grouping for error events
4. write events and groups to SQLite
5. enqueue alert callbacks onto a separate in-process alert dispatcher

Queue knobs:

- `URGENTRY_PIPELINE_QUEUE_SIZE` — default `1000`
- `URGENTRY_PIPELINE_WORKERS` — default `1`

If the queue is full:

- ingest returns `503`
- clients should retry

The scheduler role requeues expired leased jobs so abandoned work is recoverable.
It also applies retention sweeps and marks overdue persisted monitors as `missed` based on their stored schedule.

The worker role advances durable backfill runs from `backfill_runs`, reclaiming expired run leases and resuming from the stored cursor.

## Compatibility harness

Black-box compatibility tests live in `internal/compat`.

They include:

- protocol and migration harness checks
- the live SDK matrix for Node, Python, Go, Java, and other runtimes available in the local environment

Run them directly:

```bash
cd .
make test-compat
make test-merge
```

`make test` is the fast local loop (during development) -- it is not sufficient for merging. `make test-fast-with-timings` writes `test-results/fast-suite.{jsonl,json}` so CI can keep the raw package stream plus the summarized slowest packages and budget checks. `make test-merge` is the canonical merge-safe command (before every PR merge). It includes Markdown link checks, repo tidy drift checks, the fast-suite timing and package-budget report, `make test-cover`, `internal/compat`, and `govulncheck`.

The protocol and migration eval harnesses remain private-monorepo tooling today. They are not part of the curated public repo export and should be treated as internal release-validation lanes rather than public repo commands.

## Profiling workflow

Use the broad benchmark suite for general hot-path checks, and the deterministic harness for comparable regression tracking:

```bash
cd .
make bench
mkdir -p profiles/latest
make profile-bench > profiles/latest/bench.txt
make profile
make profile-trace
```

Manual single-scenario example:

```bash
cd .
go run ./cmd/urgentry profile \
  --scenario=store-python-full \
  --kind=cpu \
  --iterations=200 \
  --gomaxprocs=1 \
  --out-dir=profiles/manual/cpu/store-python-full
```

Operational rules:

- use `make bench` for queue, SQLite, and HTTP benchmark coverage without ordinary tests
- keep `GOMAXPROCS`, scenario, and iteration count fixed between comparisons
- treat each profile kind as a separate run
- use fresh output directories so SQLite, WAL, and blob sizes are measured cleanly
- use `make profile-bench` output with `benchstat` for microbench regression comparisons

Gate policy:

- treat `make bench` and `make selfhosted-bench` as scheduled or operator-facing perf lanes, not routine merge gates
- treat the future PR perf gate as a narrow deterministic lane with hard budgets on selected hot paths and binary size
- keep the public merge-safe command focused on `make test-merge`

Use the Tiny-mode operator canary when you need isolated runtime load, retention, and recovery validation rather than developer profiling:

```bash
bash eval/dimensions/performance/run.sh
```

That command boots fresh local Tiny-mode nodes per scenario and writes:

- `eval/reports/performance/steady-ingest/`
- `eval/reports/performance/pipeline-backpressure/`
- `eval/reports/performance/query-load/`
- `eval/reports/performance/retention-interference/`
- `eval/reports/performance/latest-summary.json`
- `eval/reports/results/performance.*.json`

See `docs/urgentry-profiling-guide.md` for the full artifact layout and live-endpoint guidance.

For the post-Tiny serviceability package around broader observability features, see `docs/urgentry-observability-ops.md`.

## Health checks

Example:

```bash
curl -s http://localhost:8080/healthz
curl -s http://localhost:8080/readyz
```

`/healthz` includes:

- role
- env
- async backend
- cache backend
- current time
- version when set
- queue depth when a pipeline is configured
- metrics snapshot fields when metrics are configured

## Login and bootstrap

On first boot:

1. start Urgentry
2. copy the bootstrap email and password from logs
3. open `http://localhost:8080/login/`
4. sign in

For CLI or automation:

1. copy the bootstrap PAT from logs
2. call `/api/0/...` with `Authorization: Bearer <pat>`

## Troubleshooting

### `401` on ingest

Check:

- the public key exists
- the key is active
- the request path project matches the key’s `project_id`

### `401` on `/api/0/...`

Check:

- you are using a PAT or logged-in session
- you are not using a project key

### `403` on mutating web routes

Check:

- the session is still valid
- the request includes a CSRF token
- the user’s org role allows the action

### `503` on ingest

The durable queue is full.

Actions:

- start or scale `--role=worker`
- check `/healthz` queue depth
- inspect process logs for repeated job failures

### Web route returns login redirect

Expected when the route is mounted in `all` or `api` and no valid session cookie is present.

### Web route returns `404`

Expected when you are talking to `ingest`, `worker`, or `scheduler`, which do not mount the web surface.

## Validation

```bash
cd .
make test            # fast local loop (during development)
make test-compat
make test-merge      # canonical merge-safe command (before every PR merge)
make lint
make build
make test-race
make bench
```

`make test` alone is not sufficient for merging. Always run `make test-merge` before a PR merge.

## Related docs

- [urgentry-auth-schema-and-route-matrix.md](auth-schema-and-route-matrix.md)
- [urgentry-schema-starter-spec.md](schema-starter-spec.md)
