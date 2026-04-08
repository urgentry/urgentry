# Versioning Policy

Urgentry follows [Semantic Versioning 2.0.0](https://semver.org/).

## Version Format

`MAJOR.MINOR.PATCH[-PRERELEASE]`

Git tags use a `v` prefix: `v0.1.0`, `v1.0.0-rc.1`.

## Bump Rules

| Component | When |
|-----------|------|
| **MAJOR** | Breaking API or protocol changes. Includes: removed endpoints, changed wire formats, incompatible config keys, SDK contract breaks. |
| **MINOR** | New features that are backward-compatible. Includes: new endpoints, new config options with defaults, new CLI subcommands. |
| **PATCH** | Bug fixes only. No new features, no behavioral changes beyond the fix. |

## Pre-release Suffixes

| Suffix | Meaning |
|--------|---------|
| `-alpha.N` | Unstable, API may change without notice. |
| `-beta.N`  | Feature-complete for the release, API unlikely to change. |
| `-rc.N`    | Release candidate. Only bug fixes between rc and final. |

## Source of Truth

The canonical version is the latest `v*` git tag reachable from HEAD.
For untagged builds, `git describe --tags --always --dirty` produces a dev version string.

The repo-root `VERSION` file stores the *next planned* release version and is used as a fallback when no git tags exist (e.g., in source tarballs without `.git/`).

## Build Embedding

The build toolchain injects version, commit SHA, and build timestamp into the binary via `-ldflags`:

```
-X urgentry/internal/config.Version=<version>
-X urgentry/internal/config.Commit=<commit>
-X urgentry/internal/config.BuildDate=<date>
```

Run `urgentry version` to inspect the embedded build metadata.
