# Changelog

All notable changes to the public Urgentry repository should be recorded here.

The release workflow requires a section for every published tag.

## [Unreleased]

## [v0.2.8] - 2026-04-13

### Added

- A persistent synthetic live-seeding path for end-to-end Tiny QA and browser verification.
- Fresh April 13 benchmark evidence for the default self-hosted feature matrix, including `k8scontroller` and `clickhouse` on/off tradeoffs.

### Changed

- The benchmark note now anchors on fresh April 13 Tiny and self-hosted release-lane reruns, with the shared-host noise caveat made explicit.
- Starter dashboard and starter view controls now carry unique accessible labels instead of repeated generic button names.

### Fixed

- `/crons/` now redirects to `/monitors/` and `/admin/` now redirects to `/manage/`, so legacy deep links no longer 404.
- Monitor next-check-in text now uses due/overdue wording instead of rendering negative relative times.
- The private export/release path now characterizes the curated public tree before release, and the public export excludes the private synthetic live-seeding helper.

## [v0.2.7] - 2026-04-12

### Added

- Proof-backed semantic compatibility tracking for release health, workflows, relay, and enterprise identity paths.

### Changed

- The public release story now aligns to the parity closeout: SCIM lifecycle semantics, env-configured SAML routes, and the bounded trusted relay path are all reflected in the compatibility docs.
- Generated synthetic registry and fixture manifests now match the expanded SDK corpus.

### Fixed

- Org session aggregation now uses the real session timestamp column instead of a nonexistent field.
- Merge and unmerge routes now reject invalid targets instead of silently accepting cross-project or self-target transitions.

## [v0.2.6] - 2026-04-12

### Added

- The packaged self-hosted Compose path now documents the optional logs-only ClickHouse pilot and the matching `COMPOSE_PROFILES=columnar` env shape.

### Changed

- Tightened the public repository so it only ships the user-facing Tiny and self-hosted product surface.

### Fixed

- The benchmark ingest CLI now creates parent report directories before writing JSON output, so fresh benchmark reruns do not fail on clean hosts.
- The self-hosted Compose smoke path now proves the optional ClickHouse logs pilot with real log ingest and org logs readback instead of relying on ad hoc operator setup.

## [v0.2.5] - 2026-04-08

### Fixed

- The public lint configuration now uses a dedicated public `.golangci.yml` instead of inheriting the private repo's `revive`-heavy rules.
- The standalone public release gate now matches the locally verified lint surface instead of failing on configuration conflicts.

## [v0.2.4] - 2026-04-08

### Fixed

- The shared build helper is now POSIX-shell compatible, so both GitHub Actions runners and the public Docker image build use the same release path successfully.
- OCI source metadata now points at `https://github.com/urgentry/urgentry`.

## [v0.2.3] - 2026-04-08

### Fixed

- The public release gate no longer fails on unchecked test writes in middleware tests.
- Saved-search JSON handlers now check encoding errors instead of dropping them.
- The public lint gate now skips legacy revive-only style debt so tagged releases can ship on the standalone public repo.

## [v0.2.2] - 2026-04-08

### Fixed

- The public tagged release workflow now builds through the Tiny launch gate on GitHub Actions runners.
- The public release path now uses the corrected Bash build helper when packaging release binaries and smoke-test builds.

## [v0.2.1] - 2026-04-08

### Added

- GitHub Release packaging for Linux, macOS, and Windows from the standalone public repo.
- GHCR publication for `ghcr.io/urgentry/urgentry`.

### Changed

- The public release workflow now requires a matching changelog entry and publishes release notes from it.
- The public README now explains the problem, target user, and first run much more directly.
- The public Tiny launch gate no longer runs on every PR by default.

## [v0.2.0] - 2026-04-08

### Added

- Public release artifacts for Linux, macOS, and Windows.
- Standalone Tiny and self-hosted public deployment docs.

### Changed

- Public CI and release workflows now validate the standalone public repo.
- The public repository now uses the retained-copyright FSL license text.

### Removed

- Synthetic artifacts, synthetic commands, internal compatibility harnesses, and other private validation surfaces from the public repo.
