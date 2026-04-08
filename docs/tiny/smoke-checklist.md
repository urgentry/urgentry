# Urgentry Tiny Smoke Checklist

Use this checklist on a fresh Tiny-mode node before you hand it to anyone else.

It is split into two parts:

- the exact automated smoke the repo already runs in CI
- the manual product checks that confirm the main Tiny-mode surfaces still work together

## Part 1: automated boot smoke

Run the same command CI uses:

```bash
cd .
make tiny-smoke
```

That check must pass before you keep going. It proves:

- Tiny mode boots on a fresh data directory
- `/readyz` becomes healthy
- `/login/` renders
- the bootstrap owner can sign in
- the bootstrap PAT can call the management API

## Part 2: manual product smoke

Start Urgentry on a fresh data directory:

```bash
cd .
URGENTRY_DATA_DIR="$(mktemp -d)" ./urgentry serve --role=all
```

Save the bootstrap owner email, password, PAT, and public ingest key from the startup logs.

### 1. Login and core navigation

- Open `http://localhost:8080/login/`
- Sign in with the bootstrap owner account
- Confirm these pages load without errors:
  - `/`
  - `/issues/`
  - `/discover/`
  - `/dashboards/`
  - `/logs/`
  - `/releases/`
  - `/replays/`
  - `/profiles/`
  - `/monitors/`
  - `/settings/`
  - `/ops/`
- On `/`, confirm the analytics home shows more than topline counters:
  - the issue watchlist renders
  - starter views render
  - recent transactions, logs, releases, replays, and profiles each render either live rows or a clean empty state

### 2. Management API

Use the bootstrap PAT:

```bash
curl \
  -H "Authorization: Bearer <bootstrap_pat>" \
  http://localhost:8080/api/0/organizations/
```

Expected result:

- HTTP `200`
- the default organization is returned

### 3. Event ingest and issue creation

Send one event with the bootstrap public key:

```bash
curl -X POST http://localhost:8080/api/default-project/store/ \
  -H "Content-Type: application/json" \
  -H "X-Sentry-Auth: Sentry sentry_key=<public_key>,sentry_version=7" \
  -d '{
    "event_id": "abcdef01234567890abcdef012345678",
    "message": "Tiny smoke event",
    "level": "error",
    "platform": "python",
    "tags": {"smoke":"tiny"}
  }'
```

Expected result:

- HTTP `200`
- response body contains the same event ID
- `/issues/` shows a new issue
- `/events/abcdef01234567890abcdef012345678/` renders

### 4. Discover, logs, and dashboards

- Open `/discover/`
- Run a query such as `smoke:tiny`
- run one builder query with comma-separated grammar, for example aggregates `count, p95(duration.ms)`, group by `project, transaction`, and sort `-p95`
  the page should render grouped columns for `project`, `transaction`, `count`, and `p95` instead of falling back to a validation error
- open the starter views for slow endpoints and top failing endpoints
  both starter views should render a transaction table instead of an empty or invalid-query state
- Save it once as a private query
- Open the saved query detail page
- add one scheduled report for that saved query
- Clone it
- Add the query to a dashboard
- Open `/logs/` and verify the page renders cleanly
- open the noisy loggers starter view
  the starter view should render grouped logger rows instead of falling back to the generic empty state
- Open `/dashboards/` and confirm the dashboard renders
- On the dashboard detail page:
  - set a refresh cadence
  - add at least one dashboard-level filter such as `environment`
  - add one annotation note
  - create a stat widget with warning and critical thresholds
  - confirm the page shows the refresh badge, filter chips, annotation text, and threshold summary
- open one widget drilldown page and confirm the widget contract, effective filters, and query explain block render
- add one scheduled report from the widget drilldown page
- export one widget result and create one widget snapshot to confirm those flows still work with dashboard-level filters in place
- if the scheduler is running, confirm the report list eventually shows a last snapshot link and the Tiny outbox records a queued `tiny-report` email

### 5. Traces

If the node already has trace data from your SDK tests, open `/traces/{trace_id}/` and confirm the detail view loads.

If not, use an SDK or OTLP smoke fixture from your local workflow, then confirm:

- the trace detail page loads
- spans are visible
- related errors still link back to issues

### 6. Replays and profiles

If you have replay and profile fixtures available from your local test workflow:

- ingest one replay
- ingest one profile
- confirm `/replays/` lists the replay
- confirm `/profiles/` lists the profile
- open the replay and profile detail pages

If you do not have fixtures handy, at minimum confirm both pages render and show an empty state instead of failing.

### 7. Releases and monitors

- Open `/releases/`
- Create a release through the API or your SDK flow
- confirm the release list and release detail pages render
- if there is a prior release, confirm the detail page shows release comparison, environment movement, transaction movement, and latest deploy impact sections
- open `/monitors/`
- create or update one monitor
- confirm the monitor list shows the new check

### 8. Export and operator views

- Open `/ops/` and confirm runtime metadata renders
- trigger an organization export from the UI or API
- confirm the export completes and returns a payload

### 9. Alerts

At minimum confirm:

- `/alerts/` renders
- the alert list shows existing rules or an empty state
- creating or editing an alert does not fail

## Pass criteria

Tiny mode is ready for handoff when:

- `make tiny-smoke` passes
- login works
- management API auth works
- event ingest creates an issue
- Discover and dashboards work
- dashboard refresh, filter, annotation, export, and snapshot flows work
- logs, releases, monitors, replays, profiles, and ops pages render
- export and alerts do not fail on basic flows

## Related docs

- [QUICKSTART.md](../../QUICKSTART.md)
- [build-guide](build-guide.md)
- [deploy-guide](deploy-guide.md)
- [Operations guide](../architecture/operations-guide.md)
