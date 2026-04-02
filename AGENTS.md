# AGENTS.md

App-local instructions for `apps/urgentry/`.

These rules apply to the Go product inside the monorepo.

## Architecture

**Tiny mode** (current default): single binary, SQLite, local auth, durable SQLite jobs.

```
SDK → Ingest (HTTP) → project-key auth → SQLite jobs → Worker → Normalize → Group → SQLite
                                                                      ↓
Web UI ← session auth ← API ← SQLite                                 ~/.urgentry/urgentry.db
```

The Tiny-mode web surface includes an analytics home at `/` that mixes issue watchlists, recent transactions, logs, releases, replays, profiles, starter views, and saved-query widgets, plus org-wide `/discover/`, `/logs/`, `/replays/`, `/profiles/`, `/traces/{trace_id}/`, `/monitors/`, and org-admin `/ops/` pages, an org-wide `/api/0/organizations/{org}/issues/`, `/discover/`, `/logs/`, and `/ops/overview/` query/admin surface, curated starter views for slow endpoints, noisy loggers, and failing endpoints on the Discover and Logs pages, a Discover builder that accepts comma-separated columns, aggregates, group-by fields, and sort rules, project replay/profile APIs with playback-oriented replay manifest/timeline/pane/asset routes plus a scrubber-driven `/replays/{id}/` player that supports pane deep links and linked issue/trace context, normalized top-down, bottom-up, flamegraph, hot-path, and comparison query routes, a filterable `/profiles/` list plus `/profiles/{id}/` views that render those projections with issue/release/trace links, direct discover-to-trace/profile navigation for transactions, release-health metrics on `/releases/`, release workflow details on `/releases/{version}/` including release-scoped profile highlights, release-over-release comparison cards, deploy impact summaries, and native processing/reprocess controls, project ownership rules plus per-surface telemetry retention/storage controls on `/settings/`, replay sampling/privacy/size-cap controls on `/settings/`, explicit project retention operator routes under `/api/0/projects/{org}/{project}/retention/{surface}/...`, dashboard widgets derived from saved issue searches plus dedicated `/dashboards/{id}/widgets/{widget_id}/` drilldown pages with source contracts, effective filters, explain output, export links, widget snapshots, and per-user scheduled reports, durable org-admin backfill APIs for historical reprocessing, explicit native debug-file reprocessing, issue and event detail native processing visibility, issue detail workflows for comments, bookmarks, subscriptions, release-aware resolution that binds "next release" markers when a new release is created, and basic merge/unmerge operations. Alert notifications can also carry related profile summaries when Tiny mode finds a matching profile for a slow transaction or release-health signal. Set `URGENTRY_BASE_URL` in Tiny mode when you want snapshot and scheduled-report emails to carry absolute links. Org-wide query surfaces require explicit `org:query:read`, the operator overview requires `org:admin`, and the heavy query paths are cost- and quota-guarded with explicit `429` responses.
Project service hooks are runtime-wired in Tiny mode and dispatch `event.created`, `event.alert`, `issue.created`, and `issue.resolved` to active hook URLs.
SCIM support is partial today but broader than the original `/Users` slice: `/api/0/organizations/{org}/scim/v2/Users` now mounts list/get/create/patch/delete, and `/api/0/organizations/{org}/scim/v2/Groups` mounts list/get/create/patch/delete with team-backed membership materialization. Compatibility aliases also cover deprecated org `/alert-rules/`, org-level `/monitors/{slug}/` plus `/checkins/`, team-scoped `/external-teams/`, project replay `recording-segments/{segment_id}` and `viewed-by`, and read-only Sentry app inventory or installation listing routes.

**Serious self-hosted** (current operator path): Postgres-backed control-plane services when `URGENTRY_CONTROL_DATABASE_URL` is configured, plus MinIO, Valkey, NATS JetStream, and the telemetry bridge for query-heavy reads when `URGENTRY_TELEMETRY_DATABASE_URL` is configured.
**Cloud** (future): add ClickHouse for analytics scale.

Keep ClickHouse off the MVP critical path.

## Build and run

```bash
make build        # optimized Tiny-mode binary (default)
make build-tiny   # compatibility alias for make build
make build-debug  # unstripped local debug binary
make run          # build + run on :8080
make tiny-smoke   # boot Tiny mode, render /login/, verify bootstrap login + PAT
make tiny-launch-gate # release/handoff gate (before public releases)
make test         # fast local loop (during development)
make test-compat  # compatibility harness + live SDK matrix
make test-merge   # canonical merge-safe command (before every PR merge)
make test-race    # fast suite with race detector
make test-cover   # with coverage report
make lint         # go vet + golangci-lint
make bench        # broad benchmark suite, not the routine merge gate
make selfhosted-bench # serious self-hosted performance + soak eval lane
make selfhosted-eval  # serious self-hosted readiness scorecard (requires Docker Compose, kind, and kubectl)
make profile-bench  # deterministic microbenchmarks
make profile        # fixed-scenario CPU/heap/allocs capture
make profile-trace  # fixed-scenario execution traces
bash ../../eval/dimensions/performance/run.sh  # isolated Tiny-mode load/canary/recovery harness
make fuzz         # fuzz tests (30s each)
```

Or directly:
```bash
go run ./cmd/urgentry serve --role=all
go run ./cmd/urgentry profile --scenario=store-basic-error --kind=cpu --iterations=200 --gomaxprocs=1 --out-dir=profiles/manual/cpu/store-basic-error
go run ./cmd/urgentry self-hosted preflight --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL"
go run ./cmd/urgentry self-hosted status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL"
go run ./cmd/urgentry self-hosted maintenance-status --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
go run ./cmd/urgentry self-hosted enter-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --reason "upgrade window"
go run ./cmd/urgentry self-hosted leave-maintenance --control-dsn "$URGENTRY_CONTROL_DATABASE_URL"
go run ./cmd/urgentry self-hosted record-action --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --action secret.rotate --detail "rotated operator secret"
go run ./cmd/urgentry self-hosted migrate-control --dsn "$URGENTRY_CONTROL_DATABASE_URL"
go run ./cmd/urgentry self-hosted migrate-telemetry --dsn "$URGENTRY_TELEMETRY_DATABASE_URL" --telemetry-backend=postgres
go run ./cmd/urgentry self-hosted backup-plan --telemetry-backend=postgres --blob-backend=s3 --async-backend=jetstream --cache-backend=valkey
go run ./cmd/urgentry self-hosted security-report --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --telemetry-dsn "$URGENTRY_TELEMETRY_DATABASE_URL"
go run ./cmd/urgentry self-hosted rotate-bootstrap --control-dsn "$URGENTRY_CONTROL_DATABASE_URL" --email "$URGENTRY_BOOTSTRAP_EMAIL" --password "$URGENTRY_BOOTSTRAP_PASSWORD" --pat "$URGENTRY_BOOTSTRAP_PAT"
go run ./cmd/urgentry self-hosted verify-backup --dir /tmp/urgentry-backup --strict-target-match=true
go run ./cmd/urgentry self-hosted rollback-plan --current-control-version 3 --target-control-version 2 --current-telemetry-version 4 --target-telemetry-version 3
bash deploy/compose/ops.sh preflight
bash deploy/compose/ops.sh maintenance-status
bash deploy/compose/ops.sh enter-maintenance "upgrade window"
bash deploy/compose/ops.sh leave-maintenance
bash deploy/compose/ops.sh record-action secret.rotate "rotated operator secret"
bash deploy/compose/ops.sh security-report
bash deploy/compose/ops.sh rotate-bootstrap
bash deploy/compose/ops.sh verify-backup /tmp/urgentry-backup
bash deploy/compose/backup.sh /tmp/urgentry-backup
bash deploy/compose/restore.sh --verify-only /tmp/urgentry-backup
bash deploy/compose/restore.sh /tmp/urgentry-backup
URGENTRY_SELF_HOSTED_BACKUP_DIR=/tmp/urgentry-backup bash deploy/compose/upgrade.sh
bash deploy/compose/drills.sh backup-restore
../../eval/run-selfhosted.sh
make test
make test-compat
```

## Package layout

```
cmd/urgentry/          binary entrypoint
internal/
  app/               runtime wiring, role selection
  api/               REST API handlers
  auth/              project-key auth, sessions, PAT auth, authorization
  attachment/        file attachments
  alert/             alert rules + evaluation
  config/            env-based configuration
  domain/            shared types: Event, Group, IssueStatus, Level
  discover/          canonical Discover query AST, parser, and validator
  envelope/          Sentry envelope wire format parser
  feedback/          user feedback persistence
  grouping/          deterministic event grouping engine
  http/              HTTP server composition
  httputil/          shared JSON response helpers
  ingest/            store, envelope, and minidump HTTP handlers
  issue/             group model, processor (normalize→group→store)
  logging/           zerolog setup
  middleware/        CORS, compression, chain
  migration/         import/export payloads and cutover helpers
  normalize/         event normalization (tags, timestamps, levels)
  notify/            Tiny-mode notification outbox delivery
  pipeline/          durable SQLite-backed job queue + scheduler maintenance
  postgrescontrol/   serious self-hosted Postgres control-plane migrations
  proguard/          ProGuard mapping files + resolver
  runtimeasync/      serious self-hosted JetStream and Valkey contract
  sqlite/            SQLite persistence (events, groups, auth, jobs, migrations, import/export)
  store/             storage interfaces + in-memory implementations
  sourcemap/         JS source maps + resolver
  telemetrybridge/   serious self-hosted Postgres or Timescale bridge migrations plus rebuild projector
  web/               HTMX web UI (SQLite-backed templates and CSS)
  project/           project settings
  release/           releases, deploys, and release-health surface
pkg/
  dsn/               DSN parser
  id/                shared ID generation
deploy/compose/      Docker Compose for self-hosted
```

## Coding rules

**Rob Pike's 5 rules** as default engineering guidance:

1. Don't guess where time goes. Don't add speed hacks without proof.
2. Measure before optimizing.
3. Fancy algorithms lose on small `n`. Start simple.
4. Simple algorithms and data structures over clever ones.
5. Data dominates. Choose clear data structures first.

Practical interpretation:
- prefer clear structs and explicit flow over clever abstraction
- prefer straightforward SQL and repositories over magic
- prefer simple handlers and worker logic over framework complexity
- if a design feels clever, simplify it once more

## Implementation guardrails

Prefer:
- thin vertical slices
- explicit interfaces around event store, blob store, queue, and cache
- deterministic normalization and grouping behavior
- `internal/domain/` for shared types
- `pkg/id.New()` for ID generation
- `httputil.WriteJSON` / `httputil.WriteError` for HTTP responses
- `zerolog` for structured logging

Avoid:
- generalized plugin systems
- microservice splits
- dual-write complexity
- extracting packages just because code *might* be reused
- `log.Printf` (use `zerolog`)
- inline `json.NewEncoder(w).Encode` (use `httputil`)
- local `generateID()` functions (use `pkg/id`)

## Testing discipline

- Every public function needs a test
- Run `make test-race` before merging concurrent code
- Fixture-driven tests load from `../../eval/fixtures/` (store, envelope, grouping, negative) and `internal/testfixtures/` for product-specific deterministic corpora such as profiles and native crash flows
- Black-box compatibility harness tests live in `internal/compat/` and are expected to stay green in `make test-compat` / `make test-merge`
- Golden snapshot tests in `internal/normalize/golden_test.go`
- Fuzz tests in `internal/envelope/fuzz_test.go` and `internal/normalize/fuzz_test.go`
- Benchmarks cover `internal/pipeline`, `internal/sqlite`, `internal/http`, and `internal/nativesym`, plus the deterministic parser/normalize/grouping suites in `internal/envelope/bench_test.go`, `internal/normalize/bench_test.go`, and `internal/grouping/bench_test.go`; profile ingest/query benchmarks are backed by `internal/testfixtures/profiles`, and native stackwalk/symbol benchmarks are backed by `internal/testfixtures/nativecrash`
- Deterministic scenario profiles are captured through `internal/profile/` and the `urgentry profile` command
- Table-driven tests with `t.Run()` subtests
- `t.Helper()` on test helpers
- `t.TempDir()` for test data directories

## SQLite conventions

- Data dir: `~/.urgentry/` (override: `URGENTRY_DATA_DIR`)
- Blob backend: default local files under `URGENTRY_DATA_DIR`, or `URGENTRY_BLOB_BACKEND=s3` plus `URGENTRY_S3_ENDPOINT`, `URGENTRY_S3_BUCKET`, `URGENTRY_S3_ACCESS_KEY`, `URGENTRY_S3_SECRET_KEY`, `URGENTRY_S3_REGION`, `URGENTRY_S3_PREFIX`, and `URGENTRY_S3_USE_TLS`
- WAL mode enabled, `MaxOpenConns(1)` (single writer)
- Migrations auto-apply on startup via `internal/sqlite/migrations.go`
- Use `INSERT OR IGNORE` for idempotent event writes
- Use `INSERT ... ON CONFLICT DO UPDATE` for atomic group upserts
- Project keys are ingest-only; `/api/0/...` uses PAT or session auth
- Worker claims durable jobs from SQLite and also advances durable `backfill_runs`; scheduler requeues expired leases and runs singleton maintenance
- Queue/runtime knobs: `URGENTRY_PIPELINE_QUEUE_SIZE`, `URGENTRY_PIPELINE_WORKERS`, `URGENTRY_HTTP_READ_HEADER_TIMEOUT`, `URGENTRY_HTTP_READ_TIMEOUT`, `URGENTRY_HTTP_WRITE_TIMEOUT`, `URGENTRY_HTTP_IDLE_TIMEOUT`
- Live profiling stays disabled unless `URGENTRY_PROFILING_ENABLED=true`; remote `/debug/pprof` and `/debug/fgprof` access requires `URGENTRY_PROFILING_TOKEN`
- Nullable SQL columns → `sql.NullString` in Go (not bare `string`)

## Documentation rule

When app behavior or structure changes, update relevant docs in the same change:
- `README.md`
- `QUICKSTART.md`
- `docs/architecture/operations-guide.md`
- `docs/tiny/` and `docs/self-hosted/` when mode-specific behavior changes
- this file

Before closing any task, audit the affected docs and update them in the same change. Documentation review is part of done, not follow-up work.

## Commit rule

- Commit after each completed task so the app can be rolled back cleanly.
- Keep each commit scoped to one task.
- Use non-interactive git commands only.
- If code changes behavior, include the required doc updates in the same commit.

## Validation

Before considering app work done:
```bash
make test       # fast local loop (during development)
make test-merge # canonical merge-safe command (before every PR merge)
make test-compat # compat + SDK matrix pass
make test-race  # fast race suite passes
make lint       # no lint issues
make build      # compiles clean
bash ../../eval/dimensions/performance/run.sh # when changing runtime, queue, retention, or perf-critical paths
```

`make test` alone is not sufficient for merging. Always run `make test-merge` before a PR merge.

If you change repo layout or developer workflow, confirm commands in `README.md`, root `AGENTS.md`, and `QUICKSTART.md` still match reality.
