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
```

## Pull request rules

- keep each change scoped
- add or update tests for behavior changes
- keep docs in sync
- use non-interactive git commands
- maintainers must add a matching `CHANGELOG.md` entry before pushing a public release tag
- maintainers are the only ones who cut public release tags and GitHub Releases

## How pull requests land

Review happens in this repository.

If maintainers accept a PR, they replay the change through the canonical source tree and sync the result back here before merging. That means the final merge commit may differ from the exact commit in the PR branch. The public result should still contain the accepted change.

For release mechanics in this repository, see [RELEASING.md](RELEASING.md).

## Community and security

- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [SECURITY.md](SECURITY.md)
- [SUPPORT.md](SUPPORT.md)
