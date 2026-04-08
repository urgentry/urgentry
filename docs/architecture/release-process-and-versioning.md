# Urgentry Release Process and Versioning

Use this guide when you need to cut a public Tiny-mode release, verify the artifacts, and explain what a version string means.

## Versioning

Urgentry uses two version forms today:

1. release versions such as `v0.1.0`
2. development versions from `git describe --tags --always --dirty`

The app binary prints its embedded version with:

```bash
urgentry version
```

For development builds:

- `make build` embeds `VERSION`, which defaults to `git describe --tags --always --dirty`
- `make build-tiny` is the same optimized build, kept as a compatibility alias
- `make build-debug` gives you an unstripped local binary when you need symbols

For release builds:

- `make release VERSION=v0.1.0` embeds `v0.1.0`

The optimized build profile shared by `make build`, `make release`, Tiny smoke, and the Dockerfile is:

- `CGO_ENABLED=0`
- `-tags netgo,osusergo`
- `-trimpath`
- `-buildvcs=false`
- `-ldflags "-s -w -X main.version=<version>"`

## Public Tiny release checklist

Run this from ``:

```bash
make test              # fast local loop (during development)
make test-merge        # canonical merge-safe command (before every PR merge)
make tiny-launch-gate  # release/handoff gate (before public releases)
make release VERSION=v0.1.0
```

`make test` is the fast local loop -- it is not sufficient for merging. `make test-merge` is the canonical merge-safe command. `make tiny-launch-gate` is the release/handoff gate required before any public Tiny release or external handoff. They do different jobs, and public release docs should keep that split clear.

## What `make release` produces

`make release VERSION=v0.1.0` runs `scripts/release.sh` and writes artifacts under `dist/`.

Current output:

- raw binaries for Linux and macOS on `amd64` and `arm64`
- one `.tar.gz` archive for each raw binary
- `SHA256SUMS`

See [urgentry-tiny-release-artifact-matrix.md](../tiny/release-artifact-matrix.md) for exact names.

## Verify the release artifacts

On macOS:

```bash
cd dist
shasum -a 256 -c SHA256SUMS
```

On Linux:

```bash
cd dist
sha256sum -c SHA256SUMS
```

## Tagging and publishing

The public release flow should stay simple:

1. update [CHANGELOG.md](../CHANGELOG.md)
2. make sure Tiny docs still match the shipped artifact and install paths
3. create the release artifacts with `make release VERSION=<tag>`
4. verify `SHA256SUMS`
5. create and push the Git tag
6. let the `Release` GitHub Actions workflow publish the files from `dist/` to GitHub Releases

Example tag commands:

```bash
git tag -a v0.1.0 -m "Urgentry v0.1.0"
git push origin v0.1.0
```

## GitHub release automation

The repo ships `.github/workflows/release.yml`.

It does three things:

- runs `make release VERSION=<tag>`
- verifies `dist/SHA256SUMS`
- uploads every file under `dist/` to the GitHub release for that tag

Release publication policy:

- the workflow must only publish a revision that passed `make tiny-launch-gate`
- `make test-merge` does not replace the launch gate for public releases
- manual workflow dispatch is a dry run for the launch gate plus binary packaging; container publication remains tag-only

Use tag pushes for real releases. Use manual workflow dispatch when you want a CI-built dry run without publishing a release. The benchmark workflow is intentionally decoupled from release tags so public release latency stays bound to launch-gate and artifact publication work only.

## Upgrade communication

Every public release should say:

- what changed for Tiny users
- whether build, install, or deploy steps changed
- whether operators need any extra smoke checks after upgrade

Keep the public upgrade surface in:

- [CHANGELOG.md](../CHANGELOG.md)
- [README.md](../README.md)
- [QUICKSTART.md](../QUICKSTART.md)
- the Tiny build, install, and deploy guides when commands or artifact names change

## When to cut a release

Cut a Tiny release when all of these are true:

- the validation commands above pass
- the Tiny docs are current
- the release artifacts build and verify cleanly
- the public smoke flow still works on a fresh node

## Related docs

- [urgentry-tiny-release-artifact-matrix.md](../tiny/release-artifact-matrix.md)
- [urgentry-tiny-binary-install-guide.md](../tiny/binary-install-guide.md)
- [urgentry-tiny-smoke-checklist.md](../tiny/smoke-checklist.md)
- [../CHANGELOG.md](../CHANGELOG.md)
