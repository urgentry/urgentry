# Urgentry compatibility matrix

## Goal

Define the compatibility target for a clean-room Go implementation that lets users mostly keep existing Sentry SDK instrumentation and only change DSN / URI / credentials.

Status labels:
- **P0**: required for first credible migration
- **P1**: required for modern Sentry compatibility claim
- **P2**: important but can follow initial launch

## 1. Auth and routing

| Capability | Priority | Notes |
|---|---:|---|
| DSN parsing and validation | P0 | Must accept standard project-key based DSN patterns |
| Project key auth | P0 | Core ingest auth path |
| Base URI replacement | P0 | Minimal customer config change principle |
| Ingest rate-limit responses | P0 | SDK-visible behavior |
| API token auth | P0 | Admin/UI/API workflows |
| Session/cookie auth for web app | P1 | Needed for full web parity |
| Trusted relay / edge auth concepts | P2 | Needed if edge relay compatibility is pursued |

## 2. Ingest protocols

| Capability | Priority | Notes |
|---|---:|---|
| Legacy store endpoint compatibility | P0 | Needed for older SDK paths and ecosystem edge cases |
| Envelope ingestion | P0 | Primary modern SDK path |
| Compressed payload handling | P0 | Common SDK behavior |
| Attachments in envelopes | P0 | Required for common mobile/native workflows |
| Standalone attachment flows | P1 | Implemented in Tiny mode through `POST /api/0/projects/{org}/{project}/attachments/` plus event attachment list/download routes |
| Minidump/native crash ingest | P1 | Implemented in Tiny mode as staged release-scoped native crash ingest with durable receipts, dump-derived image extraction, async stackwalk jobs, explicit processing states, duplicate-delivery dedupe/restaging, and end-to-end fixture coverage for Apple-style, Linux-style, and fallback crashes |
| Security report ingest | P1 | Implemented in Tiny mode for CSP/report-to style reports |
| Monitor check-ins / crons | P1 | Implemented in Tiny mode via envelope `check_in` items |
| User feedback payloads | P0 | Common product workflow |
| Client reports / dropped-event accounting | P1 | Implemented in Tiny mode via persisted outcomes |
| OTLP traces ingest | P1 | Implemented for OTLP/HTTP JSON ingest |
| OTLP logs ingest | P1 | Implemented for OTLP/HTTP JSON ingest |

## 3. Event model

| Capability | Priority | Notes |
|---|---:|---|
| Event ID handling | P0 | Stable correlation primitive |
| Release/environment support | P0 | Core issue and deploy workflows |
| Tags/contexts/extra/user/request | P0 | Must preserve search/debug usefulness |
| Breadcrumb handling | P0 | Common SDK output |
| Stacktrace frame normalization | P0 | Grouping and source maps depend on this |
| Fingerprint support | P0 | Required for advanced users |
| SDK metadata storage | P0 | Debugging and compatibility visibility |
| Session payloads | P1 | Implemented for release health |
| Span/transaction payloads | P1 | Implemented in Tiny mode |
| Log payloads | P1 | OTLP/logs support |
| Replay/profile metadata | P2 | Implemented in Tiny mode via replay/profile envelope items plus project API and web list/detail surfaces, playback-oriented replay manifest/timeline/pane/asset APIs, a scrubber-driven replay player with deep-linkable anchors and linked issue/trace context, replay attachment downloads, server-injected canonical replay read services shared by API/web, project-scoped replay sampling/privacy/size-cap controls enforced at ingest through the SQLite replay-config boundary, replay retention restore that rebuilds canonical replay indexes, a deterministic replay corpus with player/manifest regression tests and replay benchmarks, and canonical profile manifests with sample/frame/function summaries |

## 4. Grouping and issue lifecycle

| Capability | Priority | Notes |
|---|---:|---|
| Deterministic grouping engine | P0 | Core product credibility |
| Group hash versioning | P0 | Safe evolution over time |
| Fingerprint override support | P0 | Required for power users |
| Issue create/update semantics | P0 | Basic workflow |
| Regression detection | P1 | Important parity behavior |
| Merge/unmerge workflows | P2 | Shipped in Tiny mode; deeper cross-project merge policy can follow |
| Assignment/status tracking | P0 | Core product workflow |
| Ownership rules | P2 | Implemented in Tiny mode with project-scoped path/title/tag routing |

## 5. Query and search

| Capability | Priority | Notes |
|---|---:|---|
| Issue list filtering | P0 | Must support day-to-day use |
| Event detail retrieval | P0 | Must support debugging |
| Tag facets and top values | P0 | Core query UX |
| Time-series aggregations | P1 | Needed for alerts and dashboards |
| Release filtering | P0 | Core workflow |
| Environment filtering | P0 | Core workflow |
| Search language compatibility | P1 | Implemented: `is:`, `!is:`, `has:`, `!has:`, `release:`, `level:`, `environment:`, `event.type:`, `assigned:`, `platform:`, `firstSeen:`, `lastSeen:`, `times_seen:`, `bookmarks:`, tag filters, negation, and free text; parsed into a versioned Discover AST and validator |
| Trace exploration | P1 | Implemented via project transactions and trace-detail APIs |
| Log exploration | P1 | Modern compatibility |

## 6. Releases and deploys

| Capability | Priority | Notes |
|---|---:|---|
| Release creation and association | P0 | Common workflow |
| Deploy tracking | P1 | Implemented in Tiny mode via release detail APIs and `/releases/{version}/`, including latest-deploy impact deltas |
| Commit association / suspects | P2 | Implemented in Tiny mode with manual commit association and heuristic suspect issue linking |
| Release health sessions | P1 | Needed for stronger parity |

## 7. Artifacts and symbolication

| Capability | Priority | Notes |
|---|---:|---|
| JavaScript source maps | P0 | Mandatory for web compatibility |
| Artifact bundle metadata | P1 | Modern JS workflow |
| ProGuard mappings | P0 | Android support |
| dSYM / native debug file ingestion | P1 | Implemented for release-scoped debug file uploads, normalized native symbol-source catalog persistence, staged minidump receipts, explicit native-event reprocess support, per-debug-file and release-scoped reprocess controls, operator-visible reprocess status, symbolication readiness classification, and native reprocess roundtrip assertions in the compatibility harness |
| Symbol/source lookup and caching | P1 | Implemented for Breakpad-style symbol lookup plus ELF symbol-table lookup in Tiny mode through the native symbol-source catalog and per-crash image catalogs with debug/code/build/module precedence; event/release surfaces now expose resolved vs unresolved native frame counts and latest processing failures, and the native fixture corpus keeps normalized-frame snapshots stable across refactors |
| Source-context enrichment | P2 | Nice-to-have later |

## 8. Alerts and notifications

| Capability | Priority | Notes |
|---|---:|---|
| Basic issue alerts | P0 | Minimum product bar |
| Email notifications | P0 | Lowest-friction default |
| Webhook notifications | P0 | Good integration primitive |
| Slack notifications | P1 | Implemented in Tiny mode through Slack incoming-webhook actions |
| Performance alerts | P1 | Implemented in Tiny mode for slow-transaction alert rules |
| Advanced workflow rules | P2 | Can follow |

## 9. Admin and project config

| Capability | Priority | Notes |
|---|---:|---|
| Organizations/projects/teams/members | P0 | Required |
| Project key management | P0 | Required |
| Project-level settings | P0 | Required |
| API tokens | P0 | Required |
| Audit logs | P1 | Important for paid/enterprise use |
| SSO/SAML/SCIM | P2 | Partial SCIM support now includes `/Users` list/get/create/patch/delete plus team-backed `/Groups` list/get/create/patch/delete; SSO and SAML remain later |
| Retention controls | P1 | Strong self-hosting value |

## 10. Import/migration

| Capability | Priority | Notes |
|---|---:|---|
| DSN swap migration docs | P0 | Essential growth lever |
| Project/release import | P0 | Makes switch practical |
| Issue import | P1 | Stronger migration story |
| Source map/debug file import | P1 | Implemented for source maps, ProGuard, and generic native debug-file kinds with artifact bodies |
| API compatibility adapters | P1 | Makes tooling migration easier |

## 11. Product rollout target

## P0 launch slice

A credible initial launch should include:
- DSN/project-key compatibility
- legacy store + envelope ingest
- events/errors
- issue grouping and issue lifecycle basics
- releases/environments
- attachments
- user feedback
- source maps + ProGuard basics
- issue search/list/detail
- project settings / key management
- basic issue alerts + email/webhooks
- migration/import path

## P1 expansion slice

Current shipped P1 compatibility includes:
- OTLP logs
- standalone attachment upload/download
- better search-language compatibility for the Tiny-mode query subset
- Discover builder support for comma-separated columns, multiple aggregates, multi-field group-by, and sort rules
- event list cursor navigation that preserves page size and query params across `Link` headers
- issue comments/activity, bookmarks, subscriptions, and merge/unmerge basics
- deploy tracking and release detail pages
- source-scanned OpenAPI route coverage for 218 of 218 Sentry operations in `research/sentry-openapi-schema.json`, including org Prevent repository-management, deprecated org `alert-rules`, org monitor CRUD/checkins, replay recording-segment detail, replay viewed-by, team external-team aliases, Sentry app inventory/get/update/delete, Sentry app installation listing, installation-backed external issue create/delete plus org issue external-issue reads, org preprod artifact install-details and size-analysis, project preprod build-distribution latest, and org issue autofix get/start

## P2 deep parity slice

Current shipped P2 compatibility includes:
- replay/profile metadata list and detail surfaces
- ownership rules
- manual commit association and suspect issue hints

Longer-term:
- full session replay record/playback workflows
- deeper profiling product surfaces
- merge/unmerge parity
- advanced ownership rules
- deep commit/suspect integration
- enterprise identity and governance features
- trusted relay/edge scenarios

See `docs/urgentry-deep-parity-roadmap.md` for the historical post-Tiny execution plan that produced the shipped native, Discover, replay, profiling, and observability surface. The active forward-looking program is now `docs/urgentry-serious-self-hosted-roadmap.md`.

## 12. Compatibility test harness requirements

Every row above should eventually be backed by black-box tests with:
- SDK fixture corpus by language/platform
- expected request/response behavior
- golden normalized-event snapshots
- grouping/output snapshots
- artifact resolution fixtures
- rate-limit and quota contract tests
- migration smoke tests

## 13. Truth in marketing rule

Do not claim “full Sentry compatibility” until at least:
- all P0 items are proven in automated tests
- most P1 items are implemented or explicitly excluded with clear docs
- supported SDKs and known gaps are published in a public compatibility matrix

Current known OpenAPI gaps after the latest parity sweep:
- none in `research/sentry-openapi-schema.json`; remaining parity risk is response-depth drift on experimental or partially-modeled product surfaces rather than unmatched operations
