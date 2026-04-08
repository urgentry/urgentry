# Urgentry Tiny Launch Gate

Use this before a public Tiny-mode release, a design-partner drop, or any launch candidate you expect someone else to install.

The goal is simple: one command, one workflow, and a clear coverage map for the risks that matter before you put Tiny mode in front of strangers.

## Run it locally

```bash
cd .
make tiny-launch-gate
```

The gate is defined in `scripts/tiny-launch-gate.sh`.

## Run it in GitHub Actions

The repo also ships `.github/workflows/tiny-launch-gate.yml`.

Run it from the Actions tab when you want a clean CI pass before a release or external handoff.

## What the gate covers

`make tiny-launch-gate` checks these areas:

- docs hygiene
  - `bash scripts/check-markdown-links.sh`
- build and first boot
  - `make build`
  - `make tiny-smoke`
- auth and session behavior
  - `go test ./internal/auth -run 'TestAuthorizer(...)'`
- migration and local state safety
  - `go test ./internal/sqlite -run 'TestMigrationsIdempotent|TestDashboardStoreMigrationApplied|...'`
- query guard and rate limiting
  - `go test ./internal/sqlite -run 'TestQueryGuardStore...'`
  - `go test ./internal/api -run 'TestAPIOrganizationDiscoverQuotaExhaustion|TestAPIReplayQueryCostGuard'`
  - `go test ./internal/web -run 'TestDiscoverPageReturnsQueryGuardRateLimit'`
- retention and archive or restore behavior
  - `go test ./internal/sqlite -run 'TestRetentionStore...'`
  - `go test ./internal/api -run 'TestRetentionArchiveAndRestoreReplays'`
- replay and profile ingest
  - `go test ./internal/ingest -run 'TestEnvelopeHandler(...)'`
- replay, profile, and analytics UI flows
  - `go test ./internal/web -run 'TestDiscoverAndLogsPages|TestDiscoverSavedQueryAndDashboardFlows|TestDashboardStarterTemplates|TestAnalyticsPagesShowOnboardingGuides|TestReplayAndProfilePages|...'`
- release packaging and install artifacts
  - `make release VERSION=tiny-launch-gate`
  - `sha256sum -c dist/SHA256SUMS`
- static analysis
  - `make lint`

## What it does not replace

The launch gate does not replace:

- `make test-compat` when you changed SDK or protocol behavior
- manual product review from [smoke-checklist](smoke-checklist.md)
- a real release tag build from `.github/workflows/release.yml`

## When to require it

Run the gate when you touch any of these:

- Tiny-mode auth or first-run setup
- public install or release docs
- Discover, dashboards, logs, replay, profile, or trace flows
- retention or replay or profile storage
- release artifacts or packaging
- query guard or quota behavior

## Related docs

- [smoke-checklist](smoke-checklist.md)
- [Release process](../architecture/release-process-and-versioning.md)
- [release-artifact-matrix](release-artifact-matrix.md)
- [Operations guide](../architecture/operations-guide.md)
