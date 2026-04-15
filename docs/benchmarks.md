# Benchmarks

These numbers answer a simple question: what do Tiny mode, urgentry self-hosted, and Sentry self-hosted look like on the same machine when you feed them the same error-tracking workload?

The workload for these comparisons stays narrow on purpose:

- envelope ingest only
- 70/30 small and medium error mix
- the same issue and event query probes after load

## Large box, current reference

Server:

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

Read that table this way:

- Tiny is the easy path. It stays small and simple, and the recovered default binary is back in the old low-memory class.
- Self-hosted gives you much more headroom than Tiny on the same box and keeps query latency under `50 ms`, but you pay for that with a bigger operational footprint.
- Sentry still wins raw ingest latency on this host, but it uses far more memory and its post-load query latency is much worse.

The current public line is anchored on the April 13, 2026 host reruns for Tiny and the self-hosted release lane, with Sentry kept as the latest same-host three-way reference row.

## Self-hosted feature tradeoffs

| Variant | Build tags | ClickHouse pilot | Linux amd64 binary | Stable ingest ceiling | Ingest p95 | Query p95 | Peak memory |
|---|---|---|---:|---:|---:|---:|---:|
| Default self-hosted | `default` | off | `33.86 MB` | `2200 eps` | `0.689 ms` | `50.48 ms` | `392.0 MB` |
| Controller build tag | `k8scontroller` | off | `62.63 MB` | `2200 eps` | `0.702 ms` | `47.68 ms` | `414.4 MB` |
| ClickHouse pilot | `clickhouse` | on | `40.29 MB` | `2500 eps` | `0.588 ms` | `44.71 ms` | `442.7 MB` |
| ClickHouse + controller | `clickhouse,k8scontroller` | on | `68.95 MB` | `2500 eps` | `0.576 ms` | `47.58 ms` | `427.0 MB` |

Read that table this way:

- The binary-size tradeoff is the clearest signal: controller support roughly doubles the default binary, while the logs-only ClickHouse pilot adds about `6.4 MB` over the lean default.
- Peak-memory differences are also clear enough to guide operator decisions.
- The throughput spread between `2200` and `2500` should be treated as directional on this shared benchmark host. The controller code and the logs-only ClickHouse pilot are mostly dormant on the issue/event path exercised here, so some of that spread is host noise.
- The old self-hosted query and peak-memory line is not treated as an absolute host floor anymore. Same-host control reruns of the recovered `5d11c88` binary on April 13 bounced above and below the old line, so the current release lane now uses a dedicated capped rerun plus the feature matrix to separate real regressions from host variance.

This public repo ships Tiny and self-hosted. Sentry stays in the table so you can compare the tradeoffs. It is not part of what this repo ships.

## Small box, lower-cost reference

Server:

- `urgentry`
- Ubuntu 24.04, Linux 6.8
- 2 vCPU
- 3.73 GiB RAM
- 38 GB root disk

| Target | Result on the small box | Validation note | Notes |
|---|---|---|---|
| Tiny | `100 eps`; ingest p95 `6.8 ms`; query p95 `79.9 ms`; peak RSS `44.8 MB` | core ingest and query flow worked | one process, SQLite |
| Self-hosted | `100 eps`; ingest p95 `5.3 ms`; query p95 `55.5 ms`; peak stack RSS `184.0 MB` | basic split-role flow worked | much higher memory use than Tiny on the same host |
| Sentry self-hosted `26.3.1`, `errors-only` | install did not complete | install failed on this host | the official install stalled the Docker control plane on this machine |

This smaller-box run is still useful if you are deciding whether a cheap VPS is realistic. Tiny is the clear first stop there. Self-hosted is possible, but it asks for more memory and more operational patience. Sentry did not finish installing on that host.
