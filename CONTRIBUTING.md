# Contributing to Urgentry

Small, focused changes land best.

## Before opening a pull request

1. Read [README.md](README.md) and [QUICKSTART.md](QUICKSTART.md)
2. Run the smallest relevant validation
3. Update user-facing docs in the same change when behavior changes

## Common validation

```bash
make test
make lint
make test-merge
```

For release-path changes:

```bash
make tiny-launch-gate
make tiny-sentry-baseline
make selfhosted-sentry-baseline
```

## Pull request rules

- keep each change scoped
- add or update tests for behavior changes
- keep docs in sync
- use non-interactive git commands

## Community and security

- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [SECURITY.md](SECURITY.md)
- [SUPPORT.md](SUPPORT.md)
