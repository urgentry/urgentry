#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
  echo "usage: release-notes-from-changelog.sh <tag>" >&2
  exit 2
fi

if [[ ! -f CHANGELOG.md ]]; then
  echo "CHANGELOG.md not found" >&2
  exit 1
fi

awk -v tag="${TAG}" '
  $0 ~ ("^## \\[" tag "\\]") { capture = 1; next }
  capture && /^## \[/ { exit }
  capture { print }
' CHANGELOG.md > .release-notes.tmp

if [[ ! -s .release-notes.tmp ]]; then
  echo "missing changelog entry for ${TAG} in CHANGELOG.md" >&2
  rm -f .release-notes.tmp
  exit 1
fi

cat .release-notes.tmp
rm -f .release-notes.tmp
