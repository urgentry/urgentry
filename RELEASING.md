# Releasing Urgentry

This file covers the public release flow for `urgentry/urgentry`.

## Who cuts releases

Maintainers cut release tags and publish releases.

Contributors should not push release tags.

## What triggers a release

Push a `v*` tag on this repository.

Example:

```bash
git tag -a v0.2.7 -m "Release v0.2.7"
git push origin v0.2.7
```

## What must be ready first

1. `main` contains the code and docs you want to ship
2. `CHANGELOG.md` contains a matching section for the tag
3. the release workflow file in `.github/workflows/release.yml` is current

## What the release publishes

- GitHub Release assets for Linux, macOS, and Windows
- checksums in `SHA256SUMS`
- a GHCR image from the Docker job for maintainers

Verify anonymous GHCR pulls before documenting the image as a public install path.

## Contributor note

Pull requests are reviewed here.

If maintainers accept a PR, they replay the change through the canonical source tree and sync the result back here before merging. The final merge commit may not match the exact PR branch commit, but the public result should contain the accepted change.
