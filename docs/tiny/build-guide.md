# Urgentry Tiny Build Guide

Use this guide when you want a local binary, a repeatable source build, or the exact optimized Tiny-mode build profile the repo ships today.

If you want the full Tiny artifact matrix, release archive names, and checksum file layout, use [release-artifact-matrix](release-artifact-matrix.md).

## Requirements

- Go 1.26
- the repo checked out locally

## Default local build

```bash
cd .
make build
```

That writes `urgentry`.
The default build is already the small release-shaped Tiny-mode binary. It uses:

- `CGO_ENABLED=0`
- `-tags netgo,osusergo`
- `-trimpath`
- `-buildvcs=false`
- `-ldflags "-s -w -X main.version=<version>"`

Run it:

```bash
cd .
./urgentry serve --role=all
```

## `build-tiny` compatibility alias

`make build-tiny` still exists for compatibility with older docs and scripts:

```bash
cd .
make build-tiny
```

It produces the same optimized binary as `make build`.

## Debug build

If you need an unstripped local binary for debugging:

```bash
cd .
make build-debug
```

## Manual source build

If you want the repo’s shared build script instead of `make`:

```bash
cd .
bash ./scripts/build-urgentry.sh --output urgentry
```

That is the same path `make build`, `make build-tiny`, `make release`, Tiny smoke, and the Tiny Dockerfile now use.

## Measured size results

Measured on macOS arm64 with Go `1.26.1` against the current `urgentry` binary:

| Build profile | Raw bytes | gzip bytes | xz bytes | Raw delta vs normal |
| --- | ---: | ---: | ---: | ---: |
| normal `go build` with version only | 43,206,466 | 21,192,309 | 17,954,948 | baseline |
| stripped + `-trimpath` | 29,860,514 | 10,428,177 | 7,364,012 | `-30.89%` |
| default optimized profile | 29,773,106 | 10,395,187 | 7,336,580 | `-31.09%` |
| rejected `-gcflags=all=-l` variant | 25,914,002 | 8,661,602 | 6,142,436 | `-40.02%` |

The repo keeps the default optimized profile, not the `-gcflags=all=-l` variant. That no-inline build was smaller, but a simple `urgentry version` startup check slowed from about `0.05s` to about `0.14s`, which is the wrong trade for a server binary.

## Verify the build

```bash
cd .
./urgentry version
./urgentry serve --role=all
```

Open `http://localhost:8080/login/` and confirm that the bootstrap credentials appear in the startup logs.

## Quick validation

Use the fast local loop during development:

```bash
cd .
make test    # fast local loop (during development)
make lint
```

Before a PR merge, run the canonical merge-safe command instead:

```bash
cd .
make test-merge   # canonical merge-safe command (before every PR merge)
```

Before handing the binary to anyone outside the repo, run the release/handoff gate:

```bash
cd .
make tiny-launch-gate   # release/handoff gate (before public releases)
```

## Put the binary on your PATH

One simple local install path:

```bash
cd .
install -m 0755 ./urgentry ~/.local/bin/urgentry
```

Then:

```bash
urgentry version
urgentry serve --role=all
```

## Docker build

The repo ships a Tiny-mode Dockerfile in ``.

```bash
cd .
docker build --build-arg VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -t urgentry:local .
```

Run it:

```bash
docker run --rm -p 8080:8080 -v urgentry-data:/data urgentry:local
```

## Common build problems

### `go: command not found`

Install Go 1.26 first, then rerun the build.

### `address already in use`

Something else already owns port `8080`. Start Urgentry on a different port:

```bash
cd .
URGENTRY_HTTP_ADDR=:8090 ./urgentry serve --role=all
```

### Bootstrap credentials disappeared

Urgentry prints them once when it initializes a fresh data directory. If you missed them, stop the process, remove the throwaway test data directory, and start again on a fresh path.

## Next docs

- [QUICKSTART.md](../../QUICKSTART.md)
- [release-artifact-matrix](release-artifact-matrix.md)
- [deploy-guide](deploy-guide.md)
- [Release process](../architecture/release-process-and-versioning.md)
- [Operations guide](../architecture/operations-guide.md)
