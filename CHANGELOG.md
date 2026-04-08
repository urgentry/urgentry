# Changelog

## v0.2.0 (2026-04-01)

Sentry compatibility test suite. 128 conformance tests validate that Urgentry's ingest, API, and auth paths behave like Sentry's. Code overhaul fixes silent error drops and cuts gzip allocations by 77%.

### Compatibility test suite

OpenAPI endpoint matcher parses Sentry's 128-endpoint schema and maps each one to a Urgentry route. 37 matched, 181 unmatched, 110 Urgentry-only. Response shape validator checks JSON structure against the OpenAPI spec. Coverage matrix scores overall compatibility and writes markdown or JSON reports.

Relay protocol conformance tests (ported from getsentry/relay integration tests):

- Store endpoint: 12 tests. Basic event, legacy auth, query string auth, CORS, auth rejection, gzip, deflate, rate limiting, empty body, malformed JSON, event ID round-trip, concurrent writes.
- Envelope endpoint: 10 tests. Event, multi-item, transaction, session, user feedback, check-in, gzip, auth, empty body, malformed header.
- Minidump: 6 tests. Upload, auth, missing file, extra fields, content type, concurrent.
- OTLP: 6 tests. Traces, logs, auth, protobuf rejection, JSON accept, span structure conversion.
- Attachments: 6 tests. Single, large (1MB), multiple, content types, retrieval via API, auth.
- Metrics: 7 tests. Statsd format, bucket format, counter, distribution, gauge, set, auth.
- Feedback: 6 tests. Envelope, user report, retrieval, contact email, linked event, auth.
- Sessions: 8 tests. Init, update, exited, crashed, aggregates, auth, release association.
- Monitors: 7 tests. Check-in via envelope, ok/error/in_progress status, duration, list API, auth.
- Security reports: 7 tests. CSP, CSP with csp-report content type, Report-To format, Expect-CT, HPKP, auth, event creation.
- Normalization: 10 tests. Long message, default level, platform, timestamp, event ID, release, environment, tags, stack trace in_app, PII scrubbing.

E2E validation tests:

- Login flow: 8 tests. Bootstrap login, invalid password, CSRF protection, session cookie, logout, PAT auth, API auth required, API with PAT.
- DSN ingest: 6 tests. DSN retrieval, store flow, envelope flow, multiple events, event retrieval API, issue creation.
- API CRUD: 10 tests. Projects, organizations, teams, alert rules, dashboards, releases, saved searches (skipped), project keys, response format, pagination.
- Discover query: 8 tests. Basic query, fields list, aggregation, filtering, sorting, pagination, auth, invalid query.
- Self-hosted mode: 8 tests. Bootstrap, DSN generation, health endpoint, static assets, project creation, multi-user, data persistence across restarts.

SDK cross-validation harnesses generate ready-to-run scripts for JavaScript (@sentry/node), Python (sentry-sdk), and Go (sentry-go). Each script sends test events and transactions, captures responses, and diffs them against a real Sentry instance. Diff report generator aggregates API, protocol, and SDK results into a scored markdown report with gap analysis.

### Bug fixes

- httputil: JSON encode failures now return HTTP 500 with a plain text fallback instead of an empty 200.
- pipeline: processItem and MarkDone failures get logged with project and job context instead of being silently dropped.
- auth_store: three DB writes that discarded errors (session last_seen_at, automation token last_used_at, user token last_used_at) now propagate the error to the caller.

### Performance

- gzip reader pool in the decompress middleware and OTLP handler. Allocations per compressed request dropped from 54KB to 12KB (77% reduction). Throughput went from ~49 MB/s to ~55 MB/s.

### Refactoring

- Decomposed three large sqlite store files into seven. profile_store_support.go (1153 lines) split into profile_normalize.go (522, pure normalization logic with no DB dependency), sqlite_helpers.go (139, shared JSON/type coercion), and a smaller profile_store_support.go (510). release_store.go (857) split into core CRUD (488) and release_store_regression.go (378, analytics). monitor_store.go (813) split into core (655) and monitor_cron.go (160, pure scheduling computation).
- Consolidated compat test helpers. Moved buildEnvelope, jsonPayload, apiGet/Post/Put/Delete, readJSON, requireStatus into the shared compat_test.go. Deleted unused waitForFeedbackByEmail.
- Deduplicated firstNonEmpty variants across monitor_store, native_control_store, and profile_store_support via sqlite_helpers.go.

### Tests

- Tagged all 20 compat test files with `//go:build integration`. Fast CI loops skip them (76s saved). Run with `-tags integration`.
- Added FuzzParse for the search parser. 2.8 million inputs, zero crashes.
- Added four envelope handler benchmarks: single event (137 MB/s), transaction (183 MB/s), multi-item (74 MB/s), compressed (53 MB/s).

## v0.1.0

All prior changes.

### Features

- add structured search syntax with operators for issue queries
- add interactive span waterfall visualization to trace detail
- add competitive ingest throughput benchmark harness
- inline attachment viewer and raw JSON syntax highlighting
- establish semver versioning with structured build metadata
- wire uptime monitoring, sampling, and quota into runtime
- add SAML SSO provider and SCIM user provisioning
- add Jira integration and data forwarding
- add OIDC SSO provider and team-scoped issue ownership routing
- add uptime monitoring, dynamic sampling, and quota management
- add postgres migration for project_memberships table
- add granular project-level RBAC (owner/admin/member/viewer)
- add code mappings and Discord integration
- add feedback widget + PagerDuty integration
- add GitHub integration with commit linking and suspect commits
- add transaction trends and Apdex scoring to performance page
- add persistent environment selector to web UI navigation
- add integration plugin framework with webhook and OAuth patterns
- add metric alert evaluation loop
- add context panels to event detail (device, OS, browser, runtime, app, GPU)
- resolve minified stack frames using uploaded source maps
- add /performance/ overview page with transaction summary
- add metric alert rule model and CRUD API
- add rich stack trace rendering on event detail page
- add breadcrumb timeline to event detail page
- add persistent environment selector to web UI header
- add time range selector to web UI navigation
- add backlog health check to /readyz probe
- add live telemetry projection via durable async fanout
- make /readyz probe dependency-aware by role
- add package-group coverage reporting to coversummary
- add live kubernetes self-hosted smoke
- add self-hosted secret rotation workflow
- add node-churn self-hosted soak
- add active-active self-hosted drill
- expand tiny discover builder grammar
- build a real tiny analytics home page
- add scheduled analytics reports
- add dashboard widget drilldowns
- add starter analytics views
- add release regression analytics
- explain discover query failures
- add dashboard filters refresh and thresholds
- add install-wide operator audit ledger
- add maintenance mode drain workflow
- persist install state for operators
- expose operator diagnostics export
- wire operator slo health
- add support bundle contract
- add self-hosted pitr contract
- add migration compatibility gate
- add mixed-version preflight contract
- add telemetry export contract
- add telemetry bridge observability contract
- add hosted abuse control contract
- add hosted region placement contract
- add hosted usage ledger contract
- add self-hosted blob lifecycle contract
- add self-hosted cluster contract
- add self-hosted upgrade contract
- add self-hosted fanout contract
- add telemetry engine adr
- add self-hosted distribution contract
- add self-hosted scale gate
- add self-hosted repair contract
- add query execution contract
- add self-hosted slo pack
- add hosted validation contract
- add hosted migration assistant contract
- add hosted signup contract
- add hosted billing export contract
- add hosted quota contract
- add hosted lifecycle contract
- define hosted secret rotation workflow
- back hosted support policy with code
- define hosted recovery drills
- define hosted support access policy
- add hosted tenancy contract
- define hosted rollout contract
- define hosted plan catalog
- add shareable analytics snapshots
- add analytics onboarding guides
- add starter dashboard packs
- add tiny analytics exports
- add saved query management views
- add tiny saved query detail flows
- enrich tiny saved query assets
- add org-shared saved queries
- execute serious profiles through telemetry bridge
- execute serious discover through telemetry bridge
- move serious issue queries onto control services
- wire serious runtime to postgres control plane
- add serious self-hosted eval and perf gates
- add telemetry bridge rebuild backfills
- route serious self-hosted queries through telemetry bridge
- add operator overview surfaces
- add self-hosted backup and restore drills
- ship serious self-hosted runtime substrate
- harden blob custody read paths
- add postgres control workflow stores
- add postgres admin and monitor stores
- add postgres control catalog store
- add postgres control auth store
- verify artifact imports and exports
- add retention archive restore operator api
- add s3 blob store support
- add telemetry bridge migration contract
- add postgres control-plane migrations
- enforce replay privacy and retention controls
- ship replay player ui
- add replay playback api routes
- add replay manifest indexing at ingest
- ship discover and dashboard web flows
- persist discover dashboards and widgets
- add sqlite discover execution engine
- add discover query parser and saved search ast
- expose native reprocess control plane
- add multi-format native symbol resolvers
- stage native minidump stackwalking
- persist native symbol and crash catalogs
- link profiles into traces releases and alerts
- ship profiling web ui
- add profile query apis
- add canonical profile storage
- add observability performance canary harness
- add durable backfill orchestration
- add org query guardrails
- add telemetry retention archive controls
- bind next-release issue resolution to releases
- enrich native replay and profile detail flows
- enrich issue detail workflow context
- add monitor detail timeline
- add ownership rules and release workflow
- add replay profile and native ingest surfaces
- add discover logs and dashboard widgets
- add tiny mode monitor and release alerts
- add tiny-mode issue workflow polish
- close remaining sqlite parity gaps
- add slack and performance alerts
- add security report ingest
- add transactions and otlp trace ingest
- track alert dispatch backpressure metrics
- add monitor check-ins and client-report accounting
- add native symbolication

### Bug Fixes

- resolve vet and staticcheck warnings from parity merge
- resolve build issues from multi-agent merge
- register Discord in default integration registry and clean up unused import
- use %w instead of %v for error wrapping in fmt.Errorf
- resolve all staticcheck warnings
- make envelope ingest atomic under queue backpressure
- make durability verification strict instead of warning-only
- make required SDK runtimes explicit in compatibility matrix
- harden compose and k8s manifests to fail closed on insecure defaults
- fail startup on critical bootstrap and install-state errors
- degrade gracefully when Valkey query guard is unavailable
- make eval runner fail closed on broken or missing dimension results
- correct replay_timeline table name to replay_timeline_items in bridge query
- redact bootstrap credentials from startup logs and rotation summaries
- harden self-hosted compose churn drills
- use control catalog for trace reads
- guard overlapping backfill scopes
- drop dead projector cursor helper
- harden self-hosted operator gate
- restore eval scorecard generation
- polish replay detail summaries
- [F-018] cover profiling routes in tests
- [F-017] require profiling token when configured
- [F-016] stabilize sqlite benchmark seed time
- [F-015] stabilize http benchmark seed time
- [F-014] add profile-bench capture setup
- [F-013] profile-bench capture directory
- [F-012] cover non-heap profile runs
- [F-011] document trace profile tree
- [F-010] clear profile-trace output root
- [F-009] cover normalize parse branches
- [F-008] cover grouping fallback benchmarks
- [F-007] skip tests in bench target
- [F-006] benchmark real store path
- [F-003] sync profiling guide
- [F-005] test profiling localhost bypass directly
- [F-004] cover non-heap profile writers
- [F-003] surface profile table count errors
- [F-002] restore GOMAXPROCS after profiling
- [F-001] make sparklines use one clock
- satisfy alert lint
- make trace lookup and otlp retries robust
- harden otlp ids and event classification
- stabilize transaction trace normalization

### Refactoring

- split postgrescontrol store files by domain concern
- split replay_store.go and profile_store.go by read/write/index paths
- split api_sqlite_test.go into domain-focused test files
- split sqlite/migrations.go by domain concern
- unify discover filter planning onto shared PlanFilter engine
- split web_test.go into domain-focused test suites
- unify bridge and SQLite discover row mapping onto shared engine
- reject serious-mode topologies that use URGENTRY_DATA_DIR without PostgreSQL
- remove source-db cross-query from bridge freshness checks
- bundle telemetry query service dependencies from composition root
- collapse duplicate SQL arg builders and fetch-limit logic into discovershared
- split retention store by surface
- bundle analytics runtime services
- unify web failure responses
- split control-plane contracts by surface
- validate startup deps before serving
- decouple web profile views from api dto types
- register telemetry projector families
- split telemetry bridge readers by dataset
- split app runtime assembly and boot paths
- inject runtime stores into transport layers
- move telemetry query assembly to app
- split sqlite web read models
- make pipeline lifecycle explicit
- split sqlite import export flows
- codify secondary failure handling
- unify api error responses
- share discover execution across backends
- take telemetry projection out of query reads
- move api user shadow sync off auth reads
- split k8s bundle by role
- split profile store support files
- share profile query logic across backends
- freeze pipeline lifecycle before start
- extract async queue and lease contracts
- port control-plane handlers behind shared contracts
- centralize replay policy normalization
- inject replay services into web
- unify settings and alerts web reads
- move web search and counts behind stores
- remove legacy management auth fallback
- simplify benchmark seed titles
- simplify profiling workflow surfaces
- simplify benchmark setup
- simplify profiling and web store helpers
- remove unused web store prepare path
- harden sqlite tiny mode runtime
- remove api import export sql copy
- move sqlite import export behind stores
- require sqlite for web ui
- unify web read models on store types
- unify catalog models and sqlite writes
- remove fabricated web fallback data

### Documentation

- restructure documentation by audience into tiny/, self-hosted/, architecture/
- define minimum HA baseline for serious self-hosted mode
- publish supported toolchain and runtime matrix
- align CI job naming and contributor guide with validation tiers
- annotate validation tiers across all docs
- lock audit decisions into ADRs
- add inline runtime architecture diagrams
- clarify beads vc workflow
- clarify beads vc workflow
- fix beads workflow instructions
- mark telemetry graduation contract
- reconcile hosted roadmap beads
- record completed self-hosted next-step roadmap
- reconcile hosted tenancy roadmap
- define hosted tenancy model
- define hosted support recovery drills
- add hosted recovery drills
- define hosted deploy orchestration
- define hosted plan entitlements
- tighten public repo policies
- add tiny feedback intake loop
- add release process and versioning guide
- add tiny container deployment guide
- add tiny local deployment guide
- add tiny binary install guide
- define tiny release artifact matrix
- add tiny smoke checklist
- split tiny public docs from internal context
- add public repo benchmark
- add support and changelog surfaces
- harden tiny build and deploy guides
- rewrite public tiny-mode docs
- fix public markdown links
- add github issue and pr templates
- rewrite readme for public launch
- add public governance docs
- mark serious self-hosted roadmap complete
- note shipped blob custody reads
- dedupe s3 config quickstart entries
- surface control-plane migration scaffold
- define self-hosted async runtime contract
- tighten self-hosted runtime lease contract
- define self-hosted async runtime contract
- define serious self-hosted control plane
- define serious self-hosted telemetry bridge
- define serious self-hosted async runtime contract
- define serious self-hosted control-plane cutover
- define self-hosted control plane contract
- define self-hosted control-plane contract
- add serious self-hosted roadmap
- align replay docs with shipped runtime
- add profile schema ADR
- add profile schema ADR
- add replay manifest ADR
- add discover query ADR
- add native stackwalking ADR
- deepen post-tiny analytics ADR
- add observability operator package
- add post-tiny analytics adr
- add deep parity roadmap
- correct profiling artifact layout
- sync workflow and profiling guidance
- require per-task commits

### CI

- add binary size budget check (40MB ceiling, ~30MB baseline)
- add enforceable performance budgets for hot-path benchmarks
- pin govulncheck and golangci-lint to explicit versions
- add scheduled flake-hunting workflow
- require Tiny launch gate before release publication
- add bench-pr target for short deterministic PR benchmark lane
- enforce numeric coverage threshold in test-cover gate
- add tiny boot smoke

### Performance

- add -p 4 to test-race and test-cover for reduced wall time
- make export artifact materialization explicit
- collapse repeated web page scope reads
- cache stepped projector progress
- reduce issue endpoint heap churn
- remove profile repair work from list reads
- trim replay ingest metadata churn
- reuse parsed profile manifests on ingest
- stream organization exports
- collapse dashboard and settings read paths

### Tests

- cover bridge-backed discover harness with stale projection edge cases
- add bridge correctness tests for replay, profile, and trace projector surfaces
- add projection worker unit tests
- add integration race lane over booted server path
- split hermetic SDK correctness from live ecosystem drift
- expand PR Tiny smoke to cover landing and core surfaces
- add characterization tests for telemetrybridge, telemetryquery, postgrescontrol, selfhostedops, discoverharness, discovershared, and testpostgres
- add characterization tests for zero-coverage helper packages
- harden postgres test harness readiness
- split web dashboard harness from monolith
- cover live helper packages and drop dead shims
- add package timing budgets and artifacts
- characterize workflow failure paths
- reuse migrated postgres templates across packages
- cover telemetrybridge freshness and profiles
- cover telemetryquery sqlite service
- expand self-hosted ops edge coverage
- cover self-hosted cli paths
- cover controlplane sqlite defaults
- benchmark bridge reads and projector backlog
- stabilize async ingest and pipeline coverage
- align eval coverage reporting
- harden coverage summary reporting
- benchmark replay reindex path
- unify postgres integration harness
- add dual-backend control-plane harness
- harden postgres control-plane migration checks
- add replay fidelity harness
- add discover compatibility harness
- add native crash compat corpus and benchmarks
- add deterministic profile corpus and benchmarks
- add queue sqlite and http benchmarks
- cover auth cli and sqlite stores
- refresh sdk matrix results

### Other

- style: remove remaining make([]T, 0) in postgrescontrol scan loops
- style: remove unnecessary make([]T, 0) allocations across codebase
- chore: update chromedp, gobwas/ws, and transitive dependencies
- bench: add serious-mode canary for bridge, control-plane, and operator paths
- chore: refresh dependency risk surface
- bench: add pipeline lifecycle benchmarks
- tooling: standardize shell helper invocation
- build: expand merge health gates
- build: tidy urgentry module dependencies
- build: make optimized tiny binary the default
- build: add tiny launch hardening gate
- build: automate tiny release publishing
- chore: automate dependency updates
- ops: add compose self-hosted command wrapper
- ops: add serious self-hosted role restart drill
- ops: harden serious self-hosted secret preflight
- eval: add self-hosted soak and capacity checks
- ops: harden self-hosted recovery and upgrade safety
- ops: harden backup verification and upgrade safety
- ops: add self-hosted migration tooling
- schema: harden telemetry bridge baseline
- bd: close urgentry-6b9.5.1
- bd init: initialize beads issue tracking
- bench: fix pipeline queue sizing
- bench: prebuild store request bodies
- cmd: track urgentry entrypoint
- bench: make fixtures deterministic
- chore: bump deps and clean stale stub metadata
- api: bound and harden organization imports
- build: split compat test lane for per-merge coverage
- chore: snapshot repo baseline

