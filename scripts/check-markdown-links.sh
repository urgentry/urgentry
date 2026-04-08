#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if git grep -nE '\]\(/Users/[^)]*\)' -- '*.md'; then
  echo
  echo "Found local filesystem links in Markdown. Rewrite them to repo-relative links before merging."
  exit 1
fi

