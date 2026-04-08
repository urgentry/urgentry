# Urgentry

Urgentry is a self-hosted error and telemetry system written in Go.

The public starting point is **Tiny mode**: one binary, SQLite, local auth, durable jobs, and a real web UI. You can run it on a laptop, a small VM, or a single server and get issue triage, logs, traces, replays, profiles, dashboards, alerts, releases, monitors, and import or export workflows without standing up a pile of infrastructure.

The repo also ships a **serious self-hosted preview** for split-role deployments on Postgres, MinIO, Valkey, and NATS. Tiny mode is the easiest way to evaluate Urgentry. Serious self-hosted is the next step when you need shared infrastructure and operational depth, but it is still the preview path, not the public default.

## What ships today

### Tiny mode

Tiny mode is the public wedge. It is SQLite-first and works as a single-node product.

You get:

- local email and password login plus PAT-based management APIs
- Sentry-style event, envelope, minidump, OTLP trace, OTLP log, replay, profile, and monitor ingest
- issue triage with comments, bookmarks, subscriptions, merge or unmerge, and release-aware resolution
- an analytics home at `/` that blends issue watchlists, recent transactions, logs, releases, replays, profiles, starter views, and saved query widgets so a new install has a real landing page instead of an empty shell
- org-wide Discover, logs, dashboards, traces, replays, and profiles, with private or org-shared saved queries, starter views for slow endpoints, noisy loggers, and failing endpoints, starter dashboard packs, widget drilldowns, frozen share links, scheduled reports, first-run onboarding guides, descriptions, tags, per-user favorites, dedicated management views, CSV or JSON exports across analytics surfaces, and a Discover builder that accepts comma-separated columns, aggregates, group-by fields, and sort rules
- alerts, release health, deploy markers, suspect issues, and native debug-file reprocessing
- import and export, retention controls, operator views, and compatibility or performance harnesses

### Serious self-hosted

Serious self-hosted is already in the repo and already tested, but it is not the public entry path.

It adds:

- Postgres-backed control-plane services
- JetStream-backed async execution
- Valkey-backed shared quota and cache state
- MinIO or S3-compatible blob storage
- split `api`, `ingest`, `worker`, and `scheduler` roles
- operator preflight, backup, restore, rollback, and readiness scorecards

## Quick start

If you want to try Urgentry, start here:

```bash
cd .
make build
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

Then open:

- `http://localhost:8080/login/`

On first boot, Urgentry creates:

- a default organization
- a default project
- a bootstrap owner account
- a bootstrap PAT
- a default public ingest key

When no `URGENTRY_BOOTSTRAP_*` env vars are set, Urgentry generates random credentials and prints them in full to the startup log. Copy the email, password, and PAT — they are shown only once. You can also pre-set them via environment variables (see QUICKSTART.md).

`make build` is the repo default on purpose. It produces the optimized Tiny-mode binary the release and Docker paths use too: `CGO_ENABLED=0`, `netgo,osusergo`, `-trimpath`, no VCS stamp, and stripped symbols.

Set `URGENTRY_BASE_URL` to the public Tiny-mode URL when you want frozen snapshot pages and scheduled report emails to carry absolute links.
Webhook-style outbound destinations and uptime monitors accept only public HTTP(S) targets. Tiny mode rejects localhost, loopback, link-local, and private-network addresses on both create and delivery paths.

When you want the full first-run flow, use [QUICKSTART.md](QUICKSTART.md).

## Common commands

Run these from the repo root:

```bash
bash ./scripts/run-urgentry.sh
bash ./scripts/test-urgentry.sh
```

Or run the app module directly:

```bash
cd .
make build
make tiny-smoke
make test                # fast local loop
make lint
make test-merge          # canonical merge-safe command
make test-race
make bench
make profile-bench
make selfhosted-bench
make selfhosted-eval
make tiny-launch-gate    # release/handoff gate
make tiny-sentry-baseline # replay the upstream-Sentry baseline corpus against Tiny mode
make selfhosted-sentry-baseline # replay the upstream-Sentry baseline corpus against serious self-hosted mode
make synthetic-registry  # regenerate .synthetic/*.json coverage registries
make synthetic-generate  # generate .synthetic/generated corpora and manifests
make synthetic-audit     # summarize synthetic coverage against the registry
make synthetic-check     # run synthetic package tests and scenario-pack gates
make release VERSION=v0.1.0
```

`make test-merge` is the canonical merge-safe command -- run it before every PR merge. `make test` alone is not sufficient for merging. `make tiny-launch-gate` is the stronger release/handoff gate required before any public Tiny release or external handoff.

Use the Tiny-mode performance canary when you touch hot paths:

```bash
bash eval/dimensions/performance/run.sh
```

`make selfhosted-bench` now retries transient compose boot failures and reports median steady-ingest and query samples by default so small self-hosted deltas are less sensitive to single-run host noise.

`output/**/*.png` is tracked with Git LFS. Keep large generated binary artifacts on that path or add a new explicit LFS rule before committing them.

Use the Tiny-mode boot smoke when you touch startup, auth, or first-run flows:

```bash
cd .
make tiny-smoke
```

Use the serious self-hosted readiness gate when you touch the split-role path:

```bash
bash eval/run-selfhosted.sh
```

That gate now requires Docker Compose, `kind`, and `kubectl` because it runs the split-role Compose drills first, then boots a disposable single-node `kind` cluster for the live Kubernetes smoke.

## Documentation

| Path | Audience |
|------|----------|
| **[docs/tiny/](docs/tiny/)** | Tiny mode — build, install, deploy, operate a single-node instance |
| **[docs/self-hosted/](docs/self-hosted/)** | Serious self-hosted — Compose, Kubernetes, HA, backup, upgrade |
| [docs/architecture/](docs/architecture/) | Internal ADRs, design docs, roadmaps |
| [docs/reference/](docs/reference/) | Schema specs, compatibility matrices |
| [QUICKSTART.md](QUICKSTART.md) | Shortest path to a running Tiny node |

## Repo layout

This repo is a monorepo, but you do not need to understand the whole tree to use Urgentry.

The parts that matter most:

```text
.
├──    product code, deploy assets, Go module
├── docs/          architecture, operations, roadmaps, design notes
├── eval/          scorecards, fixtures, validation harnesses
├── scripts/       repo-root helpers
└── research/      clean-room reference material
```

Important app paths:

- `cmd/urgentry/` - binary entrypoint
- `internal/` - application code
- `deploy/` - Compose and Kubernetes assets
- `configs/` - example config

## Clean-room rule

This repo keeps upstream reference material under `research/reference/`.

That material is for scope study and compatibility pressure only. Do not copy implementation into Urgentry from those references.

## Contributing and security

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [MAINTAINERS.md](MAINTAINERS.md)
- [SUPPORT.md](SUPPORT.md)
- [CHANGELOG.md](CHANGELOG.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [AGENTS.md](AGENTS.md)
- [AGENTS.md](AGENTS.md)
