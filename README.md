![Gentry banner](assets/gentry/github_banner.png)

# Urgentry

Urgentry is Sentry-compatible error tracking you can run yourself, with compatibility tracked as route coverage plus semantic behavior.

## Why this exists

A lot of teams want Sentry-compatible error tracking, but they do not want a hosted bill or a huge self-hosted install just to catch exceptions. Urgentry is the smaller option.

This public repo ships two deployment paths:

- `Tiny mode`: one binary on one machine
- `Self-hosted mode`: split roles on PostgreSQL, MinIO, Valkey, and NATS

## Who it is for

- You already use Sentry SDKs and do not want to re-instrument
- You want a local or self-hosted install you can understand
- You want a small single-node path first, with a bigger path when you need it
- You want the data and the upgrade path under your control

## Start in under a minute

```bash
make build
URGENTRY_BASE_URL=http://localhost:8080 ./urgentry serve --role=all
```

Then open `http://localhost:8080/login/`.

If you want the shortest route, start with [QUICKSTART.md](QUICKSTART.md).

## Reference benchmarks

Current large-box reference, apples to apples:

- `urgentry-test`
- Ubuntu 24.04, Linux 6.8
- 8 vCPU
- 30.6 GiB RAM
- 226 GB root disk

| Target | Highest stable comparison load | Ingest p95 | Query p95 | Peak memory | Quality checks |
|---|---:|---:|---:|---:|---:|
| Tiny | `400 eps` | `10.08 ms` | `78.66 ms` | `52.3 MB` | `6/6` |
| Self-hosted | `2200 eps` | `0.71 ms` | `48.82 ms` | `391.8 MB` | `6/6` |
| Sentry self-hosted `26.3.1`, `errors-only` | `1000 eps` | `0.62 ms` | `1400.81 ms` | `8191.7 MB` | `6/6` |

This repo ships Tiny and self-hosted. Sentry is here as a reference point so you can compare the tradeoffs on the same machine.

For the full benchmark note, including the smaller-box reference run and methodology, see [docs/benchmarks.md](docs/benchmarks.md).

## Pick a mode

| Mode | Use it when | What you run |
|---|---|---|
| Tiny | Start here if you want one binary on one machine, the lowest memory footprint, or a simple path for local and small-server installs. On the recovered large-box reference it holds `400 eps` at `52.7 MB` peak memory. | one binary, SQLite |
| Self-hosted | Use this when you need shared infrastructure, split roles, object storage, or sustained ingest in the low thousands on the large-box reference while keeping query p95 under `50 ms`. | `api`, `ingest`, `worker`, `scheduler` plus PostgreSQL, MinIO, Valkey, NATS |

This table now anchors on fresh April 13 host reruns for Tiny and the self-hosted release lane, with the Sentry row kept as the latest same-host three-way reference. The default binary is lean again; ClickHouse and controller support now require explicit build tags instead of shipping in every default build.

Self-hosted feature tradeoff summary:
- default lean build: `33.86 MB` binary, `2200 eps`, `392.0 MB` peak memory
- `k8scontroller`: `62.63 MB` binary, `2200 eps`, `414.4 MB` peak memory
- `clickhouse`: `40.29 MB` binary, `2500 eps`, `442.7 MB` peak memory
- `clickhouse,k8scontroller`: `68.95 MB` binary, `2500 eps`, `427.0 MB` peak memory

See [docs/benchmarks.md](docs/benchmarks.md) for the full matrix and caveats.

If you only know that you need Sentry compatibility on one machine, start with Tiny. Move to self-hosted when you need the operational shape or the extra throughput headroom.

## What you get

- Issue tracking with grouping, merge and unmerge, assignment, comments, and subscriptions
- Discover, logs, traces, replay, and profiling surfaces in the same product
- Alerts and cron monitor support
- A web UI plus API routes that cover existing Sentry SDK clients on the common paths
- One codebase for both the single-node and split-role paths

## Downloads and releases

- GitHub Releases publish packaged builds for Linux, macOS, and Windows
- Docker images publish to `ghcr.io/urgentry/urgentry`
- Every public release ships with a matching entry in [CHANGELOG.md](CHANGELOG.md)
- Direct download details live in [deploy/direct/README.md](deploy/direct/README.md)
- Maintainer release steps live in [RELEASING.md](RELEASING.md)

## Where to go next

- [QUICKSTART.md](QUICKSTART.md) for the first Tiny install
- [docs/tiny/README.md](docs/tiny/README.md) for the single-node path
- [docs/self-hosted/README.md](docs/self-hosted/README.md) for the split-role path
- [deploy/README.md](deploy/README.md) for install options
- [CHANGELOG.md](CHANGELOG.md) for release notes

## Support and security

- [SUPPORT.md](SUPPORT.md)
- [SECURITY.md](SECURITY.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
