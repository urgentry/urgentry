# Urgentry Quick Start

This is the shortest path to a working Tiny-mode node.

Tiny mode is the public starting point for Urgentry:

- one binary
- one SQLite-backed data directory
- one web UI
- one login flow

If you want packaging, Docker, reverse proxy, or upgrade details, use the guides linked at the end.

## Prerequisites

- Go 1.25
- a free local port, usually `8080`

## Build

```bash
cd .
make build
```

That leaves the binary at `urgentry`.
That default build already uses the optimized Tiny-mode profile the repo ships in release archives and the Docker image.

## Start Urgentry

```bash
cd .
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

On first boot, Urgentry creates:

- a default organization
- a default project
- a bootstrap owner account
- a bootstrap PAT
- a public ingest key

When no `URGENTRY_BOOTSTRAP_*` environment variables are set, Urgentry generates random credentials and prints them **in full** to the startup log:

```
bootstrap owner account created — save these credentials, they are shown only once
  email=admin@urgentry.local password=urgentry-a1b2c3d4e5f6g7h8 pat=gpat_...
```

Copy the `email` and `password` values — you'll need them to log in. They are only shown once.

You can also set your own credentials before first boot:

```bash
URGENTRY_BOOTSTRAP_EMAIL=you@example.com \
URGENTRY_BOOTSTRAP_PASSWORD=your-password \
urgentry serve
```

Set `URGENTRY_BASE_URL` to the URL people will actually open if you want frozen snapshot pages and scheduled report emails to carry absolute links.

## Sign in

Open http://localhost:8080/login/ and sign in with the bootstrap email and password from the startup log above.

After login, the main Tiny-mode surfaces are:

- `/` for the analytics home
- `/issues/`
- `/discover/`
- `/dashboards/`
- `/logs/`
- `/releases/`
- `/traces/{trace_id}/`
- `/replays/`
- `/profiles/`
- `/monitors/`
- `/settings/`

Each analytics page also carries a short first-run guide so a new install can tell the difference between Discover, dashboards, logs, traces, replays, and profiles without digging through the docs first.
The root page is not just a counter strip anymore. It is the Tiny-mode analytics home, so it should show issue pressure, recent transactions, logs, releases, replays, profiles, starter views, and any saved-query widgets the signed-in user already pinned.
`/discover/` ships starter views for slow endpoints and failing endpoints, and `/logs/` ships a noisy-loggers starter, so a fresh install can open useful queries before anyone learns the full search syntax.
The Discover builder takes comma-separated `columns`, `aggregate`, `group_by`, and `order_by` values. That means you can move from a raw table like `timestamp, transaction, duration.ms` to a grouped table like `count, p95(duration.ms)` by `project, transaction` without leaving the form.

## Send your first event

Use the public project key from startup logs. The default project ID is `default-project`.

```bash
curl -X POST http://localhost:8080/api/default-project/store/ \
  -H "Content-Type: application/json" \
  -H "X-Sentry-Auth: Sentry sentry_key=<public_key>,sentry_version=7" \
  -d '{
    "event_id": "abcdef01234567890abcdef012345678",
    "message": "Something went wrong",
    "level": "error",
    "platform": "python"
  }'
```

Expected response:

```json
{"id":"abcdef01234567890abcdef012345678"}
```

You should now see the event in `/issues/` and `/events/{event_id}/`.

## Common next steps

Save and reuse analytics queries:

- build a query in `/discover/`
- try a richer builder query such as columns `timestamp, transaction, duration.ms`, aggregates `count, p95(duration.ms)`, group by `project, transaction`, or sort `-p95, project`
- save it as private or org-shared
- add a short description, tags, and favorite the queries you want pinned at the top
- open `/discover/queries/{id}/` to review, update, clone, delete, and reuse shared queries
- export Discover, saved-query, and dashboard-widget live results as CSV or JSON when you need to hand the current data to another tool
- use the starter views on `/discover/` and `/logs/` when you want a fast first pass on slow endpoints, noisy loggers, or failing transactions
- add it to `/dashboards/`
- if you want a faster start, create one of the starter dashboard packs from `/dashboards/` and adjust the widgets after you see your own data
- open a dashboard widget drilldown when you need to inspect the widget source, effective filters, or normalized query explain block
- if you need a stable link for a handoff, create a frozen snapshot from a saved query or a dashboard widget
- if you want that handoff to recur automatically, add a scheduled report from the saved-query detail page or the widget drilldown page

Use the management API with the bootstrap PAT:

```bash
curl \
  -H "Authorization: Bearer <bootstrap_pat>" \
  http://localhost:8080/api/0/organizations/
```

Check the current build metadata:

```bash
cd .
./urgentry version
```

Re-run the boot, login, and bootstrap-PAT flow exactly the way CI does:

```bash
cd .
make tiny-smoke
```

When you want the canonical merge-safe command (required before every PR merge), run:

```bash
cd .
make test-merge   # canonical merge-safe command
```

`make test` alone is the fast local loop -- it is not sufficient for merging.

Before you hand a build to anyone outside the repo, run the release/handoff gate:

```bash
cd .
make tiny-launch-gate   # release/handoff gate
```

## Read next

- [Tiny mode docs](docs/tiny/) — build, deploy, operate
- [Self-hosted docs](docs/self-hosted/) — Compose, Kubernetes, HA
- [Operations guide](docs/architecture/operations-guide.md)
- [Profiling guide](docs/architecture/profiling-guide.md)
